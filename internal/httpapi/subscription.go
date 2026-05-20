package httpapi

import (
	"fmt"
	"net/http"
	"strings"

	"chatgpt2api/internal/service"
	"chatgpt2api/internal/util"

	qrcode "github.com/skip2/go-qrcode"
)

func (a *App) handleSubscriptionConfig(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.requireIdentity(w, r, ""); !ok {
		return
	}
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	util.WriteJSON(w, http.StatusOK, map[string]any{"config": a.subscriptions.PublicConfig()})
}

func (a *App) handleSubscriptionCheckout(w http.ResponseWriter, r *http.Request) {
	identity, ok := a.requireIdentity(w, r, "")
	if !ok {
		return
	}
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	body, err := readJSONMap(r)
	if err != nil {
		util.WriteError(w, http.StatusBadRequest, "invalid json body")
		return
	}
	checkout, err := a.subscriptions.CreateCheckout(identity, util.Clean(body["plan_id"]), util.Clean(body["pay_type"]), a.resolveImageBaseURL(r))
	if err != nil {
		util.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	util.WriteJSON(w, http.StatusOK, map[string]any{
		"order":        checkout.Order,
		"mode":         checkout.Mode,
		"action":       checkout.Action,
		"fields":       checkout.Fields,
		"url":          checkout.URL,
		"qr_code":      checkout.QRCode,
		"qr_image_url": checkout.QRImageURL,
	})
}

func (a *App) handleSubscriptionPaymentQR(w http.ResponseWriter, r *http.Request) {
	identity, ok := a.requireIdentity(w, r, "")
	if !ok {
		return
	}
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	orderID := util.Clean(r.URL.Query().Get("order"))
	order, found := a.subscriptions.OrderRecord(orderID)
	if !found {
		util.WriteError(w, http.StatusNotFound, "order not found")
		return
	}
	if identity.Role != service.AuthRoleAdmin && order.OwnerID != identityScope(identity) {
		util.WriteError(w, http.StatusForbidden, "permission denied")
		return
	}
	qrCode, ok := a.subscriptions.OrderQRCode(orderID)
	if !ok {
		util.WriteError(w, http.StatusNotFound, "payment qrcode not found")
		return
	}
	png, err := qrcode.Encode(qrCode, qrcode.Medium, 420)
	if err != nil {
		util.WriteError(w, http.StatusInternalServerError, "render payment qrcode failed")
		return
	}
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(png)
}

func (a *App) handleSubscriptionOrder(w http.ResponseWriter, r *http.Request) {
	identity, ok := a.requireIdentity(w, r, "")
	if !ok {
		return
	}
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	orderID := util.Clean(r.URL.Query().Get("id"))
	order, found := a.subscriptions.OrderRecord(orderID)
	if !found {
		util.WriteError(w, http.StatusNotFound, "order not found")
		return
	}
	if identity.Role != service.AuthRoleAdmin && order.OwnerID != identityScope(identity) {
		util.WriteError(w, http.StatusForbidden, "permission denied")
		return
	}
	util.WriteJSON(w, http.StatusOK, map[string]any{"order": map[string]any{
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
	}})
}

func (a *App) handleAdminSubscription(w http.ResponseWriter, r *http.Request) {
	identity, ok := a.requireIdentity(w, r, "")
	if !ok {
		return
	}
	if identity.Role != service.AuthRoleAdmin {
		util.WriteError(w, http.StatusForbidden, "permission denied")
		return
	}
	switch r.Method {
	case http.MethodGet:
		util.WriteJSON(w, http.StatusOK, map[string]any{
			"config": a.subscriptions.AdminConfig(),
			"orders": a.subscriptions.Orders(util.ToInt(r.URL.Query().Get("limit"), 100)),
		})
	case http.MethodPost:
		body, err := readJSONMap(r)
		if err != nil {
			util.WriteError(w, http.StatusBadRequest, "invalid json body")
			return
		}
		config, err := a.subscriptions.UpdateConfig(body)
		if err != nil {
			util.WriteError(w, http.StatusBadRequest, err.Error())
			return
		}
		util.WriteJSON(w, http.StatusOK, map[string]any{
			"config": config,
			"orders": a.subscriptions.Orders(100),
		})
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (a *App) handleSubscriptionPaymentNotify(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if err := r.ParseForm(); err != nil {
		util.WriteError(w, http.StatusBadRequest, "invalid payment notify")
		return
	}
	values := stringMapFromFormValues(r.Form)
	order, shouldGrant, err := a.subscriptions.CompleteNotify(values, func(order service.SubscriptionOrder) error {
		_, err := a.profiles.AdjustUserQuota(order.OwnerID, order.Quota)
		return err
	})
	if err != nil {
		category := subscriptionNotifyErrorCategory(err)
		a.logger.Warning("subscription payment notify rejected", "category", category, "error", err.Error(), "order", values["out_trade_no"])
		_, _ = fmt.Fprint(w, "fail")
		return
	}
	if shouldGrant {
		a.logger.Info("subscription quota granted", "order", order.ID, "owner", order.OwnerID, "quota", order.Quota)
	} else {
		a.logger.Info("subscription payment notify already applied", "order", order.ID, "owner", order.OwnerID, "status", order.Status, "quota_granted", order.QuotaGranted)
	}
	_, _ = fmt.Fprint(w, "success")
}

func subscriptionNotifyErrorCategory(err error) string {
	if err == nil {
		return "none"
	}
	text := strings.ToLower(err.Error())
	switch {
	case strings.Contains(text, "sign"):
		return "signature"
	case strings.Contains(text, "money") || strings.Contains(text, "amount"):
		return "amount"
	case strings.Contains(text, "order not found") || strings.Contains(text, "out_trade_no"):
		return "order"
	case strings.Contains(text, "successful") || strings.Contains(text, "trade"):
		return "status"
	case strings.Contains(text, "owner") || strings.Contains(text, "quota"):
		return "grant"
	default:
		return "system"
	}
}

func (a *App) handleSubscriptionPaymentReturn(w http.ResponseWriter, r *http.Request) {
	orderID := util.Clean(r.URL.Query().Get("out_trade_no"))
	target := "/subscription"
	if orderID != "" {
		target += "?order=" + orderID
	}
	http.Redirect(w, r, target, http.StatusFound)
}

func stringMapFromFormValues(values map[string][]string) map[string]string {
	out := map[string]string{}
	for key, item := range values {
		if len(item) == 0 {
			continue
		}
		out[key] = item[0]
	}
	return out
}
