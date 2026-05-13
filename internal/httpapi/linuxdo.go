package httpapi

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"chatgpt2api/internal/config"
	"chatgpt2api/internal/service"
	"chatgpt2api/internal/util"
	"chatgpt2api/internal/version"
)

const (
	linuxDoOAuthCookiePath        = "/auth/linuxdo"
	linuxDoOAuthStateCookieName   = "linuxdo_oauth_state"
	linuxDoOAuthVerifierCookie    = "linuxdo_oauth_verifier"
	linuxDoOAuthRedirectCookie    = "linuxdo_oauth_redirect"
	linuxDoOAuthCookieMaxAgeSec   = 10 * 60
	linuxDoOAuthDefaultRedirectTo = "/image"
	linuxDoOAuthDefaultCallback   = "/auth/linuxdo/callback"
	linuxDoOAuthMaxRedirectLen    = 2048
	linuxDoOAuthMaxFragmentLen    = 512
	linuxDoOAuthMaxSubjectLen     = 64
)

type linuxDoTokenResponse struct {
	AccessToken  string
	TokenType    string
	ExpiresIn    int64
	RefreshToken string
	Scope        string
}

type linuxDoUserInfo struct {
	Username string
	Subject  string
	Level    string
}

func (a *App) handleAuthProviders(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	util.WriteJSON(w, http.StatusOK, map[string]any{
		"linuxdo": map[string]any{
			"enabled": a.config.LinuxDoOAuth().Ready(),
		},
		"registration": map[string]any{
			"enabled": a.config.RegistrationEnabled(),
		},
	})
}

func (a *App) handleLinuxDoOAuthStart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	cfg, ok := a.requireLinuxDoOAuthConfig(w)
	if !ok {
		return
	}

	state := util.RandomTokenURL(32)
	redirectTo := sanitizeFrontendRedirectPath(r.URL.Query().Get("redirect"))
	if redirectTo == "" {
		redirectTo = linuxDoOAuthDefaultRedirectTo
	}

	secureCookie := isHTTPSRequest(r)
	setLinuxDoCookie(w, linuxDoOAuthStateCookieName, encodeLinuxDoCookieValue(state), linuxDoOAuthCookieMaxAgeSec, secureCookie)
	setLinuxDoCookie(w, linuxDoOAuthRedirectCookie, encodeLinuxDoCookieValue(redirectTo), linuxDoOAuthCookieMaxAgeSec, secureCookie)

	codeChallenge := ""
	if cfg.UsePKCE {
		verifier := util.RandomTokenURL(48)
		codeChallenge = linuxDoCodeChallenge(verifier)
		setLinuxDoCookie(w, linuxDoOAuthVerifierCookie, encodeLinuxDoCookieValue(verifier), linuxDoOAuthCookieMaxAgeSec, secureCookie)
	}

	authURL, err := buildLinuxDoAuthorizeURL(cfg, state, codeChallenge)
	if err != nil {
		util.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	http.Redirect(w, r, authURL, http.StatusFound)
}

func (a *App) handleLinuxDoOAuthCallback(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	cfg := a.config.LinuxDoOAuth()
	frontendCallback := sanitizeFrontendCallbackURL(cfg.FrontendRedirectURL)

	if providerErr := strings.TrimSpace(r.URL.Query().Get("error")); providerErr != "" {
		redirectLinuxDoOAuthError(w, r, frontendCallback, "provider_error", providerErr, r.URL.Query().Get("error_description"))
		return
	}

	if !cfg.Ready() {
		redirectLinuxDoOAuthError(w, r, frontendCallback, "config_error", "Linuxdo login is not configured", "")
		return
	}

	code := strings.TrimSpace(r.URL.Query().Get("code"))
	state := strings.TrimSpace(r.URL.Query().Get("state"))
	if code == "" || state == "" {
		redirectLinuxDoOAuthError(w, r, frontendCallback, "missing_params", "missing code/state", "")
		return
	}

	secureCookie := isHTTPSRequest(r)
	defer func() {
		clearLinuxDoCookie(w, linuxDoOAuthStateCookieName, secureCookie)
		clearLinuxDoCookie(w, linuxDoOAuthVerifierCookie, secureCookie)
		clearLinuxDoCookie(w, linuxDoOAuthRedirectCookie, secureCookie)
	}()

	expectedState, err := readLinuxDoCookieDecoded(r, linuxDoOAuthStateCookieName)
	if err != nil || expectedState == "" || state != expectedState {
		redirectLinuxDoOAuthError(w, r, frontendCallback, "invalid_state", "invalid oauth state", "")
		return
	}

	redirectTo, _ := readLinuxDoCookieDecoded(r, linuxDoOAuthRedirectCookie)
	redirectTo = sanitizeFrontendRedirectPath(redirectTo)
	if redirectTo == "" {
		redirectTo = linuxDoOAuthDefaultRedirectTo
	}

	codeVerifier := ""
	if cfg.UsePKCE {
		codeVerifier, _ = readLinuxDoCookieDecoded(r, linuxDoOAuthVerifierCookie)
		if codeVerifier == "" {
			redirectLinuxDoOAuthError(w, r, frontendCallback, "missing_verifier", "missing pkce verifier", "")
			return
		}
	}

	token, err := linuxDoExchangeCode(r.Context(), a.proxy.HTTPClient(30*time.Second), cfg, code, codeVerifier)
	if err != nil {
		redirectLinuxDoOAuthError(w, r, frontendCallback, "token_exchange_failed", "failed to exchange oauth code", singleLine(err.Error()))
		return
	}
	userInfo, err := linuxDoFetchUserInfo(r.Context(), a.proxy.HTTPClient(30*time.Second), cfg, token)
	if err != nil {
		redirectLinuxDoOAuthError(w, r, frontendCallback, "userinfo_failed", "failed to fetch user info", singleLine(err.Error()))
		return
	}

	ownerID := linuxDoOwnerID(userInfo.Subject)
	sessionItem, rawSessionKey, err := a.auth.UpsertLinuxDoSessionIfAllowed(service.AuthOwner{
		ID:           ownerID,
		Name:         userInfo.Username,
		Provider:     service.AuthProviderLinuxDo,
		LinuxDoLevel: userInfo.Level,
	}, a.config.RegistrationEnabled())
	if err != nil {
		if errors.Is(err, service.ErrAuthUserCreationDisabled) {
			redirectLinuxDoOAuthError(w, r, frontendCallback, "registration_disabled", "已关闭注册通道", "")
			return
		}
		redirectLinuxDoOAuthError(w, r, frontendCallback, "login_failed", "failed to create local session", "")
		return
	}
	if !util.ToBool(sessionItem["enabled"]) {
		redirectLinuxDoOAuthError(w, r, frontendCallback, "account_disabled", "account is disabled", "")
		return
	}

	fragment := url.Values{}
	fragment.Set("key", rawSessionKey)
	fragment.Set("role", service.AuthRoleUser)
	fragment.Set("subject_id", ownerID)
	fragment.Set("name", userInfo.Username)
	fragment.Set("version", version.Get())
	fragment.Set("redirect", redirectTo)
	setAuthSessionCookie(w, r, rawSessionKey)
	redirectWithFragment(w, r, frontendCallback, fragment)
}

func (a *App) requireLinuxDoOAuthConfig(w http.ResponseWriter) (config.LinuxDoOAuthConfig, bool) {
	cfg := a.config.LinuxDoOAuth()
	if !cfg.Enabled {
		util.WriteError(w, http.StatusNotFound, "Linuxdo login is disabled")
		return config.LinuxDoOAuthConfig{}, false
	}
	if !cfg.Ready() {
		util.WriteError(w, http.StatusBadRequest, "Linuxdo login is not configured")
		return config.LinuxDoOAuthConfig{}, false
	}
	return cfg, true
}

func buildLinuxDoAuthorizeURL(cfg config.LinuxDoOAuthConfig, state string, codeChallenge string) (string, error) {
	u, err := url.Parse(cfg.AuthorizeURL)
	if err != nil {
		return "", fmt.Errorf("parse authorize url: %w", err)
	}
	q := u.Query()
	q.Set("response_type", "code")
	q.Set("client_id", cfg.ClientID)
	q.Set("redirect_uri", cfg.RedirectURL)
	if strings.TrimSpace(cfg.Scopes) != "" {
		q.Set("scope", cfg.Scopes)
	}
	q.Set("state", state)
	if cfg.UsePKCE {
		q.Set("code_challenge", codeChallenge)
		q.Set("code_challenge_method", "S256")
	}
	u.RawQuery = q.Encode()
	return u.String(), nil
}

func linuxDoExchangeCode(ctx context.Context, client *http.Client, cfg config.LinuxDoOAuthConfig, code string, codeVerifier string) (*linuxDoTokenResponse, error) {
	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("client_id", cfg.ClientID)
	form.Set("code", code)
	form.Set("redirect_uri", cfg.RedirectURL)
	if cfg.UsePKCE {
		form.Set("code_verifier", codeVerifier)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, cfg.TokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	switch strings.ToLower(strings.TrimSpace(cfg.TokenAuthMethod)) {
	case "", "client_secret_post":
		form.Set("client_secret", cfg.ClientSecret)
		req.Body = io.NopCloser(strings.NewReader(form.Encode()))
		req.ContentLength = int64(len(form.Encode()))
	case "client_secret_basic":
		req.SetBasicAuth(cfg.ClientID, cfg.ClientSecret)
	case "none":
	default:
		return nil, fmt.Errorf("unsupported token auth method: %s", cfg.TokenAuthMethod)
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request token: %w", err)
	}
	defer resp.Body.Close()
	bodyBytes, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	body := strings.TrimSpace(string(bodyBytes))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		providerErr, providerDesc := parseOAuthProviderError(body)
		detail := fmt.Sprintf("token exchange status=%d", resp.StatusCode)
		if providerErr != "" {
			detail += " error=" + providerErr
		}
		if providerDesc != "" {
			detail += " error_description=" + providerDesc
		}
		return nil, errors.New(detail)
	}

	token, ok := parseLinuxDoTokenResponse(body)
	if !ok || token.AccessToken == "" {
		return nil, errors.New("token response missing access_token")
	}
	if token.TokenType == "" {
		token.TokenType = "Bearer"
	}
	return token, nil
}

func linuxDoFetchUserInfo(ctx context.Context, client *http.Client, cfg config.LinuxDoOAuthConfig, token *linuxDoTokenResponse) (linuxDoUserInfo, error) {
	authorization, err := buildBearerAuthorization(token.TokenType, token.AccessToken)
	if err != nil {
		return linuxDoUserInfo{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, cfg.UserInfoURL, nil)
	if err != nil {
		return linuxDoUserInfo{}, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", authorization)
	resp, err := client.Do(req)
	if err != nil {
		return linuxDoUserInfo{}, fmt.Errorf("request userinfo: %w", err)
	}
	defer resp.Body.Close()
	bodyBytes, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return linuxDoUserInfo{}, fmt.Errorf("userinfo status=%d", resp.StatusCode)
	}
	return linuxDoParseUserInfo(string(bodyBytes), cfg)
}

func linuxDoParseUserInfo(body string, cfg config.LinuxDoOAuthConfig) (linuxDoUserInfo, error) {
	var payload any
	decoder := json.NewDecoder(strings.NewReader(body))
	decoder.UseNumber()
	if err := decoder.Decode(&payload); err != nil {
		return linuxDoUserInfo{}, fmt.Errorf("decode userinfo: %w", err)
	}
	subject := firstNonEmpty(
		jsonPathString(payload, cfg.UserInfoIDPath),
		jsonPathString(payload, "sub"),
		jsonPathString(payload, "id"),
		jsonPathString(payload, "user_id"),
		jsonPathString(payload, "uid"),
		jsonPathString(payload, "user.id"),
		jsonPathString(payload, "data.id"),
	)
	subject = strings.TrimSpace(subject)
	if subject == "" {
		return linuxDoUserInfo{}, errors.New("userinfo missing id field")
	}
	if !isSafeLinuxDoSubject(subject) {
		return linuxDoUserInfo{}, errors.New("userinfo returned invalid id field")
	}
	username := firstNonEmpty(
		jsonPathString(payload, cfg.UserInfoUsernamePath),
		jsonPathString(payload, "username"),
		jsonPathString(payload, "preferred_username"),
		jsonPathString(payload, "name"),
		jsonPathString(payload, "user.username"),
		jsonPathString(payload, "user.name"),
		jsonPathString(payload, "data.username"),
		jsonPathString(payload, "data.name"),
	)
	username = strings.TrimSpace(username)
	if username == "" {
		username = "linuxdo_" + subject
	}
	level := firstNonEmpty(
		jsonPathString(payload, "trust_level"),
		jsonPathString(payload, "trustLevel"),
		jsonPathString(payload, "level"),
		jsonPathString(payload, "user.trust_level"),
		jsonPathString(payload, "user.trustLevel"),
		jsonPathString(payload, "user.level"),
		jsonPathString(payload, "data.trust_level"),
		jsonPathString(payload, "data.trustLevel"),
		jsonPathString(payload, "data.level"),
	)
	return linuxDoUserInfo{
		Username: username,
		Subject:  subject,
		Level:    strings.TrimSpace(level),
	}, nil
}

func parseLinuxDoTokenResponse(body string) (*linuxDoTokenResponse, bool) {
	body = strings.TrimSpace(body)
	if body == "" {
		return nil, false
	}
	var payload map[string]any
	if json.Unmarshal([]byte(body), &payload) == nil && payload != nil {
		accessToken := strings.TrimSpace(stringFromAny(payload["access_token"]))
		if accessToken != "" {
			return &linuxDoTokenResponse{
				AccessToken:  accessToken,
				TokenType:    strings.TrimSpace(stringFromAny(payload["token_type"])),
				ExpiresIn:    int64FromAny(payload["expires_in"]),
				RefreshToken: strings.TrimSpace(stringFromAny(payload["refresh_token"])),
				Scope:        strings.TrimSpace(stringFromAny(payload["scope"])),
			}, true
		}
	}
	values, err := url.ParseQuery(body)
	if err != nil {
		return nil, false
	}
	accessToken := strings.TrimSpace(values.Get("access_token"))
	if accessToken == "" {
		return nil, false
	}
	expiresIn, _ := strconv.ParseInt(strings.TrimSpace(values.Get("expires_in")), 10, 64)
	return &linuxDoTokenResponse{
		AccessToken:  accessToken,
		TokenType:    strings.TrimSpace(values.Get("token_type")),
		ExpiresIn:    expiresIn,
		RefreshToken: strings.TrimSpace(values.Get("refresh_token")),
		Scope:        strings.TrimSpace(values.Get("scope")),
	}, true
}

func parseOAuthProviderError(body string) (providerErr string, providerDesc string) {
	body = strings.TrimSpace(body)
	if body == "" {
		return "", ""
	}
	var payload map[string]any
	if json.Unmarshal([]byte(body), &payload) == nil && payload != nil {
		providerErr = firstNonEmpty(stringFromAny(payload["error"]), stringFromAny(payload["code"]))
		providerDesc = firstNonEmpty(stringFromAny(payload["error_description"]), stringFromAny(payload["message"]), stringFromAny(payload["detail"]))
		if nested, _ := payload["error"].(map[string]any); nested != nil {
			providerDesc = firstNonEmpty(providerDesc, stringFromAny(nested["message"]))
		}
		if providerErr != "" || providerDesc != "" {
			return providerErr, providerDesc
		}
	}
	values, err := url.ParseQuery(body)
	if err != nil {
		return "", ""
	}
	return firstNonEmpty(values.Get("error"), values.Get("code")),
		firstNonEmpty(values.Get("error_description"), values.Get("error_message"), values.Get("message"))
}

func jsonPathString(payload any, path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	current := payload
	for _, part := range strings.Split(path, ".") {
		part = strings.TrimSpace(part)
		if part == "" {
			return ""
		}
		obj, ok := current.(map[string]any)
		if !ok {
			return ""
		}
		next, exists := obj[part]
		if !exists {
			return ""
		}
		current = next
	}
	return stringFromAny(current)
}

func stringFromAny(value any) string {
	switch v := value.(type) {
	case string:
		return v
	case json.Number:
		return v.String()
	case float64:
		if v == float64(int64(v)) {
			return strconv.FormatInt(int64(v), 10)
		}
		return strconv.FormatFloat(v, 'f', -1, 64)
	case bool:
		return strconv.FormatBool(v)
	default:
		return ""
	}
}

func int64FromAny(value any) int64 {
	switch v := value.(type) {
	case json.Number:
		n, _ := v.Int64()
		return n
	case float64:
		return int64(v)
	case string:
		n, _ := strconv.ParseInt(strings.TrimSpace(v), 10, 64)
		return n
	default:
		return 0
	}
}

func redirectLinuxDoOAuthError(w http.ResponseWriter, r *http.Request, frontendCallback string, code string, message string, description string) {
	fragment := url.Values{}
	fragment.Set("error", truncateFragmentValue(code))
	if strings.TrimSpace(message) != "" {
		fragment.Set("error_message", truncateFragmentValue(message))
	}
	if strings.TrimSpace(description) != "" {
		fragment.Set("error_description", truncateFragmentValue(description))
	}
	redirectWithFragment(w, r, frontendCallback, fragment)
}

func redirectWithFragment(w http.ResponseWriter, r *http.Request, frontendCallback string, fragment url.Values) {
	target := sanitizeFrontendCallbackURL(frontendCallback)
	u, err := url.Parse(target)
	if err != nil {
		http.Redirect(w, r, linuxDoOAuthDefaultCallback, http.StatusFound)
		return
	}
	encodedFragment := fragment.Encode()
	if decodedFragment, decodeErr := url.QueryUnescape(encodedFragment); decodeErr == nil {
		u.Fragment = decodedFragment
		u.RawFragment = encodedFragment
	} else {
		u.Fragment = encodedFragment
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Pragma", "no-cache")
	http.Redirect(w, r, u.String(), http.StatusFound)
}

func sanitizeFrontendRedirectPath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" || len(path) > linuxDoOAuthMaxRedirectLen {
		return ""
	}
	if !strings.HasPrefix(path, "/") || strings.HasPrefix(path, "//") || strings.Contains(path, "://") || strings.ContainsAny(path, "\r\n") {
		return ""
	}
	return path
}

func sanitizeFrontendCallbackURL(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return linuxDoOAuthDefaultCallback
	}
	if strings.ContainsAny(value, "\r\n") {
		return linuxDoOAuthDefaultCallback
	}
	u, err := url.Parse(value)
	if err != nil {
		return linuxDoOAuthDefaultCallback
	}
	if u.Scheme != "" && !strings.EqualFold(u.Scheme, "http") && !strings.EqualFold(u.Scheme, "https") {
		return linuxDoOAuthDefaultCallback
	}
	if u.Scheme == "" && (!strings.HasPrefix(value, "/") || strings.HasPrefix(value, "//")) {
		return linuxDoOAuthDefaultCallback
	}
	return value
}

func isHTTPSRequest(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	proto := strings.ToLower(strings.TrimSpace(r.Header.Get("X-Forwarded-Proto")))
	return proto == "https"
}

func encodeLinuxDoCookieValue(value string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(value))
}

func decodeLinuxDoCookieValue(value string) (string, error) {
	raw, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

func readLinuxDoCookieDecoded(r *http.Request, name string) (string, error) {
	cookie, err := r.Cookie(name)
	if err != nil {
		return "", err
	}
	return decodeLinuxDoCookieValue(cookie.Value)
}

func setLinuxDoCookie(w http.ResponseWriter, name string, value string, maxAgeSec int, secure bool) {
	http.SetCookie(w, &http.Cookie{
		Name:     name,
		Value:    value,
		Path:     linuxDoOAuthCookiePath,
		MaxAge:   maxAgeSec,
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
	})
}

func clearLinuxDoCookie(w http.ResponseWriter, name string, secure bool) {
	setLinuxDoCookie(w, name, "", -1, secure)
}

func linuxDoCodeChallenge(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

func buildBearerAuthorization(tokenType, accessToken string) (string, error) {
	tokenType = strings.TrimSpace(tokenType)
	if tokenType == "" {
		tokenType = "Bearer"
	}
	if !strings.EqualFold(tokenType, "Bearer") {
		return "", fmt.Errorf("unsupported token_type: %s", tokenType)
	}
	accessToken = strings.TrimSpace(accessToken)
	if accessToken == "" {
		return "", errors.New("missing access_token")
	}
	if strings.ContainsAny(accessToken, " \t\r\n") {
		return "", errors.New("access_token contains whitespace")
	}
	return "Bearer " + accessToken, nil
}

func isSafeLinuxDoSubject(subject string) bool {
	subject = strings.TrimSpace(subject)
	if subject == "" || len(subject) > linuxDoOAuthMaxSubjectLen {
		return false
	}
	for _, r := range subject {
		switch {
		case r >= '0' && r <= '9':
		case r >= 'a' && r <= 'z':
		case r >= 'A' && r <= 'Z':
		case r == '_' || r == '-':
		default:
			return false
		}
	}
	return true
}

func linuxDoOwnerID(subject string) string {
	return service.AuthProviderLinuxDo + ":" + strings.TrimSpace(subject)
}

func truncateFragmentValue(value string) string {
	value = strings.TrimSpace(value)
	if len(value) <= linuxDoOAuthMaxFragmentLen {
		return value
	}
	value = value[:linuxDoOAuthMaxFragmentLen]
	for !utf8.ValidString(value) {
		value = value[:len(value)-1]
	}
	return value
}

func singleLine(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	return strings.Join(strings.Fields(value), " ")
}
