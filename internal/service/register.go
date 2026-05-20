package service

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"math"
	mathrand "math/rand"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"chatgpt2api/internal/storage"
	"chatgpt2api/internal/util"
)

const (
	registerModeTotal     = "total"
	registerModeQuota     = "quota"
	registerModeAvailable = "available"

	registerAuthBase                 = "https://auth.openai.com"
	registerPlatformBase             = "https://platform.openai.com"
	registerPlatformOAuthClientID    = "app_2SKx67EdpoN0G6j64rFvigXD"
	registerPlatformOAuthRedirectURI = registerPlatformBase + "/auth/callback"
	registerPlatformOAuthAudience    = "https://api.openai.com/v1"
	registerPlatformAuth0Client      = "eyJuYW1lIjoiYXV0aDAtc3BhLWpzIiwidmVyc2lvbiI6IjEuMjEuMCJ9"
	registerUserAgent                = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/145.0.0.0 Safari/537.36"
	registerSecCHUA                  = `"Google Chrome";v="145", "Not?A_Brand";v="8", "Chromium";v="145"`
	registerSecCHUAFullVersionList   = `"Chromium";v="145.0.0.0", "Not:A-Brand";v="99.0.0.0", "Google Chrome";v="145.0.0.0"`
	registerSentinelBase             = "https://sentinel.openai.com"
	registerSentinelSDK              = registerSentinelBase + "/sentinel/20260124ceb8/sdk.js"
	registerSentinelMaxAttempts      = 500000
	registerSentinelErrorPrefix      = "wQ8Lk5FbGpA2NcR9dShT6gYjU7VxZ4D"
	registerTokenExchangeMaxAttempts = 2
)

var (
	registerFirstNames       = []string{"James", "Robert", "John", "Michael", "David", "Mary", "Emma", "Olivia"}
	registerLastNames        = []string{"Smith", "Johnson", "Williams", "Brown", "Jones", "Garcia", "Miller"}
	registerOAuthCodePattern = regexp.MustCompile(`(?i)(?:[?&]|&amp;|%3f|%26)code(?:=|%3d)([A-Za-z0-9._~%+-]+)`)
)

type RegisterService struct {
	mu          sync.Mutex
	path        string
	store       storage.JSONDocumentBackend
	docName     string
	accounts    *AccountService
	config      map[string]any
	logs        []map[string]any
	runnerAlive bool
	subscribers map[chan string]struct{}
}

type registerWorkerResult struct {
	ok     bool
	index  int
	result map[string]any
	err    string
	cost   float64
}

type registerWorker struct {
	service  *RegisterService
	index    int
	config   map[string]any
	mail     map[string]any
	client   *http.Client
	deviceID string
}

type registerSentinelTokenGenerator struct {
	deviceID  string
	userAgent string
	sid       string
}

func NewRegisterService(dataDir string, accounts *AccountService, backend ...storage.Backend) *RegisterService {
	s := &RegisterService{
		path:        filepath.Join(dataDir, "register.json"),
		store:       firstJSONDocumentStore(backend),
		docName:     "register.json",
		accounts:    accounts,
		config:      registerDefaultConfig(),
		subscribers: map[chan string]struct{}{},
	}
	s.config = s.load()
	if util.ToBool(s.config["enabled"]) {
		s.startLocked(false)
	}
	return s
}

func (s *RegisterService) Get() map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.snapshotLocked()
}

func (s *RegisterService) Update(updates map[string]any) map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.config = normalizeRegisterConfig(mergeMaps(s.config, updates))
	s.saveLocked()
	s.notifyLocked()
	return s.snapshotLocked()
}

func (s *RegisterService) Start() map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.startLocked(true)
	return s.snapshotLocked()
}

func (s *RegisterService) Stop() map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.config["enabled"] = false
	stats := util.StringMap(s.config["stats"])
	stats["updated_at"] = util.NowISO()
	s.config["stats"] = stats
	s.appendLogLocked("已请求停止注册任务，正在等待当前运行任务结束", "yellow")
	s.saveLocked()
	s.notifyLocked()
	return s.snapshotLocked()
}

func (s *RegisterService) Reset() map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.logs = nil
	s.config["stats"] = registerZeroStats(util.ToInt(s.config["threads"], 1), s.poolMetricsLocked())
	s.saveLocked()
	s.notifyLocked()
	return s.snapshotLocked()
}

func (s *RegisterService) Subscribe(ctx context.Context) <-chan string {
	ch := make(chan string, 8)
	s.mu.Lock()
	s.subscribers[ch] = struct{}{}
	initial := s.snapshotJSONLocked()
	s.mu.Unlock()
	ch <- initial
	go func() {
		<-ctx.Done()
		s.mu.Lock()
		delete(s.subscribers, ch)
		s.mu.Unlock()
		close(ch)
	}()
	return ch
}

func (s *RegisterService) Events(ctx context.Context) <-chan string {
	return s.Subscribe(ctx)
}

func (s *RegisterService) SnapshotJSON() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.snapshotJSONLocked()
}

func (s *RegisterService) startLocked(resetLogs bool) {
	if s.runnerAlive {
		s.config["enabled"] = true
		s.saveLocked()
		s.notifyLocked()
		return
	}
	if resetLogs {
		s.logs = nil
	}
	s.config["enabled"] = true
	stats := registerZeroStats(util.ToInt(s.config["threads"], 1), s.poolMetricsLocked())
	stats["job_id"] = util.NewHex(32)
	stats["started_at"] = util.NowISO()
	stats["updated_at"] = util.NowISO()
	s.config["stats"] = stats
	s.saveLocked()
	s.runnerAlive = true
	s.notifyLocked()
	mode := util.Clean(s.config["mode"])
	if mode == "" {
		mode = registerModeTotal
	}
	s.appendLogLocked(fmt.Sprintf("注册任务启动，模式=%s，线程数=%d", mode, util.ToInt(s.config["threads"], 1)), "yellow")
	go s.run()
}

func (s *RegisterService) run() {
	cfg := s.Get()
	threads := maxInt(1, util.ToInt(cfg["threads"], 1))
	submitted, running, done, success, fail := 0, 0, 0, 0, 0
	results := make(chan registerWorkerResult, threads)
	for {
		current := s.Get()
		for util.ToBool(current["enabled"]) && !s.targetReached(current, submitted) && running < threads {
			submitted++
			running++
			workerCfg := cloneMap(current)
			workerCfg["mail"] = cloneMap(util.StringMap(current["mail"]))
			go func(index int, config map[string]any) {
				results <- s.runWorker(index, config)
			}(submitted, workerCfg)
			current = s.Get()
		}
		s.bumpStats(map[string]any{"running": running, "done": done, "success": success, "fail": fail})
		if running == 0 {
			mode := util.Clean(current["mode"])
			if !util.ToBool(current["enabled"]) || mode == "" || mode == registerModeTotal {
				break
			}
			time.Sleep(time.Duration(maxInt(1, util.ToInt(current["check_interval"], 5))) * time.Second)
			continue
		}
		res := <-results
		running--
		done++
		if res.ok {
			success++
		} else {
			fail++
		}
	}
	s.bumpStats(map[string]any{"running": 0, "done": done, "success": success, "fail": fail, "finished_at": util.NowISO()})
	s.mu.Lock()
	s.runnerAlive = false
	s.config["enabled"] = false
	s.saveLocked()
	s.notifyLocked()
	s.appendLogLocked(fmt.Sprintf("注册任务结束，成功%d，失败%d", success, fail), "yellow")
	s.mu.Unlock()
}

func (s *RegisterService) runWorker(index int, config map[string]any) registerWorkerResult {
	start := time.Now()
	worker, err := newRegisterWorker(s, index, config)
	if err != nil {
		s.appendLog(fmt.Sprintf("任务%d 初始化失败，原因: %v", index, err), "red")
		return registerWorkerResult{ok: false, index: index, err: err.Error(), cost: time.Since(start).Seconds()}
	}
	defer worker.close()
	s.appendLog(fmt.Sprintf("[任务%d] 任务启动", index), "")
	result, runErr := worker.run(context.Background())
	cost := time.Since(start).Seconds()
	if runErr != nil {
		s.appendLog(fmt.Sprintf("任务%d 注册失败，本次耗时%.1fs，原因: %v", index, cost, runErr), "red")
		return registerWorkerResult{ok: false, index: index, err: runErr.Error(), cost: cost}
	}
	accessToken := util.Clean(result["access_token"])
	if accessToken == "" {
		err = fmt.Errorf("register flow did not return access_token")
		s.appendLog(fmt.Sprintf("任务%d 注册失败，本次耗时%.1fs，原因: %v", index, cost, err), "red")
		return registerWorkerResult{ok: false, index: index, err: err.Error(), cost: cost}
	}
	if s.accounts != nil {
		s.accounts.AddAccounts([]string{accessToken})
		go s.accounts.RefreshAccounts(context.Background(), []string{accessToken})
	}
	s.appendLog(fmt.Sprintf("%s 注册成功，本次耗时%.1fs", util.Clean(result["email"]), cost), "green")
	return registerWorkerResult{ok: true, index: index, result: result, cost: cost}
}

func newRegisterWorker(service *RegisterService, index int, config map[string]any) (*registerWorker, error) {
	deviceID := util.NewUUID()
	client, err := registerHTTPClient(util.Clean(config["proxy"]), 60*time.Second, deviceID)
	if err != nil {
		return nil, err
	}
	return &registerWorker{
		service:  service,
		index:    index,
		config:   config,
		mail:     util.StringMap(config["mail"]),
		client:   client,
		deviceID: deviceID,
	}, nil
}

func registerHTTPClient(proxy string, timeout time.Duration, deviceID string) (*http.Client, error) {
	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil, err
	}
	transport := transportForProxy("")
	if strings.TrimSpace(proxy) != "" {
		parsed, parseErr := url.Parse(proxy)
		if parseErr != nil {
			return nil, parseErr
		}
		if parsed.Host == "" {
			return nil, fmt.Errorf("invalid proxy url")
		}
		transport = transportForProxyURL(parsed)
	}
	client := &http.Client{Timeout: timeout, Transport: transport, Jar: jar}
	authURL, _ := url.Parse(registerAuthBase)
	if authURL != nil {
		jar.SetCookies(authURL, []*http.Cookie{
			{Name: "oai-did", Value: deviceID, Domain: ".auth.openai.com", Path: "/"},
			{Name: "oai-did", Value: deviceID, Domain: "auth.openai.com", Path: "/"},
		})
	}
	return client, nil
}

func (w *registerWorker) close() {
	if w.client != nil {
		w.client.CloseIdleConnections()
	}
}

func (w *registerWorker) run(ctx context.Context) (map[string]any, error) {
	w.step("开始创建邮箱")
	mailbox, err := createRegisterMailbox(w.mail, "")
	if err != nil {
		return nil, err
	}
	email := util.Clean(mailbox["address"])
	if email == "" {
		return nil, fmt.Errorf("mail provider did not return address")
	}
	w.step("邮箱创建完成: " + email)
	password := registerRandomPassword(16)
	firstName, lastName := registerRandomName()
	if err := w.platformAuthorize(ctx, email); err != nil {
		return nil, err
	}
	if err := w.registerUser(ctx, email, password); err != nil {
		return nil, err
	}
	if err := w.sendOTP(ctx); err != nil {
		return nil, err
	}
	w.step("开始等待注册验证码")
	code, err := waitRegisterCode(ctx, w.mail, mailbox)
	if err != nil {
		return nil, err
	}
	if code == "" {
		return nil, fmt.Errorf("waiting for register verification code timed out")
	}
	w.step("收到注册验证码: " + code)
	if err := w.validateOTP(ctx, code); err != nil {
		return nil, err
	}
	if err := w.createAccount(ctx, firstName+" "+lastName, registerRandomBirthdate()); err != nil {
		return nil, err
	}
	tokens, err := w.loginAndExchangeTokensWithRetry(ctx, email, password, mailbox)
	if err != nil {
		return nil, err
	}
	tokens["email"] = email
	tokens["password"] = password
	tokens["created_at"] = util.NowISO()
	return tokens, nil
}

func registerAuthorizeErrorDetail(payload map[string]any) string {
	errPayload := util.StringMap(payload["error"])
	if len(errPayload) == 0 {
		return registerResponseDetail(payload)
	}
	var parts []string
	if code := util.Clean(errPayload["code"]); code != "" {
		parts = append(parts, code)
	}
	if message := util.Clean(errPayload["message"]); message != "" {
		parts = append(parts, message)
	}
	if len(parts) == 0 {
		return registerResponseDetail(payload)
	}
	return ": " + strings.Join(parts, " - ")
}

func registerResponseDetail(payload map[string]any) string {
	if len(payload) == 0 {
		return ""
	}
	if summary := registerHTMLChallengeSummary(payload); summary != "" {
		return ", detail=" + summary
	}
	data, err := json.Marshal(payload)
	if err != nil || len(data) == 0 {
		return ""
	}
	return ", detail=" + string(data)
}

func registerHTMLChallengeSummary(payload map[string]any) string {
	body := strings.TrimSpace(util.Clean(payload["body"]))
	if body == "" {
		return ""
	}
	lower := strings.ToLower(body)
	if strings.Contains(lower, "cf_chl") ||
		strings.Contains(lower, "challenge-platform") ||
		strings.Contains(lower, "just a moment") ||
		strings.Contains(lower, "enable javascript and cookies to continue") ||
		strings.Contains(lower, "cloudflare") {
		return `{"body":"upstream returned Cloudflare challenge page"}`
	}
	if strings.Contains(lower, "<html") || strings.Contains(lower, "<!doctype html") || strings.Contains(lower, "<body") {
		return `{"body":"upstream returned HTML error page"}`
	}
	return ""
}

func registerFailedToCreateAccount(payload map[string]any) bool {
	return util.Clean(payload["message"]) == "Failed to create account. Please try again."
}

func registerNeedsEmailOTP(payload map[string]any, continueURL string) bool {
	page := util.StringMap(payload["page"])
	pageType := util.Clean(page["type"])
	if pageType == "email_otp_verification" {
		return true
	}
	text := strings.ToLower(continueURL + " " + util.Clean(payload["body"]))
	if strings.Contains(text, "email-verification") || strings.Contains(text, "email-otp") || strings.Contains(text, "email_otp") {
		return true
	}
	if len(payload) > 0 {
		data, err := json.Marshal(payload)
		if err == nil {
			lower := strings.ToLower(string(data))
			return strings.Contains(lower, "email_otp") || strings.Contains(lower, "verification code")
		}
	}
	return false
}

func registerInvalidState(payload map[string]any) bool {
	errPayload := util.StringMap(payload["error"])
	code := strings.ToLower(util.Clean(errPayload["code"]))
	message := strings.ToLower(util.Clean(errPayload["message"]))
	return code == "invalid_state" || strings.Contains(message, "invalid session")
}

func (w *registerWorker) platformAuthorize(ctx context.Context, email string) error {
	w.step("开始 platform authorize")
	values := registerAuthorizeParams(email, w.deviceID, registerRandomToken(), registerRandomToken(), registerPKCEChallenge())
	status, payload, err := w.request(ctx, http.MethodGet, registerAuthBase+"/api/accounts/authorize?"+values.Encode(), nil, w.navigateHeaders(registerPlatformBase+"/"), true)
	if err != nil {
		return err
	}
	if status != http.StatusOK {
		return fmt.Errorf("platform_authorize_http_%d%s", status, registerAuthorizeErrorDetail(payload))
	}
	w.step("platform authorize 完成")
	return nil
}

func (w *registerWorker) registerUser(ctx context.Context, email, password string) error {
	w.step("开始提交注册密码")
	headers := w.jsonHeaders(registerAuthBase + "/create-account/password")
	token, err := w.buildSentinelToken(ctx, "username_password_create")
	if err != nil {
		return err
	}
	headers["openai-sentinel-token"] = token
	status, payload, err := w.request(ctx, http.MethodPost, registerAuthBase+"/api/accounts/user/register", map[string]any{
		"username": email,
		"password": password,
	}, headers, true)
	if err != nil {
		return err
	}
	if status != http.StatusOK {
		if registerFailedToCreateAccount(payload) {
			w.step("注册失败提示: 邮箱域名很可能因滥用被封禁，请更换邮箱域名")
		}
		return fmt.Errorf("user_register_http_%d%s", status, registerResponseDetail(payload))
	}
	w.step("提交注册密码完成")
	return nil
}

func (w *registerWorker) sendOTP(ctx context.Context) error {
	w.step("开始发送验证码")
	status, _, err := w.request(ctx, http.MethodGet, registerAuthBase+"/api/accounts/email-otp/send", nil, w.navigateHeaders(registerAuthBase+"/create-account/password"), true)
	if err != nil {
		return err
	}
	if status != http.StatusOK && status != http.StatusFound {
		return fmt.Errorf("send_otp_http_%d", status)
	}
	w.step("发送验证码完成")
	return nil
}

func (w *registerWorker) validateOTP(ctx context.Context, code string) error {
	w.step("开始校验验证码 " + code)
	if _, err := w.validateOTPCode(ctx, code); err != nil {
		return err
	}
	w.step("验证码校验完成")
	return nil
}

func (w *registerWorker) createAccount(ctx context.Context, name, birthdate string) error {
	w.step("开始创建账号资料")
	headers := w.jsonHeaders(registerAuthBase + "/about-you")
	token, err := w.buildSentinelToken(ctx, "oauth_create_account")
	if err != nil {
		return err
	}
	headers["openai-sentinel-token"] = token
	status, payload, err := w.request(ctx, http.MethodPost, registerAuthBase+"/api/accounts/create_account", map[string]any{
		"name":      name,
		"birthdate": birthdate,
	}, headers, true)
	if err != nil {
		return err
	}
	if status != http.StatusOK && status != http.StatusFound {
		if registerFailedToCreateAccount(payload) {
			w.step("创建账号失败提示: 邮箱域名很可能因滥用被封禁，请更换邮箱域名")
		}
		return fmt.Errorf("create_account_http_%d%s", status, registerResponseDetail(payload))
	}
	w.step("创建账号资料完成")
	return nil
}

func (w *registerWorker) loginAndExchangeTokensWithRetry(ctx context.Context, email, password string, mailbox map[string]any) (map[string]any, error) {
	var lastErr error
	for attempt := 1; attempt <= registerTokenExchangeMaxAttempts; attempt++ {
		loginWorker, err := w.newLoginSession()
		if err != nil {
			return nil, err
		}
		tokens, err := loginWorker.loginAndExchangeTokens(ctx, email, password, mailbox)
		loginWorker.close()
		if err == nil {
			return tokens, nil
		}
		lastErr = err
		if !registerShouldRetryLoginExchange(err) || attempt == registerTokenExchangeMaxAttempts {
			return nil, err
		}
		w.step("登录换 token 状态失效，重建会话重试")
		time.Sleep(registerJitterDelay(400*time.Millisecond, 1200*time.Millisecond))
	}
	return nil, lastErr
}

func (w *registerWorker) newLoginSession() (*registerWorker, error) {
	deviceID := util.NewUUID()
	client, err := registerHTTPClient(util.Clean(w.config["proxy"]), 60*time.Second, deviceID)
	if err != nil {
		return nil, err
	}
	if w.client != nil && w.client.Transport != nil {
		if _, ok := w.client.Transport.(*http.Transport); !ok {
			client.Transport = w.client.Transport
		}
	}
	return &registerWorker{
		service:  w.service,
		index:    w.index,
		config:   w.config,
		mail:     w.mail,
		client:   client,
		deviceID: deviceID,
	}, nil
}

func registerShouldRetryLoginExchange(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "invalid_state") ||
		strings.Contains(message, "invalid session") ||
		strings.Contains(message, "token exchange callback code not found")
}

func registerJitterDelay(minDelay, maxDelay time.Duration) time.Duration {
	if maxDelay <= minDelay {
		return minDelay
	}
	return minDelay + time.Duration(mathrand.Int63n(int64(maxDelay-minDelay)))
}

func (w *registerWorker) loginAndExchangeTokens(ctx context.Context, email, password string, mailbox map[string]any) (map[string]any, error) {
	w.step("开始独立登录换 token")
	codeVerifier, codeChallenge := generateRegisterPKCE()
	values := registerAuthorizeParams(email, w.deviceID, registerRandomToken(), registerRandomToken(), codeChallenge)
	status, payload, err := w.request(ctx, http.MethodGet, registerAuthBase+"/api/accounts/authorize?"+values.Encode(), nil, w.navigateHeaders(registerPlatformBase+"/"), true)
	if err != nil {
		return nil, err
	}
	if status != http.StatusOK {
		return nil, fmt.Errorf("platform_login_authorize_http_%d%s", status, registerAuthorizeErrorDetail(payload))
	}
	w.step("登录 authorize 完成")
	headers := w.jsonHeaders(registerAuthBase + "/log-in/password")
	token, err := w.buildSentinelToken(ctx, "password_verify")
	if err != nil {
		return nil, err
	}
	headers["openai-sentinel-token"] = token
	status, payload, err = w.request(ctx, http.MethodPost, registerAuthBase+"/api/accounts/password/verify", map[string]any{
		"password": password,
	}, headers, false)
	if err != nil {
		return nil, err
	}
	continueURL := util.Clean(payload["continue_url"])
	if status != http.StatusOK {
		if status == http.StatusUnauthorized || status == http.StatusConflict {
			if registerNeedsEmailOTP(payload, continueURL) {
				w.step(fmt.Sprintf("密码校验进入邮箱验证码分支（HTTP %d）", status))
			} else if registerInvalidState(payload) {
				return nil, fmt.Errorf("password_verify_http_%d%s", status, registerResponseDetail(payload))
			} else {
				return nil, fmt.Errorf("password_verify_http_%d%s", status, registerResponseDetail(payload))
			}
		} else {
			return nil, fmt.Errorf("password_verify_http_%d%s", status, registerResponseDetail(payload))
		}
	} else {
		w.step("密码校验完成")
	}
	if registerNeedsEmailOTP(payload, continueURL) {
		w.step("独立登录需要邮箱验证码")
		code, waitErr := waitRegisterCode(ctx, w.mail, mailbox)
		if waitErr != nil {
			return nil, waitErr
		}
		if code == "" {
			return nil, fmt.Errorf("independent login waiting for verification code timed out")
		}
		otpPayload, otpErr := w.validateOTPCode(ctx, code)
		if otpErr != nil {
			return nil, otpErr
		}
		if next := util.Clean(otpPayload["continue_url"]); next != "" {
			continueURL = next
		}
		w.step("独立登录验证码校验完成")
	}
	if continueURL == "" {
		continueURL = registerAuthBase + "/sign-in-with-chatgpt/codex/consent"
	}
	code, err := w.followConsentForCode(ctx, continueURL)
	if err != nil {
		return nil, err
	}
	if code == "" {
		return nil, fmt.Errorf("token exchange callback code not found")
	}
	status, tokenPayload, err := w.requestForm(ctx, registerAuthBase+"/oauth/token", url.Values{
		"grant_type":    []string{"authorization_code"},
		"code":          []string{code},
		"redirect_uri":  []string{registerPlatformOAuthRedirectURI},
		"client_id":     []string{registerPlatformOAuthClientID},
		"code_verifier": []string{codeVerifier},
	})
	if err != nil {
		return nil, err
	}
	if status != http.StatusOK {
		return nil, fmt.Errorf("oauth_token_http_%d", status)
	}
	accessToken := util.Clean(tokenPayload["access_token"])
	refreshToken := util.Clean(tokenPayload["refresh_token"])
	idToken := util.Clean(tokenPayload["id_token"])
	if accessToken == "" || refreshToken == "" || idToken == "" {
		return nil, fmt.Errorf("token exchange response missing access_token, refresh_token, or id_token")
	}
	w.step("token 换取完成")
	return map[string]any{
		"access_token":  accessToken,
		"refresh_token": refreshToken,
		"id_token":      idToken,
	}, nil
}

func (w *registerWorker) followConsentForCode(ctx context.Context, continueURL string) (string, error) {
	current := continueURL
	if strings.HasPrefix(current, "/") {
		current = registerAuthBase + current
	}
	if code := registerOAuthCode(current); code != "" {
		return code, nil
	}
	noRedirect := *w.client
	noRedirect.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		return http.ErrUseLastResponse
	}
	for i := 0; i < 10; i++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, current, nil)
		if err != nil {
			return "", err
		}
		for key, value := range w.navigateHeaders(current) {
			req.Header.Set(key, value)
		}
		resp, err := noRedirect.Do(req)
		if err != nil {
			return "", err
		}
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 32768))
		resp.Body.Close()
		if code := registerOAuthCode(resp.Request.URL.String()); code != "" {
			return code, nil
		}
		location := strings.TrimSpace(resp.Header.Get("Location"))
		if code := registerOAuthCode(location); code != "" {
			return code, nil
		}
		if code := registerOAuthCode(string(body)); code != "" {
			return code, nil
		}
		if location == "" {
			location = registerRefreshLocation(resp.Header.Get("Refresh"))
		}
		if location == "" || (resp.StatusCode < 300 || resp.StatusCode >= 400) {
			break
		}
		next, err := resolveRegisterLocation(current, location)
		if err != nil {
			return "", err
		}
		current = next
	}
	return w.selectWorkspaceForConsentCode(ctx, continueURL)
}

func (w *registerWorker) validateOTPCode(ctx context.Context, code string) (map[string]any, error) {
	status, payload, err := w.request(ctx, http.MethodPost, registerAuthBase+"/api/accounts/email-otp/validate", map[string]any{"code": code}, w.jsonHeaders(registerAuthBase+"/email-verification"), true)
	if err != nil {
		return nil, err
	}
	if status == http.StatusOK {
		return payload, nil
	}
	headers := w.jsonHeaders(registerAuthBase + "/email-verification")
	token, tokenErr := w.buildSentinelToken(ctx, "authorize_continue")
	if tokenErr != nil {
		return nil, fmt.Errorf("validate_otp_http_%d; sentinel fallback failed: %w", status, tokenErr)
	}
	headers["openai-sentinel-token"] = token
	status, payload, err = w.request(ctx, http.MethodPost, registerAuthBase+"/api/accounts/email-otp/validate", map[string]any{"code": code}, headers, true)
	if err != nil {
		return nil, err
	}
	if status != http.StatusOK {
		return nil, fmt.Errorf("validate_otp_http_%d", status)
	}
	return payload, nil
}

func (w *registerWorker) selectWorkspaceForConsentCode(ctx context.Context, consentURL string) (string, error) {
	workspaceID := w.authSessionWorkspaceID()
	if workspaceID == "" {
		return "", nil
	}
	if strings.HasPrefix(consentURL, "/") {
		consentURL = registerAuthBase + consentURL
	}
	headers := w.jsonHeaders(consentURL)
	status, wsPayload, wsHeaders, err := w.requestDetailed(ctx, http.MethodPost, registerAuthBase+"/api/accounts/workspace/select", map[string]any{
		"workspace_id": workspaceID,
	}, headers, false)
	if err != nil {
		return "", err
	}
	if code := registerOAuthCode(wsHeaders.Get("Location")); code != "" {
		return code, nil
	}
	if code := registerOAuthCode(util.Clean(wsPayload["continue_url"])); code != "" {
		return code, nil
	}
	if status < 200 || status >= 400 {
		return "", fmt.Errorf("workspace_select_http_%d", status)
	}
	data := util.StringMap(wsPayload["data"])
	orgs := util.AsMapSlice(data["orgs"])
	if len(orgs) == 0 {
		return "", nil
	}
	orgID := util.Clean(orgs[0]["id"])
	if orgID == "" {
		return "", nil
	}
	orgBody := map[string]any{"org_id": orgID}
	if projects := util.AsMapSlice(orgs[0]["projects"]); len(projects) > 0 {
		if projectID := util.Clean(projects[0]["id"]); projectID != "" {
			orgBody["project_id"] = projectID
		}
	}
	orgReferer := firstNonEmpty(util.Clean(wsPayload["continue_url"]), consentURL)
	status, orgPayload, orgHeaders, err := w.requestDetailed(ctx, http.MethodPost, registerAuthBase+"/api/accounts/organization/select", orgBody, w.jsonHeaders(orgReferer), false)
	if err != nil {
		return "", err
	}
	if code := registerOAuthCode(orgHeaders.Get("Location")); code != "" {
		return code, nil
	}
	if code := registerOAuthCode(util.Clean(orgPayload["continue_url"])); code != "" {
		return code, nil
	}
	if status < 200 || status >= 400 {
		return "", fmt.Errorf("organization_select_http_%d", status)
	}
	return "", nil
}

func (w *registerWorker) authSessionWorkspaceID() string {
	if w.client == nil || w.client.Jar == nil {
		return ""
	}
	authURL, err := url.Parse(registerAuthBase)
	if err != nil {
		return ""
	}
	var raw string
	for _, cookie := range w.client.Jar.Cookies(authURL) {
		if cookie.Name == "oai-client-auth-session" {
			raw = cookie.Value
			break
		}
	}
	if raw == "" {
		return ""
	}
	firstPart := strings.Split(raw, ".")[0]
	padding := len(firstPart) % 4
	if padding != 0 {
		firstPart += strings.Repeat("=", 4-padding)
	}
	data, err := base64.URLEncoding.DecodeString(firstPart)
	if err != nil {
		return ""
	}
	var payload map[string]any
	if json.Unmarshal(data, &payload) != nil {
		return ""
	}
	workspaces := util.AsMapSlice(payload["workspaces"])
	if len(workspaces) == 0 {
		return ""
	}
	return util.Clean(workspaces[0]["id"])
}

func (w *registerWorker) buildSentinelToken(ctx context.Context, flow string) (string, error) {
	generator := newRegisterSentinelTokenGenerator(w.deviceID, registerUserAgent)
	reqPayload := map[string]any{
		"p":    generator.generateRequirementsToken(),
		"id":   w.deviceID,
		"flow": flow,
	}
	body, err := registerCompactJSONBytes(reqPayload)
	if err != nil {
		return "", err
	}
	headers := registerSentinelHeaders()
	status, payload, err := w.requestRawJSON(ctx, http.MethodPost, registerSentinelBase+"/backend-api/sentinel/req", body, headers)
	if err != nil {
		return "", err
	}
	challengeToken := util.Clean(payload["token"])
	if status != http.StatusOK || challengeToken == "" {
		return "", fmt.Errorf("sentinel_req_failed_%d", status)
	}
	proof := util.StringMap(payload["proofofwork"])
	var pValue string
	if util.ToBool(proof["required"]) && util.Clean(proof["seed"]) != "" {
		pValue = generator.generateToken(util.Clean(proof["seed"]), firstNonEmpty(util.Clean(proof["difficulty"]), "0"))
	} else {
		pValue = generator.generateRequirementsToken()
	}
	tokenPayload := map[string]any{
		"p":    pValue,
		"t":    "",
		"c":    challengeToken,
		"id":   w.deviceID,
		"flow": flow,
	}
	data, err := registerCompactJSONBytes(tokenPayload)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func (w *registerWorker) requestRawJSON(ctx context.Context, method, target string, body []byte, headers map[string]string) (int, map[string]any, error) {
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		req, err := http.NewRequestWithContext(ctx, method, target, bytes.NewReader(body))
		if err != nil {
			return 0, nil, err
		}
		for key, value := range headers {
			if strings.TrimSpace(value) != "" {
				req.Header.Set(key, value)
			}
		}
		resp, err := w.client.Do(req)
		if err != nil {
			lastErr = err
			if attempt < 2 {
				time.Sleep(time.Second)
				continue
			}
			return 0, nil, err
		}
		defer resp.Body.Close()
		payload := map[string]any{}
		_ = util.DecodeJSON(resp.Body, &payload)
		return resp.StatusCode, payload, nil
	}
	if lastErr != nil {
		return 0, nil, lastErr
	}
	return 0, nil, fmt.Errorf("raw request failed")
}

func (w *registerWorker) request(ctx context.Context, method, target string, payload any, headers map[string]string, followRedirects bool) (int, map[string]any, error) {
	status, payloadMap, _, err := w.requestDetailed(ctx, method, target, payload, headers, followRedirects)
	return status, payloadMap, err
}

func (w *registerWorker) requestDetailed(ctx context.Context, method, target string, payload any, headers map[string]string, followRedirects bool) (int, map[string]any, http.Header, error) {
	var body io.Reader
	var bodyData []byte
	if payload != nil {
		data, err := json.Marshal(payload)
		if err != nil {
			return 0, nil, nil, err
		}
		bodyData = data
	}
	client := w.client
	if !followRedirects {
		noRedirect := *w.client
		noRedirect.CheckRedirect = func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		}
		client = &noRedirect
	}
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		if payload != nil {
			body = bytes.NewReader(bodyData)
		} else {
			body = nil
		}
		req, err := http.NewRequestWithContext(ctx, method, target, body)
		if err != nil {
			return 0, nil, nil, err
		}
		for key, value := range headers {
			if strings.TrimSpace(value) != "" {
				req.Header.Set(key, value)
			}
		}
		resp, err := client.Do(req)
		if err != nil {
			lastErr = err
			if attempt < 2 {
				time.Sleep(time.Second)
				continue
			}
			return 0, nil, nil, err
		}
		defer resp.Body.Close()
		payloadMap := map[string]any{}
		if strings.Contains(resp.Header.Get("Content-Type"), "application/json") {
			_ = util.DecodeJSON(resp.Body, &payloadMap)
		} else {
			data, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
			if len(data) > 0 {
				payloadMap["body"] = string(data)
			}
		}
		return resp.StatusCode, payloadMap, resp.Header.Clone(), nil
	}
	if lastErr != nil {
		return 0, nil, nil, lastErr
	}
	return 0, nil, nil, fmt.Errorf("request failed")
}

func (w *registerWorker) requestForm(ctx context.Context, target string, form url.Values) (int, map[string]any, error) {
	body := []byte(form.Encode())
	headers := map[string]string{
		"Content-Type": "application/x-www-form-urlencoded",
		"Accept":       "application/json",
		"User-Agent":   registerUserAgent,
	}
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, bytes.NewReader(body))
		if err != nil {
			return 0, nil, err
		}
		for key, value := range headers {
			req.Header.Set(key, value)
		}
		resp, err := w.client.Do(req)
		if err != nil {
			lastErr = err
			if attempt < 2 {
				time.Sleep(time.Second)
				continue
			}
			return 0, nil, err
		}
		defer resp.Body.Close()
		payload := map[string]any{}
		_ = util.DecodeJSON(resp.Body, &payload)
		return resp.StatusCode, payload, nil
	}
	if lastErr != nil {
		return 0, nil, lastErr
	}
	return 0, nil, fmt.Errorf("form request failed")
}

func (w *registerWorker) navigateHeaders(referer string) map[string]string {
	headers := map[string]string{
		"Accept":                      "text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,*/*;q=0.8",
		"Accept-Language":             "en-US,en;q=0.9",
		"Upgrade-Insecure-Requests":   "1",
		"User-Agent":                  registerUserAgent,
		"sec-ch-ua":                   registerSecCHUA,
		"sec-ch-ua-arch":              `"x86_64"`,
		"sec-ch-ua-bitness":           `"64"`,
		"sec-ch-ua-full-version-list": registerSecCHUAFullVersionList,
		"sec-ch-ua-mobile":            "?0",
		"sec-ch-ua-model":             `""`,
		"sec-ch-ua-platform":          `"Windows"`,
		"sec-ch-ua-platform-version":  `"10.0.0"`,
		"sec-fetch-dest":              "document",
		"sec-fetch-mode":              "navigate",
		"sec-fetch-site":              "same-origin",
		"sec-fetch-user":              "?1",
	}
	if referer != "" {
		headers["Referer"] = referer
	}
	return headers
}

func (w *registerWorker) jsonHeaders(referer string) map[string]string {
	headers := map[string]string{
		"Accept":                      "application/json",
		"Accept-Language":             "en-US,en;q=0.9",
		"Content-Type":                "application/json",
		"Origin":                      registerAuthBase,
		"priority":                    "u=1, i",
		"User-Agent":                  registerUserAgent,
		"oai-device-id":               w.deviceID,
		"sec-ch-ua":                   registerSecCHUA,
		"sec-ch-ua-arch":              `"x86_64"`,
		"sec-ch-ua-bitness":           `"64"`,
		"sec-ch-ua-full-version-list": registerSecCHUAFullVersionList,
		"sec-ch-ua-mobile":            "?0",
		"sec-ch-ua-model":             `""`,
		"sec-ch-ua-platform":          `"Windows"`,
		"sec-ch-ua-platform-version":  `"10.0.0"`,
		"sec-fetch-dest":              "empty",
		"sec-fetch-mode":              "cors",
		"sec-fetch-site":              "same-origin",
	}
	for key, value := range registerTraceHeaders() {
		headers[key] = value
	}
	if referer != "" {
		headers["Referer"] = referer
	}
	return headers
}

func (w *registerWorker) step(text string) {
	w.service.appendLog(fmt.Sprintf("[任务%d] %s", w.index, text), "")
}

func registerDefaultConfig() map[string]any {
	stats := registerZeroStats(64, map[string]any{"current_quota": 0, "current_available": 0})
	return map[string]any{
		"mail": map[string]any{
			"request_timeout": 15,
			"wait_timeout":    30,
			"wait_interval":   3,
			"providers":       []map[string]any{},
		},
		"proxy":            "",
		"total":            20000,
		"threads":          64,
		"mode":             registerModeTotal,
		"target_quota":     100,
		"target_available": 10,
		"check_interval":   5,
		"enabled":          false,
		"stats":            stats,
	}
}

func registerZeroStats(threads int, metrics map[string]any) map[string]any {
	return map[string]any{
		"success":           0,
		"fail":              0,
		"done":              0,
		"running":           0,
		"threads":           maxInt(1, threads),
		"elapsed_seconds":   0,
		"avg_seconds":       0,
		"success_rate":      0,
		"current_quota":     util.ToInt(metrics["current_quota"], 0),
		"current_available": util.ToInt(metrics["current_available"], 0),
		"updated_at":        util.NowISO(),
	}
}

func normalizeRegisterConfig(raw map[string]any) map[string]any {
	cfg := registerDefaultConfig()
	for key, value := range raw {
		if key == "stats" || key == "logs" {
			continue
		}
		cfg[key] = value
	}
	cfg["proxy"] = strings.TrimSpace(util.Clean(cfg["proxy"]))
	cfg["total"] = maxInt(1, util.ToInt(cfg["total"], 1))
	cfg["threads"] = maxInt(1, util.ToInt(cfg["threads"], 1))
	mode := util.Clean(cfg["mode"])
	if mode != registerModeQuota && mode != registerModeAvailable {
		mode = registerModeTotal
	}
	cfg["mode"] = mode
	cfg["target_quota"] = maxInt(1, util.ToInt(cfg["target_quota"], 1))
	cfg["target_available"] = maxInt(1, util.ToInt(cfg["target_available"], 1))
	cfg["check_interval"] = maxInt(1, util.ToInt(cfg["check_interval"], 5))
	cfg["enabled"] = util.ToBool(cfg["enabled"])
	cfg["mail"] = normalizeRegisterMailConfig(util.StringMap(cfg["mail"]))
	stats := registerZeroStats(util.ToInt(cfg["threads"], 1), map[string]any{
		"current_quota":     util.ToInt(util.StringMap(raw["stats"])["current_quota"], 0),
		"current_available": util.ToInt(util.StringMap(raw["stats"])["current_available"], 0),
	})
	for key, value := range util.StringMap(raw["stats"]) {
		stats[key] = value
	}
	stats["threads"] = util.ToInt(cfg["threads"], 1)
	cfg["stats"] = stats
	return cfg
}

func normalizeRegisterMailConfig(raw map[string]any) map[string]any {
	cfg := map[string]any{
		"request_timeout": maxInt(1, util.ToInt(raw["request_timeout"], 15)),
		"wait_timeout":    maxInt(1, util.ToInt(raw["wait_timeout"], 30)),
		"wait_interval":   maxInt(1, util.ToInt(raw["wait_interval"], 3)),
		"user_agent":      firstNonEmpty(util.Clean(raw["user_agent"]), registerUserAgent),
	}
	providers := util.AsMapSlice(raw["providers"])
	out := make([]map[string]any, 0, len(providers))
	for _, provider := range providers {
		item := util.CopyMap(provider)
		item["type"] = util.Clean(item["type"])
		item["enable"] = util.ToBool(item["enable"])
		if item["domain"] != nil {
			item["domain"] = util.AsStringSlice(item["domain"])
		}
		out = append(out, item)
	}
	cfg["providers"] = out
	return cfg
}

func (s *RegisterService) load() map[string]any {
	raw, ok := loadStoredJSON(s.store, s.docName, s.path).(map[string]any)
	if !ok {
		return normalizeRegisterConfig(nil)
	}
	return normalizeRegisterConfig(raw)
}

func (s *RegisterService) saveLocked() {
	_ = saveStoredJSON(s.store, s.docName, s.path, s.config)
}

func (s *RegisterService) snapshotLocked() map[string]any {
	out := cloneMap(s.config)
	out["mail"] = cloneMap(util.StringMap(s.config["mail"]))
	out["stats"] = cloneMap(util.StringMap(s.config["stats"]))
	logs := make([]map[string]any, len(s.logs))
	for i, item := range s.logs {
		logs[i] = cloneMap(item)
	}
	out["logs"] = logs
	return out
}

func (s *RegisterService) snapshotJSONLocked() string {
	data, err := json.Marshal(s.snapshotLocked())
	if err != nil {
		return "{}"
	}
	return string(data)
}

func (s *RegisterService) notifyLocked() {
	payload := s.snapshotJSONLocked()
	for ch := range s.subscribers {
		select {
		case ch <- payload:
		default:
			select {
			case <-ch:
			default:
			}
			select {
			case ch <- payload:
			default:
			}
		}
	}
}

func (s *RegisterService) appendLog(text, level string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.appendLogLocked(text, level)
}

func (s *RegisterService) appendLogLocked(text, level string) {
	item := map[string]any{
		"time":  util.NowISO(),
		"text":  text,
		"level": firstNonEmpty(level, "info"),
	}
	s.logs = append(s.logs, item)
	if len(s.logs) > 300 {
		s.logs = append([]map[string]any(nil), s.logs[len(s.logs)-300:]...)
	}
	s.notifyLocked()
}

func (s *RegisterService) poolMetricsLocked() map[string]any {
	if s.accounts == nil {
		return map[string]any{"current_quota": 0, "current_available": 0}
	}
	items := s.accounts.ListAccounts()
	quota := 0
	available := 0
	for _, item := range items {
		if util.Clean(item["status"]) != "正常" {
			continue
		}
		available++
		if !util.ToBool(item["imageQuotaUnknown"]) {
			quota += util.ToInt(item["quota"], 0)
		}
	}
	return map[string]any{"current_quota": quota, "current_available": available}
}

func (s *RegisterService) targetReached(cfg map[string]any, submitted int) bool {
	metrics := s.poolMetrics()
	s.bumpStats(metrics)
	mode := util.Clean(cfg["mode"])
	switch mode {
	case registerModeQuota:
		reached := util.ToInt(metrics["current_quota"], 0) >= util.ToInt(cfg["target_quota"], 1)
		s.appendLog(fmt.Sprintf("检查号池：当前正常账号=%d，当前剩余额度=%d，目标额度=%d，%s", util.ToInt(metrics["current_available"], 0), util.ToInt(metrics["current_quota"], 0), util.ToInt(cfg["target_quota"], 1), registerSkipText(reached)), "yellow")
		return reached
	case registerModeAvailable:
		reached := util.ToInt(metrics["current_available"], 0) >= util.ToInt(cfg["target_available"], 1)
		s.appendLog(fmt.Sprintf("检查号池：当前正常账号=%d，目标账号=%d，当前剩余额度=%d，%s", util.ToInt(metrics["current_available"], 0), util.ToInt(cfg["target_available"], 1), util.ToInt(metrics["current_quota"], 0), registerSkipText(reached)), "yellow")
		return reached
	default:
		return submitted >= util.ToInt(cfg["total"], 1)
	}
}

func registerSkipText(reached bool) string {
	if reached {
		return "跳过注册"
	}
	return "继续注册"
}

func (s *RegisterService) poolMetrics() map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.poolMetricsLocked()
}

func (s *RegisterService) bumpStats(updates map[string]any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	stats := util.StringMap(s.config["stats"])
	for key, value := range updates {
		stats[key] = value
	}
	if startedAt := util.Clean(stats["started_at"]); startedAt != "" {
		if started, err := time.Parse(time.RFC3339Nano, startedAt); err == nil {
			elapsed := math.Round(time.Since(started).Seconds()*10) / 10
			stats["elapsed_seconds"] = elapsed
			success := util.ToInt(stats["success"], 0)
			fail := util.ToInt(stats["fail"], 0)
			if success > 0 {
				stats["avg_seconds"] = math.Round((elapsed/float64(success))*10) / 10
			} else {
				stats["avg_seconds"] = 0
			}
			stats["success_rate"] = math.Round((float64(success)*100/float64(maxInt(1, success+fail)))*10) / 10
		}
	}
	stats["updated_at"] = util.NowISO()
	s.config["stats"] = stats
	s.saveLocked()
	s.notifyLocked()
}

func cloneMap(in map[string]any) map[string]any {
	if in == nil {
		return map[string]any{}
	}
	data, err := json.Marshal(in)
	if err != nil {
		return util.CopyMap(in)
	}
	var out map[string]any
	if json.Unmarshal(data, &out) != nil {
		return util.CopyMap(in)
	}
	return out
}

func registerRandomPassword(length int) string {
	if length < 8 {
		length = 8
	}
	upper := "ABCDEFGHIJKLMNOPQRSTUVWXYZ"
	lower := "abcdefghijklmnopqrstuvwxyz"
	digits := "0123456789"
	special := "!@#$%"
	all := upper + lower + digits + special
	value := []byte{
		upper[mathrand.Intn(len(upper))],
		lower[mathrand.Intn(len(lower))],
		digits[mathrand.Intn(len(digits))],
		special[mathrand.Intn(len(special))],
	}
	for len(value) < length {
		value = append(value, all[mathrand.Intn(len(all))])
	}
	mathrand.Shuffle(len(value), func(i, j int) {
		value[i], value[j] = value[j], value[i]
	})
	return string(value)
}

func registerRandomName() (string, string) {
	return registerFirstNames[mathrand.Intn(len(registerFirstNames))], registerLastNames[mathrand.Intn(len(registerLastNames))]
}

func registerRandomBirthdate() string {
	year := 1996 + mathrand.Intn(11)
	month := 1 + mathrand.Intn(12)
	day := 1 + mathrand.Intn(28)
	return fmt.Sprintf("%04d-%02d-%02d", year, month, day)
}

func registerRandomToken() string {
	return util.RandomTokenURL(24)
}

func registerPKCEChallenge() string {
	_, challenge := generateRegisterPKCE()
	return challenge
}

func generateRegisterPKCE() (string, string) {
	buf := make([]byte, 64)
	_, _ = rand.Read(buf)
	verifier := base64.RawURLEncoding.EncodeToString(buf)
	sum := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(sum[:])
	return verifier, challenge
}

func registerAuthorizeParams(email, deviceID, state, nonce, codeChallenge string) url.Values {
	values := url.Values{}
	values.Set("issuer", registerAuthBase)
	values.Set("client_id", registerPlatformOAuthClientID)
	values.Set("audience", registerPlatformOAuthAudience)
	values.Set("redirect_uri", registerPlatformOAuthRedirectURI)
	values.Set("device_id", deviceID)
	values.Set("screen_hint", "login_or_signup")
	values.Set("max_age", "0")
	values.Set("login_hint", email)
	values.Set("scope", "openid profile email offline_access")
	values.Set("response_type", "code")
	values.Set("response_mode", "query")
	values.Set("state", state)
	values.Set("nonce", nonce)
	values.Set("code_challenge", codeChallenge)
	values.Set("code_challenge_method", "S256")
	values.Set("auth0Client", registerPlatformAuth0Client)
	return values
}

func registerOAuthCode(target string) string {
	if strings.TrimSpace(target) == "" {
		return ""
	}
	for _, candidate := range registerOAuthCodeCandidates(target) {
		parsed, err := url.Parse(candidate)
		if err != nil {
			continue
		}
		if code := strings.TrimSpace(parsed.Query().Get("code")); code != "" {
			return code
		}
		if parsed.Fragment != "" {
			values, parseErr := url.ParseQuery(parsed.Fragment)
			if parseErr == nil {
				if code := strings.TrimSpace(values.Get("code")); code != "" {
					return code
				}
			}
		}
	}
	if match := registerOAuthCodePattern.FindStringSubmatch(target); len(match) > 1 {
		code, decodeErr := url.QueryUnescape(match[1])
		if decodeErr == nil {
			return strings.TrimSpace(code)
		}
		return strings.TrimSpace(match[1])
	}
	return ""
}

func registerOAuthCodeCandidates(target string) []string {
	target = strings.TrimSpace(target)
	candidates := []string{target}
	if decoded, err := url.QueryUnescape(target); err == nil && decoded != target {
		candidates = append(candidates, decoded)
	}
	return candidates
}

func registerRefreshLocation(header string) string {
	header = strings.TrimSpace(header)
	if header == "" {
		return ""
	}
	for _, part := range strings.Split(header, ";") {
		key, value, ok := strings.Cut(strings.TrimSpace(part), "=")
		if !ok || !strings.EqualFold(strings.TrimSpace(key), "url") {
			continue
		}
		return strings.Trim(strings.TrimSpace(value), `"'`)
	}
	return ""
}

func resolveRegisterLocation(baseURL, location string) (string, error) {
	base, err := url.Parse(baseURL)
	if err != nil {
		return "", err
	}
	next, err := url.Parse(location)
	if err != nil {
		return "", err
	}
	return base.ResolveReference(next).String(), nil
}

func newRegisterSentinelTokenGenerator(deviceID, userAgent string) *registerSentinelTokenGenerator {
	return &registerSentinelTokenGenerator{
		deviceID:  deviceID,
		userAgent: userAgent,
		sid:       util.NewUUID(),
	}
}

func (g *registerSentinelTokenGenerator) config() []any {
	perfNow := 1000 + mathrand.Float64()*49000
	return []any{
		"1920x1080",
		time.Now().UTC().Format("Mon Jan 02 2006 15:04:05 GMT+0000 (Coordinated Universal Time)"),
		int64(4294705152),
		mathrand.Float64(),
		g.userAgent,
		registerSentinelSDK,
		nil,
		nil,
		"en-US",
		mathrand.Float64(),
		registerRandomChoice([]string{"vendorSub-undefined", "plugins-undefined", "mimeTypes-undefined", "hardwareConcurrency-undefined"}),
		registerRandomChoice([]string{"location", "implementation", "URL", "documentURI", "compatMode"}),
		registerRandomChoice([]string{"Object", "Function", "Array", "Number", "parseFloat", "undefined"}),
		perfNow,
		g.sid,
		"",
		registerRandomChoiceInt([]int{4, 8, 12, 16}),
		float64(time.Now().UnixMilli()) - perfNow,
	}
}

func (g *registerSentinelTokenGenerator) generateRequirementsToken() string {
	data := g.config()
	data[3] = 1
	data[9] = math.Round(5 + mathrand.Float64()*45)
	return "gAAAAAC" + registerBase64JSON(data)
}

func (g *registerSentinelTokenGenerator) generateToken(seed, difficulty string) string {
	start := time.Now()
	data := g.config()
	if difficulty == "" {
		difficulty = "0"
	}
	for i := 0; i < registerSentinelMaxAttempts; i++ {
		data[3] = i
		data[9] = math.Round(float64(time.Since(start).Milliseconds()))
		payload := registerBase64JSON(data)
		hash := registerFNV1A32(seed + payload)
		prefixLen := minInt(len(difficulty), len(hash))
		if hash[:prefixLen] <= difficulty[:prefixLen] {
			return "gAAAAAB" + payload + "~S"
		}
	}
	return "gAAAAAB" + registerSentinelErrorPrefix + registerBase64JSON("None")
}

func registerBase64JSON(value any) string {
	data, err := registerCompactJSONBytes(value)
	if err != nil {
		return ""
	}
	return base64.StdEncoding.EncodeToString(data)
}

func registerCompactJSONBytes(value any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(value); err != nil {
		return nil, err
	}
	return bytes.TrimSpace(buf.Bytes()), nil
}

func registerFNV1A32(text string) string {
	hash := uint32(2166136261)
	for _, ch := range text {
		hash ^= uint32(ch)
		hash *= 16777619
	}
	hash ^= hash >> 16
	hash *= 2246822507
	hash ^= hash >> 13
	hash *= 3266489909
	hash ^= hash >> 16
	return fmt.Sprintf("%08x", hash)
}

func registerSentinelHeaders() map[string]string {
	return map[string]string{
		"Content-Type":       "text/plain;charset=UTF-8",
		"Referer":            registerSentinelBase + "/backend-api/sentinel/frame.html",
		"Origin":             registerSentinelBase,
		"User-Agent":         registerUserAgent,
		"sec-ch-ua":          registerSecCHUA,
		"sec-ch-ua-mobile":   "?0",
		"sec-ch-ua-platform": `"Windows"`,
		"sec-fetch-dest":     "empty",
		"sec-fetch-mode":     "cors",
		"sec-fetch-site":     "same-origin",
	}
}

func registerTraceHeaders() map[string]string {
	traceID := util.NewHex(32)
	parentID := registerRandomUint64()
	parentHex := fmt.Sprintf("%016x", parentID)
	parentText := strconv.FormatUint(parentID, 10)
	return map[string]string{
		"traceparent":                 "00-" + traceID + "-" + parentHex + "-01",
		"tracestate":                  "dd=s:1;o:rum",
		"x-datadog-origin":            "rum",
		"x-datadog-parent-id":         parentText,
		"x-datadog-sampling-priority": "1",
		"x-datadog-trace-id":          strconv.FormatUint(registerRandomUint64(), 10),
	}
}

func registerRandomUint64() uint64 {
	var buf [8]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return uint64(mathrand.Int63())
	}
	var value uint64
	for _, b := range buf {
		value = (value << 8) | uint64(b)
	}
	return value
}

func registerRandomChoice(values []string) string {
	if len(values) == 0 {
		return ""
	}
	return values[mathrand.Intn(len(values))]
}

func registerRandomChoiceInt(values []int) int {
	if len(values) == 0 {
		return 0
	}
	return values[mathrand.Intn(len(values))]
}
