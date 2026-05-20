package service

import (
	"context"
	"crypto"
	"crypto/md5"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"chatgpt2api/internal/storage"
	"chatgpt2api/internal/util"
)

const (
	subscriptionConfigDocumentName    = "subscription_config.json"
	subscriptionOrdersDocumentName    = "subscription_orders.json"
	subscriptionOrderStatusPending    = "pending"
	subscriptionOrderStatusPaid       = "paid"
	subscriptionPaymentModeEPay       = "epay"
	subscriptionPaymentModeAlipayOpen = "alipay_open"
	defaultAlipayGatewayURL           = "https://openapi.alipay.com/gateway.do"
)

type SubscriptionPlan struct {
	ID          string
	Name        string
	Description string
	Price       string
	Quota       int
	Enabled     bool
	Recommended bool
}

type SubscriptionPaymentConfig struct {
	Enabled         bool
	Mode            string
	GatewayURL      string
	MerchantID      string
	MerchantKey     string
	AppPrivateKey   string
	AlipayPublicKey string
	PayTypes        []string
	SiteName        string
}

type SubscriptionConfig struct {
	Enabled bool
	Payment SubscriptionPaymentConfig
	Plans   []SubscriptionPlan
}

type SubscriptionOrder struct {
	ID           string
	OwnerID      string
	OwnerName    string
	Provider     string
	PlanID       string
	PlanName     string
	Quota        int
	Money        string
	PayType      string
	Status       string
	TradeNo      string
	RawNotify    map[string]any
	CreatedAt    string
	UpdatedAt    string
	PaidAt       string
	QuotaGranted bool
}

type SubscriptionCheckout struct {
	Order      map[string]any
	Mode       string
	Action     string
	Fields     map[string]string
	URL        string
	QRCode     string
	QRImageURL string
}

type SubscriptionService struct {
	mu            sync.Mutex
	configPath    string
	ordersPath    string
	store         storage.JSONDocumentBackend
	config        SubscriptionConfig
	orders        []SubscriptionOrder
	configDocName string
	ordersDocName string
}

func NewSubscriptionService(dataDir string, backend storage.Backend) *SubscriptionService {
	s := &SubscriptionService{
		configPath:    filepath.Join(dataDir, "subscription_config.json"),
		ordersPath:    filepath.Join(dataDir, "subscription_orders.json"),
		store:         jsonDocumentStoreFromBackend(backend),
		configDocName: subscriptionConfigDocumentName,
		ordersDocName: subscriptionOrdersDocumentName,
	}
	s.config = normalizeSubscriptionConfig(loadStoredJSON(s.store, s.configDocName, s.configPath))
	s.orders = normalizeSubscriptionOrders(loadStoredJSON(s.store, s.ordersDocName, s.ordersPath))
	return s
}

func (s *SubscriptionService) PublicConfig() map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	return publicSubscriptionConfig(s.config)
}

func (s *SubscriptionService) AdminConfig() map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	return adminSubscriptionConfig(s.config)
}

func (s *SubscriptionService) UpdateConfig(updates map[string]any) (map[string]any, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	next := s.config
	if value, ok := updates["enabled"]; ok {
		next.Enabled = util.ToBool(value)
	}
	if rawPayment, ok := updates["payment"].(map[string]any); ok {
		next.Payment = mergeSubscriptionPaymentConfig(next.Payment, rawPayment)
	}
	if rawPlans, ok := updates["plans"]; ok {
		plans := normalizeSubscriptionPlans(rawPlans)
		if len(plans) == 0 {
			return nil, errors.New("至少需要保留一个订阅套餐")
		}
		next.Plans = plans
	}
	next = normalizeSubscriptionConfig(configToRaw(next))
	s.config = next
	if err := s.saveConfigLocked(); err != nil {
		return nil, err
	}
	return adminSubscriptionConfig(s.config), nil
}

func (s *SubscriptionService) CreateCheckout(identity Identity, planID, payType, baseURL string) (SubscriptionCheckout, error) {
	ownerID := identityScopeFromIdentity(identity)
	if ownerID == "" {
		return SubscriptionCheckout{}, errors.New("当前账号不能购买订阅")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.config.Enabled {
		return SubscriptionCheckout{}, errors.New("订阅功能未开启")
	}
	payment := s.config.Payment
	if err := validateSubscriptionPaymentReady(payment); err != nil {
		return SubscriptionCheckout{}, err
	}
	plan, ok := findSubscriptionPlan(s.config.Plans, planID)
	if !ok || !plan.Enabled {
		return SubscriptionCheckout{}, errors.New("订阅套餐不可用")
	}
	payType = normalizeSubscriptionPayType(payType, payment.PayTypes)
	if payType == "" {
		return SubscriptionCheckout{}, errors.New("请选择支付方式")
	}
	order := SubscriptionOrder{
		ID:        "sub_" + util.NewHex(18),
		OwnerID:   ownerID,
		OwnerName: identity.Name,
		Provider:  identity.Provider,
		PlanID:    plan.ID,
		PlanName:  plan.Name,
		Quota:     maxInt(0, plan.Quota),
		Money:     normalizeMoney(plan.Price),
		PayType:   payType,
		Status:    subscriptionOrderStatusPending,
		CreatedAt: util.NowISO(),
		UpdatedAt: util.NowISO(),
	}
	s.orders = append([]SubscriptionOrder{order}, s.orders...)
	if err := s.saveOrdersLocked(); err != nil {
		return SubscriptionCheckout{}, err
	}
	if payment.Mode == subscriptionPaymentModeAlipayOpen {
		checkout, err := s.createAlipayOpenCheckout(payment, order, plan, baseURL)
		if err != nil {
			return SubscriptionCheckout{}, err
		}
		for index := range s.orders {
			if s.orders[index].ID == order.ID {
				s.orders[index].RawNotify = map[string]any{"qr_code": checkout.QRCode}
				s.orders[index].UpdatedAt = util.NowISO()
				if err := s.saveOrdersLocked(); err != nil {
					return SubscriptionCheckout{}, err
				}
				checkout.Order = publicSubscriptionOrder(s.orders[index])
				break
			}
		}
		return checkout, nil
	}
	action, err := subscriptionSubmitURL(payment.GatewayURL)
	if err != nil {
		return SubscriptionCheckout{}, err
	}
	fields := map[string]string{
		"pid":          payment.MerchantID,
		"type":         payType,
		"out_trade_no": order.ID,
		"notify_url":   strings.TrimRight(baseURL, "/") + "/api/subscription/payment/notify",
		"return_url":   strings.TrimRight(baseURL, "/") + "/api/subscription/payment/return?out_trade_no=" + url.QueryEscape(order.ID),
		"name":         plan.Name,
		"money":        order.Money,
		"sitename":     payment.SiteName,
	}
	fields["sign"] = EPayMD5Sign(fields, payment.MerchantKey)
	fields["sign_type"] = "MD5"
	return SubscriptionCheckout{
		Order:  publicSubscriptionOrder(order),
		Mode:   subscriptionPaymentModeEPay,
		Action: action,
		Fields: fields,
		URL:    action + "?" + encodePaymentFields(fields),
	}, nil
}

func (s *SubscriptionService) CompleteNotify(values map[string]string, grant func(SubscriptionOrder) error) (SubscriptionOrder, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	payment := s.config.Payment
	if payment.Mode == subscriptionPaymentModeAlipayOpen {
		if strings.TrimSpace(payment.AlipayPublicKey) == "" {
			return SubscriptionOrder{}, false, errors.New("支付宝公钥未配置")
		}
		if !VerifyAlipayRSA2Sign(values, payment.AlipayPublicKey) {
			return SubscriptionOrder{}, false, errors.New("invalid alipay sign")
		}
	} else {
		if strings.TrimSpace(payment.MerchantKey) == "" {
			return SubscriptionOrder{}, false, errors.New("码支付未配置")
		}
		if !VerifyEPayMD5Sign(values, payment.MerchantKey) {
			return SubscriptionOrder{}, false, errors.New("invalid payment sign")
		}
	}
	orderID := strings.TrimSpace(values["out_trade_no"])
	if orderID == "" {
		return SubscriptionOrder{}, false, errors.New("missing out_trade_no")
	}
	for index, order := range s.orders {
		if order.ID != orderID {
			continue
		}
		if order.Status == subscriptionOrderStatusPaid && order.QuotaGranted {
			return order, false, nil
		}
		if !paymentNotifySuccess(values) {
			return order, false, errors.New("payment is not successful")
		}
		if money := strings.TrimSpace(firstNonEmpty(values["money"], values["total_amount"], values["buyer_pay_amount"], values["receipt_amount"])); money != "" && normalizeMoney(money) != order.Money {
			return order, false, errors.New("payment money mismatch")
		}
		now := util.NowISO()
		order.Status = subscriptionOrderStatusPaid
		order.TradeNo = strings.TrimSpace(values["trade_no"])
		order.RawNotify = stringMapFromStrings(values)
		order.PaidAt = now
		order.UpdatedAt = now
		order.QuotaGranted = false
		s.orders[index] = order
		if err := s.saveOrdersLocked(); err != nil {
			return SubscriptionOrder{}, false, err
		}
		if grant != nil {
			if err := grant(order); err != nil {
				return order, false, err
			}
		}
		order.QuotaGranted = true
		s.orders[index] = order
		if err := s.saveOrdersLocked(); err != nil {
			return SubscriptionOrder{}, false, err
		}
		return order, true, nil
	}
	return SubscriptionOrder{}, false, errors.New("order not found")
}

func (s *SubscriptionService) Order(id string) map[string]any {
	order, ok := s.OrderRecord(id)
	if !ok {
		return nil
	}
	return publicSubscriptionOrder(order)
}

func (s *SubscriptionService) OrderQRCode(id string) (string, bool) {
	id = util.Clean(id)
	if id == "" {
		return "", false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, order := range s.orders {
		if order.ID != id {
			continue
		}
		if qrCode := util.Clean(order.RawNotify["qr_code"]); qrCode != "" && order.Status == subscriptionOrderStatusPending {
			return qrCode, true
		}
		return "", false
	}
	return "", false
}

func (s *SubscriptionService) OrderRecord(id string) (SubscriptionOrder, bool) {
	id = util.Clean(id)
	if id == "" {
		return SubscriptionOrder{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, order := range s.orders {
		if order.ID == id {
			return order, true
		}
	}
	return SubscriptionOrder{}, false
}

func (s *SubscriptionService) Orders(limit int) []map[string]any {
	if limit <= 0 {
		limit = 100
	}
	if limit > 500 {
		limit = 500
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]map[string]any, 0, minInt(limit, len(s.orders)))
	for index, order := range s.orders {
		if index >= limit {
			break
		}
		out = append(out, publicSubscriptionOrder(order))
	}
	return out
}

func (s *SubscriptionService) saveConfigLocked() error {
	return saveStoredJSON(s.store, s.configDocName, s.configPath, configToRaw(s.config))
}

func (s *SubscriptionService) saveOrdersLocked() error {
	items := make([]map[string]any, 0, len(s.orders))
	for _, order := range s.orders {
		items = append(items, orderToRaw(order))
	}
	return saveStoredJSON(s.store, s.ordersDocName, s.ordersPath, map[string]any{"items": items})
}

func normalizeSubscriptionConfig(raw any) SubscriptionConfig {
	obj := util.StringMap(raw)
	payment := normalizeSubscriptionPaymentConfig(obj["payment"])
	plans := normalizeSubscriptionPlans(obj["plans"])
	if len(plans) == 0 {
		plans = defaultSubscriptionPlans()
	}
	return SubscriptionConfig{
		Enabled: util.ToBool(util.ValueOr(obj["enabled"], true)),
		Payment: payment,
		Plans:   plans,
	}
}

func normalizeSubscriptionPaymentConfig(raw any) SubscriptionPaymentConfig {
	obj := util.StringMap(raw)
	payTypes := normalizeSubscriptionPayTypes(obj["pay_types"])
	if len(payTypes) == 0 {
		payTypes = []string{"alipay", "wxpay", "qqpay"}
	}
	mode := normalizeSubscriptionPaymentMode(util.Clean(obj["mode"]))
	return SubscriptionPaymentConfig{
		Enabled:         util.ToBool(obj["enabled"]),
		Mode:            mode,
		GatewayURL:      normalizePaymentGatewayURL(mode, util.Clean(obj["gateway_url"])),
		MerchantID:      strings.TrimSpace(util.Clean(obj["merchant_id"])),
		MerchantKey:     strings.TrimSpace(util.Clean(obj["merchant_key"])),
		AppPrivateKey:   normalizePEMKey(firstNonEmpty(util.Clean(obj["app_private_key"]), util.Clean(obj["private_key"]))),
		AlipayPublicKey: normalizePEMKey(firstNonEmpty(util.Clean(obj["alipay_public_key"]), util.Clean(obj["public_key"]))),
		PayTypes:        payTypes,
		SiteName:        firstNonEmpty(util.Clean(obj["site_name"]), "1818.pro"),
	}
}

func mergeSubscriptionPaymentConfig(current SubscriptionPaymentConfig, updates map[string]any) SubscriptionPaymentConfig {
	if value, ok := updates["enabled"]; ok {
		current.Enabled = util.ToBool(value)
	}
	if value, ok := updates["mode"]; ok {
		current.Mode = normalizeSubscriptionPaymentMode(util.Clean(value))
	}
	if value, ok := updates["gateway_url"]; ok {
		current.GatewayURL = strings.TrimSpace(util.Clean(value))
	}
	if value, ok := updates["merchant_id"]; ok {
		current.MerchantID = strings.TrimSpace(util.Clean(value))
	}
	if value, ok := updates["merchant_key"]; ok {
		if key := strings.TrimSpace(util.Clean(value)); key != "" {
			current.MerchantKey = key
		}
	}
	if value, ok := updates["app_private_key"]; ok {
		if key := normalizePEMKey(util.Clean(value)); key != "" {
			current.AppPrivateKey = key
		}
	}
	if value, ok := updates["alipay_public_key"]; ok {
		if key := normalizePEMKey(util.Clean(value)); key != "" {
			current.AlipayPublicKey = key
		}
	}
	if value, ok := updates["clear_merchant_key"]; ok && util.ToBool(value) {
		current.MerchantKey = ""
	}
	if value, ok := updates["clear_app_private_key"]; ok && util.ToBool(value) {
		current.AppPrivateKey = ""
	}
	if value, ok := updates["clear_alipay_public_key"]; ok && util.ToBool(value) {
		current.AlipayPublicKey = ""
	}
	if value, ok := updates["pay_types"]; ok {
		current.PayTypes = normalizeSubscriptionPayTypes(value)
	}
	if value, ok := updates["site_name"]; ok {
		current.SiteName = firstNonEmpty(util.Clean(value), "1818.pro")
	}
	return normalizeSubscriptionPaymentConfig(map[string]any{
		"enabled":           current.Enabled,
		"mode":              current.Mode,
		"gateway_url":       current.GatewayURL,
		"merchant_id":       current.MerchantID,
		"merchant_key":      current.MerchantKey,
		"app_private_key":   current.AppPrivateKey,
		"alipay_public_key": current.AlipayPublicKey,
		"pay_types":         current.PayTypes,
		"site_name":         current.SiteName,
	})
}

func normalizeSubscriptionPlans(raw any) []SubscriptionPlan {
	out := make([]SubscriptionPlan, 0)
	seen := map[string]struct{}{}
	for _, item := range anyList(raw) {
		obj, ok := item.(map[string]any)
		if !ok {
			continue
		}
		id := strings.ToLower(strings.TrimSpace(util.Clean(obj["id"])))
		if id == "" {
			id = strings.ToLower(strings.ReplaceAll(util.Clean(obj["name"]), " ", "-"))
		}
		id = strings.Map(func(r rune) rune {
			if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_' || r == '-' {
				return r
			}
			return -1
		}, id)
		if id == "" {
			id = "plan-" + util.NewHex(6)
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		name := firstNonEmpty(util.Clean(obj["name"]), id)
		price := normalizeMoney(util.ValueOr(obj["price"], "0"))
		quota := maxInt(0, util.ToInt(obj["quota"], 0))
		out = append(out, SubscriptionPlan{
			ID:          id,
			Name:        name,
			Description: util.Clean(obj["description"]),
			Price:       price,
			Quota:       quota,
			Enabled:     util.ToBool(util.ValueOr(obj["enabled"], true)),
			Recommended: util.ToBool(obj["recommended"]),
		})
	}
	return out
}

func normalizeSubscriptionOrders(raw any) []SubscriptionOrder {
	if obj, ok := raw.(map[string]any); ok {
		raw = obj["items"]
	}
	out := make([]SubscriptionOrder, 0)
	for _, item := range anyList(raw) {
		obj, ok := item.(map[string]any)
		if !ok {
			continue
		}
		order := SubscriptionOrder{
			ID:           util.Clean(obj["id"]),
			OwnerID:      util.Clean(obj["owner_id"]),
			OwnerName:    util.Clean(obj["owner_name"]),
			Provider:     util.Clean(obj["provider"]),
			PlanID:       util.Clean(obj["plan_id"]),
			PlanName:     util.Clean(obj["plan_name"]),
			Quota:        maxInt(0, util.ToInt(obj["quota"], 0)),
			Money:        normalizeMoney(obj["money"]),
			PayType:      normalizeSubscriptionPayType(util.Clean(obj["pay_type"]), nil),
			Status:       firstNonEmpty(util.Clean(obj["status"]), subscriptionOrderStatusPending),
			TradeNo:      util.Clean(obj["trade_no"]),
			CreatedAt:    util.Clean(obj["created_at"]),
			UpdatedAt:    util.Clean(obj["updated_at"]),
			PaidAt:       util.Clean(obj["paid_at"]),
			QuotaGranted: util.ToBool(obj["quota_granted"]),
		}
		if rawNotify, ok := obj["raw_notify"].(map[string]any); ok {
			order.RawNotify = util.CopyMap(rawNotify)
		}
		if order.ID != "" {
			out = append(out, order)
		}
	}
	return out
}

func defaultSubscriptionPlans() []SubscriptionPlan {
	return []SubscriptionPlan{
		{ID: "starter", Name: "轻量版", Description: "适合低频商品图、头像图和日常试用。", Price: "19.00", Quota: 300, Enabled: true},
		{ID: "creator", Name: "创作版", Description: "覆盖参考图、高清图、批量任务和更快排队。", Price: "69.00", Quota: 1500, Enabled: true, Recommended: true},
		{ID: "business", Name: "商业版", Description: "适合团队、多用户、电商主图和 API 调用。", Price: "199.00", Quota: 6000, Enabled: true},
	}
}

func publicSubscriptionConfig(config SubscriptionConfig) map[string]any {
	plans := make([]map[string]any, 0, len(config.Plans))
	for _, plan := range config.Plans {
		if !plan.Enabled {
			continue
		}
		plans = append(plans, publicSubscriptionPlan(plan))
	}
	return map[string]any{
		"enabled":       config.Enabled,
		"payment_ready": subscriptionPaymentReady(config.Payment),
		"payment": map[string]any{
			"enabled":   config.Payment.Enabled,
			"mode":      config.Payment.Mode,
			"pay_types": append([]string(nil), config.Payment.PayTypes...),
			"site_name": config.Payment.SiteName,
		},
		"plans": plans,
	}
}

func adminSubscriptionConfig(config SubscriptionConfig) map[string]any {
	payment := config.Payment
	plans := make([]map[string]any, 0, len(config.Plans))
	for _, plan := range config.Plans {
		plans = append(plans, publicSubscriptionPlan(plan))
	}
	return map[string]any{
		"enabled": config.Enabled,
		"payment": map[string]any{
			"enabled":                      payment.Enabled,
			"mode":                         payment.Mode,
			"gateway_url":                  payment.GatewayURL,
			"merchant_id":                  payment.MerchantID,
			"merchant_key":                 "",
			"merchant_key_configured":      payment.MerchantKey != "",
			"app_private_key":              "",
			"app_private_key_configured":   payment.AppPrivateKey != "",
			"alipay_public_key":            "",
			"alipay_public_key_configured": payment.AlipayPublicKey != "",
			"pay_types":                    append([]string(nil), payment.PayTypes...),
			"site_name":                    payment.SiteName,
		},
		"plans": plans,
	}
}

func publicSubscriptionPlan(plan SubscriptionPlan) map[string]any {
	return map[string]any{
		"id":          plan.ID,
		"name":        plan.Name,
		"description": plan.Description,
		"price":       plan.Price,
		"quota":       plan.Quota,
		"enabled":     plan.Enabled,
		"recommended": plan.Recommended,
	}
}

func publicSubscriptionOrder(order SubscriptionOrder) map[string]any {
	return map[string]any{
		"id":            order.ID,
		"owner_id":      order.OwnerID,
		"owner_name":    order.OwnerName,
		"provider":      order.Provider,
		"plan_id":       order.PlanID,
		"plan_name":     order.PlanName,
		"quota":         order.Quota,
		"money":         order.Money,
		"pay_type":      order.PayType,
		"status":        order.Status,
		"trade_no":      order.TradeNo,
		"created_at":    order.CreatedAt,
		"updated_at":    order.UpdatedAt,
		"paid_at":       order.PaidAt,
		"quota_granted": order.QuotaGranted,
	}
}

func configToRaw(config SubscriptionConfig) map[string]any {
	plans := make([]map[string]any, 0, len(config.Plans))
	for _, plan := range config.Plans {
		plans = append(plans, publicSubscriptionPlan(plan))
	}
	return map[string]any{
		"enabled": config.Enabled,
		"payment": map[string]any{
			"enabled":           config.Payment.Enabled,
			"mode":              config.Payment.Mode,
			"gateway_url":       config.Payment.GatewayURL,
			"merchant_id":       config.Payment.MerchantID,
			"merchant_key":      config.Payment.MerchantKey,
			"app_private_key":   config.Payment.AppPrivateKey,
			"alipay_public_key": config.Payment.AlipayPublicKey,
			"pay_types":         append([]string(nil), config.Payment.PayTypes...),
			"site_name":         config.Payment.SiteName,
		},
		"plans": plans,
	}
}

func orderToRaw(order SubscriptionOrder) map[string]any {
	return map[string]any{
		"id":            order.ID,
		"owner_id":      order.OwnerID,
		"owner_name":    order.OwnerName,
		"provider":      order.Provider,
		"plan_id":       order.PlanID,
		"plan_name":     order.PlanName,
		"quota":         order.Quota,
		"money":         order.Money,
		"pay_type":      order.PayType,
		"status":        order.Status,
		"trade_no":      order.TradeNo,
		"raw_notify":    order.RawNotify,
		"created_at":    order.CreatedAt,
		"updated_at":    order.UpdatedAt,
		"paid_at":       order.PaidAt,
		"quota_granted": order.QuotaGranted,
	}
}

func findSubscriptionPlan(plans []SubscriptionPlan, id string) (SubscriptionPlan, bool) {
	id = strings.ToLower(strings.TrimSpace(id))
	for _, plan := range plans {
		if plan.ID == id {
			return plan, true
		}
	}
	return SubscriptionPlan{}, false
}

func normalizeSubscriptionPayTypes(value any) []string {
	raw := util.AsStringSlice(value)
	if len(raw) == 0 {
		if text := strings.TrimSpace(fmt.Sprint(value)); text != "" && text != "<nil>" {
			raw = strings.FieldsFunc(text, func(r rune) bool { return r == ',' || r == '，' || r == ' ' || r == '\n' || r == '\t' })
		}
	}
	allowed := map[string]struct{}{"alipay": {}, "wxpay": {}, "qqpay": {}}
	out := make([]string, 0, len(raw))
	seen := map[string]struct{}{}
	for _, item := range raw {
		payType := strings.ToLower(strings.TrimSpace(item))
		if _, ok := allowed[payType]; !ok {
			continue
		}
		if _, ok := seen[payType]; ok {
			continue
		}
		seen[payType] = struct{}{}
		out = append(out, payType)
	}
	return out
}

func normalizeSubscriptionPayType(value string, allowed []string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	switch value {
	case "alipay", "wxpay", "qqpay":
	default:
		value = ""
	}
	if value == "" {
		if len(allowed) > 0 {
			return allowed[0]
		}
		return ""
	}
	if len(allowed) == 0 {
		return value
	}
	for _, item := range allowed {
		if item == value {
			return value
		}
	}
	return allowed[0]
}

func normalizeSubscriptionPaymentMode(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case subscriptionPaymentModeAlipayOpen, "alipay", "alipay_openapi", "openapi":
		return subscriptionPaymentModeAlipayOpen
	default:
		return subscriptionPaymentModeEPay
	}
}

func normalizePaymentGatewayURL(mode, gateway string) string {
	gateway = strings.TrimSpace(gateway)
	if gateway == "" && mode == subscriptionPaymentModeAlipayOpen {
		return defaultAlipayGatewayURL
	}
	return gateway
}

func subscriptionPaymentReady(payment SubscriptionPaymentConfig) bool {
	return validateSubscriptionPaymentReady(payment) == nil
}

func validateSubscriptionPaymentReady(payment SubscriptionPaymentConfig) error {
	if !payment.Enabled {
		return errors.New("订阅支付未启用")
	}
	if payment.Mode == subscriptionPaymentModeAlipayOpen {
		if strings.TrimSpace(payment.GatewayURL) == "" {
			return errors.New("支付宝网关未配置")
		}
		if strings.TrimSpace(payment.MerchantID) == "" {
			return errors.New("支付宝 APPID 未配置")
		}
		if strings.TrimSpace(payment.AppPrivateKey) == "" {
			return errors.New("支付宝应用私钥未配置")
		}
		if strings.TrimSpace(payment.AlipayPublicKey) == "" {
			return errors.New("支付宝公钥未配置")
		}
		return nil
	}
	if strings.TrimSpace(payment.GatewayURL) == "" || strings.TrimSpace(payment.MerchantID) == "" || strings.TrimSpace(payment.MerchantKey) == "" {
		return errors.New("码支付未配置，请联系管理员")
	}
	return nil
}

func subscriptionSubmitURL(gateway string) (string, error) {
	gateway = strings.TrimRight(strings.TrimSpace(gateway), "/")
	if gateway == "" {
		return "", errors.New("码支付网关不能为空")
	}
	parsed, err := url.Parse(gateway)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return "", errors.New("码支付网关必须是完整 http(s) 地址")
	}
	if !strings.HasSuffix(parsed.Path, "/submit.php") {
		parsed.Path = strings.TrimRight(parsed.Path, "/") + "/submit.php"
	}
	return parsed.String(), nil
}

func (s *SubscriptionService) createAlipayOpenCheckout(payment SubscriptionPaymentConfig, order SubscriptionOrder, plan SubscriptionPlan, baseURL string) (SubscriptionCheckout, error) {
	gateway := strings.TrimSpace(payment.GatewayURL)
	if gateway == "" {
		gateway = defaultAlipayGatewayURL
	}
	notifyURL := strings.TrimRight(baseURL, "/") + "/api/subscription/payment/notify"
	returnURL := strings.TrimRight(baseURL, "/") + "/api/subscription/payment/return?out_trade_no=" + url.QueryEscape(order.ID)
	bizContent, err := json.Marshal(map[string]any{
		"out_trade_no":    order.ID,
		"total_amount":    order.Money,
		"subject":         plan.Name,
		"store_id":        firstNonEmpty(payment.SiteName, "1818.pro"),
		"timeout_express": "10m",
	})
	if err != nil {
		return SubscriptionCheckout{}, err
	}
	fields := map[string]string{
		"app_id":      payment.MerchantID,
		"method":      "alipay.trade.precreate",
		"format":      "JSON",
		"charset":     "utf-8",
		"sign_type":   "RSA2",
		"timestamp":   time.Now().Format("2006-01-02 15:04:05"),
		"version":     "1.0",
		"notify_url":  notifyURL,
		"return_url":  returnURL,
		"biz_content": string(bizContent),
	}
	sign, err := AlipayRSA2Sign(fields, payment.AppPrivateKey)
	if err != nil {
		return SubscriptionCheckout{}, err
	}
	fields["sign"] = sign
	endpoint := gateway + "?" + encodePaymentFields(fields)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return SubscriptionCheckout{}, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return SubscriptionCheckout{}, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil {
		return SubscriptionCheckout{}, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return SubscriptionCheckout{}, fmt.Errorf("支付宝预下单失败: HTTP %d", resp.StatusCode)
	}
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		return SubscriptionCheckout{}, fmt.Errorf("支付宝预下单响应解析失败: %w", err)
	}
	rawResp := util.StringMap(payload["alipay_trade_precreate_response"])
	code := util.Clean(rawResp["code"])
	if code != "10000" {
		message := firstNonEmpty(util.Clean(rawResp["sub_msg"]), util.Clean(rawResp["msg"]), "支付宝预下单失败")
		return SubscriptionCheckout{}, errors.New(message)
	}
	qrCode := util.Clean(rawResp["qr_code"])
	if qrCode == "" {
		return SubscriptionCheckout{}, errors.New("支付宝未返回动态二维码")
	}
	if sign := util.Clean(payload["sign"]); sign != "" {
		responseForSign := compactJSONValue(payload["alipay_trade_precreate_response"])
		if responseForSign != "" && !VerifyAlipayRSA2Payload(responseForSign, sign, payment.AlipayPublicKey) {
			return SubscriptionCheckout{}, errors.New("支付宝预下单响应验签失败")
		}
	}
	qrImageURL := strings.TrimRight(baseURL, "/") + "/api/subscription/payment/qr?order=" + url.QueryEscape(order.ID)
	return SubscriptionCheckout{
		Order:      publicSubscriptionOrder(order),
		Mode:       subscriptionPaymentModeAlipayOpen,
		Action:     gateway,
		Fields:     fields,
		URL:        "/subscription?order=" + url.QueryEscape(order.ID),
		QRCode:     qrCode,
		QRImageURL: qrImageURL,
	}, nil
}

func compactJSONValue(value any) string {
	data, err := json.Marshal(value)
	if err != nil {
		return ""
	}
	return string(data)
}

func normalizeMoney(value any) string {
	text := strings.TrimSpace(fmt.Sprint(value))
	text = strings.TrimPrefix(text, "¥")
	text = strings.TrimSpace(text)
	if text == "" || text == "<nil>" {
		return "0.00"
	}
	number, err := strconv.ParseFloat(text, 64)
	if err != nil || number < 0 {
		number = 0
	}
	return fmt.Sprintf("%.2f", number)
}

func EPayMD5Sign(values map[string]string, key string) string {
	keys := make([]string, 0, len(values))
	for name, value := range values {
		if name == "sign" || name == "sign_type" || strings.TrimSpace(value) == "" {
			continue
		}
		keys = append(keys, name)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, name := range keys {
		parts = append(parts, name+"="+values[name])
	}
	sum := md5.Sum([]byte(strings.Join(parts, "&") + key))
	return hex.EncodeToString(sum[:])
}

func VerifyEPayMD5Sign(values map[string]string, key string) bool {
	got := strings.ToLower(strings.TrimSpace(values["sign"]))
	if got == "" {
		return false
	}
	return got == EPayMD5Sign(values, key)
}

func AlipayRSA2Sign(values map[string]string, privateKey string) (string, error) {
	key, err := parseRSAPrivateKey(privateKey)
	if err != nil {
		return "", err
	}
	content := alipaySignContent(values)
	digest := sha256.Sum256([]byte(content))
	signature, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, digest[:])
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(signature), nil
}

func VerifyAlipayRSA2Sign(values map[string]string, publicKey string) bool {
	sign := strings.TrimSpace(values["sign"])
	if sign == "" {
		return false
	}
	return VerifyAlipayRSA2Payload(alipaySignContent(values), sign, publicKey)
}

func VerifyAlipayRSA2Payload(content, sign, publicKey string) bool {
	key, err := parseRSAPublicKey(publicKey)
	if err != nil {
		return false
	}
	signature, err := base64.StdEncoding.DecodeString(strings.TrimSpace(sign))
	if err != nil {
		return false
	}
	digest := sha256.Sum256([]byte(content))
	return rsa.VerifyPKCS1v15(key, crypto.SHA256, digest[:], signature) == nil
}

func alipaySignContent(values map[string]string) string {
	keys := make([]string, 0, len(values))
	for name, value := range values {
		if name == "sign" || name == "sign_type" || strings.TrimSpace(value) == "" {
			continue
		}
		keys = append(keys, name)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, name := range keys {
		parts = append(parts, name+"="+values[name])
	}
	return strings.Join(parts, "&")
}

func parseRSAPrivateKey(privateKey string) (*rsa.PrivateKey, error) {
	block, err := parsePEMBlock(privateKey, "PRIVATE KEY")
	if err != nil {
		return nil, err
	}
	if key, err := x509.ParsePKCS8PrivateKey(block.Bytes); err == nil {
		if rsaKey, ok := key.(*rsa.PrivateKey); ok {
			return rsaKey, nil
		}
		return nil, errors.New("private key is not RSA")
	}
	key, err := x509.ParsePKCS1PrivateKey(block.Bytes)
	if err != nil {
		return nil, err
	}
	return key, nil
}

func parseRSAPublicKey(publicKey string) (*rsa.PublicKey, error) {
	block, err := parsePEMBlock(publicKey, "PUBLIC KEY")
	if err != nil {
		return nil, err
	}
	if key, err := x509.ParsePKIXPublicKey(block.Bytes); err == nil {
		if rsaKey, ok := key.(*rsa.PublicKey); ok {
			return rsaKey, nil
		}
		return nil, errors.New("public key is not RSA")
	}
	key, err := x509.ParsePKCS1PublicKey(block.Bytes)
	if err != nil {
		return nil, err
	}
	return key, nil
}

func parsePEMBlock(text, defaultType string) (*pem.Block, error) {
	text = normalizePEMKey(text)
	if text == "" {
		return nil, errors.New("key is empty")
	}
	if !strings.Contains(text, "-----BEGIN") {
		text = "-----BEGIN " + defaultType + "-----\n" + wrapBase64Lines(text) + "\n-----END " + defaultType + "-----"
	}
	block, _ := pem.Decode([]byte(text))
	if block == nil {
		return nil, errors.New("invalid PEM key")
	}
	return block, nil
}

func normalizePEMKey(value string) string {
	value = strings.TrimSpace(value)
	value = strings.ReplaceAll(value, "\\n", "\n")
	return value
}

func wrapBase64Lines(value string) string {
	value = strings.Join(strings.Fields(value), "")
	if value == "" {
		return ""
	}
	var b strings.Builder
	for len(value) > 64 {
		b.WriteString(value[:64])
		b.WriteByte('\n')
		value = value[64:]
	}
	b.WriteString(value)
	return b.String()
}

func encodePaymentFields(fields map[string]string) string {
	values := url.Values{}
	keys := make([]string, 0, len(fields))
	for key := range fields {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		values.Set(key, fields[key])
	}
	return values.Encode()
}

func paymentNotifySuccess(values map[string]string) bool {
	status := strings.ToUpper(strings.TrimSpace(firstNonEmpty(values["trade_status"], values["status"])))
	switch status {
	case "TRADE_SUCCESS", "TRADE_FINISHED", "SUCCESS", "1":
		return true
	}
	return false
}

func stringMapFromStrings(values map[string]string) map[string]any {
	out := make(map[string]any, len(values))
	for key, value := range values {
		out[key] = value
	}
	return out
}
