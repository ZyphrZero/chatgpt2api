package service

import (
	"context"
	"crypto/sha1"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net/http"
	"net/mail"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"chatgpt2api/internal/util"
)

var (
	registerMailDomainMu    sync.Mutex
	registerMailProviderMu  sync.Mutex
	registerMailDomainSeq   int
	registerMailProviderSeq int

	registerMailCodePatterns = []*regexp.Regexp{
		regexp.MustCompile(`(?is)background-color:\s*#F3F3F3[^>]*>[\s\S]*?(\d{6})[\s\S]*?</p>`),
		regexp.MustCompile(`(?i)(?:Verification code|code is|代码为|验证码)[:\s]*(\d{6})`),
		regexp.MustCompile(`(?is)>\s*(\d{6})\s*<`),
		regexp.MustCompile(`\b(\d{6})\b`),
	}
)

type registerMailboxProvider interface {
	CreateMailbox(username string) (map[string]any, error)
	FetchLatestMessage(mailbox map[string]any) (map[string]any, error)
	Close()
}

type registerMailSettings struct {
	RequestTimeout time.Duration
	WaitTimeout    time.Duration
	WaitInterval   time.Duration
	UserAgent      string
}

type registerHTTPMailProvider struct {
	client *http.Client
	conf   registerMailSettings
}

type registerCloudflareTempMailProvider struct {
	registerHTTPMailProvider
	entry map[string]any
}

type registerTempMailLOLProvider struct {
	registerHTTPMailProvider
	entry map[string]any
}

type registerMailtempEduProvider struct {
	registerHTTPMailProvider
	entry map[string]any
}

type registerDuckMailProvider struct {
	registerHTTPMailProvider
	entry map[string]any
}

type registerGPTMailProvider struct {
	registerHTTPMailProvider
	entry map[string]any
}

type registerMoEmailProvider struct {
	registerHTTPMailProvider
	entry map[string]any
}

func createRegisterMailbox(mailConfig map[string]any, username string) (map[string]any, error) {
	provider, err := createRegisterMailProvider(mailConfig, "", "")
	if err != nil {
		return nil, err
	}
	defer provider.Close()
	return provider.CreateMailbox(username)
}

func waitRegisterCode(ctx context.Context, mailConfig map[string]any, mailbox map[string]any) (string, error) {
	provider, err := createRegisterMailProvider(mailConfig, util.Clean(mailbox["provider"]), util.Clean(mailbox["provider_ref"]))
	if err != nil {
		return "", err
	}
	defer provider.Close()
	conf := registerMailSettingsFromConfig(mailConfig)
	deadline := time.NewTimer(conf.WaitTimeout)
	defer deadline.Stop()
	for {
		message, fetchErr := provider.FetchLatestMessage(mailbox)
		if fetchErr == nil && message != nil {
			if code := extractUnseenRegisterMailCode(mailbox, message); code != "" {
				return code, nil
			}
		}
		interval := time.NewTimer(conf.WaitInterval)
		select {
		case <-ctx.Done():
			interval.Stop()
			return "", ctx.Err()
		case <-deadline.C:
			interval.Stop()
			return "", nil
		case <-interval.C:
		}
	}
}

func createRegisterMailProvider(mailConfig map[string]any, providerName, providerRef string) (registerMailboxProvider, error) {
	entry, err := selectRegisterMailEntry(mailConfig, providerName, providerRef)
	if err != nil {
		return nil, err
	}
	conf := registerMailSettingsFromConfig(mailConfig)
	client := registerMailHTTPClient(conf.RequestTimeout)
	base := registerHTTPMailProvider{client: client, conf: conf}
	switch util.Clean(entry["type"]) {
	case "cloudflare_temp_email":
		return &registerCloudflareTempMailProvider{registerHTTPMailProvider: base, entry: entry}, nil
	case "tempmail_lol":
		return &registerTempMailLOLProvider{registerHTTPMailProvider: base, entry: entry}, nil
	case "mailtemp_edu":
		return &registerMailtempEduProvider{registerHTTPMailProvider: base, entry: entry}, nil
	case "duckmail":
		return &registerDuckMailProvider{registerHTTPMailProvider: base, entry: entry}, nil
	case "gptmail":
		return &registerGPTMailProvider{registerHTTPMailProvider: base, entry: entry}, nil
	case "moemail":
		return &registerMoEmailProvider{registerHTTPMailProvider: base, entry: entry}, nil
	default:
		return nil, fmt.Errorf("unsupported mail.provider: %s", util.Clean(entry["type"]))
	}
}

func registerMailSettingsFromConfig(mailConfig map[string]any) registerMailSettings {
	return registerMailSettings{
		RequestTimeout: time.Duration(maxInt(1, util.ToInt(mailConfig["request_timeout"], 15))) * time.Second,
		WaitTimeout:    time.Duration(maxInt(1, util.ToInt(mailConfig["wait_timeout"], 30))) * time.Second,
		WaitInterval:   time.Duration(maxInt(1, util.ToInt(mailConfig["wait_interval"], 3))) * time.Second,
		UserAgent:      firstNonEmpty(util.Clean(mailConfig["user_agent"]), "Mozilla/5.0"),
	}
}

func registerMailHTTPClient(timeout time.Duration) *http.Client {
	if timeout <= 0 {
		timeout = 15 * time.Second
	}
	return &http.Client{
		Timeout: timeout,
		Transport: &http.Transport{
			Proxy: http.ProxyFromEnvironment,
			TLSClientConfig: &tls.Config{
				MinVersion: tls.VersionTLS12,
			},
		},
	}
}

func registerMailEntries(mailConfig map[string]any) []map[string]any {
	providers := util.AsMapSlice(mailConfig["providers"])
	out := make([]map[string]any, 0, len(providers))
	for index, item := range providers {
		entry := util.CopyMap(item)
		entry["provider_ref"] = fmt.Sprintf("%s#%d", util.Clean(entry["type"]), index+1)
		out = append(out, entry)
	}
	return out
}

func selectRegisterMailEntry(mailConfig map[string]any, providerName, providerRef string) (map[string]any, error) {
	entries := registerMailEntries(mailConfig)
	enabled := make([]map[string]any, 0, len(entries))
	for _, entry := range entries {
		if util.ToBool(entry["enable"]) {
			enabled = append(enabled, entry)
		}
	}
	if len(enabled) == 0 {
		return nil, fmt.Errorf("mail.providers has no enabled provider")
	}
	if providerRef != "" {
		for _, entry := range entries {
			if util.Clean(entry["provider_ref"]) == providerRef {
				return util.CopyMap(entry), nil
			}
		}
	}
	if providerName != "" {
		for _, entry := range enabled {
			if util.Clean(entry["type"]) == providerName {
				return util.CopyMap(entry), nil
			}
		}
	}
	if len(enabled) == 1 {
		return util.CopyMap(enabled[0]), nil
	}
	registerMailProviderMu.Lock()
	entry := util.CopyMap(enabled[registerMailProviderSeq%len(enabled)])
	registerMailProviderSeq = (registerMailProviderSeq + 1) % len(enabled)
	registerMailProviderMu.Unlock()
	return entry, nil
}

func extractRegisterMailCode(message map[string]any) string {
	textContent, htmlContent := extractRegisterMailContent(message)
	content := strings.TrimSpace(strings.Join([]string{
		util.Clean(message["subject"]),
		textContent,
		htmlContent,
	}, "\n"))
	if content == "" {
		return ""
	}
	for _, pattern := range registerMailCodePatterns {
		match := pattern.FindStringSubmatch(content)
		if len(match) > 1 {
			code := strings.TrimSpace(match[1])
			if code != "" && code != "177010" {
				return code
			}
		}
	}
	return ""
}

func extractUnseenRegisterMailCode(mailbox map[string]any, message map[string]any) string {
	ref := registerMailMessageRef(message)
	seen := registerSeenMailRefs(mailbox["_seen_code_message_refs"])
	if ref != "" {
		if _, ok := seen[ref]; ok {
			return ""
		}
	}
	code := extractRegisterMailCode(message)
	if code == "" || ref == "" {
		return code
	}
	existing := registerSeenMailRefList(mailbox["_seen_code_message_refs"])
	mailbox["_seen_code_message_refs"] = append(existing, ref)
	return code
}

func registerSeenMailRefs(value any) map[string]struct{} {
	out := map[string]struct{}{}
	for _, item := range registerSeenMailRefList(value) {
		out[item] = struct{}{}
	}
	return out
}

func registerSeenMailRefList(value any) []string {
	switch typed := value.(type) {
	case []string:
		return append([]string(nil), typed...)
	case []any:
		out := make([]string, 0, len(typed))
		for _, item := range typed {
			if ref := util.Clean(item); ref != "" {
				out = append(out, ref)
			}
		}
		return out
	default:
		return nil
	}
}

func registerMailMessageRef(message map[string]any) string {
	provider := util.Clean(message["provider"])
	mailbox := util.Clean(message["mailbox"])
	if id := registerMessageID(message); id != "" {
		return "id:" + provider + ":" + mailbox + ":" + id
	}
	textContent, htmlContent := extractRegisterMailContent(message)
	received := util.Clean(message["received_at"])
	content := strings.Join([]string{util.Clean(message["subject"]), textContent, htmlContent}, "\n")
	if strings.TrimSpace(content) == "" {
		return ""
	}
	sum := sha1.Sum([]byte(content))
	return fmt.Sprintf("content:%s:%s:%s:%x", provider, mailbox, received, sum[:8])
}

func extractRegisterMailContent(data map[string]any) (string, string) {
	textContent := firstNonEmpty(
		registerContentString(data["text_content"]),
		registerContentString(data["text"]),
		registerContentString(data["body_text"]),
		registerContentString(data["body"]),
		registerContentString(data["content"]),
	)
	htmlContent := firstNonEmpty(
		registerContentString(data["html_content"]),
		registerContentString(data["html"]),
		registerContentString(data["html_body"]),
		registerContentString(data["body_html"]),
	)
	if textContent != "" || htmlContent != "" {
		return textContent, htmlContent
	}
	raw, ok := data["raw"].(string)
	if !ok || strings.TrimSpace(raw) == "" {
		return "", ""
	}
	textContent, htmlContent = parseRegisterRawMail(raw)
	if textContent == "" && htmlContent == "" {
		return raw, ""
	}
	return textContent, htmlContent
}

func registerContentString(value any) string {
	switch typed := value.(type) {
	case nil:
		return ""
	case string:
		return strings.TrimSpace(typed)
	case []string:
		return strings.TrimSpace(strings.Join(typed, ""))
	case []any:
		parts := make([]string, 0, len(typed))
		for _, item := range typed {
			if text := registerContentString(item); text != "" {
				parts = append(parts, text)
			}
		}
		return strings.TrimSpace(strings.Join(parts, ""))
	default:
		return util.Clean(value)
	}
}

func parseRegisterRawMail(raw string) (string, string) {
	message, err := mail.ReadMessage(strings.NewReader(raw))
	if err != nil {
		return raw, ""
	}
	plain, html := parseRegisterMIMEBody(message.Header.Get("Content-Type"), message.Header.Get("Content-Transfer-Encoding"), message.Body)
	return strings.TrimSpace(strings.Join(plain, "\n")), strings.TrimSpace(strings.Join(html, "\n"))
}

func parseRegisterMIMEBody(contentType, transferEncoding string, body io.Reader) ([]string, []string) {
	mediaType, params, err := mime.ParseMediaType(contentType)
	if err != nil {
		mediaType = strings.TrimSpace(strings.ToLower(strings.Split(contentType, ";")[0]))
	}
	if strings.HasPrefix(mediaType, "multipart/") {
		boundary := params["boundary"]
		if boundary == "" {
			return nil, nil
		}
		reader := multipart.NewReader(body, boundary)
		var plain []string
		var html []string
		for {
			part, partErr := reader.NextPart()
			if partErr == io.EOF {
				break
			}
			if partErr != nil {
				break
			}
			partPlain, partHTML := parseRegisterMIMEBody(part.Header.Get("Content-Type"), part.Header.Get("Content-Transfer-Encoding"), part)
			plain = append(plain, partPlain...)
			html = append(html, partHTML...)
		}
		return plain, html
	}
	payload, err := readRegisterMIMEPayload(body, transferEncoding)
	if err != nil || strings.TrimSpace(payload) == "" {
		return nil, nil
	}
	if mediaType == "text/html" {
		return nil, []string{payload}
	}
	if mediaType == "" || strings.HasPrefix(mediaType, "text/") {
		return []string{payload}, nil
	}
	return nil, nil
}

func readRegisterMIMEPayload(body io.Reader, transferEncoding string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(transferEncoding)) {
	case "base64":
		data, err := io.ReadAll(body)
		if err != nil {
			return "", err
		}
		cleaned := strings.NewReplacer("\r", "", "\n", "", " ", "", "\t", "").Replace(string(data))
		decoded, err := base64.StdEncoding.DecodeString(cleaned)
		if err != nil {
			return "", err
		}
		return string(decoded), nil
	case "quoted-printable":
		data, err := io.ReadAll(quotedprintable.NewReader(body))
		if err != nil {
			return "", err
		}
		return string(data), nil
	default:
		data, err := io.ReadAll(body)
		if err != nil {
			return "", err
		}
		return string(data), nil
	}
}

func registerMessageMatchesEmail(data map[string]any, email string) bool {
	target := strings.ToLower(strings.TrimSpace(email))
	if target == "" {
		return true
	}
	var candidates []string
	for _, key := range []string{"to", "mailTo", "receiver", "receivers", "address", "email", "envelope_to"} {
		if value, ok := data[key]; ok {
			candidates = append(candidates, registerTextCandidates(value)...)
		}
	}
	if len(candidates) == 0 {
		return true
	}
	for _, candidate := range candidates {
		if strings.Contains(strings.ToLower(strings.TrimSpace(candidate)), target) {
			return true
		}
	}
	return false
}

func registerTextCandidates(value any) []string {
	switch typed := value.(type) {
	case string:
		return []string{typed}
	case map[string]any:
		var out []string
		for _, key := range []string{"address", "email", "name", "value"} {
			if item, ok := typed[key]; ok {
				out = append(out, registerTextCandidates(item)...)
			}
		}
		return out
	case []any:
		var out []string
		for _, item := range typed {
			out = append(out, registerTextCandidates(item)...)
		}
		return out
	case []map[string]any:
		var out []string
		for _, item := range typed {
			out = append(out, registerTextCandidates(item)...)
		}
		return out
	default:
		return nil
	}
}

func latestRegisterMailMessage(items []map[string]any) map[string]any {
	if len(items) == 0 {
		return nil
	}
	candidates := append([]map[string]any(nil), items...)
	sort.SliceStable(candidates, func(i, j int) bool {
		left := registerMessageReceivedAt(candidates[i])
		right := registerMessageReceivedAt(candidates[j])
		if !left.IsZero() || !right.IsZero() {
			if !left.Equal(right) {
				return left.After(right)
			}
			return registerMessageID(candidates[i]) > registerMessageID(candidates[j])
		}
		return false
	})
	return candidates[0]
}

func registerMessageReceivedAt(data map[string]any) time.Time {
	for _, key := range []string{"created_at", "createdAt", "received_at", "receivedAt", "date", "timestamp"} {
		if value, ok := data[key]; ok {
			if parsed := parseRegisterMailTime(value); !parsed.IsZero() {
				return parsed
			}
		}
	}
	return time.Time{}
}

func registerMessageID(data map[string]any) string {
	return util.Clean(firstNonNil(data["id"], data["message_id"], data["_id"], data["token"], data["@id"]))
}

func parseRegisterMailTime(value any) time.Time {
	switch typed := value.(type) {
	case int:
		return time.Unix(int64(typed), 0).UTC()
	case int64:
		return time.Unix(typed, 0).UTC()
	case float64:
		return time.Unix(int64(typed), 0).UTC()
	case json.Number:
		if integer, err := typed.Int64(); err == nil {
			return time.Unix(integer, 0).UTC()
		}
		if number, err := typed.Float64(); err == nil {
			return time.Unix(int64(number), 0).UTC()
		}
	}
	text := util.Clean(value)
	if text == "" {
		return time.Time{}
	}
	if parsed, err := time.Parse(time.RFC3339Nano, text); err == nil {
		return parsed
	}
	if parsed, err := time.Parse(time.RFC1123Z, text); err == nil {
		return parsed
	}
	if parsed, err := time.Parse(time.RFC1123, text); err == nil {
		return parsed
	}
	if parsed, err := mail.ParseDate(text); err == nil {
		return parsed
	}
	return time.Time{}
}

func registerRandomMailboxName() string {
	return fmt.Sprintf("%s%d%s", randomLower(5), rand.Intn(999), randomLower(2+rand.Intn(2)))
}

func registerRandomSubdomainLabel() string {
	return randomAlphaNum(4 + rand.Intn(7))
}

func nextRegisterDomain(domains []string) (string, error) {
	filtered := make([]string, 0, len(domains))
	for _, domain := range domains {
		if item := strings.TrimSpace(domain); item != "" {
			filtered = append(filtered, item)
		}
	}
	if len(filtered) == 0 {
		return "", fmt.Errorf("mail domain is required")
	}
	if len(filtered) == 1 {
		return filtered[0], nil
	}
	registerMailDomainMu.Lock()
	value := filtered[registerMailDomainSeq%len(filtered)]
	registerMailDomainSeq = (registerMailDomainSeq + 1) % len(filtered)
	registerMailDomainMu.Unlock()
	return value, nil
}

func randomLower(n int) string {
	const letters = "abcdefghijklmnopqrstuvwxyz"
	var b strings.Builder
	for i := 0; i < n; i++ {
		b.WriteByte(letters[rand.Intn(len(letters))])
	}
	return b.String()
}

func randomAlphaNum(n int) string {
	const chars = "abcdefghijklmnopqrstuvwxyz0123456789"
	var b strings.Builder
	for i := 0; i < n; i++ {
		b.WriteByte(chars[rand.Intn(len(chars))])
	}
	return b.String()
}

func (p *registerHTTPMailProvider) Close() {
	p.client.CloseIdleConnections()
}

func (p *registerCloudflareTempMailProvider) CreateMailbox(username string) (map[string]any, error) {
	apiBase := strings.TrimRight(util.Clean(p.entry["api_base"]), "/")
	adminPassword := util.Clean(p.entry["admin_password"])
	domain, err := nextRegisterDomain(util.AsStringSlice(p.entry["domain"]))
	if err != nil {
		return nil, err
	}
	payload := map[string]any{
		"enablePrefix": true,
		"name":         firstNonEmpty(strings.TrimSpace(username), registerRandomMailboxName()),
		"domain":       domain,
	}
	data, err := registerMailRequestJSON(p.client, http.MethodPost, apiBase+"/admin/new_address", map[string]string{
		"Content-Type": "application/json",
		"User-Agent":   p.conf.UserAgent,
		"x-admin-auth": adminPassword,
	}, nil, payload, http.StatusOK)
	if err != nil {
		return nil, err
	}
	address := util.Clean(data["address"])
	token := util.Clean(data["jwt"])
	if address == "" || token == "" {
		return nil, fmt.Errorf("cloudflare_temp_email response missing address or jwt")
	}
	return map[string]any{"provider": "cloudflare_temp_email", "provider_ref": p.entry["provider_ref"], "address": address, "token": token}, nil
}

func (p *registerCloudflareTempMailProvider) FetchLatestMessage(mailbox map[string]any) (map[string]any, error) {
	apiBase := strings.TrimRight(util.Clean(p.entry["api_base"]), "/")
	token := util.Clean(mailbox["token"])
	data, err := registerMailRequestJSON(p.client, http.MethodGet, apiBase+"/api/mails", map[string]string{
		"Authorization": "Bearer " + token,
		"User-Agent":    p.conf.UserAgent,
	}, map[string]string{"limit": "10", "offset": "0"}, nil, http.StatusOK)
	if err != nil {
		return nil, err
	}
	items := util.AsMapSlice(data["results"])
	messages := make([]map[string]any, 0, len(items))
	for _, item := range items {
		if registerMessageMatchesEmail(item, util.Clean(mailbox["address"])) {
			messages = append(messages, item)
		}
	}
	if len(messages) == 0 {
		return nil, nil
	}
	message := latestRegisterMailMessage(messages)
	textContent, htmlContent := extractRegisterMailContent(message)
	return map[string]any{
		"subject":      util.Clean(message["subject"]),
		"text_content": textContent,
		"html_content": htmlContent,
		"raw":          message["raw"],
	}, nil
}

func (p *registerTempMailLOLProvider) CreateMailbox(username string) (map[string]any, error) {
	payload := map[string]any{}
	domains := util.AsStringSlice(p.entry["domain"])
	if len(domains) > 0 {
		domain := domains[rand.Intn(len(domains))]
		if strings.HasPrefix(domain, "*.") && len(domain) > 2 {
			payload["domain"] = registerRandomSubdomainLabel() + "." + strings.TrimPrefix(domain, "*.")
			payload["prefix"] = registerRandomMailboxName()
		} else if strings.TrimSpace(domain) != "" {
			payload["domain"] = strings.TrimSpace(domain)
		}
	}
	if username = strings.TrimSpace(username); username != "" && payload["prefix"] == nil {
		payload["prefix"] = username
	}
	data, err := registerMailRequestJSON(p.client, http.MethodPost, "https://api.tempmail.lol/v2/inbox/create", map[string]string{
		"Content-Type": "application/json",
		"User-Agent":   p.conf.UserAgent,
		"Accept":       "application/json",
		"Authorization": func() string {
			if key := util.Clean(p.entry["api_key"]); key != "" {
				return "Bearer " + key
			}
			return ""
		}(),
	}, nil, payload, http.StatusOK, http.StatusCreated)
	if err != nil {
		return nil, err
	}
	address := util.Clean(data["address"])
	token := util.Clean(data["token"])
	if address == "" || token == "" {
		return nil, fmt.Errorf("tempmail_lol response missing address or token")
	}
	return map[string]any{"provider": "tempmail_lol", "provider_ref": p.entry["provider_ref"], "address": address, "token": token}, nil
}

func (p *registerTempMailLOLProvider) FetchLatestMessage(mailbox map[string]any) (map[string]any, error) {
	data, err := registerMailRequestJSON(p.client, http.MethodGet, "https://api.tempmail.lol/v2/inbox", map[string]string{
		"User-Agent": p.conf.UserAgent,
		"Accept":     "application/json",
	}, map[string]string{"token": util.Clean(mailbox["token"])}, nil, http.StatusOK)
	if err != nil {
		return nil, err
	}
	items := util.AsMapSlice(firstNonNil(data["emails"], data["messages"]))
	if len(items) == 0 {
		return nil, nil
	}
	latest := latestRegisterMailMessage(items)
	textContent, htmlContent := extractRegisterMailContent(latest)
	return map[string]any{
		"subject":      util.Clean(latest["subject"]),
		"text_content": textContent,
		"html_content": htmlContent,
		"raw":          latest["raw"],
	}, nil
}

func (p *registerMailtempEduProvider) CreateMailbox(username string) (map[string]any, error) {
	apiBase := strings.TrimRight(util.Clean(p.entry["api_base"]), "/")
	if apiBase == "" {
		apiBase = "https://mailtemp.edu.pl/api"
	}
	payload := map[string]any{}
	if username = strings.TrimSpace(username); username != "" {
		payload["prefix"] = username
	}
	if domainID, err := p.mailtempEduDomainID(apiBase); err != nil || domainID != 0 {
		if err != nil {
			return nil, fmt.Errorf("mailtemp_edu domain lookup failed: %w", err)
		}
		payload["domain_id"] = domainID
	}
	data, err := registerMailRequestJSON(p.client, http.MethodPost, apiBase+"/create-session", map[string]string{
		"Content-Type": "application/json",
		"User-Agent":   p.conf.UserAgent,
		"Accept":       "application/json",
	}, nil, payload, http.StatusOK, http.StatusCreated)
	if err != nil {
		return nil, err
	}
	if status := util.Clean(data["status"]); status != "" && status != "success" {
		return nil, fmt.Errorf("mailtemp_edu create-session failed: %s", firstNonEmpty(util.Clean(data["message"]), status))
	}
	address := util.Clean(data["email"])
	token := util.Clean(data["token"])
	if address == "" || token == "" {
		return nil, fmt.Errorf("mailtemp_edu response missing email or token")
	}
	return map[string]any{"provider": "mailtemp_edu", "provider_ref": p.entry["provider_ref"], "address": address, "token": token, "expires_at": data["expires_at"]}, nil
}

func (p *registerMailtempEduProvider) mailtempEduDomainID(apiBase string) (int, error) {
	configured := util.AsStringSlice(p.entry["domain"])
	domains, err := registerMailRequestJSON(p.client, http.MethodGet, apiBase+"/domains", map[string]string{
		"User-Agent": p.conf.UserAgent,
		"Accept":     "application/json",
	}, nil, nil, http.StatusOK)
	if err != nil {
		if len(configured) == 0 {
			return 0, nil
		}
		return 0, err
	}
	items := util.AsMapSlice(domains["domains"])
	if len(items) == 0 {
		return 0, nil
	}
	if len(configured) == 0 {
		return util.ToInt(items[0]["id"], 0), nil
	}
	wanted := map[string]struct{}{}
	for _, domain := range configured {
		if domain = strings.ToLower(strings.TrimPrefix(strings.TrimSpace(domain), "@")); domain != "" {
			wanted[domain] = struct{}{}
		}
	}
	for _, item := range items {
		domain := strings.ToLower(strings.TrimPrefix(util.Clean(item["domain"]), "@"))
		if _, ok := wanted[domain]; ok {
			return util.ToInt(item["id"], 0), nil
		}
	}
	return 0, fmt.Errorf("configured domain not found: %s", strings.Join(configured, ","))
}

func (p *registerMailtempEduProvider) FetchLatestMessage(mailbox map[string]any) (map[string]any, error) {
	apiBase := strings.TrimRight(util.Clean(p.entry["api_base"]), "/")
	if apiBase == "" {
		apiBase = "https://mailtemp.edu.pl/api"
	}
	token := util.Clean(mailbox["token"])
	data, err := registerMailRequestJSON(p.client, http.MethodGet, apiBase+"/check-mail", map[string]string{
		"User-Agent": p.conf.UserAgent,
		"Accept":     "application/json",
	}, map[string]string{"token": token, "force": "1"}, nil, http.StatusOK)
	if err != nil {
		return nil, err
	}
	if status := util.Clean(data["status"]); status != "" && status != "success" {
		if util.Clean(data["message"]) == "Session expired" {
			return nil, fmt.Errorf("mailtemp_edu session expired")
		}
		return nil, fmt.Errorf("mailtemp_edu check-mail failed: %s", firstNonEmpty(util.Clean(data["message"]), status))
	}
	items := util.AsMapSlice(data["messages"])
	if len(items) == 0 {
		return nil, nil
	}
	latest := latestRegisterMailMessage(items)
	messageID := registerMessageID(latest)
	message := latest
	if messageID != "" {
		detail, detailErr := registerMailRequestJSON(p.client, http.MethodGet, apiBase+"/read-mail", map[string]string{
			"User-Agent": p.conf.UserAgent,
			"Accept":     "application/json",
		}, map[string]string{"token": token, "id": messageID}, nil, http.StatusOK)
		if detailErr == nil {
			if nested := util.StringMap(detail["message"]); len(nested) > 0 {
				message = nested
			} else {
				message = detail
			}
		}
	}
	textContent, htmlContent := extractRegisterMailContent(message)
	return map[string]any{
		"provider":     "mailtemp_edu",
		"mailbox":      util.Clean(mailbox["address"]),
		"message_id":   firstNonEmpty(registerMessageID(message), messageID),
		"subject":      firstNonEmpty(util.Clean(message["subject"]), util.Clean(latest["subject"])),
		"sender":       firstNonEmpty(util.Clean(message["sender_email"]), util.Clean(message["from"]), util.Clean(latest["sender_email"]), util.Clean(latest["from"])),
		"text_content": textContent,
		"html_content": htmlContent,
		"received_at":  firstNonNil(message["received_at"], message["created_at"], message["date"], message["timestamp"], latest["received_at"], latest["created_at"], latest["date"], latest["timestamp"]),
		"raw":          firstNonNil(message["raw"], message["source"]),
	}, nil
}

func (p *registerDuckMailProvider) CreateMailbox(username string) (map[string]any, error) {
	apiKey := util.Clean(p.entry["api_key"])
	domains, err := registerMailRequestAny(p.client, http.MethodGet, "https://api.duckmail.sbs/domains", map[string]string{
		"Authorization": "Bearer " + apiKey,
		"User-Agent":    p.conf.UserAgent,
		"Accept":        "application/json",
	}, nil, nil, http.StatusOK, http.StatusCreated)
	if err != nil {
		return nil, err
	}
	domain := util.Clean(p.entry["default_domain"])
	for _, item := range duckMailItems(domains) {
		if value := util.Clean(item["domain"]); value != "" {
			domain = value
			break
		}
	}
	if domain == "" {
		domain = "duckmail.sbs"
	}
	password := randomAlphaNum(12)
	address := firstNonEmpty(strings.TrimSpace(username), registerRandomMailboxName()) + "@" + domain
	payload := map[string]any{"address": address, "password": password}
	account, err := registerMailRequestJSON(p.client, http.MethodPost, "https://api.duckmail.sbs/accounts", map[string]string{
		"Authorization": "Bearer " + apiKey,
		"User-Agent":    p.conf.UserAgent,
		"Accept":        "application/json",
		"Content-Type":  "application/json",
	}, nil, payload, http.StatusOK, http.StatusCreated)
	if err != nil {
		return nil, err
	}
	tokenData, err := registerMailRequestJSON(p.client, http.MethodPost, "https://api.duckmail.sbs/token", map[string]string{
		"Authorization": "Bearer " + apiKey,
		"User-Agent":    p.conf.UserAgent,
		"Accept":        "application/json",
		"Content-Type":  "application/json",
	}, nil, payload, http.StatusOK, http.StatusCreated)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"provider":     "duckmail",
		"provider_ref": p.entry["provider_ref"],
		"address":      address,
		"token":        util.Clean(tokenData["token"]),
		"password":     password,
		"account_id":   util.Clean(account["id"]),
	}, nil
}

func (p *registerDuckMailProvider) FetchLatestMessage(mailbox map[string]any) (map[string]any, error) {
	token := util.Clean(mailbox["token"])
	data, err := registerMailRequestAny(p.client, http.MethodGet, "https://api.duckmail.sbs/messages", map[string]string{
		"Authorization": "Bearer " + token,
		"User-Agent":    p.conf.UserAgent,
		"Accept":        "application/json",
	}, map[string]string{"page": "1"}, nil, http.StatusOK, http.StatusCreated)
	if err != nil {
		return nil, err
	}
	items := duckMailItems(data)
	if len(items) == 0 {
		return nil, nil
	}
	messageID := strings.TrimPrefix(util.Clean(firstNonNil(items[0]["id"], items[0]["@id"])), "/messages/")
	if messageID == "" {
		return nil, nil
	}
	message, err := registerMailRequestJSON(p.client, http.MethodGet, "https://api.duckmail.sbs/messages/"+messageID, map[string]string{
		"Authorization": "Bearer " + token,
		"User-Agent":    p.conf.UserAgent,
		"Accept":        "application/json",
	}, nil, nil, http.StatusOK)
	if err != nil {
		return nil, err
	}
	textContent, htmlContent := extractRegisterMailContent(message)
	return map[string]any{
		"subject":      util.Clean(message["subject"]),
		"text_content": textContent,
		"html_content": htmlContent,
		"raw":          message["raw"],
	}, nil
}

func (p *registerGPTMailProvider) CreateMailbox(username string) (map[string]any, error) {
	payload := map[string]any{}
	if username = strings.TrimSpace(username); username != "" {
		payload["prefix"] = username
	}
	if domain := util.Clean(p.entry["default_domain"]); domain != "" {
		payload["domain"] = domain
	}
	method := http.MethodGet
	var requestBody any
	if len(payload) > 0 {
		method = http.MethodPost
		requestBody = payload
	}
	data, err := registerMailRequestAny(p.client, method, "https://mail.chatgpt.org.uk/api/generate-email", map[string]string{
		"X-API-Key":    util.Clean(p.entry["api_key"]),
		"User-Agent":   p.conf.UserAgent,
		"Accept":       "application/json",
		"Content-Type": "application/json",
	}, nil, requestBody, http.StatusOK)
	if err != nil {
		return nil, err
	}
	typed := util.StringMap(data)
	payloadMap := util.StringMap(firstNonNil(typed["data"], data))
	address := util.Clean(payloadMap["email"])
	if address == "" {
		return nil, fmt.Errorf("gptmail response missing email")
	}
	return map[string]any{"provider": "gptmail", "provider_ref": p.entry["provider_ref"], "address": address}, nil
}

func (p *registerGPTMailProvider) FetchLatestMessage(mailbox map[string]any) (map[string]any, error) {
	data, err := registerMailRequestAny(p.client, http.MethodGet, "https://mail.chatgpt.org.uk/api/emails", map[string]string{
		"X-API-Key":  util.Clean(p.entry["api_key"]),
		"User-Agent": p.conf.UserAgent,
		"Accept":     "application/json",
	}, map[string]string{"email": util.Clean(mailbox["address"])}, nil, http.StatusOK)
	if err != nil {
		return nil, err
	}
	body := util.StringMap(data)
	if nested := util.StringMap(body["data"]); len(nested) > 0 {
		body = nested
	}
	items := util.AsMapSlice(firstNonNil(body["emails"], body))
	if len(items) == 0 {
		return nil, nil
	}
	latest := latestRegisterMailMessage(items)
	if id := util.Clean(latest["id"]); id != "" {
		detail, detailErr := registerMailRequestAny(p.client, http.MethodGet, "https://mail.chatgpt.org.uk/api/email/"+id, map[string]string{
			"X-API-Key":  util.Clean(p.entry["api_key"]),
			"User-Agent": p.conf.UserAgent,
			"Accept":     "application/json",
		}, nil, nil, http.StatusOK)
		if detailErr == nil {
			if typed, ok := detail.(map[string]any); ok && typed["data"] != nil {
				latest = util.StringMap(typed["data"])
			} else if typed, ok := detail.(map[string]any); ok {
				latest = typed
			}
		}
	}
	textContent, htmlContent := extractRegisterMailContent(latest)
	return map[string]any{
		"subject":      util.Clean(latest["subject"]),
		"text_content": textContent,
		"html_content": htmlContent,
		"raw":          latest["raw"],
	}, nil
}

func (p *registerMoEmailProvider) CreateMailbox(username string) (map[string]any, error) {
	apiBase := strings.TrimRight(util.Clean(p.entry["api_base"]), "/")
	if apiBase == "" {
		return nil, fmt.Errorf("moemail api_base is required")
	}
	domain, err := nextRegisterDomain(util.AsStringSlice(p.entry["domain"]))
	if err != nil {
		return nil, err
	}
	payload := map[string]any{
		"name":       firstNonEmpty(strings.TrimSpace(username), registerRandomMailboxName()),
		"expiryTime": util.ToInt(p.entry["expiry_time"], 0),
		"domain":     domain,
	}
	data, err := registerMailRequestJSON(p.client, http.MethodPost, apiBase+"/api/emails/generate", map[string]string{
		"X-API-Key":    util.Clean(p.entry["api_key"]),
		"Content-Type": "application/json",
		"User-Agent":   p.conf.UserAgent,
		"Accept":       "application/json",
	}, nil, payload, http.StatusOK, http.StatusCreated)
	if err != nil {
		return nil, err
	}
	address := util.Clean(data["email"])
	emailID := firstNonEmpty(util.Clean(data["id"]), util.Clean(data["email_id"]))
	if address == "" || emailID == "" {
		return nil, fmt.Errorf("MoEmail missing email or id")
	}
	return map[string]any{"provider": "moemail", "provider_ref": p.entry["provider_ref"], "address": address, "email_id": emailID}, nil
}

func (p *registerMoEmailProvider) FetchLatestMessage(mailbox map[string]any) (map[string]any, error) {
	apiBase := strings.TrimRight(util.Clean(p.entry["api_base"]), "/")
	emailID := util.Clean(mailbox["email_id"])
	if apiBase == "" {
		return nil, fmt.Errorf("moemail api_base is required")
	}
	if emailID == "" {
		return nil, fmt.Errorf("MoEmail missing email_id")
	}
	data, err := registerMailRequestJSON(p.client, http.MethodGet, apiBase+"/api/emails/"+emailID, map[string]string{
		"X-API-Key":    util.Clean(p.entry["api_key"]),
		"Content-Type": "application/json",
		"User-Agent":   p.conf.UserAgent,
		"Accept":       "application/json",
	}, nil, nil, http.StatusOK)
	if err != nil {
		return nil, err
	}
	items := util.AsMapSlice(data["messages"])
	if len(items) == 0 {
		return nil, nil
	}
	latest := latestRegisterMailMessage(items)
	messageID := firstNonEmpty(util.Clean(latest["id"]), util.Clean(latest["message_id"]), util.Clean(latest["_id"]))
	message := latest
	raw := any(data)
	if messageID != "" {
		detail, detailErr := registerMailRequestJSON(p.client, http.MethodGet, apiBase+"/api/emails/"+emailID+"/"+messageID, map[string]string{
			"X-API-Key":    util.Clean(p.entry["api_key"]),
			"Content-Type": "application/json",
			"User-Agent":   p.conf.UserAgent,
			"Accept":       "application/json",
		}, nil, nil, http.StatusOK)
		if detailErr != nil {
			return nil, detailErr
		}
		raw = detail
		if nested := util.StringMap(detail["message"]); len(nested) > 0 {
			message = nested
		} else {
			message = detail
		}
	}
	textContent, htmlContent := extractRegisterMailContent(message)
	sender := firstNonNil(message["from"], message["sender"])
	if senderMap, ok := sender.(map[string]any); ok {
		sender = firstNonNil(senderMap["address"], senderMap["email"], senderMap["name"])
	}
	return map[string]any{
		"provider":     "moemail",
		"mailbox":      util.Clean(mailbox["address"]),
		"message_id":   messageID,
		"subject":      firstNonEmpty(util.Clean(message["subject"]), util.Clean(latest["subject"])),
		"sender":       util.Clean(sender),
		"text_content": textContent,
		"html_content": htmlContent,
		"received_at":  firstNonNil(message["createdAt"], message["created_at"], message["receivedAt"], message["date"], message["timestamp"], latest["createdAt"], latest["created_at"], latest["receivedAt"], latest["date"], latest["timestamp"]),
		"raw":          raw,
	}, nil
}

func duckMailItems(data any) []map[string]any {
	switch typed := data.(type) {
	case []any:
		return util.AsMapSlice(typed)
	case map[string]any:
		return util.AsMapSlice(firstNonNil(typed["hydra:member"], typed["member"], typed["data"]))
	default:
		return nil
	}
}

func registerMailRequestJSON(client *http.Client, method, target string, headers map[string]string, query map[string]string, payload any, expected ...int) (map[string]any, error) {
	data, err := registerMailRequestAny(client, method, target, headers, query, payload, expected...)
	if err != nil {
		return nil, err
	}
	return util.StringMap(data), nil
}

func registerMailRequestAny(client *http.Client, method, target string, headers map[string]string, query map[string]string, payload any, expected ...int) (any, error) {
	var bodyReader *strings.Reader
	if payload == nil {
		bodyReader = strings.NewReader("")
	} else {
		data, err := json.Marshal(payload)
		if err != nil {
			return nil, err
		}
		bodyReader = strings.NewReader(string(data))
	}
	if len(query) > 0 {
		parsed, err := url.Parse(target)
		if err != nil {
			return nil, err
		}
		values := parsed.Query()
		for key, value := range query {
			if strings.TrimSpace(value) != "" {
				values.Set(key, value)
			}
		}
		parsed.RawQuery = values.Encode()
		target = parsed.String()
	}
	req, err := http.NewRequest(method, target, bodyReader)
	if err != nil {
		return nil, err
	}
	for key, value := range headers {
		if strings.TrimSpace(value) != "" {
			req.Header.Set(key, value)
		}
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if !registerExpectedStatus(resp.StatusCode, expected...) {
		return nil, fmt.Errorf("mail request failed: %s %s -> HTTP %d", method, target, resp.StatusCode)
	}
	if resp.StatusCode == http.StatusNoContent {
		return map[string]any{}, nil
	}
	var data any
	if err := util.DecodeJSON(resp.Body, &data); err != nil {
		return nil, err
	}
	return data, nil
}

func registerExpectedStatus(status int, expected ...int) bool {
	for _, item := range expected {
		if status == item {
			return true
		}
	}
	return false
}

func firstNonNil(values ...any) any {
	for _, value := range values {
		if value != nil {
			return value
		}
	}
	return nil
}
