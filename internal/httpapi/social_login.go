package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"chatgpt2api/internal/config"
	"chatgpt2api/internal/service"
	"chatgpt2api/internal/util"
	"chatgpt2api/internal/version"
)

const (
	socialLoginCookiePath        = "/auth/social"
	socialLoginStateCookieName   = "social_login_state"
	socialLoginRedirectCookie    = "social_login_redirect"
	socialLoginProviderCookie    = "social_login_provider"
	socialLoginInviteCookieName  = "social_login_invite"
	socialLoginCookieMaxAgeSec   = 10 * 60
	socialLoginDefaultRedirectTo = "/image"
	socialLoginStartTimeout      = 6 * time.Second
	socialLoginCallbackTimeout   = 8 * time.Second
)

type socialLoginUser struct {
	ID       string
	Name     string
	Avatar   string
	Provider string
}

func (a *App) handleSocialLoginStart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	cfg := a.config.SocialLogin()
	if !cfg.Ready() {
		util.WriteError(w, http.StatusBadRequest, "social login is not configured")
		return
	}
	provider := normalizeSocialProvider(r.URL.Query().Get("type"))
	if provider == "" || !cfg.ProviderEnabled(provider) {
		util.WriteError(w, http.StatusBadRequest, "social login provider is not enabled")
		return
	}
	state := util.RandomTokenURL(32)
	redirectTo := sanitizeFrontendRedirectPath(r.URL.Query().Get("redirect"))
	inviteCode := strings.TrimSpace(r.URL.Query().Get("invite"))
	if redirectTo == "" {
		redirectTo = socialLoginDefaultRedirectTo
	}
	secureCookie := isHTTPSRequest(r)
	setSocialLoginCookie(w, socialLoginStateCookieName, encodeLinuxDoCookieValue(state), socialLoginCookieMaxAgeSec, secureCookie)
	setSocialLoginCookie(w, socialLoginRedirectCookie, encodeLinuxDoCookieValue(redirectTo), socialLoginCookieMaxAgeSec, secureCookie)
	setSocialLoginCookie(w, socialLoginProviderCookie, encodeLinuxDoCookieValue(provider), socialLoginCookieMaxAgeSec, secureCookie)
	if inviteCode != "" {
		setSocialLoginCookie(w, socialLoginInviteCookieName, encodeLinuxDoCookieValue(inviteCode), socialLoginCookieMaxAgeSec, secureCookie)
	}

	loginCtx, cancel := context.WithTimeout(r.Context(), socialLoginStartTimeout)
	defer cancel()
	loginURL, err := fetchSocialLoginRedirectURL(loginCtx, a.proxy.HTTPClient(socialLoginStartTimeout), cfg, provider, state)
	if err != nil {
		util.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	http.Redirect(w, r, loginURL, http.StatusFound)
}

func (a *App) handleSocialLoginCallback(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	cfg := a.config.SocialLogin()
	frontendCallback := sanitizeFrontendCallbackURL(cfg.FrontendRedirectURL)
	if !cfg.Ready() {
		redirectLinuxDoOAuthError(w, r, frontendCallback, "config_error", "聚合登录未配置", "")
		return
	}

	code := strings.TrimSpace(r.URL.Query().Get("code"))
	state := strings.TrimSpace(r.URL.Query().Get("state"))
	provider := normalizeSocialProvider(firstNonEmpty(r.URL.Query().Get("type"), r.URL.Query().Get("provider")))
	if code == "" {
		redirectLinuxDoOAuthError(w, r, frontendCallback, "missing_code", "missing code", "")
		return
	}
	secureCookie := isHTTPSRequest(r)
	defer func() {
		clearSocialLoginCookie(w, socialLoginStateCookieName, secureCookie)
		clearSocialLoginCookie(w, socialLoginRedirectCookie, secureCookie)
		clearSocialLoginCookie(w, socialLoginProviderCookie, secureCookie)
		clearSocialLoginCookie(w, socialLoginInviteCookieName, secureCookie)
	}()

	expectedState, _ := readLinuxDoCookieDecoded(r, socialLoginStateCookieName)
	if expectedState == "" || state == "" || expectedState != state {
		redirectLinuxDoOAuthError(w, r, frontendCallback, "invalid_state", "invalid oauth state", "")
		return
	}
	if provider == "" {
		provider, _ = readLinuxDoCookieDecoded(r, socialLoginProviderCookie)
		provider = normalizeSocialProvider(provider)
	}
	if provider == "" || !cfg.ProviderEnabled(provider) {
		redirectLinuxDoOAuthError(w, r, frontendCallback, "invalid_provider", "invalid social login provider", "")
		return
	}

	redirectTo, _ := readLinuxDoCookieDecoded(r, socialLoginRedirectCookie)
	redirectTo = sanitizeFrontendRedirectPath(redirectTo)
	if redirectTo == "" {
		redirectTo = socialLoginDefaultRedirectTo
	}
	inviteCode, _ := readLinuxDoCookieDecoded(r, socialLoginInviteCookieName)

	userCtx, cancel := context.WithTimeout(r.Context(), socialLoginCallbackTimeout)
	defer cancel()
	userInfo, err := fetchSocialLoginUser(userCtx, a.proxy.HTTPClient(socialLoginCallbackTimeout), cfg, provider, code)
	if err != nil {
		redirectLinuxDoOAuthError(w, r, frontendCallback, "userinfo_failed", "failed to fetch social user info", singleLine(err.Error()))
		return
	}

	sessionItem, rawSessionKey, err := a.auth.UpsertProviderSession(service.AuthOwner{
		ID:       userInfo.ID,
		Name:     userInfo.Name,
		Provider: userInfo.Provider,
	}, userInfo.Provider, providerDisplayName(userInfo.Provider))
	if err != nil {
		redirectLinuxDoOAuthError(w, r, frontendCallback, "login_failed", "failed to create local session", "")
		return
	}
	if !util.ToBool(sessionItem["enabled"]) {
		redirectLinuxDoOAuthError(w, r, frontendCallback, "account_disabled", "account is disabled", "")
		return
	}
	if a.profiles != nil {
		outcome, err := a.profiles.EnsureRegisteredUserOutcome(userInfo.ID, inviteCode, a.config.UserFreeQuota(), a.config.InviteRewardQuota(), a.config.InviteeBonusQuota())
		if err != nil {
			redirectLinuxDoOAuthError(w, r, frontendCallback, "profile_init_failed", "failed to initialize user rewards", singleLine(err.Error()))
			return
		}
		a.emitRegistrationNotifications(userInfo.ID, outcome)
	}

	fragment := url.Values{}
	fragment.Set("key", rawSessionKey)
	fragment.Set("role", service.AuthRoleUser)
	fragment.Set("role_id", util.Clean(sessionItem["role_id"]))
	fragment.Set("role_name", util.Clean(sessionItem["role_name"]))
	fragment.Set("provider", userInfo.Provider)
	fragment.Set("subject_id", userInfo.ID)
	fragment.Set("name", userInfo.Name)
	fragment.Set("version", version.Get())
	fragment.Set("redirect", redirectTo)
	setAuthSessionCookie(w, r, rawSessionKey)
	redirectWithFragment(w, r, frontendCallback, fragment)
}

func normalizeSocialProvider(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "qq":
		return "qq"
	case "wx", "wechat":
		return "wx"
	case "douyin", "dy":
		return "douyin"
	default:
		return ""
	}
}

func providerDisplayName(provider string) string {
	switch provider {
	case "qq":
		return "QQ 用户"
	case "wx":
		return "微信用户"
	case "douyin":
		return "抖音用户"
	default:
		return "第三方用户"
	}
}

func setSocialLoginCookie(w http.ResponseWriter, name string, value string, maxAgeSec int, secure bool) {
	http.SetCookie(w, &http.Cookie{
		Name:     name,
		Value:    value,
		Path:     socialLoginCookiePath,
		MaxAge:   maxAgeSec,
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
	})
}

func clearSocialLoginCookie(w http.ResponseWriter, name string, secure bool) {
	setSocialLoginCookie(w, name, "", -1, secure)
}

func buildSocialLoginURL(cfg config.SocialLoginConfig, provider string, state string) (string, error) {
	baseURL := strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/")
	if baseURL == "" {
		return "", fmt.Errorf("social login base url is required")
	}
	u, err := url.Parse(baseURL + "/connect.php")
	if err != nil {
		return "", err
	}
	q := u.Query()
	q.Set("act", "login")
	q.Set("appid", cfg.AppID)
	q.Set("appkey", cfg.AppKey)
	q.Set("type", provider)
	q.Set("redirect_uri", cfg.RedirectURL)
	if state != "" {
		q.Set("state", state)
	}
	u.RawQuery = q.Encode()
	return u.String(), nil
}

func fetchSocialLoginRedirectURL(ctx context.Context, client *http.Client, cfg config.SocialLoginConfig, provider string, state string) (string, error) {
	loginURL, err := buildSocialLoginURL(cfg, provider, state)
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, loginURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/json, text/plain, */*")
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	bodyBytes, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("social login start status=%d", resp.StatusCode)
	}
	var payload map[string]any
	if err := json.Unmarshal(bodyBytes, &payload); err != nil {
		return "", fmt.Errorf("decode social login start: %w", err)
	}
	if code := util.ToInt(payload["code"], -1); code != 0 {
		return "", fmt.Errorf("social login start failed: %s", strings.TrimSpace(firstNonEmpty(stringFromAny(payload["msg"]), "unknown error")))
	}
	redirectURL := strings.TrimSpace(firstNonEmpty(stringFromAny(payload["url"]), stringFromAny(payload["qrcode"])))
	if redirectURL == "" {
		return "", fmt.Errorf("social login start missing redirect url")
	}
	return redirectURL, nil
}

func fetchSocialLoginUser(ctx context.Context, client *http.Client, cfg config.SocialLoginConfig, provider, code string) (socialLoginUser, error) {
	baseURL := strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/")
	u, err := url.Parse(baseURL + "/connect.php")
	if err != nil {
		return socialLoginUser{}, err
	}
	q := u.Query()
	q.Set("act", "callback")
	q.Set("appid", cfg.AppID)
	q.Set("appkey", cfg.AppKey)
	q.Set("type", provider)
	q.Set("code", code)
	u.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return socialLoginUser{}, err
	}
	req.Header.Set("Accept", "application/json, text/plain, */*")
	resp, err := client.Do(req)
	if err != nil {
		return socialLoginUser{}, err
	}
	defer resp.Body.Close()
	bodyBytes, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return socialLoginUser{}, fmt.Errorf("social callback status=%d", resp.StatusCode)
	}
	var payload map[string]any
	if err := json.Unmarshal(bodyBytes, &payload); err != nil {
		return socialLoginUser{}, fmt.Errorf("decode social callback: %w", err)
	}
	uid := strings.TrimSpace(firstNonEmpty(
		stringFromAny(payload["social_uid"]),
		stringFromAny(payload["uid"]),
		stringFromAny(payload["openid"]),
		stringFromAny(payload["id"]),
	))
	if uid == "" {
		return socialLoginUser{}, fmt.Errorf("social callback missing uid")
	}
	nickname := strings.TrimSpace(firstNonEmpty(
		stringFromAny(payload["nickname"]),
		stringFromAny(payload["name"]),
		providerDisplayName(provider),
	))
	return socialLoginUser{
		ID:       uid,
		Name:     nickname,
		Avatar:   strings.TrimSpace(firstNonEmpty(stringFromAny(payload["faceimg"]), stringFromAny(payload["avatar"]))),
		Provider: provider,
	}, nil
}
