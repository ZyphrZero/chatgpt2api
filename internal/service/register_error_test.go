package service

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

type registerErrorRoundTripFunc func(*http.Request) (*http.Response, error)

func (f registerErrorRoundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func registerErrorJSONResponse(req *http.Request, status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
		Request:    req,
	}
}

func registerErrorHTMLResponse(req *http.Request, status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     http.Header{"Content-Type": []string{"text/html; charset=utf-8"}},
		Body:       io.NopCloser(strings.NewReader(body)),
		Request:    req,
	}
}

func testRegisterWorker(transport http.RoundTripper) (*registerWorker, *RegisterService) {
	service := &RegisterService{subscribers: map[chan string]struct{}{}}
	return &registerWorker{
		service:  service,
		index:    1,
		client:   &http.Client{Transport: transport},
		deviceID: "device-1",
	}, service
}

func TestPlatformAuthorizeIncludesUpstreamErrorDetail(t *testing.T) {
	worker, _ := testRegisterWorker(registerErrorRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.URL.Path != "/api/accounts/authorize" {
			t.Fatalf("unexpected request path: %s", req.URL.Path)
		}
		return registerErrorJSONResponse(req, http.StatusForbidden, `{"error":{"code":"country_blocked","message":"not allowed"}}`), nil
	}))

	err := worker.platformAuthorize(context.Background(), "user@example.test")
	if err == nil {
		t.Fatal("platformAuthorize() returned nil error")
	}
	if got := err.Error(); !strings.Contains(got, "platform_authorize_http_403: country_blocked - not allowed") {
		t.Fatalf("platformAuthorize() error = %q", got)
	}
}

func TestRegisterUserIncludesResponseDetailAndDomainHint(t *testing.T) {
	worker, service := testRegisterWorker(registerErrorRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		switch req.URL.Path {
		case "/backend-api/sentinel/req":
			return registerErrorJSONResponse(req, http.StatusOK, `{"token":"challenge-token","proofofwork":{"required":false}}`), nil
		case "/api/accounts/user/register":
			if req.Header.Get("openai-sentinel-token") == "" {
				t.Fatal("register request did not include sentinel token")
			}
			return registerErrorJSONResponse(req, http.StatusBadRequest, `{"message":"Failed to create account. Please try again.","request_id":"req_1"}`), nil
		default:
			t.Fatalf("unexpected request path: %s", req.URL.Path)
			return nil, nil
		}
	}))

	err := worker.registerUser(context.Background(), "user@example.test", "password")
	if err == nil {
		t.Fatal("registerUser() returned nil error")
	}
	if got := err.Error(); !strings.Contains(got, "user_register_http_400, detail=") || !strings.Contains(got, "Failed to create account") {
		t.Fatalf("registerUser() error = %q", got)
	}
	if !registerLogsContain(service.logs, "邮箱域名很可能因滥用被封禁") {
		t.Fatalf("logs did not contain domain-block hint: %#v", service.logs)
	}
}

func TestCreateAccountIncludesResponseDetailAndDomainHint(t *testing.T) {
	worker, service := testRegisterWorker(registerErrorRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		switch req.URL.Path {
		case "/backend-api/sentinel/req":
			return registerErrorJSONResponse(req, http.StatusOK, `{"token":"challenge-token","proofofwork":{"required":false}}`), nil
		case "/api/accounts/create_account":
			if req.Header.Get("openai-sentinel-token") == "" {
				t.Fatal("create account request did not include sentinel token")
			}
			return registerErrorJSONResponse(req, http.StatusBadRequest, `{"message":"Failed to create account. Please try again.","request_id":"req_2"}`), nil
		default:
			t.Fatalf("unexpected request path: %s", req.URL.Path)
			return nil, nil
		}
	}))

	err := worker.createAccount(context.Background(), "Test User", "1990-01-01")
	if err == nil {
		t.Fatal("createAccount() returned nil error")
	}
	if got := err.Error(); !strings.Contains(got, "create_account_http_400, detail=") || !strings.Contains(got, "Failed to create account") {
		t.Fatalf("createAccount() error = %q", got)
	}
	if !registerLogsContain(service.logs, "邮箱域名很可能因滥用被封禁") {
		t.Fatalf("logs did not contain domain-block hint: %#v", service.logs)
	}
}

func TestRegisterUserSummarizesCloudflareChallengeHTML(t *testing.T) {
	worker, _ := testRegisterWorker(registerErrorRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		switch req.URL.Path {
		case "/backend-api/sentinel/req":
			return registerErrorJSONResponse(req, http.StatusOK, `{"token":"challenge-token","proofofwork":{"required":false}}`), nil
		case "/api/accounts/user/register":
			return registerErrorHTMLResponse(req, http.StatusForbidden, `<!doctype html><title>Just a moment...</title><script src="/cdn-cgi/challenge-platform/h/b"></script>`), nil
		default:
			t.Fatalf("unexpected request path: %s", req.URL.Path)
			return nil, nil
		}
	}))

	err := worker.registerUser(context.Background(), "user@example.test", "password")
	if err == nil {
		t.Fatal("registerUser() returned nil error")
	}
	got := err.Error()
	if !strings.Contains(got, "user_register_http_403, detail=") ||
		!strings.Contains(got, "upstream returned Cloudflare challenge page") ||
		strings.Contains(got, "challenge-platform/h/b") {
		t.Fatalf("registerUser() error = %q", got)
	}
}

func TestPasswordVerifyConflictIncludesResponseDetail(t *testing.T) {
	worker, _ := testRegisterWorker(registerErrorRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		switch req.URL.Path {
		case "/api/accounts/authorize":
			return registerErrorJSONResponse(req, http.StatusOK, `{}`), nil
		case "/backend-api/sentinel/req":
			return registerErrorJSONResponse(req, http.StatusOK, `{"token":"challenge-token","proofofwork":{"required":false}}`), nil
		case "/api/accounts/password/verify":
			return registerErrorJSONResponse(req, http.StatusConflict, `{"error":{"code":"state_conflict","message":"already in progress"},"request_id":"req_3"}`), nil
		default:
			t.Fatalf("unexpected request path: %s", req.URL.Path)
			return nil, nil
		}
	}))

	_, err := worker.loginAndExchangeTokens(context.Background(), "user@example.test", "password", map[string]any{"address": "user@example.test"})
	if err == nil {
		t.Fatal("loginAndExchangeTokens() returned nil error")
	}
	got := err.Error()
	if !strings.Contains(got, "password_verify_http_409, detail=") || !strings.Contains(got, "state_conflict") {
		t.Fatalf("loginAndExchangeTokens() error = %q", got)
	}
}

func TestLoginAndExchangeRetriesInvalidStateWithFreshSession(t *testing.T) {
	attempt := 0
	worker := &registerWorker{
		service: &RegisterService{subscribers: map[chan string]struct{}{}},
		index:   1,
		config:  map[string]any{},
		mail:    map[string]any{},
	}
	worker.client = &http.Client{Transport: registerErrorRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		switch req.URL.Path {
		case "/api/accounts/authorize":
			return registerErrorJSONResponse(req, http.StatusOK, `{}`), nil
		case "/backend-api/sentinel/req":
			return registerErrorJSONResponse(req, http.StatusOK, `{"token":"challenge-token","proofofwork":{"required":false}}`), nil
		case "/api/accounts/password/verify":
			attempt++
			if attempt == 1 {
				return registerErrorJSONResponse(req, http.StatusConflict, `{"error":{"code":"invalid_state","message":"Invalid session. Please start over."}}`), nil
			}
			return registerErrorJSONResponse(req, http.StatusOK, `{"continue_url":"https://platform.openai.com/auth/callback?code=callback-code&state=s"}`), nil
		case "/oauth/token":
			return registerErrorJSONResponse(req, http.StatusOK, `{"access_token":"access","refresh_token":"refresh","id_token":"id"}`), nil
		default:
			t.Fatalf("unexpected request path: %s", req.URL.Path)
			return nil, nil
		}
	})}

	tokens, err := worker.loginAndExchangeTokensWithRetry(context.Background(), "user@example.test", "password", map[string]any{"address": "user@example.test"})
	if err != nil {
		t.Fatalf("loginAndExchangeTokensWithRetry() error = %v", err)
	}
	if attempt != 2 {
		t.Fatalf("password verify attempts = %d, want 2", attempt)
	}
	if tokens["access_token"] != "access" {
		t.Fatalf("tokens = %#v", tokens)
	}
	if !registerLogsContain(worker.service.logs, "重建会话重试") {
		t.Fatalf("logs did not contain retry note: %#v", worker.service.logs)
	}
}

func registerLogsContain(logs []map[string]any, text string) bool {
	for _, item := range logs {
		if strings.Contains(toString(item["text"]), text) {
			return true
		}
	}
	return false
}
