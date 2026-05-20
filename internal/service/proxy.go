package service

import (
	"context"
	"crypto/tls"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"chatgpt2api/internal/util"

	"github.com/enetx/g"
	"github.com/enetx/surf"
)

type ProxyConfig interface {
	Proxy() string
}

type ProxyService struct {
	config ProxyConfig
}

func NewProxyService(config ProxyConfig) *ProxyService {
	return &ProxyService{config: config}
}

func HTTPClientForProxy(proxy string, timeout time.Duration) *http.Client {
	return &http.Client{Timeout: timeout, Transport: transportForProxy(proxy)}
}

func (s *ProxyService) HTTPClient(timeout time.Duration) *http.Client {
	return HTTPClientForProxy(s.config.Proxy(), timeout)
}

func (s *ProxyService) BrowserHTTPClient(timeout time.Duration) *http.Client {
	return browserHTTPClient(s.config.Proxy(), timeout)
}

func (s *ProxyService) BrowserHTTPClientWithProfile(profile string, timeout time.Duration) *http.Client {
	return browserHTTPClientForProfile(s.config.Proxy(), profile, timeout)
}

func (s *ProxyService) Test(candidate string, timeout time.Duration) map[string]any {
	candidate = strings.TrimSpace(candidate)
	if candidate == "" {
		candidate = s.config.Proxy()
	}
	candidate = strings.TrimSpace(candidate)
	if candidate == "" {
		return map[string]any{"ok": false, "status": 0, "latency_ms": 0, "error": "proxy url is required"}
	}
	parsed, err := url.Parse(candidate)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https" && parsed.Scheme != "socks5" && parsed.Scheme != "socks5h") {
		return map[string]any{"ok": false, "status": 0, "latency_ms": 0, "error": "invalid proxy url"}
	}
	client := browserHTTPClientForProfile(candidate, "", timeout)
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "https://chatgpt.com/", nil)
	req.Header.Set("user-agent", "Mozilla/5.0 (chatgpt2api proxy test)")
	start := time.Now()
	resp, err := client.Do(req)
	latency := time.Since(start).Milliseconds()
	if err != nil {
		message := err.Error()
		if detail, ok := util.SummarizeUpstreamConnectionError(message); ok {
			message = detail
		}
		return map[string]any{"ok": false, "status": 0, "latency_ms": latency, "error": message}
	}
	defer resp.Body.Close()
	ok := resp.StatusCode < 500
	var message any
	if !ok {
		message = resp.Status
	}
	return map[string]any{"ok": ok, "status": resp.StatusCode, "latency_ms": latency, "error": message}
}

func browserHTTPClient(proxy string, timeout time.Duration) *http.Client {
	return browserHTTPClientForProfile(proxy, "", timeout)
}

func browserHTTPClientForProfile(proxy, profile string, timeout time.Duration) *http.Client {
	builder := surf.NewClient().
		Builder().
		SecureTLS()
	builder = applyBrowserProfile(builder, profile).
		Session().
		Timeout(timeout)

	if proxy = strings.TrimSpace(proxy); proxy != "" {
		builder = builder.Proxy(g.String(proxy))
	}

	client, err := builder.Build().Result()
	if err != nil {
		return &http.Client{Timeout: timeout, Transport: transportForProxy(proxy)}
	}
	return client.Std()
}

func applyBrowserProfile(builder *surf.Builder, profile string) *surf.Builder {
	impersonate := builder.Impersonate()
	normalized := strings.ToLower(strings.TrimSpace(profile))
	switch {
	case strings.Contains(normalized, "android"):
		impersonate = impersonate.Android()
	case strings.Contains(normalized, "ios"), strings.Contains(normalized, "iphone"), strings.Contains(normalized, "ipad"):
		impersonate = impersonate.IOS()
	case strings.Contains(normalized, "mac"), strings.Contains(normalized, "darwin"):
		impersonate = impersonate.MacOS()
	case strings.Contains(normalized, "linux"):
		impersonate = impersonate.Linux()
	default:
		impersonate = impersonate.Windows()
	}
	if strings.Contains(normalized, "firefox") || strings.Contains(normalized, "ff") {
		return impersonate.Firefox()
	}
	return impersonate.Chrome()
}

func transportForProxy(candidate string) *http.Transport {
	transport := baseTransport()
	if candidate == "" {
		return transport
	}
	proxyURL, err := url.Parse(candidate)
	if err != nil || proxyURL.Host == "" {
		return transport
	}
	return transportForProxyURL(proxyURL)
}

func transportForProxyURL(proxyURL *url.URL) *http.Transport {
	transport := baseTransport()
	switch strings.ToLower(proxyURL.Scheme) {
	case "http", "https":
		transport.Proxy = http.ProxyURL(proxyURL)
	case "socks5", "socks5h":
		transport.Proxy = nil
		transport.DialContext = socks5DialContext(proxyURL)
	}
	return transport
}

func baseTransport() *http.Transport {
	return &http.Transport{
		Proxy: nil,
		TLSClientConfig: &tls.Config{
			MinVersion: tls.VersionTLS12,
		},
	}
}

func socks5DialContext(proxyURL *url.URL) func(context.Context, string, string) (net.Conn, error) {
	return func(ctx context.Context, network, address string) (net.Conn, error) {
		dialer := &net.Dialer{}
		conn, err := dialer.DialContext(ctx, network, proxyURL.Host)
		if err != nil {
			return nil, err
		}
		if deadline, ok := ctx.Deadline(); ok {
			_ = conn.SetDeadline(deadline)
			defer func() {
				_ = conn.SetDeadline(time.Time{})
			}()
		}
		if err := socks5Handshake(ctx, conn, proxyURL, address); err != nil {
			_ = conn.Close()
			return nil, err
		}
		return conn, nil
	}
}

func socks5Handshake(ctx context.Context, conn net.Conn, proxyURL *url.URL, target string) error {
	methods := []byte{0x00}
	username := ""
	password := ""
	if proxyURL.User != nil {
		username = proxyURL.User.Username()
		password, _ = proxyURL.User.Password()
		if len(username) > 255 || len(password) > 255 {
			return fmt.Errorf("socks credentials are too long")
		}
		methods = append(methods, 0x02)
	}
	if _, err := conn.Write(append([]byte{0x05, byte(len(methods))}, methods...)); err != nil {
		return err
	}
	response := make([]byte, 2)
	if _, err := io.ReadFull(conn, response); err != nil {
		return err
	}
	if response[0] != 0x05 {
		return fmt.Errorf("invalid socks version %d", response[0])
	}
	switch response[1] {
	case 0x00:
	case 0x02:
		if username == "" && password == "" {
			return fmt.Errorf("socks proxy requires username/password authentication")
		}
		auth := []byte{0x01, byte(len(username))}
		auth = append(auth, []byte(username)...)
		auth = append(auth, byte(len(password)))
		auth = append(auth, []byte(password)...)
		if _, err := conn.Write(auth); err != nil {
			return err
		}
		if _, err := io.ReadFull(conn, response); err != nil {
			return err
		}
		if response[1] != 0x00 {
			return fmt.Errorf("socks authentication failed")
		}
	default:
		return fmt.Errorf("socks proxy rejected authentication methods")
	}
	address, err := socks5Address(ctx, proxyURL.Scheme, target)
	if err != nil {
		return err
	}
	request := []byte{0x05, 0x01, 0x00}
	request = append(request, address...)
	if _, err := conn.Write(request); err != nil {
		return err
	}
	return readSocks5ConnectResponse(conn)
}

func socks5Address(ctx context.Context, scheme, target string) ([]byte, error) {
	host, portText, err := net.SplitHostPort(target)
	if err != nil {
		return nil, err
	}
	port, err := strconv.Atoi(portText)
	if err != nil || port < 0 || port > 65535 {
		return nil, fmt.Errorf("invalid target port %q", portText)
	}
	var out []byte
	if strings.EqualFold(scheme, "socks5h") {
		if len(host) > 255 {
			return nil, fmt.Errorf("target host is too long")
		}
		out = append(out, 0x03, byte(len(host)))
		out = append(out, []byte(host)...)
	} else if ip := net.ParseIP(host); ip != nil {
		out = appendSOCKSIP(out, ip)
	} else {
		ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
		if err != nil {
			return nil, err
		}
		if len(ips) == 0 {
			return nil, fmt.Errorf("no address found for %s", host)
		}
		out = appendSOCKSIP(out, ips[0].IP)
	}
	var portBytes [2]byte
	binary.BigEndian.PutUint16(portBytes[:], uint16(port))
	out = append(out, portBytes[:]...)
	return out, nil
}

func appendSOCKSIP(out []byte, ip net.IP) []byte {
	if v4 := ip.To4(); v4 != nil {
		out = append(out, 0x01)
		return append(out, v4...)
	}
	out = append(out, 0x04)
	return append(out, ip.To16()...)
}

func readSocks5ConnectResponse(conn net.Conn) error {
	header := make([]byte, 4)
	if _, err := io.ReadFull(conn, header); err != nil {
		return err
	}
	if header[0] != 0x05 {
		return fmt.Errorf("invalid socks version %d", header[0])
	}
	if header[1] != 0x00 {
		return fmt.Errorf("socks connect failed: %s", socks5Status(header[1]))
	}
	toRead := 0
	switch header[3] {
	case 0x01:
		toRead = net.IPv4len
	case 0x03:
		length := make([]byte, 1)
		if _, err := io.ReadFull(conn, length); err != nil {
			return err
		}
		toRead = int(length[0])
	case 0x04:
		toRead = net.IPv6len
	default:
		return fmt.Errorf("invalid socks address type %d", header[3])
	}
	if _, err := io.CopyN(io.Discard, conn, int64(toRead+2)); err != nil {
		return err
	}
	return nil
}

func socks5Status(code byte) string {
	switch code {
	case 0x01:
		return "general failure"
	case 0x02:
		return "connection not allowed"
	case 0x03:
		return "network unreachable"
	case 0x04:
		return "host unreachable"
	case 0x05:
		return "connection refused"
	case 0x06:
		return "ttl expired"
	case 0x07:
		return "command not supported"
	case 0x08:
		return "address type not supported"
	default:
		return fmt.Sprintf("status %d", code)
	}
}
