package config

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"chatgpt2api/internal/storage"
	"chatgpt2api/internal/util"
)

var settingEnvKeys = map[string]string{
	"base_url":                           "CHATGPT2API_BASE_URL",
	"proxy":                              "CHATGPT2API_PROXY",
	"refresh_account_interval_minute":    "CHATGPT2API_REFRESH_ACCOUNT_INTERVAL_MINUTE",
	"image_concurrent_limit":             "CHATGPT2API_IMAGE_CONCURRENT_LIMIT",
	"image_task_timeout_seconds":         "CHATGPT2API_IMAGE_TASK_TIMEOUT_SECONDS",
	"user_default_concurrent_limit":      "CHATGPT2API_USER_DEFAULT_CONCURRENT_LIMIT",
	"user_default_rpm_limit":             "CHATGPT2API_USER_DEFAULT_RPM_LIMIT",
	"image_retention_days":               "CHATGPT2API_IMAGE_RETENTION_DAYS",
	"auto_remove_invalid_accounts":       "CHATGPT2API_AUTO_REMOVE_INVALID_ACCOUNTS",
	"auto_remove_rate_limited_accounts":  "CHATGPT2API_AUTO_REMOVE_RATE_LIMITED_ACCOUNTS",
	"log_retention_days":                 "CHATGPT2API_LOG_RETENTION_DAYS",
	"log_levels":                         "CHATGPT2API_LOG_LEVELS",
	"linuxdo_enabled":                    "CHATGPT2API_LINUXDO_ENABLED",
	"linuxdo_client_id":                  "CHATGPT2API_LINUXDO_CLIENT_ID",
	"linuxdo_client_secret":              "CHATGPT2API_LINUXDO_CLIENT_SECRET",
	"linuxdo_redirect_url":               "CHATGPT2API_LINUXDO_REDIRECT_URL",
	"linuxdo_frontend_redirect_url":      "CHATGPT2API_LINUXDO_FRONTEND_REDIRECT_URL",
	"qq_enabled":                         "CHATGPT2API_QQ_ENABLED",
	"qq_client_id":                       "CHATGPT2API_QQ_CLIENT_ID",
	"qq_client_secret":                   "CHATGPT2API_QQ_CLIENT_SECRET",
	"qq_redirect_url":                    "CHATGPT2API_QQ_REDIRECT_URL",
	"qq_frontend_redirect_url":           "CHATGPT2API_QQ_FRONTEND_REDIRECT_URL",
	"social_login_base_url":              "CHATGPT2API_SOCIAL_LOGIN_BASE_URL",
	"social_login_app_id":                "CHATGPT2API_SOCIAL_LOGIN_APP_ID",
	"social_login_app_key":               "CHATGPT2API_SOCIAL_LOGIN_APP_KEY",
	"social_login_redirect_url":          "CHATGPT2API_SOCIAL_LOGIN_REDIRECT_URL",
	"social_login_frontend_redirect_url": "CHATGPT2API_SOCIAL_LOGIN_FRONTEND_REDIRECT_URL",
	"social_login_qq_enabled":            "CHATGPT2API_SOCIAL_LOGIN_QQ_ENABLED",
	"social_login_wx_enabled":            "CHATGPT2API_SOCIAL_LOGIN_WX_ENABLED",
	"social_login_douyin_enabled":        "CHATGPT2API_SOCIAL_LOGIN_DOUYIN_ENABLED",
	"update_repo":                        "CHATGPT2API_UPDATE_REPO",
	"update_github_token":                "CHATGPT2API_UPDATE_GITHUB_TOKEN",
	"registration_enabled":               "CHATGPT2API_REGISTRATION_ENABLED",
	"registration_require_invite_code":   "CHATGPT2API_REGISTRATION_REQUIRE_INVITE_CODE",
	"user_free_quota":                    "CHATGPT2API_USER_FREE_QUOTA",
	"invite_reward_quota":                "CHATGPT2API_INVITE_REWARD_QUOTA",
	"invitee_bonus_quota":                "CHATGPT2API_INVITEE_BONUS_QUOTA",
	"turnstile_enabled":                  "CHATGPT2API_TURNSTILE_ENABLED",
	"turnstile_site_key":                 "CHATGPT2API_TURNSTILE_SITE_KEY",
	"turnstile_secret_key":               "CHATGPT2API_TURNSTILE_SECRET_KEY",
	"image_upscale_2k_quota_cost":        "CHATGPT2API_IMAGE_UPSCALE_2K_QUOTA_COST",
	"image_upscale_4k_quota_cost":        "CHATGPT2API_IMAGE_UPSCALE_4K_QUOTA_COST",
	"checkin_enabled":                    "CHATGPT2API_CHECKIN_ENABLED",
	"checkin_rewards":                    "CHATGPT2API_CHECKIN_REWARDS",
	"login_page_image_url":               "CHATGPT2API_LOGIN_PAGE_IMAGE_URL",
	"login_page_image_mode":              "CHATGPT2API_LOGIN_PAGE_IMAGE_MODE",
	"login_page_image_zoom":              "CHATGPT2API_LOGIN_PAGE_IMAGE_ZOOM",
	"login_page_image_position_x":        "CHATGPT2API_LOGIN_PAGE_IMAGE_POSITION_X",
	"login_page_image_position_y":        "CHATGPT2API_LOGIN_PAGE_IMAGE_POSITION_Y",
}

var envKeyRE = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

const (
	defaultImageTaskTimeoutSeconds = 300
	minImageTaskTimeoutSeconds     = 30
	maxImageTaskTimeoutSeconds     = 3600
)

type Store struct {
	mu             sync.RWMutex
	RootDir        string
	DataDir        string
	EnvFile        string
	data           map[string]any
	storageBackend storage.Backend
}

type LinuxDoOAuthConfig struct {
	Enabled              bool
	ClientID             string
	ClientSecret         string
	AuthorizeURL         string
	TokenURL             string
	UserInfoURL          string
	Scopes               string
	RedirectURL          string
	FrontendRedirectURL  string
	TokenAuthMethod      string
	UsePKCE              bool
	UserInfoEmailPath    string
	UserInfoIDPath       string
	UserInfoUsernamePath string
}

type QQOAuthConfig struct {
	Enabled             bool
	ClientID            string
	ClientSecret        string
	AuthorizeURL        string
	TokenURL            string
	OpenIDURL           string
	UserInfoURL         string
	Scopes              string
	RedirectURL         string
	FrontendRedirectURL string
}

type SocialLoginConfig struct {
	BaseURL             string
	AppID               string
	AppKey              string
	RedirectURL         string
	FrontendRedirectURL string
	QQEnabled           bool
	WXEnabled           bool
	DouyinEnabled       bool
}

func NewStore() (*Store, error) {
	root, err := resolveRootDir()
	if err != nil {
		return nil, err
	}

	envFile := filepath.Join(root, ".env")
	envFileValues := readEnvObject(envFile)
	s := &Store{
		RootDir: root,
		DataDir: filepath.Join(root, "data"),
		EnvFile: envFile,
		data:    map[string]any{},
	}
	if err := os.MkdirAll(s.DataDir, 0o755); err != nil {
		return nil, err
	}
	s.loadEnvFile()
	s.data = settingsFromEnvValues(envFileValues)
	return s, nil
}

func resolveRootDir() (string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	if configured := strings.TrimSpace(os.Getenv("CHATGPT2API_ROOT")); configured != "" {
		return canonicalDir(configured)
	}
	if root := findAncestorWithFile(cwd, ".env"); root != "" {
		return root, nil
	}
	if root := findAncestorWithProjectGoMod(cwd); root != "" {
		return root, nil
	}
	return canonicalDir(cwd)
}

func findAncestorWithFile(start, name string) string {
	dir, err := filepath.Abs(start)
	if err != nil {
		return ""
	}
	for {
		info, statErr := os.Stat(filepath.Join(dir, name))
		if statErr == nil && !info.IsDir() {
			return canonicalDirOrSelf(dir)
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

func findAncestorWithProjectGoMod(start string) string {
	dir, err := filepath.Abs(start)
	if err != nil {
		return ""
	}
	for {
		data, readErr := os.ReadFile(filepath.Join(dir, "go.mod"))
		if readErr == nil && strings.Contains(string(data), "module chatgpt2api") {
			return canonicalDirOrSelf(dir)
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

func canonicalDir(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	if resolved, resolveErr := filepath.EvalSymlinks(abs); resolveErr == nil {
		return resolved, nil
	}
	return abs, nil
}

func canonicalDirOrSelf(path string) string {
	resolved, err := canonicalDir(path)
	if err != nil {
		return path
	}
	return resolved
}

func (s *Store) AdminUsername() string {
	value := strings.TrimSpace(os.Getenv("CHATGPT2API_ADMIN_USERNAME"))
	if value == "" {
		return "admin"
	}
	return value
}

func (s *Store) AdminPassword() string {
	return strings.TrimSpace(os.Getenv("CHATGPT2API_ADMIN_PASSWORD"))
}

func (s *Store) RegistrationEnabled() bool {
	return util.ToBool(s.settingValue("registration_enabled", false))
}

func (s *Store) RegistrationRequireInviteCode() bool {
	return util.ToBool(s.settingValue("registration_require_invite_code", false))
}

func (s *Store) UserFreeQuota() int {
	value := intSetting(s.settingValue("user_free_quota", 10), 10)
	if value < 0 {
		return 0
	}
	return value
}

func (s *Store) InviteRewardQuota() int {
	value := intSetting(s.settingValue("invite_reward_quota", 0), 0)
	if value < 0 {
		return 0
	}
	return value
}

func (s *Store) InviteeBonusQuota() int {
	value := intSetting(s.settingValue("invitee_bonus_quota", 0), 0)
	if value < 0 {
		return 0
	}
	return value
}

func (s *Store) TurnstileEnabled() bool {
	return util.ToBool(s.settingValue("turnstile_enabled", false))
}

func (s *Store) TurnstileSiteKey() string {
	return strings.TrimSpace(fmt.Sprint(s.settingValue("turnstile_site_key", "")))
}

func (s *Store) TurnstileSecretKey() string {
	return strings.TrimSpace(fmt.Sprint(s.settingValue("turnstile_secret_key", "")))
}

func (s *Store) TurnstileReady() bool {
	return s.TurnstileEnabled() && s.TurnstileSiteKey() != "" && s.TurnstileSecretKey() != ""
}

func (s *Store) ImageUpscale2KQuotaCost() int {
	value := intSetting(s.settingValue("image_upscale_2k_quota_cost", 2), 2)
	if value < 0 {
		return 0
	}
	return value
}

func (s *Store) ImageUpscale4KQuotaCost() int {
	value := intSetting(s.settingValue("image_upscale_4k_quota_cost", 5), 5)
	if value < 0 {
		return 0
	}
	return value
}

func (s *Store) CheckinEnabled() bool {
	return util.ToBool(s.settingValue("checkin_enabled", true))
}

func (s *Store) CheckinRewards() []int {
	return parseIntListSetting(s.settingValue("checkin_rewards", "1,1,2,2,3,3,7"), []int{1, 1, 2, 2, 3, 3, 7})
}

func (s *Store) RefreshAccountIntervalMinute() int {
	return intSetting(s.settingValue("refresh_account_interval_minute", 5), 5)
}

func (s *Store) ImageRetentionDays() int {
	value := intSetting(s.settingValue("image_retention_days", 30), 30)
	if value < 1 {
		return 1
	}
	return value
}

func (s *Store) LogRetentionDays() int {
	value := intSetting(s.settingValue("log_retention_days", 7), 7)
	if value < 1 {
		return 1
	}
	if value > 3650 {
		return 3650
	}
	return value
}

func (s *Store) ImageConcurrentLimit() int {
	value := intSetting(s.settingValue("image_concurrent_limit", 8), 8)
	if value < 1 {
		return 1
	}
	return value
}

func (s *Store) ImageTaskTimeoutSeconds() int {
	return normalizeImageTaskTimeoutSeconds(s.settingValue("image_task_timeout_seconds", defaultImageTaskTimeoutSeconds))
}

func (s *Store) UserDefaultConcurrentLimit() int {
	value := intSetting(s.settingValue("user_default_concurrent_limit", 0), 0)
	if value < 0 {
		return 0
	}
	return value
}

func (s *Store) UserDefaultRPMLimit() int {
	value := intSetting(s.settingValue("user_default_rpm_limit", 0), 0)
	if value < 0 {
		return 0
	}
	return value
}

func (s *Store) AutoRemoveInvalidAccounts() bool {
	return util.ToBool(s.settingValue("auto_remove_invalid_accounts", false))
}

func (s *Store) AutoRemoveRateLimitedAccounts() bool {
	return util.ToBool(s.settingValue("auto_remove_rate_limited_accounts", false))
}

func (s *Store) BaseURL() string {
	return strings.TrimRight(strings.TrimSpace(fmt.Sprint(s.settingValue("base_url", ""))), "/")
}

func (s *Store) Proxy() string {
	return strings.TrimSpace(fmt.Sprint(s.settingValue("proxy", "")))
}

func (s *Store) UpdateProxyURL() string {
	if value := strings.TrimSpace(os.Getenv("CHATGPT2API_UPDATE_PROXY_URL")); value != "" {
		return value
	}
	return s.Proxy()
}

func (s *Store) UpdateRepo() string {
	return normalizeUpdateRepo(s.settingValue("update_repo", "ZyphrZero/chatgpt2api"))
}

func (s *Store) UpdateGitHubToken() string {
	return strings.TrimSpace(fmt.Sprint(s.settingValue("update_github_token", "")))
}

func (s *Store) LogLevels() []string {
	raw := s.settingValue("log_levels", "")
	var parts []string
	switch v := raw.(type) {
	case []string:
		parts = v
	case []any:
		for _, item := range v {
			parts = append(parts, fmt.Sprint(item))
		}
	default:
		parts = strings.Split(fmt.Sprint(raw), ",")
	}
	allowed := map[string]struct{}{"debug": {}, "info": {}, "warning": {}, "error": {}}
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		level := strings.ToLower(strings.TrimSpace(part))
		if _, ok := allowed[level]; ok {
			out = append(out, level)
		}
	}
	return out
}

func (s *Store) LinuxDoOAuth() LinuxDoOAuthConfig {
	s.mu.RLock()
	data := util.CopyMap(s.data)
	s.mu.RUnlock()
	return s.linuxDoOAuthFromData(data)
}

func (s *Store) QQOAuth() QQOAuthConfig {
	s.mu.RLock()
	data := util.CopyMap(s.data)
	s.mu.RUnlock()
	return s.qqOAuthFromData(data)
}

func (s *Store) SocialLogin() SocialLoginConfig {
	s.mu.RLock()
	data := util.CopyMap(s.data)
	s.mu.RUnlock()
	return s.socialLoginFromData(data)
}

func (s *Store) socialLoginFromData(data map[string]any) SocialLoginConfig {
	redirectURL := strings.TrimSpace(fmt.Sprint(s.settingValueFromData(data, "social_login_redirect_url", "")))
	baseURL := strings.TrimRight(strings.TrimSpace(fmt.Sprint(s.settingValueFromData(data, "base_url", ""))), "/")
	if redirectURL == "" && baseURL != "" {
		redirectURL = baseURL + "/auth/social/oauth/callback"
	}
	frontendRedirect := strings.TrimSpace(fmt.Sprint(s.settingValueFromData(data, "social_login_frontend_redirect_url", "/auth/social/callback")))
	return SocialLoginConfig{
		BaseURL:             strings.TrimRight(strings.TrimSpace(fmt.Sprint(s.settingValueFromData(data, "social_login_base_url", ""))), "/"),
		AppID:               strings.TrimSpace(fmt.Sprint(s.settingValueFromData(data, "social_login_app_id", ""))),
		AppKey:              strings.TrimSpace(fmt.Sprint(s.settingValueFromData(data, "social_login_app_key", ""))),
		RedirectURL:         redirectURL,
		FrontendRedirectURL: frontendRedirect,
		QQEnabled:           util.ToBool(s.settingValueFromData(data, "social_login_qq_enabled", false)),
		WXEnabled:           util.ToBool(s.settingValueFromData(data, "social_login_wx_enabled", false)),
		DouyinEnabled:       util.ToBool(s.settingValueFromData(data, "social_login_douyin_enabled", false)),
	}
}

func (s *Store) qqOAuthFromData(data map[string]any) QQOAuthConfig {
	redirectURL := strings.TrimSpace(fmt.Sprint(s.settingValueFromData(data, "qq_redirect_url", "")))
	baseURL := strings.TrimRight(strings.TrimSpace(fmt.Sprint(s.settingValueFromData(data, "base_url", ""))), "/")
	if redirectURL == "" && baseURL != "" {
		redirectURL = baseURL + "/auth/qq/oauth/callback"
	}
	return QQOAuthConfig{
		Enabled:             util.ToBool(s.settingValueFromData(data, "qq_enabled", false)),
		ClientID:            strings.TrimSpace(fmt.Sprint(s.settingValueFromData(data, "qq_client_id", ""))),
		ClientSecret:        strings.TrimSpace(fmt.Sprint(s.settingValueFromData(data, "qq_client_secret", ""))),
		AuthorizeURL:        envString("CHATGPT2API_QQ_AUTHORIZE_URL", "https://graph.qq.com/oauth2.0/authorize"),
		TokenURL:            envString("CHATGPT2API_QQ_TOKEN_URL", "https://graph.qq.com/oauth2.0/token"),
		OpenIDURL:           envString("CHATGPT2API_QQ_OPENID_URL", "https://graph.qq.com/oauth2.0/me"),
		UserInfoURL:         envString("CHATGPT2API_QQ_USERINFO_URL", "https://graph.qq.com/user/get_user_info"),
		Scopes:              envString("CHATGPT2API_QQ_SCOPES", "get_user_info"),
		RedirectURL:         redirectURL,
		FrontendRedirectURL: strings.TrimSpace(fmt.Sprint(s.settingValueFromData(data, "qq_frontend_redirect_url", "/auth/qq/callback"))),
	}
}

func (s *Store) linuxDoOAuthFromData(data map[string]any) LinuxDoOAuthConfig {
	redirectURL := strings.TrimSpace(fmt.Sprint(s.settingValueFromData(data, "linuxdo_redirect_url", "")))
	baseURL := strings.TrimRight(strings.TrimSpace(fmt.Sprint(s.settingValueFromData(data, "base_url", ""))), "/")
	if redirectURL == "" && baseURL != "" {
		redirectURL = baseURL + "/auth/linuxdo/oauth/callback"
	}
	return LinuxDoOAuthConfig{
		Enabled:              util.ToBool(s.settingValueFromData(data, "linuxdo_enabled", false)),
		ClientID:             strings.TrimSpace(fmt.Sprint(s.settingValueFromData(data, "linuxdo_client_id", ""))),
		ClientSecret:         strings.TrimSpace(fmt.Sprint(s.settingValueFromData(data, "linuxdo_client_secret", ""))),
		AuthorizeURL:         envString("CHATGPT2API_LINUXDO_AUTHORIZE_URL", "https://connect.linux.do/oauth2/authorize"),
		TokenURL:             envString("CHATGPT2API_LINUXDO_TOKEN_URL", "https://connect.linux.do/oauth2/token"),
		UserInfoURL:          envString("CHATGPT2API_LINUXDO_USERINFO_URL", "https://connect.linux.do/api/user"),
		Scopes:               envString("CHATGPT2API_LINUXDO_SCOPES", "user"),
		RedirectURL:          redirectURL,
		FrontendRedirectURL:  strings.TrimSpace(fmt.Sprint(s.settingValueFromData(data, "linuxdo_frontend_redirect_url", "/auth/linuxdo/callback"))),
		TokenAuthMethod:      strings.ToLower(envString("CHATGPT2API_LINUXDO_TOKEN_AUTH_METHOD", "client_secret_post")),
		UsePKCE:              envBool("CHATGPT2API_LINUXDO_USE_PKCE", false),
		UserInfoEmailPath:    envString("CHATGPT2API_LINUXDO_USERINFO_EMAIL_PATH", ""),
		UserInfoIDPath:       envString("CHATGPT2API_LINUXDO_USERINFO_ID_PATH", ""),
		UserInfoUsernamePath: envString("CHATGPT2API_LINUXDO_USERINFO_USERNAME_PATH", ""),
	}
}

func (c LinuxDoOAuthConfig) Ready() bool {
	if !c.Enabled {
		return false
	}
	if c.ClientID == "" || c.AuthorizeURL == "" || c.TokenURL == "" || c.UserInfoURL == "" || c.RedirectURL == "" {
		return false
	}
	switch c.TokenAuthMethod {
	case "", "client_secret_post", "client_secret_basic":
		return c.ClientSecret != ""
	case "none":
		return c.UsePKCE
	default:
		return false
	}
}

func (c QQOAuthConfig) Ready() bool {
	if !c.Enabled {
		return false
	}
	return c.ClientID != "" &&
		c.ClientSecret != "" &&
		c.AuthorizeURL != "" &&
		c.TokenURL != "" &&
		c.OpenIDURL != "" &&
		c.UserInfoURL != "" &&
		c.RedirectURL != ""
}

func (c SocialLoginConfig) ProviderEnabled(provider string) bool {
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case "qq":
		return c.QQEnabled
	case "wx", "wechat":
		return c.WXEnabled
	case "douyin", "dy":
		return c.DouyinEnabled
	default:
		return false
	}
}

func (c SocialLoginConfig) Ready() bool {
	if c.BaseURL == "" || c.AppID == "" || c.AppKey == "" || c.RedirectURL == "" || c.FrontendRedirectURL == "" {
		return false
	}
	return c.QQEnabled || c.WXEnabled || c.DouyinEnabled
}

func (s *Store) ImagesDir() string {
	path := filepath.Join(s.DataDir, "images")
	_ = os.MkdirAll(path, 0o755)
	return path
}

func (s *Store) ImageThumbnailsDir() string {
	path := filepath.Join(s.DataDir, "image_thumbnails")
	_ = os.MkdirAll(path, 0o755)
	return path
}

func (s *Store) ImageMetadataDir() string {
	path := filepath.Join(s.DataDir, "image_metadata")
	_ = os.MkdirAll(path, 0o755)
	return path
}

func (s *Store) LoginPageImagesDir() string {
	path := filepath.Join(s.DataDir, "login_page_images")
	_ = os.MkdirAll(path, 0o755)
	return path
}

func (s *Store) LoginPageImageURL() string {
	return strings.TrimSpace(fmt.Sprint(s.settingValue("login_page_image_url", "")))
}

func (s *Store) LoginPageImageMode() string {
	return normalizeLoginPageImageMode(s.settingValue("login_page_image_mode", "contain"))
}

func (s *Store) LoginPageImageZoom() float64 {
	return clampFloat(floatSetting(s.settingValue("login_page_image_zoom", 1), 1), 1, 3)
}

func (s *Store) LoginPageImagePositionX() float64 {
	return clampFloat(floatSetting(s.settingValue("login_page_image_position_x", 50), 50), 0, 100)
}

func (s *Store) LoginPageImagePositionY() float64 {
	return clampFloat(floatSetting(s.settingValue("login_page_image_position_y", 50), 50), 0, 100)
}

func (s *Store) Get() map[string]any {
	s.mu.RLock()
	data := util.CopyMap(s.data)
	s.mu.RUnlock()
	data["refresh_account_interval_minute"] = s.RefreshAccountIntervalMinute()
	data["image_concurrent_limit"] = s.ImageConcurrentLimit()
	data["image_task_timeout_seconds"] = s.ImageTaskTimeoutSeconds()
	data["user_default_concurrent_limit"] = s.UserDefaultConcurrentLimit()
	data["user_default_rpm_limit"] = s.UserDefaultRPMLimit()
	data["image_retention_days"] = s.ImageRetentionDays()
	data["log_retention_days"] = s.LogRetentionDays()
	data["auto_remove_invalid_accounts"] = s.AutoRemoveInvalidAccounts()
	data["auto_remove_rate_limited_accounts"] = s.AutoRemoveRateLimitedAccounts()
	data["log_levels"] = s.LogLevels()
	data["proxy"] = s.Proxy()
	data["base_url"] = s.BaseURL()
	data["registration_enabled"] = s.RegistrationEnabled()
	data["registration_require_invite_code"] = s.RegistrationRequireInviteCode()
	data["user_free_quota"] = s.UserFreeQuota()
	data["invite_reward_quota"] = s.InviteRewardQuota()
	data["invitee_bonus_quota"] = s.InviteeBonusQuota()
	data["turnstile_enabled"] = s.TurnstileReady()
	data["turnstile_site_key"] = s.TurnstileSiteKey()
	data["turnstile_secret_configured"] = s.TurnstileSecretKey() != ""
	data["image_upscale_2k_quota_cost"] = s.ImageUpscale2KQuotaCost()
	data["image_upscale_4k_quota_cost"] = s.ImageUpscale4KQuotaCost()
	data["checkin_enabled"] = s.CheckinEnabled()
	data["checkin_rewards"] = s.CheckinRewards()
	linuxdo := s.LinuxDoOAuth()
	data["linuxdo_enabled"] = linuxdo.Enabled
	data["linuxdo_client_id"] = linuxdo.ClientID
	data["linuxdo_client_secret_configured"] = linuxdo.ClientSecret != ""
	data["linuxdo_redirect_url"] = linuxdo.RedirectURL
	data["linuxdo_frontend_redirect_url"] = linuxdo.FrontendRedirectURL
	qq := s.QQOAuth()
	data["qq_enabled"] = qq.Enabled
	data["qq_client_id"] = qq.ClientID
	data["qq_client_secret_configured"] = qq.ClientSecret != ""
	data["qq_redirect_url"] = qq.RedirectURL
	data["qq_frontend_redirect_url"] = qq.FrontendRedirectURL
	social := s.SocialLogin()
	data["social_login_base_url"] = social.BaseURL
	data["social_login_app_id"] = social.AppID
	data["social_login_app_key_configured"] = social.AppKey != ""
	data["social_login_redirect_url"] = social.RedirectURL
	data["social_login_frontend_redirect_url"] = social.FrontendRedirectURL
	data["social_login_qq_enabled"] = social.QQEnabled
	data["social_login_wx_enabled"] = social.WXEnabled
	data["social_login_douyin_enabled"] = social.DouyinEnabled
	data["update_repo"] = s.UpdateRepo()
	data["update_github_token_configured"] = s.UpdateGitHubToken() != ""
	data["login_page_image_url"] = s.LoginPageImageURL()
	data["login_page_image_mode"] = s.LoginPageImageMode()
	data["login_page_image_zoom"] = s.LoginPageImageZoom()
	data["login_page_image_position_x"] = s.LoginPageImagePositionX()
	data["login_page_image_position_y"] = s.LoginPageImagePositionY()
	delete(data, "linuxdo_client_secret")
	delete(data, "qq_client_secret")
	delete(data, "social_login_app_key")
	delete(data, "update_github_token")
	delete(data, "turnstile_secret_key")
	return data
}

func (s *Store) Update(data map[string]any) (map[string]any, error) {
	s.mu.Lock()
	next := util.CopyMap(s.data)
	for key, value := range data {
		if key == "linuxdo_client_secret_configured" {
			continue
		}
		if key == "qq_client_secret_configured" {
			continue
		}
		if key == "social_login_app_key_configured" {
			continue
		}
		if key == "update_github_token_configured" {
			continue
		}
		if key == "turnstile_secret_configured" {
			continue
		}
		if key == "linuxdo_client_secret" && strings.TrimSpace(fmt.Sprint(value)) == "" {
			continue
		}
		if key == "qq_client_secret" && strings.TrimSpace(fmt.Sprint(value)) == "" {
			continue
		}
		if key == "social_login_app_key" && strings.TrimSpace(fmt.Sprint(value)) == "" {
			continue
		}
		if key == "update_github_token" && strings.TrimSpace(fmt.Sprint(value)) == "" {
			continue
		}
		if key == "turnstile_secret_key" && strings.TrimSpace(fmt.Sprint(value)) == "" {
			continue
		}
		next[key] = value
	}
	if value, ok := next["login_page_image_mode"]; ok {
		next["login_page_image_mode"] = normalizeLoginPageImageMode(value)
	}
	if value, ok := next["image_task_timeout_seconds"]; ok {
		next["image_task_timeout_seconds"] = normalizeImageTaskTimeoutSeconds(value)
	}
	if value, ok := next["image_retention_days"]; ok {
		next["image_retention_days"] = normalizeImageRetentionDays(value)
	}
	next["update_repo"] = normalizeUpdateRepo(util.ValueOr(next["update_repo"], "ZyphrZero/chatgpt2api"))
	s.normalizeTurnstileUpdateLocked(next)
	if err := s.validateSettingsUpdateLocked(next); err != nil {
		s.mu.Unlock()
		return nil, err
	}
	s.data = next
	err := s.saveLocked()
	s.mu.Unlock()
	if err != nil {
		return nil, err
	}
	return s.Get(), nil
}

func (s *Store) CleanupOldImages() int {
	cutoff := time.Now().Add(-time.Duration(s.ImageRetentionDays()) * 24 * time.Hour)
	removed := 0
	for _, dir := range []string{s.ImagesDir(), s.ImageThumbnailsDir(), s.ImageMetadataDir()} {
		_ = filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return nil
			}
			info, statErr := d.Info()
			if statErr == nil && info.ModTime().Before(cutoff) {
				if os.Remove(path) == nil {
					removed++
				}
			}
			return nil
		})
		removeEmptyDirs(dir)
	}
	return removed
}

func (s *Store) StorageBackend() (storage.Backend, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.storageBackend != nil {
		return s.storageBackend, nil
	}
	backend, err := storage.NewBackendFromEnv(s.DataDir)
	if err != nil {
		return nil, err
	}
	s.storageBackend = backend
	return backend, nil
}

func (s *Store) settingValue(key string, fallback any) any {
	envKey := settingEnvKeys[key]
	s.mu.RLock()
	if value, ok := s.data[key]; ok {
		s.mu.RUnlock()
		return value
	}
	s.mu.RUnlock()
	if envKey != "" {
		if value, ok := os.LookupEnv(envKey); ok {
			return value
		}
	}
	return fallback
}

func (s *Store) settingValueFromData(data map[string]any, key string, fallback any) any {
	if data != nil {
		if value, ok := data[key]; ok {
			return value
		}
	}
	if envKey := settingEnvKeys[key]; envKey != "" {
		if value, ok := os.LookupEnv(envKey); ok {
			return value
		}
	}
	return fallback
}

func (s *Store) validateSettingsUpdateLocked(data map[string]any) error {
	if err := validateUpdateRepo(util.Clean(util.ValueOr(data["update_repo"], "ZyphrZero/chatgpt2api"))); err != nil {
		return err
	}
	if util.ToBool(s.settingValueFromData(data, "turnstile_enabled", false)) {
		siteKey := strings.TrimSpace(fmt.Sprint(s.settingValueFromData(data, "turnstile_site_key", "")))
		secretKey := strings.TrimSpace(fmt.Sprint(s.settingValueFromData(data, "turnstile_secret_key", "")))
		if siteKey == "" || secretKey == "" {
			return nil
		}
	}
	linuxdo := s.linuxDoOAuthFromData(data)
	if !linuxdo.Enabled {
		qq := s.qqOAuthFromData(data)
		if !qq.Enabled {
			return nil
		}
		if qq.ClientID == "" {
			return errors.New("QQ App ID is required when enabled")
		}
		if qq.ClientSecret == "" {
			return errors.New("QQ App Secret is required when enabled")
		}
		if qq.RedirectURL == "" {
			return errors.New("QQ Redirect URL is required when enabled")
		}
		if qq.FrontendRedirectURL == "" {
			return errors.New("QQ Frontend Redirect URL is required when enabled")
		}
		if err := validateAbsoluteHTTPURL(qq.RedirectURL); err != nil {
			return errors.New("QQ Redirect URL must be an absolute http(s) URL")
		}
		if err := validateFrontendRedirectURL(qq.FrontendRedirectURL); err != nil {
			return errors.New("QQ Frontend Redirect URL must be an absolute http(s) URL or a relative path")
		}
		return nil
	}
	if linuxdo.ClientID == "" {
		return errors.New("Linuxdo Client ID is required when enabled")
	}
	if linuxdo.RedirectURL == "" {
		return errors.New("Linuxdo Redirect URL is required when enabled")
	}
	if linuxdo.FrontendRedirectURL == "" {
		return errors.New("Linuxdo Frontend Redirect URL is required when enabled")
	}
	if err := validateAbsoluteHTTPURL(linuxdo.RedirectURL); err != nil {
		return errors.New("Linuxdo Redirect URL must be an absolute http(s) URL")
	}
	if err := validateFrontendRedirectURL(linuxdo.FrontendRedirectURL); err != nil {
		return errors.New("Linuxdo Frontend Redirect URL must be an absolute http(s) URL or a relative path")
	}
	switch linuxdo.TokenAuthMethod {
	case "", "client_secret_post", "client_secret_basic":
		if linuxdo.ClientSecret == "" {
			return errors.New("Linuxdo Client Secret is required when enabled")
		}
	case "none":
		if !linuxdo.UsePKCE {
			return errors.New("Linuxdo PKCE must be enabled when token auth method is none")
		}
	default:
		return errors.New("Linuxdo token auth method must be one of client_secret_post, client_secret_basic, none")
	}
	qq := s.qqOAuthFromData(data)
	if !qq.Enabled {
		return nil
	}
	if qq.ClientID == "" {
		return errors.New("QQ App ID is required when enabled")
	}
	if qq.ClientSecret == "" {
		return errors.New("QQ App Secret is required when enabled")
	}
	if qq.RedirectURL == "" {
		return errors.New("QQ Redirect URL is required when enabled")
	}
	if qq.FrontendRedirectURL == "" {
		return errors.New("QQ Frontend Redirect URL is required when enabled")
	}
	if err := validateAbsoluteHTTPURL(qq.RedirectURL); err != nil {
		return errors.New("QQ Redirect URL must be an absolute http(s) URL")
	}
	if err := validateFrontendRedirectURL(qq.FrontendRedirectURL); err != nil {
		return errors.New("QQ Frontend Redirect URL must be an absolute http(s) URL or a relative path")
	}
	social := s.socialLoginFromData(data)
	if !social.Ready() {
		return nil
	}
	if err := validateAbsoluteHTTPURL(social.BaseURL); err != nil {
		return errors.New("聚合登录接口地址必须是绝对 http(s) URL")
	}
	if social.AppID == "" {
		return errors.New("聚合登录 APPID 不能为空")
	}
	if social.AppKey == "" {
		return errors.New("聚合登录 APPKEY 不能为空")
	}
	if social.RedirectURL == "" {
		return errors.New("聚合登录后端回调地址不能为空")
	}
	if social.FrontendRedirectURL == "" {
		return errors.New("聚合登录前端完成页不能为空")
	}
	if err := validateAbsoluteHTTPURL(social.RedirectURL); err != nil {
		return errors.New("聚合登录后端回调地址必须是绝对 http(s) URL")
	}
	if err := validateFrontendRedirectURL(social.FrontendRedirectURL); err != nil {
		return errors.New("聚合登录前端完成页必须是绝对 http(s) URL 或相对路径")
	}
	return nil
}

func (s *Store) normalizeTurnstileUpdateLocked(data map[string]any) {
	if !util.ToBool(s.settingValueFromData(data, "turnstile_enabled", false)) {
		return
	}
	siteKey := strings.TrimSpace(fmt.Sprint(s.settingValueFromData(data, "turnstile_site_key", "")))
	secretKey := strings.TrimSpace(fmt.Sprint(s.settingValueFromData(data, "turnstile_secret_key", "")))
	if siteKey == "" || secretKey == "" {
		data["turnstile_enabled"] = false
	}
}

func normalizeUpdateRepo(value any) string {
	repo := strings.Trim(strings.TrimSpace(fmt.Sprint(value)), "/")
	if repo == "" {
		return "ZyphrZero/chatgpt2api"
	}
	return repo
}

func validateUpdateRepo(value string) error {
	if !regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`).MatchString(value) {
		return errors.New("Update repository must use owner/repo format")
	}
	return nil
}

func validateAbsoluteHTTPURL(value string) error {
	parsed, err := url.Parse(strings.TrimSpace(value))
	if err != nil {
		return err
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return errors.New("scheme must be http or https")
	}
	if parsed.Host == "" {
		return errors.New("host is required")
	}
	return nil
}

func validateFrontendRedirectURL(value string) error {
	value = strings.TrimSpace(value)
	if strings.ContainsAny(value, "\r\n") {
		return errors.New("newlines are not allowed")
	}
	parsed, err := url.Parse(value)
	if err != nil {
		return err
	}
	if parsed.Scheme != "" {
		if parsed.Scheme != "http" && parsed.Scheme != "https" {
			return errors.New("scheme must be http or https")
		}
		if parsed.Host == "" {
			return errors.New("host is required")
		}
		return nil
	}
	if !strings.HasPrefix(value, "/") || strings.HasPrefix(value, "//") {
		return errors.New("relative path must start with one slash")
	}
	return nil
}

func (s *Store) saveLocked() error {
	updates := map[string]string{}
	keys := make([]string, 0, len(settingEnvKeys))
	for key := range settingEnvKeys {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		if value, ok := s.data[key]; ok {
			updates[settingEnvKeys[key]] = stringifyEnvValue(value)
		}
	}
	if err := writeEnvUpdates(s.EnvFile, updates); err != nil {
		return err
	}
	for key, value := range updates {
		_ = os.Setenv(key, value)
	}
	return nil
}

func (s *Store) loadEnvFile() {
	for key, value := range readEnvObject(s.EnvFile) {
		if _, ok := os.LookupEnv(key); !ok {
			_ = os.Setenv(key, value)
		}
	}
}

func settingsFromEnvValues(values map[string]string) map[string]any {
	settings := map[string]any{}
	for settingKey, envKey := range settingEnvKeys {
		if value, ok := values[envKey]; ok {
			settings[settingKey] = value
		}
	}
	return settings
}

func intSetting(value any, fallback int) int {
	return util.ToInt(value, fallback)
}

func parseIntListSetting(value any, fallback []int) []int {
	if list, ok := value.([]int); ok {
		return append([]int(nil), list...)
	}
	if list, ok := value.([]any); ok {
		out := make([]int, 0, len(list))
		for _, item := range list {
			out = append(out, intSetting(item, 0))
		}
		if len(out) > 0 {
			return out
		}
	}
	text := strings.TrimSpace(fmt.Sprint(value))
	if text == "" {
		return append([]int(nil), fallback...)
	}
	parts := strings.FieldsFunc(text, func(r rune) bool {
		return r == ',' || r == '，' || r == ' ' || r == '\n' || r == '\t'
	})
	out := make([]int, 0, len(parts))
	for _, part := range parts {
		if strings.TrimSpace(part) == "" {
			continue
		}
		out = append(out, intSetting(part, 0))
	}
	if len(out) == 0 {
		return append([]int(nil), fallback...)
	}
	return out
}

func floatSetting(value any, fallback float64) float64 {
	switch v := value.(type) {
	case float64:
		return v
	case float32:
		return float64(v)
	case int:
		return float64(v)
	case int64:
		return float64(v)
	case string:
		n, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
		if err == nil {
			return n
		}
	}
	return fallback
}

func normalizeLoginPageImageMode(value any) string {
	mode := strings.ToLower(strings.TrimSpace(fmt.Sprint(value)))
	switch mode {
	case "cover", "contain", "fill":
		return mode
	default:
		return "contain"
	}
}

func normalizeImageTaskTimeoutSeconds(value any) int {
	seconds := intSetting(value, defaultImageTaskTimeoutSeconds)
	if seconds < minImageTaskTimeoutSeconds {
		return minImageTaskTimeoutSeconds
	}
	if seconds > maxImageTaskTimeoutSeconds {
		return maxImageTaskTimeoutSeconds
	}
	return seconds
}

func normalizeImageRetentionDays(value any) int {
	days := intSetting(value, 30)
	if days < 1 {
		return 1
	}
	return days
}

func clampFloat(value, min, max float64) float64 {
	if value < min {
		return min
	}
	if value > max {
		return max
	}
	return value
}

func envString(key, fallback string) string {
	if value, ok := os.LookupEnv(key); ok {
		return strings.TrimSpace(value)
	}
	return fallback
}

func envBool(key string, fallback bool) bool {
	if value, ok := os.LookupEnv(key); ok {
		value = strings.ToLower(strings.TrimSpace(value))
		return value == "1" || value == "true" || value == "yes" || value == "on"
	}
	return fallback
}

func readEnvObject(path string) map[string]string {
	data, err := os.ReadFile(path)
	if err != nil {
		if info, statErr := os.Stat(path); statErr == nil && info.IsDir() {
			fmt.Fprintf(os.Stderr, "Warning: .env at %q is a directory, ignoring it.\n", path)
		}
		return map[string]string{}
	}
	result := map[string]string{}
	for _, line := range strings.Split(string(data), "\n") {
		key, value, ok := parseEnvAssignment(line)
		if ok {
			result[key] = value
		}
	}
	return result
}

func parseEnvAssignment(line string) (string, string, bool) {
	stripped := strings.TrimSpace(line)
	if stripped == "" || strings.HasPrefix(stripped, "#") {
		return "", "", false
	}
	stripped = strings.TrimSpace(strings.TrimPrefix(stripped, "export "))
	key, value, ok := strings.Cut(stripped, "=")
	if !ok {
		return "", "", false
	}
	key = strings.TrimSpace(key)
	if !envKeyRE.MatchString(key) {
		return "", "", false
	}
	return key, unquoteEnvValue(value), true
}

func unquoteEnvValue(value string) string {
	value = strings.TrimSpace(value)
	if len(value) >= 2 && value[0] == value[len(value)-1] && (value[0] == '"' || value[0] == '\'') {
		inner := value[1 : len(value)-1]
		if value[0] == '"' {
			inner = strings.ReplaceAll(inner, `\n`, "\n")
			inner = strings.ReplaceAll(inner, `\r`, "\r")
			inner = strings.ReplaceAll(inner, `\t`, "\t")
			inner = strings.ReplaceAll(inner, `\"`, `"`)
			inner = strings.ReplaceAll(inner, `\\`, `\`)
		}
		return inner
	}
	for index, char := range value {
		if char == '#' && (index == 0 || value[index-1] == ' ' || value[index-1] == '\t') {
			return strings.TrimRight(value[:index], " \t")
		}
	}
	return value
}

func stringifyEnvValue(value any) string {
	switch v := value.(type) {
	case bool:
		if v {
			return "true"
		}
		return "false"
	case []string:
		return strings.Join(v, ",")
	case []int:
		items := make([]string, 0, len(v))
		for _, item := range v {
			items = append(items, strconv.Itoa(item))
		}
		return strings.Join(items, ",")
	case []any:
		items := make([]string, 0, len(v))
		for _, item := range v {
			if s := strings.TrimSpace(fmt.Sprint(item)); s != "" {
				items = append(items, s)
			}
		}
		return strings.Join(items, ",")
	default:
		return strings.TrimSpace(fmt.Sprint(util.ValueOr(value, "")))
	}
}

func writeEnvUpdates(path string, updates map[string]string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	var lines []string
	if data, err := os.ReadFile(path); err == nil {
		lines = strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n")
		if len(lines) > 0 && lines[len(lines)-1] == "" {
			lines = lines[:len(lines)-1]
		}
	}
	pending := map[string]string{}
	for key, value := range updates {
		pending[key] = value
	}
	next := make([]string, 0, len(lines)+len(updates)+1)
	for _, line := range lines {
		key, _, ok := parseEnvAssignment(line)
		if ok {
			if value, exists := pending[key]; exists {
				next = append(next, formatEnvAssignment(key, value))
				delete(pending, key)
				continue
			}
		}
		next = append(next, line)
	}
	if len(pending) > 0 {
		if len(next) > 0 && strings.TrimSpace(next[len(next)-1]) != "" {
			next = append(next, "")
		}
		keys := make([]string, 0, len(pending))
		for key := range pending {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			next = append(next, formatEnvAssignment(key, pending[key]))
		}
	}
	return os.WriteFile(path, []byte(strings.TrimRight(strings.Join(next, "\n"), "\n")+"\n"), 0o644)
}

func formatEnvAssignment(key, value string) string {
	return key + "=" + formatEnvValue(value)
}

func formatEnvValue(value string) string {
	if value == "" {
		return ""
	}
	if regexp.MustCompile(`^[A-Za-z0-9_./:@%+\-,]*$`).MatchString(value) {
		return value
	}
	value = strings.ReplaceAll(value, `\`, `\\`)
	value = strings.ReplaceAll(value, `"`, `\"`)
	value = strings.ReplaceAll(value, "\n", `\n`)
	return `"` + value + `"`
}

func removeEmptyDirs(root string) {
	var dirs []string
	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err == nil && d.IsDir() && path != root {
			dirs = append(dirs, path)
		}
		return nil
	})
	sort.Slice(dirs, func(i, j int) bool { return len(dirs[i]) > len(dirs[j]) })
	for _, dir := range dirs {
		_ = os.Remove(dir)
	}
}
