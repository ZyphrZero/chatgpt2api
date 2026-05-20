package service

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"testing"
	"time"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func registerJSONResponse(req *http.Request, status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
		Request:    req,
	}
}

func TestRegisterFNV1A32MatchesPythonImplementation(t *testing.T) {
	cases := map[string]string{
		"":            "ab3e7c0b",
		"abc":         "1cc93dbc",
		"seedpayload": "769860aa",
		"OpenAI":      "ce220710",
	}
	for input, want := range cases {
		if got := registerFNV1A32(input); got != want {
			t.Fatalf("registerFNV1A32(%q) = %s, want %s", input, got, want)
		}
	}
}

func TestBuildSentinelTokenUsesSentinelChallenge(t *testing.T) {
	worker := &registerWorker{
		deviceID: "device-1",
		client: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			if req.URL.String() != registerSentinelBase+"/backend-api/sentinel/req" {
				t.Fatalf("unexpected request URL: %s", req.URL.String())
			}
			if got := req.Header.Get("Content-Type"); got != "text/plain;charset=UTF-8" {
				t.Fatalf("Content-Type = %q", got)
			}
			return registerJSONResponse(req, http.StatusOK, `{"token":"challenge-token","proofofwork":{"required":false}}`), nil
		})},
	}

	token, err := worker.buildSentinelToken(context.Background(), "username_password_create")
	if err != nil {
		t.Fatalf("buildSentinelToken() error = %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(token), &payload); err != nil {
		t.Fatalf("sentinel token is not JSON: %v", err)
	}
	if payload["c"] != "challenge-token" || payload["id"] != "device-1" || payload["flow"] != "username_password_create" {
		t.Fatalf("sentinel payload = %#v", payload)
	}
	p, _ := payload["p"].(string)
	if !strings.HasPrefix(p, "gAAAAAC") {
		t.Fatalf("sentinel proof token = %q", p)
	}
}

func TestValidateOTPCodeRetriesWithSentinelToken(t *testing.T) {
	validateCalls := 0
	worker := &registerWorker{
		deviceID: "device-1",
		client: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			switch req.URL.Path {
			case "/api/accounts/email-otp/validate":
				validateCalls++
				if validateCalls == 1 {
					if req.Header.Get("openai-sentinel-token") != "" {
						t.Fatal("first OTP validate unexpectedly had sentinel token")
					}
					return registerJSONResponse(req, http.StatusForbidden, `{"error":"sentinel_required"}`), nil
				}
				if req.Header.Get("openai-sentinel-token") == "" {
					t.Fatal("second OTP validate did not carry sentinel token")
				}
				return registerJSONResponse(req, http.StatusOK, `{"continue_url":"/continue"}`), nil
			case "/backend-api/sentinel/req":
				return registerJSONResponse(req, http.StatusOK, `{"token":"challenge-token","proofofwork":{"required":false}}`), nil
			default:
				t.Fatalf("unexpected request path: %s", req.URL.Path)
				return nil, nil
			}
		})},
	}

	payload, err := worker.validateOTPCode(context.Background(), "123456")
	if err != nil {
		t.Fatalf("validateOTPCode() error = %v", err)
	}
	if validateCalls != 2 {
		t.Fatalf("validate calls = %d, want 2", validateCalls)
	}
	if payload["continue_url"] != "/continue" {
		t.Fatalf("payload = %#v", payload)
	}
}

func TestSelectWorkspaceForConsentCodeUsesCookieFallback(t *testing.T) {
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookiejar.New() error = %v", err)
	}
	authURL, _ := url.Parse(registerAuthBase)
	cookiePayload, _ := json.Marshal(map[string]any{"workspaces": []map[string]any{{"id": "workspace-1"}}})
	jar.SetCookies(authURL, []*http.Cookie{
		{Name: "oai-client-auth-session", Value: base64.RawURLEncoding.EncodeToString(cookiePayload) + ".rest", Path: "/"},
	})
	worker := &registerWorker{
		deviceID: "device-1",
		client: &http.Client{Jar: jar, Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			switch req.URL.Path {
			case "/api/accounts/workspace/select":
				return registerJSONResponse(req, http.StatusOK, `{"data":{"orgs":[{"id":"org-1","projects":[{"id":"project-1"}]}]},"continue_url":"https://auth.openai.com/continue"}`), nil
			case "/api/accounts/organization/select":
				resp := registerJSONResponse(req, http.StatusFound, `{}`)
				resp.Header.Set("Location", registerPlatformOAuthRedirectURI+"?code=callback-code&state=state")
				return resp, nil
			default:
				t.Fatalf("unexpected request path: %s", req.URL.Path)
				return nil, nil
			}
		})},
	}

	code, err := worker.selectWorkspaceForConsentCode(context.Background(), registerAuthBase+"/sign-in-with-chatgpt/codex/consent")
	if err != nil {
		t.Fatalf("selectWorkspaceForConsentCode() error = %v", err)
	}
	if code != "callback-code" {
		t.Fatalf("code = %q", code)
	}
}

func TestRegisterOAuthCodeExtractsEncodedAndEmbeddedCallbacks(t *testing.T) {
	cases := map[string]string{
		registerPlatformOAuthRedirectURI + "?code=query-code&state=state":                   "query-code",
		registerPlatformOAuthRedirectURI + "#code=fragment-code&state=state":                "fragment-code",
		`<a href="https://platform.openai.com/auth/callback?code=html-code&amp;state=s">`:   "html-code",
		`https%3A%2F%2Fplatform.openai.com%2Fauth%2Fcallback%3Fcode%3Durl-code%26state%3Ds`: "url-code",
	}
	for input, want := range cases {
		if got := registerOAuthCode(input); got != want {
			t.Fatalf("registerOAuthCode(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestRegisterRefreshLocationExtractsURL(t *testing.T) {
	header := `0; url="https://platform.openai.com/auth/callback?code=refresh-code&state=s"`
	if got := registerRefreshLocation(header); got != "https://platform.openai.com/auth/callback?code=refresh-code&state=s" {
		t.Fatalf("registerRefreshLocation() = %q", got)
	}
}

func TestRegisterHTTPClientUsesSOCKSTransport(t *testing.T) {
	client, err := registerHTTPClient("socks5h://127.0.0.1:1", time.Second, "device-1")
	if err != nil {
		t.Fatalf("registerHTTPClient() error = %v", err)
	}
	transport, ok := client.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("transport type = %T", client.Transport)
	}
	if transport.Proxy != nil {
		t.Fatal("SOCKS register transport should not use http.ProxyURL")
	}
	if transport.DialContext == nil {
		t.Fatal("SOCKS register transport missing DialContext")
	}
}

func TestExtractRegisterMailCodeFromRawMIME(t *testing.T) {
	raw := strings.Join([]string{
		"From: OpenAI <noreply@example.test>",
		"To: user@example.test",
		"Subject: Verify",
		"Content-Type: multipart/alternative; boundary=abc",
		"",
		"--abc",
		"Content-Type: text/plain; charset=utf-8",
		"",
		"Your verification code is 654321",
		"--abc--",
	}, "\r\n")
	if got := extractRegisterMailCode(map[string]any{"raw": raw}); got != "654321" {
		t.Fatalf("extractRegisterMailCode(raw) = %q", got)
	}
}

func TestRegisterMessageMatchesEmail(t *testing.T) {
	message := map[string]any{"to": []any{map[string]any{"address": "target@example.test"}}}
	if !registerMessageMatchesEmail(message, "target@example.test") {
		t.Fatal("matching recipient was rejected")
	}
	if registerMessageMatchesEmail(message, "other@example.test") {
		t.Fatal("non-matching recipient was accepted")
	}
}

func TestLatestRegisterMailMessageByTimestamp(t *testing.T) {
	items := []map[string]any{
		{"id": "old", "timestamp": float64(100), "subject": "old"},
		{"id": "new", "timestamp": float64(200), "subject": "new"},
	}
	if got := latestRegisterMailMessage(items); got["id"] != "new" {
		t.Fatalf("latestRegisterMailMessage() = %#v", got)
	}
}
