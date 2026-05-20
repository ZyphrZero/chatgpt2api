package service

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"chatgpt2api/internal/storage"
)

type testAccountConfig struct{}

func (testAccountConfig) AutoRemoveInvalidAccounts() bool     { return false }
func (testAccountConfig) AutoRemoveRateLimitedAccounts() bool { return false }
func (testAccountConfig) Proxy() string                       { return "" }

type testAutoRemoveAccountConfig struct{ testAccountConfig }

func (testAutoRemoveAccountConfig) AutoRemoveInvalidAccounts() bool { return true }

func TestFetchRemoteInfoBootstrapsBeforeAccountRefresh(t *testing.T) {
	var mu sync.Mutex
	var paths []string
	bootstrapped := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		paths = append(paths, r.URL.Path)
		mu.Unlock()

		switch r.URL.Path {
		case "/":
			if auth := r.Header.Get("Authorization"); auth != "" {
				t.Errorf("bootstrap request leaked authorization header %q", auth)
			}
			bootstrapped = true
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("<html>ok</html>"))
		case "/backend-api/me":
			if !bootstrapped {
				w.WriteHeader(http.StatusForbidden)
				return
			}
			if got := r.Header.Get("Authorization"); got != "Bearer token-1" {
				t.Errorf("Authorization = %q, want bearer token", got)
			}
			writeJSON(t, w, map[string]any{"email": "user@example.com", "id": "user-1"})
		case "/backend-api/conversation/init":
			if !bootstrapped {
				w.WriteHeader(http.StatusForbidden)
				return
			}
			writeJSON(t, w, map[string]any{
				"default_model_slug": "gpt-5",
				"limits_progress": []map[string]any{{
					"feature_name": "image_gen",
					"remaining":    7,
					"reset_after":  "2026-05-01T00:00:00Z",
				}},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	accounts := newTestAccountService(t)
	accounts.remoteBaseURL = server.URL
	accounts.browserHTTPClient = func(string, time.Duration) *http.Client {
		return server.Client()
	}
	accounts.AddAccounts([]string{"token-1"})

	info, err := accounts.FetchRemoteInfo(context.Background(), "token-1")
	if err != nil {
		t.Fatalf("FetchRemoteInfo() error = %v", err)
	}
	if info["email"] != "user@example.com" || info["quota"] != 7 {
		t.Fatalf("FetchRemoteInfo() = %#v", info)
	}
	mu.Lock()
	gotPaths := append([]string(nil), paths...)
	mu.Unlock()
	wantPaths := []string{"/", "/backend-api/me", "/backend-api/conversation/init"}
	if !reflect.DeepEqual(gotPaths, wantPaths) {
		t.Fatalf("request paths = %#v, want %#v", gotPaths, wantPaths)
	}
}

func TestFetchRemoteInfoSummarizesForbiddenChallenge(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("<html>ok</html>"))
		case "/backend-api/me":
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`<html><script>window._cf_chl_opt={}</script>Enable JavaScript and cookies to continue</html>`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	accounts := newTestAccountService(t)
	accounts.remoteBaseURL = server.URL
	accounts.browserHTTPClient = func(string, time.Duration) *http.Client {
		return server.Client()
	}
	accounts.AddAccounts([]string{"token-1"})

	_, err := accounts.FetchRemoteInfo(context.Background(), "token-1")
	if err == nil {
		t.Fatal("FetchRemoteInfo() error = nil")
	}
	if got := err.Error(); !strings.Contains(got, "/backend-api/me failed: HTTP 403") || !strings.Contains(got, "upstream returned Cloudflare challenge page") {
		t.Fatalf("FetchRemoteInfo() error = %q", got)
	}
}

func TestRefreshAccountsReturnsEmptyErrorsArray(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("<html>ok</html>"))
		case "/backend-api/me":
			writeJSON(t, w, map[string]any{"email": "user@example.com", "id": "user-1"})
		case "/backend-api/conversation/init":
			writeJSON(t, w, map[string]any{
				"default_model_slug": "gpt-5",
				"limits_progress": []map[string]any{{
					"feature_name": "image_gen",
					"remaining":    7,
				}},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	accounts := newTestAccountService(t)
	accounts.remoteBaseURL = server.URL
	accounts.browserHTTPClient = func(string, time.Duration) *http.Client {
		return server.Client()
	}
	accounts.AddAccounts([]string{"token-1"})

	result := accounts.RefreshAccounts(context.Background(), []string{"token-1"})
	if result["refreshed"] != 1 {
		t.Fatalf("refreshed = %#v, want 1", result["refreshed"])
	}
	errors, ok := result["errors"].([]map[string]string)
	if !ok {
		t.Fatalf("errors type = %T, want []map[string]string", result["errors"])
	}
	if errors == nil || len(errors) != 0 {
		t.Fatalf("errors = %#v, want empty non-nil slice", errors)
	}

	data, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("marshal result: %v", err)
	}
	var payload struct {
		Errors json.RawMessage `json:"errors"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if string(payload.Errors) != "[]" {
		t.Fatalf("encoded errors = %s, want []", payload.Errors)
	}
}

func TestRefreshAccountStateMarksUnauthorizedInitAsInvalid(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("<html>ok</html>"))
		case "/backend-api/me":
			writeJSON(t, w, map[string]any{"email": "user@example.com", "id": "user-1"})
		case "/backend-api/conversation/init":
			w.WriteHeader(http.StatusUnauthorized)
			writeJSON(t, w, map[string]any{"detail": "token_invalidated"})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	accounts := newTestAccountService(t)
	accounts.remoteBaseURL = server.URL
	accounts.browserHTTPClient = func(string, time.Duration) *http.Client {
		return server.Client()
	}
	accounts.AddAccounts([]string{"token-1"})
	accounts.UpdateAccount("token-1", map[string]any{"status": "正常", "quota": 5})

	account := accounts.RefreshAccountState(context.Background(), "token-1")
	if account == nil {
		t.Fatal("RefreshAccountState() = nil, want updated invalid account")
	}
	if account["status"] != "异常" {
		t.Fatalf("status = %#v, want 异常", account["status"])
	}
	if account["quota"] != 0 {
		t.Fatalf("quota = %#v, want 0", account["quota"])
	}
}

func TestRefreshAccountStateKeepsInvalidAccountWhenAutoRemoveEnabled(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("<html>ok</html>"))
		case "/backend-api/me":
			writeJSON(t, w, map[string]any{"email": "user@example.com", "id": "user-1"})
		case "/backend-api/conversation/init":
			w.WriteHeader(http.StatusUnauthorized)
			writeJSON(t, w, map[string]any{"detail": "token_invalidated"})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	dir := t.TempDir()
	accounts := NewAccountService(
		storage.NewJSONBackend(filepath.Join(dir, "accounts.json"), filepath.Join(dir, "auth_keys.json")),
		testAutoRemoveAccountConfig{},
		NewProxyService(testAutoRemoveAccountConfig{}),
		NewLogService(dir),
	)
	accounts.remoteBaseURL = server.URL
	accounts.browserHTTPClient = func(string, time.Duration) *http.Client {
		return server.Client()
	}
	accounts.AddAccounts([]string{"token-1"})
	accounts.UpdateAccount("token-1", map[string]any{"status": "正常", "quota": 5})

	account := accounts.RefreshAccountState(context.Background(), "token-1")
	if account == nil {
		t.Fatal("RefreshAccountState() = nil, want retained invalid account")
	}
	if account["status"] != "异常" {
		t.Fatalf("status = %#v, want 异常", account["status"])
	}
	if got := accounts.GetAccount("token-1"); got == nil {
		t.Fatal("GetAccount() = nil, heartbeat must not delete account")
	}
}

func TestRefreshAccountsMarksRateLimitedResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("<html>ok</html>"))
		case "/backend-api/me":
			writeJSON(t, w, map[string]any{"email": "user@example.com", "id": "user-1"})
		case "/backend-api/conversation/init":
			w.WriteHeader(http.StatusTooManyRequests)
			writeJSON(t, w, map[string]any{"error": map[string]any{"message": "You've reached the image generation limit"}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	accounts := newTestAccountService(t)
	accounts.remoteBaseURL = server.URL
	accounts.browserHTTPClient = func(string, time.Duration) *http.Client {
		return server.Client()
	}
	accounts.AddAccounts([]string{"token-1"})
	accounts.UpdateAccount("token-1", map[string]any{"status": "正常", "quota": 5})

	result := accounts.RefreshAccounts(context.Background(), []string{"token-1"})
	if result["refreshed"] != 0 {
		t.Fatalf("refreshed = %#v, want 0", result["refreshed"])
	}
	errors, ok := result["errors"].([]map[string]string)
	if !ok || len(errors) != 1 {
		t.Fatalf("errors = %#v, want one error", result["errors"])
	}
	if errors[0]["error"] != "检测到限流" {
		t.Fatalf("error = %q, want 检测到限流", errors[0]["error"])
	}
	account := accounts.GetAccount("token-1")
	if account["status"] != "限流" {
		t.Fatalf("status = %#v, want 限流", account["status"])
	}
	if account["quota"] != 0 {
		t.Fatalf("quota = %#v, want 0", account["quota"])
	}
	if account["image_quota_unknown"] != false {
		t.Fatalf("image_quota_unknown = %#v, want false", account["image_quota_unknown"])
	}
	if result["removed"] != 0 {
		t.Fatalf("removed = %#v, want 0", result["removed"])
	}
}

func TestRefreshAccountsKeepsHeartbeatFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("<html>ok</html>"))
		case "/backend-api/me":
			writeJSON(t, w, map[string]any{"email": "user@example.com", "id": "user-1"})
		case "/backend-api/conversation/init":
			w.WriteHeader(http.StatusBadGateway)
			writeJSON(t, w, map[string]any{"error": map[string]any{"message": "upstream connection reset"}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	accounts := newTestAccountService(t)
	accounts.remoteBaseURL = server.URL
	accounts.browserHTTPClient = func(string, time.Duration) *http.Client {
		return server.Client()
	}
	accounts.AddAccounts([]string{"token-1"})
	accounts.UpdateAccount("token-1", map[string]any{"status": "正常", "quota": 5})

	result := accounts.RefreshAccounts(context.Background(), []string{"token-1"})
	if result["refreshed"] != 0 {
		t.Fatalf("refreshed = %#v, want 0", result["refreshed"])
	}
	if result["removed"] != 0 {
		t.Fatalf("removed = %#v, want 0", result["removed"])
	}
	errors, ok := result["errors"].([]map[string]string)
	if !ok || len(errors) != 1 {
		t.Fatalf("errors = %#v, want one error", result["errors"])
	}
	account := accounts.GetAccount("token-1")
	if account == nil {
		t.Fatal("GetAccount() = nil, want heartbeat failure account retained")
	}
	if got := strings.TrimSpace(toString(account["check_error"])); got == "" {
		t.Fatalf("check_error = %q, want heartbeat failure recorded", got)
	}
	items, ok := result["items"].([]map[string]any)
	if !ok || len(items) != 1 {
		t.Fatalf("items = %#v, want retained account list", result["items"])
	}
}

func TestGetAvailableAccessTokenReservesKnownImageQuota(t *testing.T) {
	accounts := newTestAccountService(t)
	server := newAccountQuotaServer(t, map[string]any{
		"email": "user@example.com",
		"id":    "user-1",
	}, []map[string]any{{
		"feature_name": "image_gen",
		"remaining":    1,
	}})
	defer server.Close()
	accounts.remoteBaseURL = server.URL
	accounts.browserHTTPClient = func(string, time.Duration) *http.Client {
		return server.Client()
	}
	accounts.AddAccounts([]string{"token-1"})
	accounts.UpdateAccount("token-1", map[string]any{"status": "正常", "quota": 1})

	token, err := accounts.GetAvailableAccessToken(context.Background())
	if err != nil {
		t.Fatalf("first GetAvailableAccessToken() error = %v", err)
	}
	if token != "token-1" {
		t.Fatalf("first token = %q, want token-1", token)
	}

	if token, err := accounts.GetAvailableAccessToken(context.Background()); err == nil {
		t.Fatalf("second GetAvailableAccessToken() = %q, want no available image quota", token)
	}

	accounts.MarkImageResult("token-1", false)
	token, err = accounts.GetAvailableAccessToken(context.Background())
	if err != nil {
		t.Fatalf("GetAvailableAccessToken() after failed result error = %v", err)
	}
	if token != "token-1" {
		t.Fatalf("token after failed result = %q, want token-1", token)
	}

	accounts.MarkImageResult("token-1", true)
	if token, err := accounts.GetAvailableAccessToken(context.Background()); err == nil {
		t.Fatalf("GetAvailableAccessToken() after quota consumed = %q, want no available image quota", token)
	}
}

func TestGetAvailableImageAccessTokenDoesNotReuseInFlightKnownQuota(t *testing.T) {
	accounts := newTestAccountService(t)
	accounts.AddAccounts([]string{"token-1"})
	accounts.UpdateAccount("token-1", map[string]any{"status": "正常", "quota": 5})

	token, err := accounts.GetAvailableImageAccessToken(context.Background(), false)
	if err != nil {
		t.Fatalf("first GetAvailableImageAccessToken() error = %v", err)
	}
	if token != "token-1" {
		t.Fatalf("first token = %q, want token-1", token)
	}
	if token, err := accounts.GetAvailableImageAccessToken(context.Background(), false); err == nil {
		t.Fatalf("second GetAvailableImageAccessToken() = %q, want no reusable in-flight token", token)
	}
	accounts.MarkImageResult("token-1", false)
	token, err = accounts.GetAvailableImageAccessToken(context.Background(), false)
	if err != nil {
		t.Fatalf("GetAvailableImageAccessToken() after release error = %v", err)
	}
	if token != "token-1" {
		t.Fatalf("token after release = %q, want token-1", token)
	}
}

func TestGetAvailableAccessTokenLimitsUnknownImageQuotaToOneInFlight(t *testing.T) {
	accounts := newTestAccountService(t)
	server := newAccountQuotaServer(t, map[string]any{
		"email":     "plus@example.com",
		"id":        "user-1",
		"plan_type": "plus",
	}, nil)
	defer server.Close()
	accounts.remoteBaseURL = server.URL
	accounts.browserHTTPClient = func(string, time.Duration) *http.Client {
		return server.Client()
	}
	accounts.AddAccounts([]string{"token-1"})
	accounts.UpdateAccount("token-1", map[string]any{"status": "正常", "quota": 0, "image_quota_unknown": true, "type": "Plus"})

	token, err := accounts.GetAvailableAccessToken(context.Background())
	if err != nil {
		t.Fatalf("first GetAvailableAccessToken() error = %v", err)
	}
	if token != "token-1" {
		t.Fatalf("first token = %q, want token-1", token)
	}

	if token, err := accounts.GetAvailableAccessToken(context.Background()); err == nil {
		t.Fatalf("second GetAvailableAccessToken() = %q, want no available image quota", token)
	}

	accounts.MarkImageResult("token-1", false)
	token, err = accounts.GetAvailableAccessToken(context.Background())
	if err != nil {
		t.Fatalf("GetAvailableAccessToken() after release error = %v", err)
	}
	if token != "token-1" {
		t.Fatalf("token after release = %q, want token-1", token)
	}
	accounts.MarkImageResult("token-1", false)
}

func TestGetAvailableAccessTokenUsesFreshCheckCache(t *testing.T) {
	accounts := newTestAccountService(t)
	accounts.AddAccounts([]string{"token-1"})
	accounts.UpdateAccount("token-1", map[string]any{
		"status":     "正常",
		"quota":      2,
		"checked_at": time.Now().UTC().Format(time.RFC3339Nano),
	})
	accounts.browserHTTPClient = func(string, time.Duration) *http.Client {
		t.Fatal("fresh account should not be refreshed before use")
		return nil
	}

	token, err := accounts.GetAvailableAccessToken(context.Background())
	if err != nil {
		t.Fatalf("GetAvailableAccessToken() error = %v", err)
	}
	if token != "token-1" {
		t.Fatalf("token = %q, want token-1", token)
	}
	accounts.MarkImageResult("token-1", false)
}

func TestGetAvailableAccessTokenUsesCachedAccountWithoutRefresh(t *testing.T) {
	accounts := newTestAccountService(t)
	accounts.browserHTTPClient = func(string, time.Duration) *http.Client {
		t.Fatal("image token selection should not synchronously refresh remote account state")
		return nil
	}
	accounts.AddAccounts([]string{"token-1"})
	accounts.UpdateAccount("token-1", map[string]any{"status": "正常", "quota": 5})

	token, err := accounts.GetAvailableAccessToken(context.Background())
	if err != nil {
		t.Fatalf("GetAvailableAccessToken() error = %v", err)
	}
	if token != "token-1" {
		t.Fatalf("token = %q, want token-1", token)
	}
	accounts.MarkImageResult("token-1", false)
}

func TestGetTextAccessTokenRoundRobin(t *testing.T) {
	accounts := newTestAccountService(t)
	accounts.AddAccounts([]string{"token-1", "token-2"})
	accounts.UpdateAccount("token-1", map[string]any{"status": "正常"})
	accounts.UpdateAccount("token-2", map[string]any{"status": "正常"})

	got := []string{
		accounts.GetTextAccessToken(),
		accounts.GetTextAccessToken(),
		accounts.GetTextAccessToken(),
	}
	want := []string{"token-1", "token-2", "token-1"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("round robin tokens = %#v, want %#v", got, want)
	}
}

func TestReserveNextCandidateTokenCanFilterPaidAccounts(t *testing.T) {
	accounts := newTestAccountService(t)
	accounts.AddAccounts([]string{"free-token", "plus-token"})
	accounts.UpdateAccount("free-token", map[string]any{"status": "正常", "quota": 5, "type": "Free"})
	accounts.UpdateAccount("plus-token", map[string]any{"status": "正常", "quota": 5, "type": "Plus"})

	reservation, err := accounts.reserveNextCandidateToken(map[string]struct{}{}, IsPaidImageAccount)
	if err != nil {
		t.Fatalf("reserveNextCandidateToken() error = %v", err)
	}
	if reservation.token != "plus-token" {
		t.Fatalf("reserved token = %q, want plus-token", reservation.token)
	}
	accounts.releaseImageReservation(reservation.token)

	_, err = accounts.reserveNextCandidateToken(map[string]struct{}{"plus-token": struct{}{}}, IsPaidImageAccount)
	if err == nil {
		t.Fatal("reserveNextCandidateToken() error = nil, want no available paid token")
	}
}

func TestReserveCandidateBatchRotatesLeaderWhenBatchCoversAllPaidAccounts(t *testing.T) {
	accounts := newTestAccountService(t)
	accounts.AddAccounts([]string{"plus-1", "plus-2"})
	accounts.UpdateAccount("plus-1", map[string]any{"status": "正常", "quota": 5, "type": "Plus", "checked_at": time.Now().UTC().Format(time.RFC3339Nano)})
	accounts.UpdateAccount("plus-2", map[string]any{"status": "正常", "quota": 5, "type": "ProLite", "checked_at": time.Now().UTC().Format(time.RFC3339Nano)})

	first, err := accounts.reserveCandidateBatch(map[string]struct{}{}, IsPaidImageAccount, accountCandidateBatchSize)
	if err != nil {
		t.Fatalf("first reserveCandidateBatch() error = %v", err)
	}
	if len(first) != 2 || first[0].token != "plus-1" {
		t.Fatalf("first batch = %#v, want plus-1 as leader", first)
	}
	accounts.releaseImageReservationsExcept(first, "")

	second, err := accounts.reserveCandidateBatch(map[string]struct{}{}, IsPaidImageAccount, accountCandidateBatchSize)
	if err != nil {
		t.Fatalf("second reserveCandidateBatch() error = %v", err)
	}
	if len(second) != 2 || second[0].token != "plus-2" {
		t.Fatalf("second batch = %#v, want plus-2 as leader", second)
	}
	accounts.releaseImageReservationsExcept(second, "")
}

func TestGetAvailableAccessTokenPrefersHealthyImageAccounts(t *testing.T) {
	accounts := newTestAccountService(t)
	accounts.AddAccounts([]string{"bad-paid", "healthy-free"})
	accounts.UpdateAccount("bad-paid", map[string]any{"status": "正常", "quota": 5, "type": "Plus", "success": 100, "fail": 90})
	accounts.UpdateAccount("healthy-free", map[string]any{"status": "正常", "quota": 5, "type": "Free", "success": 1, "fail": 0})

	token, err := accounts.GetAvailableImageAccessToken(context.Background(), false)
	if err != nil {
		t.Fatalf("GetAvailableImageAccessToken() error = %v", err)
	}
	if token != "healthy-free" {
		t.Fatalf("token = %q, want healthy-free", token)
	}
	accounts.MarkImageResult(token, true)
}

func TestGetAvailableImageAccessTokenDoesNotStarveUntestedAccountsBehindNoisyProvenAccounts(t *testing.T) {
	accounts := newTestAccountService(t)
	accounts.AddAccounts([]string{"untested-free", "proven-plus"})
	accounts.UpdateAccount("untested-free", map[string]any{"status": "正常", "quota": 25, "type": "Free", "success": 0, "fail": 0})
	accounts.UpdateAccount("proven-plus", map[string]any{"status": "正常", "quota": 25, "type": "Plus", "success": 111, "fail": 89})

	token, err := accounts.GetAvailableImageAccessToken(context.Background(), false)
	if err != nil {
		t.Fatalf("GetAvailableImageAccessToken() error = %v", err)
	}
	if token != "untested-free" {
		t.Fatalf("token = %q, want untested-free", token)
	}
	accounts.MarkImageResult(token, true)
}

func TestGetAvailableImageAccessTokenRoundRobinsHealthyWindow(t *testing.T) {
	accounts := newTestAccountService(t)
	accounts.AddAccounts([]string{"token-1", "token-2", "token-3"})
	accounts.UpdateAccount("token-1", map[string]any{"status": "正常", "quota": 5, "type": "Free", "success": 1, "fail": 0})
	accounts.UpdateAccount("token-2", map[string]any{"status": "正常", "quota": 5, "type": "Free", "success": 0, "fail": 0})
	accounts.UpdateAccount("token-3", map[string]any{"status": "正常", "quota": 5, "type": "Plus", "success": 1, "fail": 0})

	got := make([]string, 0, 4)
	for range 4 {
		token, err := accounts.GetAvailableImageAccessToken(context.Background(), false)
		if err != nil {
			t.Fatalf("GetAvailableImageAccessToken() error = %v", err)
		}
		got = append(got, token)
		accounts.ReleaseImageSlot(token)
	}
	want := []string{"token-1", "token-2", "token-3", "token-1"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("tokens = %#v, want %#v", got, want)
	}
}

func TestGetAvailableImageAccessTokenPrefersHealthyPaidAccount(t *testing.T) {
	accounts := newTestAccountService(t)
	accounts.AddAccounts([]string{"healthy-free", "healthy-paid"})
	accounts.UpdateAccount("healthy-free", map[string]any{"status": "正常", "quota": 5, "type": "Free", "success": 1, "fail": 0})
	accounts.UpdateAccount("healthy-paid", map[string]any{"status": "正常", "quota": 5, "type": "Plus", "success": 1, "fail": 0})

	token, err := accounts.GetAvailableImageAccessToken(context.Background(), true)
	if err != nil {
		t.Fatalf("GetAvailableImageAccessToken() error = %v", err)
	}
	if token != "healthy-paid" {
		t.Fatalf("token = %q, want healthy-paid", token)
	}
	accounts.MarkImageResult(token, true)
}

func TestGetAvailableImageAccessTokenAllowsHealthyFreeOverUnhealthyPaid(t *testing.T) {
	accounts := newTestAccountService(t)
	accounts.AddAccounts([]string{"bad-paid", "healthy-free"})
	accounts.UpdateAccount("bad-paid", map[string]any{"status": "正常", "quota": 5, "type": "Plus", "success": 100, "fail": 90})
	accounts.UpdateAccount("healthy-free", map[string]any{"status": "正常", "quota": 5, "type": "Free", "success": 1, "fail": 0})

	token, err := accounts.GetAvailableImageAccessToken(context.Background(), true)
	if err != nil {
		t.Fatalf("GetAvailableImageAccessToken() error = %v", err)
	}
	if token != "healthy-free" {
		t.Fatalf("token = %q, want healthy-free", token)
	}
	accounts.MarkImageResult(token, true)
}

func TestGetAvailableImageAccessTokenFallsBackWhenPaidUnavailable(t *testing.T) {
	accounts := newTestAccountService(t)
	accounts.AddAccounts([]string{"limited-paid", "healthy-free"})
	accounts.UpdateAccount("limited-paid", map[string]any{"status": "限流", "quota": 0, "type": "Plus"})
	accounts.UpdateAccount("healthy-free", map[string]any{"status": "正常", "quota": 5, "type": "Free"})

	token, err := accounts.GetAvailableImageAccessToken(context.Background(), true)
	if err != nil {
		t.Fatalf("GetAvailableImageAccessToken() error = %v", err)
	}
	if token != "healthy-free" {
		t.Fatalf("token = %q, want healthy-free fallback", token)
	}
	accounts.MarkImageResult(token, true)
}

func TestApplyAccountErrorMessageDetectsImageStreamFailures(t *testing.T) {
	accounts := newTestAccountService(t)
	accounts.AddAccounts([]string{"token-invalid", "token-limited"})
	accounts.UpdateAccount("token-invalid", map[string]any{"status": "正常", "quota": 5})
	accounts.UpdateAccount("token-limited", map[string]any{"status": "正常", "quota": 5, "image_quota_unknown": true})

	message, handled := accounts.ApplyAccountErrorMessage("token-invalid", "image_stream", "auth_chat_requirements failed: status=401, body={\"detail\":\"token_invalidated\"}")
	if !handled || message != "检测到封号" {
		t.Fatalf("invalid handled = %v message = %q, want 检测到封号", handled, message)
	}
	if account := accounts.GetAccount("token-invalid"); account["status"] != "异常" || account["quota"] != 0 {
		t.Fatalf("invalid account = %#v, want status 异常 quota 0", account)
	}

	accounts.AddAccounts([]string{"token-expired"})
	accounts.UpdateAccount("token-expired", map[string]any{"status": "正常", "quota": 5})
	message, handled = accounts.ApplyAccountErrorMessage("token-expired", "text_stream", "auth failed: access token expired")
	if !handled || message != "检测到封号" {
		t.Fatalf("expired handled = %v message = %q, want 检测到封号", handled, message)
	}
	if account := accounts.GetAccount("token-expired"); account["status"] != "异常" || account["quota"] != 0 {
		t.Fatalf("expired account = %#v, want status 异常 quota 0", account)
	}

	message, handled = accounts.ApplyAccountErrorMessage("token-limited", "image_stream", "You've reached the image generation limit for now.")
	if !handled || message != "检测到限流" {
		t.Fatalf("limited handled = %v message = %q, want 检测到限流", handled, message)
	}
	if account := accounts.GetAccount("token-limited"); account["status"] != "限流" || account["quota"] != 0 || account["image_quota_unknown"] != false {
		t.Fatalf("limited account = %#v, want status 限流 quota 0 known quota", account)
	}
}

func TestApplyAccountErrorMessageIgnoresBootstrapFailures(t *testing.T) {
	accounts := newTestAccountService(t)
	accounts.AddAccounts([]string{"token-1"})
	accounts.UpdateAccount("token-1", map[string]any{"status": "正常", "quota": 5})

	message, handled := accounts.ApplyAccountErrorMessage("token-1", "refresh_accounts", "bootstrap failed: HTTP 429, body=too many requests")
	if handled {
		t.Fatalf("handled = true message = %q, want ignored bootstrap failure", message)
	}
	account := accounts.GetAccount("token-1")
	if account["status"] != "正常" || account["quota"] != 5 {
		t.Fatalf("account = %#v, want unchanged normal account", account)
	}
}

func TestApplyAccountErrorMessageCoolsDownImageStreamBootstrapTransportFailure(t *testing.T) {
	accounts := newTestAccountService(t)
	accounts.AddAccounts([]string{"token-1"})
	accounts.UpdateAccount("token-1", map[string]any{"status": "正常", "quota": 5})

	message, handled := accounts.ApplyAccountErrorMessage("token-1", "image_stream", "bootstrap failed: TLS connect error: connection reset by peer")
	if !handled {
		t.Fatal("handled = false, want image stream bootstrap transport cooldown")
	}
	if !strings.Contains(message, "upstream connection failed before TLS handshake completed") {
		t.Fatalf("message = %q", message)
	}
	account := accounts.GetAccount("token-1")
	if account["check_cooldown_until"] == nil || account["check_error"] == nil {
		t.Fatalf("account cooldown fields missing: %#v", account)
	}
}

func TestApplyAccountErrorMessageCoolsDownTransientFailures(t *testing.T) {
	accounts := newTestAccountService(t)
	accounts.AddAccounts([]string{"token-1"})
	accounts.UpdateAccount("token-1", map[string]any{"status": "正常", "quota": 5})

	message, handled := accounts.ApplyAccountErrorMessage("token-1", "text_stream", "TLS connect error: connection reset by peer")
	if !handled {
		t.Fatal("handled = false, want transient cooldown")
	}
	if !strings.Contains(message, "upstream connection failed before TLS handshake completed") {
		t.Fatalf("message = %q", message)
	}
	account := accounts.GetAccount("token-1")
	if account["check_cooldown_until"] == nil || account["check_error"] == nil {
		t.Fatalf("account cooldown fields missing: %#v", account)
	}
	if got := accounts.GetTextAccessToken(); got != "" {
		t.Fatalf("GetTextAccessToken() = %q, want empty during cooldown", got)
	}
}

func TestApplyAccountErrorMessageCoolsDownCloudflareOriginFailures(t *testing.T) {
	accounts := newTestAccountService(t)
	accounts.AddAccounts([]string{"token-1"})
	accounts.UpdateAccount("token-1", map[string]any{"status": "正常", "quota": 5})

	message, handled := accounts.ApplyAccountErrorMessage("token-1", "image_stream", "源服务器向 Cloudflare 返回了无效或不完整的响应。")
	if !handled || message != "upstream returned Cloudflare origin error page; retry with another account/proxy if it repeats" {
		t.Fatalf("handled = %v message = %q, want cloudflare origin cooldown", handled, message)
	}
	account := accounts.GetAccount("token-1")
	if account["check_cooldown_until"] == nil || account["check_error"] == nil {
		t.Fatalf("account cooldown fields missing: %#v", account)
	}
	if IsImageAccountAvailable(account) {
		t.Fatalf("account should be unavailable during check cooldown: %#v", account)
	}
}

func TestApplyAccountErrorMessageCoolsDownNoImageResult(t *testing.T) {
	accounts := newTestAccountService(t)
	accounts.AddAccounts([]string{"token-1"})
	accounts.UpdateAccount("token-1", map[string]any{"status": "正常", "quota": 5})

	message, handled := accounts.ApplyAccountErrorMessage("token-1", "image_stream", "image generation produced no image result")
	if !handled || message != "检测到临时网络异常，账号已短暂冷却" {
		t.Fatalf("handled = %v message = %q, want no-image-result cooldown", handled, message)
	}
	account := accounts.GetAccount("token-1")
	if account["check_cooldown_until"] == nil || account["check_error"] == nil {
		t.Fatalf("account cooldown fields missing: %#v", account)
	}
	if IsImageAccountAvailable(account) {
		t.Fatalf("account should be unavailable during check cooldown: %#v", account)
	}
}

func TestMarkImageAttemptTimeoutCoolsDownAccount(t *testing.T) {
	accounts := newTestAccountService(t)
	accounts.AddAccounts([]string{"token-1", "token-2"})
	accounts.UpdateAccount("token-1", map[string]any{"status": "正常", "quota": 5})
	accounts.UpdateAccount("token-2", map[string]any{"status": "正常", "quota": 5})

	token, err := accounts.GetAvailableImageAccessToken(context.Background(), false)
	if err != nil {
		t.Fatalf("GetAvailableImageAccessToken() error = %v", err)
	}
	accounts.MarkImageAttemptTimeout(token)
	account := accounts.GetAccount(token)
	if account["fail"] != 1 {
		t.Fatalf("fail = %#v, want 1 in %#v", account["fail"], account)
	}
	if !accountCheckCoolingDown(account, time.Now().UTC()) {
		t.Fatalf("timed out account should cool down: %#v", account)
	}
	next, err := accounts.GetAvailableImageAccessToken(context.Background(), false)
	if err != nil {
		t.Fatalf("GetAvailableImageAccessToken() after timeout error = %v", err)
	}
	if next == token {
		t.Fatalf("timed out token was selected again during cooldown")
	}
}

func TestApplyAccountErrorMessageCoolsDownCodexToolChoiceFailures(t *testing.T) {
	accounts := newTestAccountService(t)
	accounts.AddAccounts([]string{"token-1"})
	accounts.UpdateAccount("token-1", map[string]any{"status": "正常", "quota": 5})

	message, handled := accounts.ApplyAccountErrorMessage("token-1", "image_stream", `/backend-api/codex/responses failed: status=400, body={"error":{"message":"Tool choice 'required' must be specified with 'tools' parameter.","param":"tool_choice"}}`)
	if !handled || message != "检测到临时网络异常，账号已短暂冷却" {
		t.Fatalf("handled = %v message = %q, want codex tool choice cooldown", handled, message)
	}
	account := accounts.GetAccount("token-1")
	if account["check_cooldown_until"] == nil || account["check_error"] == nil {
		t.Fatalf("account cooldown fields missing: %#v", account)
	}
	if IsImageAccountAvailable(account) {
		t.Fatalf("account should be unavailable during check cooldown: %#v", account)
	}
}

func TestGetAvailableAccessTokenStartsImmediatelyWithoutRemoteProbe(t *testing.T) {
	accounts := newTestAccountService(t)
	accounts.browserHTTPClient = func(string, time.Duration) *http.Client {
		t.Fatal("image token selection should not block on remote probe")
		return nil
	}
	accounts.AddAccounts([]string{"token-1", "token-2"})
	accounts.UpdateAccount("token-1", map[string]any{"status": "正常", "quota": 5})
	accounts.UpdateAccount("token-2", map[string]any{"status": "正常", "quota": 5})

	start := time.Now()
	token, err := accounts.GetAvailableAccessToken(context.Background())
	if err != nil {
		t.Fatalf("GetAvailableAccessToken() error = %v", err)
	}
	if token != "token-1" {
		t.Fatalf("token = %q, want token-1", token)
	}
	if elapsed := time.Since(start); elapsed >= 100*time.Millisecond {
		t.Fatalf("GetAvailableAccessToken() took %s, want immediate cached selection", elapsed)
	}
	accounts.MarkImageResult("token-1", false)
}

func newTestAccountService(t *testing.T) *AccountService {
	t.Helper()
	dir := t.TempDir()
	return NewAccountService(
		storage.NewJSONBackend(filepath.Join(dir, "accounts.json"), filepath.Join(dir, "auth_keys.json")),
		testAccountConfig{},
		NewProxyService(testAccountConfig{}),
		NewLogService(dir),
	)
}

func newAccountQuotaServer(t *testing.T, mePayload map[string]any, limits []map[string]any) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("<html>ok</html>"))
		case "/backend-api/me":
			writeJSON(t, w, mePayload)
		case "/backend-api/conversation/init":
			payload := map[string]any{"default_model_slug": "gpt-5"}
			if limits != nil {
				payload["limits_progress"] = limits
			}
			writeJSON(t, w, payload)
		default:
			http.NotFound(w, r)
		}
	}))
}

func writeJSON(t *testing.T, w http.ResponseWriter, payload any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(payload); err != nil {
		t.Fatalf("write json: %v", err)
	}
}
