package service

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"path/filepath"
	"testing"

	"chatgpt2api/internal/storage"
)

func TestEPayMD5SignIgnoresSignFields(t *testing.T) {
	fields := map[string]string{
		"pid":          "1000",
		"type":         "alipay",
		"out_trade_no": "sub_test",
		"name":         "创作版",
		"money":        "69.00",
		"sign":         "ignored",
		"sign_type":    "MD5",
	}
	sign := EPayMD5Sign(fields, "secret")
	fields["sign"] = sign
	if !VerifyEPayMD5Sign(fields, "secret") {
		t.Fatalf("expected signature to verify")
	}
	if VerifyEPayMD5Sign(fields, "wrong") {
		t.Fatalf("signature verified with wrong key")
	}
}

func TestAlipayRSA2SignAndVerify(t *testing.T) {
	privateKey, publicKey := testRSAKeyPair(t)
	fields := map[string]string{
		"app_id":      "2021000000000000",
		"method":      "alipay.trade.precreate",
		"charset":     "utf-8",
		"sign_type":   "RSA2",
		"timestamp":   "2026-05-18 12:00:00",
		"version":     "1.0",
		"biz_content": `{"out_trade_no":"sub_test","total_amount":"0.01","subject":"测试"}`,
	}
	sign, err := AlipayRSA2Sign(fields, privateKey)
	if err != nil {
		t.Fatalf("AlipayRSA2Sign error = %v", err)
	}
	fields["sign"] = sign
	if !VerifyAlipayRSA2Sign(fields, publicKey) {
		t.Fatalf("expected alipay signature to verify")
	}
	fields["biz_content"] = `{"out_trade_no":"sub_test","total_amount":"0.02","subject":"测试"}`
	if VerifyAlipayRSA2Sign(fields, publicKey) {
		t.Fatalf("tampered alipay signature verified")
	}
}

func TestSubscriptionNotifyIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	backend := storage.NewJSONBackend(filepath.Join(dir, "accounts.json"), filepath.Join(dir, "auth_keys.json"))
	svc := NewSubscriptionService(dir, backend)
	if _, err := svc.UpdateConfig(map[string]any{
		"enabled": true,
		"payment": map[string]any{
			"enabled":      true,
			"gateway_url":  "https://pay.example.com",
			"merchant_id":  "1000",
			"merchant_key": "secret",
			"pay_types":    []any{"alipay"},
			"site_name":    "1818.pro",
		},
	}); err != nil {
		t.Fatalf("UpdateConfig error = %v", err)
	}
	svc.mu.Lock()
	svc.orders = []SubscriptionOrder{{
		ID:      "sub_test",
		OwnerID: "user-1",
		PlanID:  "creator",
		Quota:   1500,
		Money:   "69.00",
		Status:  subscriptionOrderStatusPending,
	}}
	if err := svc.saveOrdersLocked(); err != nil {
		t.Fatalf("saveOrdersLocked error = %v", err)
	}
	svc.mu.Unlock()
	values := map[string]string{
		"pid":          "1000",
		"out_trade_no": "sub_test",
		"trade_no":     "remote-1",
		"trade_status": "TRADE_SUCCESS",
		"money":        "69.00",
	}
	values["sign"] = EPayMD5Sign(values, "secret")
	values["sign_type"] = "MD5"
	grantCount := 0
	order, grant, err := svc.CompleteNotify(values, func(order SubscriptionOrder) error {
		grantCount += order.Quota
		return nil
	})
	if err != nil {
		t.Fatalf("CompleteNotify first call error = %v", err)
	}
	if !grant || order.Status != subscriptionOrderStatusPaid {
		t.Fatalf("first notify should grant quota, grant=%v order=%#v", grant, order)
	}
	_, grant, err = svc.CompleteNotify(values, func(order SubscriptionOrder) error {
		grantCount += order.Quota
		return nil
	})
	if err != nil {
		t.Fatalf("CompleteNotify second call error = %v", err)
	}
	if grant {
		t.Fatalf("second notify should not grant quota again")
	}
	if grantCount != 1500 {
		t.Fatalf("quota grant count = %d, want 1500", grantCount)
	}
}

func TestSubscriptionNotifyDoesNotGrantWhenOrderSaveFails(t *testing.T) {
	dir := t.TempDir()
	backend := storage.NewJSONBackend(filepath.Join(dir, "accounts.json"), filepath.Join(dir, "auth_keys.json"))
	svc := NewSubscriptionService(dir, backend)
	if _, err := svc.UpdateConfig(map[string]any{
		"enabled": true,
		"payment": map[string]any{
			"enabled":      true,
			"gateway_url":  "https://pay.example.com",
			"merchant_id":  "1000",
			"merchant_key": "secret",
			"pay_types":    []any{"alipay"},
			"site_name":    "1818.pro",
		},
	}); err != nil {
		t.Fatalf("UpdateConfig error = %v", err)
	}
	svc.mu.Lock()
	svc.orders = []SubscriptionOrder{{
		ID:      "sub_save_fail",
		OwnerID: "user-1",
		PlanID:  "creator",
		Quota:   1500,
		Money:   "69.00",
		Status:  subscriptionOrderStatusPending,
	}}
	svc.store = failingSaveDocumentBackend{JSONDocumentBackend: backend, failName: subscriptionOrdersDocumentName}
	svc.mu.Unlock()
	values := signedEPayNotify("sub_save_fail", "69.00", "secret")
	_, grant, err := svc.CompleteNotify(values, func(order SubscriptionOrder) error {
		t.Fatalf("grant should not run when order save fails: %#v", order)
		return nil
	})
	if err == nil {
		t.Fatalf("CompleteNotify should fail when order save fails")
	}
	if grant {
		t.Fatalf("failed order save should not report quota grant")
	}
}

func TestSubscriptionNotifyRetriesGrantAfterGrantFailure(t *testing.T) {
	dir := t.TempDir()
	backend := storage.NewJSONBackend(filepath.Join(dir, "accounts.json"), filepath.Join(dir, "auth_keys.json"))
	svc := NewSubscriptionService(dir, backend)
	if _, err := svc.UpdateConfig(map[string]any{
		"enabled": true,
		"payment": map[string]any{
			"enabled":      true,
			"gateway_url":  "https://pay.example.com",
			"merchant_id":  "1000",
			"merchant_key": "secret",
			"pay_types":    []any{"alipay"},
			"site_name":    "1818.pro",
		},
	}); err != nil {
		t.Fatalf("UpdateConfig error = %v", err)
	}
	svc.mu.Lock()
	svc.orders = []SubscriptionOrder{{
		ID:      "sub_retry_grant",
		OwnerID: "user-1",
		PlanID:  "creator",
		Quota:   1500,
		Money:   "69.00",
		Status:  subscriptionOrderStatusPending,
	}}
	if err := svc.saveOrdersLocked(); err != nil {
		t.Fatalf("saveOrdersLocked error = %v", err)
	}
	svc.mu.Unlock()
	values := signedEPayNotify("sub_retry_grant", "69.00", "secret")
	grantCalls := 0
	_, grant, err := svc.CompleteNotify(values, func(order SubscriptionOrder) error {
		grantCalls++
		return errors.New("quota store unavailable")
	})
	if err == nil {
		t.Fatalf("CompleteNotify should return grant failure")
	}
	if grant {
		t.Fatalf("failed grant should not report success")
	}
	order, ok := svc.OrderRecord("sub_retry_grant")
	if !ok || order.Status != subscriptionOrderStatusPaid || order.QuotaGranted {
		t.Fatalf("order after failed grant = %#v found=%v", order, ok)
	}
	_, grant, err = svc.CompleteNotify(values, func(order SubscriptionOrder) error {
		grantCalls++
		return nil
	})
	if err != nil {
		t.Fatalf("CompleteNotify retry error = %v", err)
	}
	if !grant {
		t.Fatalf("retry should grant quota")
	}
	if grantCalls != 2 {
		t.Fatalf("grant calls = %d, want 2", grantCalls)
	}
	order, ok = svc.OrderRecord("sub_retry_grant")
	if !ok || order.Status != subscriptionOrderStatusPaid || !order.QuotaGranted {
		t.Fatalf("order after retry = %#v found=%v", order, ok)
	}
}

func TestAlipayOpenNotifyIsIdempotent(t *testing.T) {
	privateKey, publicKey := testRSAKeyPair(t)
	dir := t.TempDir()
	backend := storage.NewJSONBackend(filepath.Join(dir, "accounts.json"), filepath.Join(dir, "auth_keys.json"))
	svc := NewSubscriptionService(dir, backend)
	if _, err := svc.UpdateConfig(map[string]any{
		"enabled": true,
		"payment": map[string]any{
			"enabled":           true,
			"mode":              "alipay_open",
			"gateway_url":       "https://openapi.alipay.com/gateway.do",
			"merchant_id":       "2021000000000000",
			"app_private_key":   privateKey,
			"alipay_public_key": publicKey,
			"pay_types":         []any{"alipay"},
			"site_name":         "1818.pro",
		},
	}); err != nil {
		t.Fatalf("UpdateConfig error = %v", err)
	}
	svc.mu.Lock()
	svc.orders = []SubscriptionOrder{{
		ID:      "sub_alipay",
		OwnerID: "user-1",
		PlanID:  "starter",
		Quota:   300,
		Money:   "0.01",
		Status:  subscriptionOrderStatusPending,
	}}
	if err := svc.saveOrdersLocked(); err != nil {
		t.Fatalf("saveOrdersLocked error = %v", err)
	}
	svc.mu.Unlock()
	values := map[string]string{
		"app_id":           "2021000000000000",
		"out_trade_no":     "sub_alipay",
		"trade_no":         "2026051822000000001",
		"trade_status":     "TRADE_SUCCESS",
		"total_amount":     "0.01",
		"buyer_pay_amount": "0.01",
		"notify_time":      "2026-05-18 12:00:00",
		"notify_type":      "trade_status_sync",
		"notify_id":        "notify-test",
	}
	sign, err := AlipayRSA2Sign(values, privateKey)
	if err != nil {
		t.Fatalf("AlipayRSA2Sign notify error = %v", err)
	}
	values["sign"] = sign
	values["sign_type"] = "RSA2"
	grantCount := 0
	order, grant, err := svc.CompleteNotify(values, func(order SubscriptionOrder) error {
		grantCount += order.Quota
		return nil
	})
	if err != nil {
		t.Fatalf("CompleteNotify first call error = %v", err)
	}
	if !grant || order.Status != subscriptionOrderStatusPaid {
		t.Fatalf("first alipay notify should grant quota, grant=%v order=%#v", grant, order)
	}
	_, grant, err = svc.CompleteNotify(values, func(order SubscriptionOrder) error {
		grantCount += order.Quota
		return nil
	})
	if err != nil {
		t.Fatalf("CompleteNotify second call error = %v", err)
	}
	if grant || grantCount != 300 {
		t.Fatalf("duplicate alipay notify grant=%v grantCount=%d", grant, grantCount)
	}
}

func signedEPayNotify(orderID, money, key string) map[string]string {
	values := map[string]string{
		"pid":          "1000",
		"out_trade_no": orderID,
		"trade_no":     "remote-1",
		"trade_status": "TRADE_SUCCESS",
		"money":        money,
	}
	values["sign"] = EPayMD5Sign(values, key)
	values["sign_type"] = "MD5"
	return values
}

type failingSaveDocumentBackend struct {
	storage.JSONDocumentBackend
	failName string
}

func (b failingSaveDocumentBackend) SaveJSONDocument(name string, value any) error {
	if name == b.failName {
		return errors.New("forced save failure")
	}
	return b.JSONDocumentBackend.SaveJSONDocument(name, value)
}

func TestSubscriptionNotifyRequiresExplicitSuccessStatus(t *testing.T) {
	dir := t.TempDir()
	backend := storage.NewJSONBackend(filepath.Join(dir, "accounts.json"), filepath.Join(dir, "auth_keys.json"))
	svc := NewSubscriptionService(dir, backend)
	if _, err := svc.UpdateConfig(map[string]any{
		"enabled": true,
		"payment": map[string]any{
			"enabled":      true,
			"gateway_url":  "https://pay.example.com",
			"merchant_id":  "1000",
			"merchant_key": "secret",
			"pay_types":    []any{"alipay"},
			"site_name":    "1818.pro",
		},
	}); err != nil {
		t.Fatalf("UpdateConfig error = %v", err)
	}
	svc.mu.Lock()
	svc.orders = []SubscriptionOrder{{
		ID:      "sub_checkout_replay",
		OwnerID: "user-1",
		PlanID:  "creator",
		Quota:   1500,
		Money:   "69.00",
		Status:  subscriptionOrderStatusPending,
	}}
	if err := svc.saveOrdersLocked(); err != nil {
		t.Fatalf("saveOrdersLocked error = %v", err)
	}
	svc.mu.Unlock()
	values := map[string]string{
		"pid":          "1000",
		"type":         "alipay",
		"out_trade_no": "sub_checkout_replay",
		"name":         "创作版",
		"money":        "69.00",
		"notify_url":   "https://example.test/api/subscription/payment/notify",
		"return_url":   "https://example.test/api/subscription/payment/return?out_trade_no=sub_checkout_replay",
		"sitename":     "1818.pro",
	}
	values["sign"] = EPayMD5Sign(values, "secret")
	values["sign_type"] = "MD5"
	_, grant, err := svc.CompleteNotify(values, func(order SubscriptionOrder) error {
		t.Fatalf("grant should not be called for checkout-field replay: %#v", order)
		return nil
	})
	if err == nil {
		t.Fatalf("CompleteNotify should reject notify without success status")
	}
	if grant {
		t.Fatalf("notify without success status granted quota")
	}
	order, ok := svc.OrderRecord("sub_checkout_replay")
	if !ok || order.Status != subscriptionOrderStatusPending || order.QuotaGranted {
		t.Fatalf("order after rejected replay = %#v found=%v", order, ok)
	}
}

func TestSubscriptionOrderRecord(t *testing.T) {
	dir := t.TempDir()
	backend := storage.NewJSONBackend(filepath.Join(dir, "accounts.json"), filepath.Join(dir, "auth_keys.json"))
	svc := NewSubscriptionService(dir, backend)
	svc.mu.Lock()
	svc.orders = []SubscriptionOrder{{ID: "sub_owner", OwnerID: "owner-1", Quota: 300}}
	if err := svc.saveOrdersLocked(); err != nil {
		t.Fatalf("saveOrdersLocked error = %v", err)
	}
	svc.mu.Unlock()
	order, ok := svc.OrderRecord("sub_owner")
	if !ok {
		t.Fatalf("expected order record")
	}
	if order.OwnerID != "owner-1" || order.Quota != 300 {
		t.Fatalf("unexpected order record: %#v", order)
	}
}

func testRSAKeyPair(t *testing.T) (string, string) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("GenerateKey error = %v", err)
	}
	privateDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatalf("MarshalPKCS8PrivateKey error = %v", err)
	}
	publicDER, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	if err != nil {
		t.Fatalf("MarshalPKIXPublicKey error = %v", err)
	}
	privatePEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: privateDER})
	publicPEM := pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: publicDER})
	return string(privatePEM), base64.StdEncoding.EncodeToString(publicDER[:]) + "\n" + string(publicPEM)
}
