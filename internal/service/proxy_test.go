package service

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"
)

func TestSOCKS5AddressModes(t *testing.T) {
	t.Run("socks5h keeps hostname for proxy-side DNS", func(t *testing.T) {
		got, err := socks5Address(context.Background(), "socks5h", "chatgpt.com:443")
		if err != nil {
			t.Fatalf("socks5Address() error = %v", err)
		}
		wantPrefix := []byte{0x03, byte(len("chatgpt.com"))}
		if string(got[:len(wantPrefix)]) != string(wantPrefix) {
			t.Fatalf("address prefix = %#v, want %#v", got[:len(wantPrefix)], wantPrefix)
		}
		if host := string(got[2 : 2+len("chatgpt.com")]); host != "chatgpt.com" {
			t.Fatalf("host = %q", host)
		}
		if got[len(got)-2] != 0x01 || got[len(got)-1] != 0xbb {
			t.Fatalf("port bytes = %#v", got[len(got)-2:])
		}
	})

	t.Run("socks5 sends numeric ip when target is ip literal", func(t *testing.T) {
		got, err := socks5Address(context.Background(), "socks5", net.JoinHostPort("127.0.0.1", "8080"))
		if err != nil {
			t.Fatalf("socks5Address() error = %v", err)
		}
		want := []byte{0x01, 127, 0, 0, 1, 0x1f, 0x90}
		if string(got) != string(want) {
			t.Fatalf("address = %#v, want %#v", got, want)
		}
	})
}

func TestBrowserHTTPClientKeepsSessionAndTimeout(t *testing.T) {
	client := browserHTTPClient("", 2*time.Second)
	if client == nil {
		t.Fatal("browserHTTPClient() returned nil")
	}
	if client.Jar == nil {
		t.Fatal("browserHTTPClient() should enable a cookie jar for browser-like sessions")
	}
	if client.Timeout != 2*time.Second {
		t.Fatalf("Timeout = %s, want %s", client.Timeout, 2*time.Second)
	}
}

func TestHTTPClientForProxyIgnoresEnvironmentWhenProxyEmpty(t *testing.T) {
	t.Setenv("HTTP_PROXY", "http://127.0.0.1:1")
	t.Setenv("HTTPS_PROXY", "http://127.0.0.1:1")
	t.Setenv("ALL_PROXY", "http://127.0.0.1:1")
	t.Setenv("NO_PROXY", "")

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	client := HTTPClientForProxy("", 2*time.Second)
	resp, err := client.Get(server.URL)
	if err != nil {
		t.Fatalf("HTTPClientForProxy(\"\") should ignore proxy environment: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusNoContent)
	}

	if got := os.Getenv("HTTP_PROXY"); got == "" {
		t.Fatal("test setup lost HTTP_PROXY")
	}
}

func TestBrowserHTTPClientPreservesCallerAuthHeaders(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer token-1" {
			t.Fatalf("Authorization = %q", got)
		}
		if got := r.Header.Get("Origin"); got != "https://chatgpt.com" {
			t.Fatalf("Origin = %q", got)
		}
		if got := r.Header.Get("Referer"); got != "https://chatgpt.com/" {
			t.Fatalf("Referer = %q", got)
		}
		if got := r.Header.Get("User-Agent"); got == "" {
			t.Fatal("User-Agent should be populated by browser impersonation")
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	client := browserHTTPClient("", 2*time.Second)
	req, err := http.NewRequest(http.MethodGet, server.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer token-1")
	req.Header.Set("Origin", "https://chatgpt.com")
	req.Header.Set("Referer", "https://chatgpt.com/")

	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("status = %d", resp.StatusCode)
	}
}
