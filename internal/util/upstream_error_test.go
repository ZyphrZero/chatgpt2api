package util

import "testing"

func TestSummarizeUpstreamConnectionError(t *testing.T) {
	cases := []string{
		`Get "https://chatgpt.com/": surf: HTTP/2 request failed: uTLS.HandshakeContext() error: EOF; HTTP/1.1 fallback failed: uTLS.HandshakeContext() error: EOF`,
		"curl: (35) OpenSSL SSL_connect: SSL_ERROR_SYSCALL",
		"TLS connect error: connection reset by peer",
		"error: OPENSSL_INTERNAL:WRONG_VERSION_NUMBER",
	}
	for _, input := range cases {
		got, ok := SummarizeUpstreamConnectionError(input)
		if !ok {
			t.Fatalf("SummarizeUpstreamConnectionError(%q) did not match", input)
		}
		if got != UpstreamConnectionFailureMessage {
			t.Fatalf("summary = %q, want %q", got, UpstreamConnectionFailureMessage)
		}
	}

	if got, ok := SummarizeUpstreamConnectionError("upstream returned 500"); ok || got != "" {
		t.Fatalf("non-connection summary = %q, %v", got, ok)
	}
}

func TestSummarizeCloudflareError(t *testing.T) {
	challenge := `<html><script>window._cf_chl_opt={}</script>Enable JavaScript and cookies to continue</html>`
	if got, ok := SummarizeCloudflareError(challenge); !ok || got != UpstreamCloudflareChallengeMessage {
		t.Fatalf("challenge summary = %q, %v", got, ok)
	}

	origin := "源服务器向 Cloudflare 返回了无效或不完整的响应。"
	if got, ok := SummarizeCloudflareError(origin); !ok || got != UpstreamCloudflareOriginErrorMessage {
		t.Fatalf("origin summary = %q, %v", got, ok)
	}

	englishOrigin := "The origin web server returned an invalid or incomplete response to Cloudflare."
	if got, ok := SummarizeCloudflareError(englishOrigin); !ok || got != UpstreamCloudflareOriginErrorMessage {
		t.Fatalf("english origin summary = %q, %v", got, ok)
	}

	if got, ok := SummarizeCloudflareError("plain upstream error"); ok || got != "" {
		t.Fatalf("non-cloudflare summary = %q, %v", got, ok)
	}
}
