package util

import "strings"

const UpstreamConnectionFailureMessage = "upstream connection failed before TLS handshake completed; check proxy reachability to chatgpt.com or change proxy"
const UpstreamCloudflareChallengeMessage = "upstream returned Cloudflare challenge page; refresh browser fingerprint/session or change proxy"
const UpstreamCloudflareOriginErrorMessage = "upstream returned Cloudflare origin error page; retry with another account/proxy if it repeats"

func SummarizeUpstreamConnectionError(message string) (string, bool) {
	text := strings.TrimSpace(message)
	if text == "" {
		return "", false
	}
	lower := strings.ToLower(text)
	if strings.Contains(lower, "utls.handshakecontext") ||
		strings.Contains(lower, "http/2 request failed") ||
		strings.Contains(lower, "http/1.1 fallback failed") ||
		strings.Contains(lower, "tls connect error") ||
		strings.Contains(lower, "openssl_internal") ||
		strings.Contains(lower, "curl: (35)") ||
		((strings.Contains(lower, "tls") || strings.Contains(lower, "handshake")) && strings.Contains(lower, "eof")) {
		return UpstreamConnectionFailureMessage, true
	}
	return "", false
}

func SummarizeCloudflareError(message string) (string, bool) {
	text := strings.TrimSpace(message)
	if text == "" {
		return "", false
	}
	lower := strings.ToLower(text)
	if strings.Contains(lower, "cf_chl") ||
		strings.Contains(lower, "challenge-platform") ||
		strings.Contains(lower, "enable javascript and cookies to continue") ||
		strings.Contains(lower, "cloudflare challenge") {
		return UpstreamCloudflareChallengeMessage, true
	}
	if strings.Contains(lower, "cloudflare") ||
		strings.Contains(text, "源服务器向 Cloudflare 返回了无效或不完整的响应") {
		return UpstreamCloudflareOriginErrorMessage, true
	}
	return "", false
}
