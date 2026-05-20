package service

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestExtractUnseenRegisterMailCodeSkipsSeenMessage(t *testing.T) {
	mailbox := map[string]any{"address": "user@example.test"}
	message := map[string]any{
		"provider":     "moemail",
		"mailbox":      "user@example.test",
		"message_id":   "message-1",
		"subject":      "Verify",
		"text_content": "Verification code: 123456",
	}

	if got := extractUnseenRegisterMailCode(mailbox, message); got != "123456" {
		t.Fatalf("first code = %q, want 123456", got)
	}
	if got := extractUnseenRegisterMailCode(mailbox, message); got != "" {
		t.Fatalf("second code = %q, want empty for already seen message", got)
	}
	seen := registerSeenMailRefList(mailbox["_seen_code_message_refs"])
	if len(seen) != 1 || !strings.Contains(seen[0], "message-1") {
		t.Fatalf("seen refs = %#v", seen)
	}
}

func TestRegisterMoEmailProviderCreatesAndReadsMailbox(t *testing.T) {
	var generatedPayload map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-API-Key") != "secret-key" {
			t.Errorf("X-API-Key = %q", r.Header.Get("X-API-Key"))
		}
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/emails/generate":
			if err := json.NewDecoder(r.Body).Decode(&generatedPayload); err != nil {
				t.Errorf("decode generate payload: %v", err)
			}
			_, _ = w.Write([]byte(`{"email":"user@example.test","id":"email-1"}`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/emails/email-1":
			_, _ = w.Write([]byte(`{"messages":[{"id":"old","subject":"Old","text":"Verification code: 111111","timestamp":100},{"id":"message-2","subject":"Verify","text":"Verification code: 222222","timestamp":200}]}`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/emails/email-1/message-2":
			_, _ = w.Write([]byte(`{"message":{"id":"message-2","subject":"Verify","text":"Verification code: 222222","timestamp":200,"from":{"email":"noreply@example.test"}}}`))
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	provider, err := createRegisterMailProvider(map[string]any{
		"request_timeout": 1,
		"providers": []map[string]any{{
			"type":        "moemail",
			"enable":      true,
			"api_base":    server.URL,
			"api_key":     "secret-key",
			"domain":      []string{"example.test"},
			"expiry_time": 15,
		}},
	}, "", "")
	if err != nil {
		t.Fatalf("createRegisterMailProvider() error = %v", err)
	}
	defer provider.Close()

	mailbox, err := provider.CreateMailbox("user")
	if err != nil {
		t.Fatalf("CreateMailbox() error = %v", err)
	}
	if mailbox["provider"] != "moemail" || mailbox["address"] != "user@example.test" || mailbox["email_id"] != "email-1" {
		t.Fatalf("mailbox = %#v", mailbox)
	}
	if generatedPayload["name"] != "user" || generatedPayload["domain"] != "example.test" || int(generatedPayload["expiryTime"].(float64)) != 15 {
		t.Fatalf("generated payload = %#v", generatedPayload)
	}

	message, err := provider.FetchLatestMessage(mailbox)
	if err != nil {
		t.Fatalf("FetchLatestMessage() error = %v", err)
	}
	if got := extractRegisterMailCode(message); got != "222222" {
		t.Fatalf("extractRegisterMailCode() = %q, want 222222; message=%#v", got, message)
	}
	if message["message_id"] != "message-2" || message["sender"] != "noreply@example.test" {
		t.Fatalf("message metadata = %#v", message)
	}
}

func TestRegisterMailtempEduProviderCreatesAndReadsMailbox(t *testing.T) {
	var createPayload map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/domains":
			_, _ = w.Write([]byte(`{"status":"success","domains":[{"id":13,"domain":"mailtemp.edu.pl"},{"id":14,"domain":"imail.edu.vn"}]}`))
		case r.Method == http.MethodPost && r.URL.Path == "/api/create-session":
			if err := json.NewDecoder(r.Body).Decode(&createPayload); err != nil {
				t.Errorf("decode create payload: %v", err)
			}
			_, _ = w.Write([]byte(`{"status":"success","email":"student@mailtemp.edu.pl","token":"session-token","expires_at":"2036-05-09 16:03:53"}`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/check-mail":
			if r.URL.Query().Get("token") != "session-token" {
				t.Errorf("check-mail token = %q", r.URL.Query().Get("token"))
			}
			_, _ = w.Write([]byte(`{"status":"success","messages":[{"id":105,"sender_email":"noreply@example.test","subject":"Verify","received_at":"2026-05-12 16:00:00"}]}`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/read-mail":
			if r.URL.Query().Get("token") != "session-token" || r.URL.Query().Get("id") != "105" {
				t.Errorf("read-mail query = %s", r.URL.RawQuery)
			}
			_, _ = w.Write([]byte(`{"status":"success","message":{"id":105,"sender_email":"noreply@example.test","subject":"Verify","body_text":"Verification code: 654321","received_at":"2026-05-12 16:00:00"}}`))
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	provider, err := createRegisterMailProvider(map[string]any{
		"request_timeout": 1,
		"providers": []map[string]any{{
			"type":     "mailtemp_edu",
			"enable":   true,
			"api_base": server.URL + "/api",
			"domain":   []string{"mailtemp.edu.pl"},
		}},
	}, "", "")
	if err != nil {
		t.Fatalf("createRegisterMailProvider() error = %v", err)
	}
	defer provider.Close()

	mailbox, err := provider.CreateMailbox("student")
	if err != nil {
		t.Fatalf("CreateMailbox() error = %v", err)
	}
	if mailbox["provider"] != "mailtemp_edu" || mailbox["address"] != "student@mailtemp.edu.pl" || mailbox["token"] != "session-token" {
		t.Fatalf("mailbox = %#v", mailbox)
	}
	if createPayload["prefix"] != "student" || int(createPayload["domain_id"].(float64)) != 13 {
		t.Fatalf("create payload = %#v", createPayload)
	}

	message, err := provider.FetchLatestMessage(mailbox)
	if err != nil {
		t.Fatalf("FetchLatestMessage() error = %v", err)
	}
	if got := extractRegisterMailCode(message); got != "654321" {
		t.Fatalf("extractRegisterMailCode() = %q, want 654321; message=%#v", got, message)
	}
	if message["message_id"] != "105" || message["sender"] != "noreply@example.test" {
		t.Fatalf("message metadata = %#v", message)
	}
}
