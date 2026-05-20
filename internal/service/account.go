package service

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"chatgpt2api/internal/storage"
	"chatgpt2api/internal/util"
)

type AccountConfig interface {
	AutoRemoveInvalidAccounts() bool
	AutoRemoveRateLimitedAccounts() bool
}

type AccountService struct {
	mu                sync.Mutex
	storage           storage.Backend
	config            AccountConfig
	proxy             *ProxyService
	logs              *LogService
	index             int
	textIndex         int
	items             []map[string]any
	imageReservations map[string]int
	remoteBaseURL     string
	browserHTTPClient func(profile string, timeout time.Duration) *http.Client
}

const (
	defaultRemoteUserAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/145.0.0.0 Safari/537.36"
	defaultRemoteSecCHUA   = `"Not:A-Brand";v="99", "Google Chrome";v="145", "Chromium";v="145"`
	defaultRemoteProfile   = "chrome145"

	accountRefreshTTL             = 90 * time.Second
	accountRefreshFailureCooldown = 45 * time.Second
	accountImageFailureCooldown   = 30 * time.Minute
	accountRefreshHTTPTimeout     = 15 * time.Second
	accountCandidateBatchSize     = 12
)

const imageAttemptTimeoutErrorMessage = "image generation attempt timed out"

func NewAccountService(backend storage.Backend, config AccountConfig, proxy *ProxyService, logs *LogService) *AccountService {
	browserHTTPClient := func(profile string, timeout time.Duration) *http.Client {
		if proxy == nil {
			return &http.Client{Timeout: timeout}
		}
		return proxy.BrowserHTTPClientWithProfile(profile, timeout)
	}
	s := &AccountService{
		storage:           backend,
		config:            config,
		proxy:             proxy,
		logs:              logs,
		imageReservations: map[string]int{},
		remoteBaseURL:     "https://chatgpt.com",
		browserHTTPClient: browserHTTPClient,
	}
	s.items = s.loadAccounts()
	return s
}

func (s *AccountService) ListTokens() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, 0, len(s.items))
	for _, item := range s.items {
		if token := util.Clean(item["access_token"]); token != "" {
			out = append(out, token)
		}
	}
	return out
}

func (s *AccountService) ListTokensByIDs(ids []string) []string {
	targets := cleanAccountIDs(ids)
	if len(targets) == 0 {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, 0, len(targets))
	for _, item := range s.items {
		token := util.Clean(item["access_token"])
		if token == "" {
			continue
		}
		if _, ok := targets[accountIDFromToken(token)]; ok {
			out = append(out, token)
		}
	}
	return out
}

func (s *AccountService) GetTokenByID(id string) string {
	id = strings.TrimSpace(id)
	if id == "" {
		return ""
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, item := range s.items {
		token := util.Clean(item["access_token"])
		if token != "" && accountIDFromToken(token) == id {
			return token
		}
	}
	return ""
}

func (s *AccountService) ListAccounts() []map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	return publicAccounts(s.items)
}

func (s *AccountService) ListLimitedTokens() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []string
	for _, item := range s.items {
		if item["status"] == "限流" {
			if token := util.Clean(item["access_token"]); token != "" {
				out = append(out, token)
			}
		}
	}
	return out
}

func (s *AccountService) AddAccounts(tokens []string) map[string]any {
	cleaned := cleanTokens(tokens)
	if len(cleaned) == 0 {
		return map[string]any{"added": 0, "skipped": 0, "items": s.ListAccounts()}
	}
	s.mu.Lock()
	indexed := map[string]map[string]any{}
	order := make([]string, 0, len(s.items)+len(cleaned))
	for _, item := range s.items {
		token := util.Clean(item["access_token"])
		if token == "" {
			continue
		}
		indexed[token] = util.CopyMap(item)
		order = append(order, token)
	}
	added, skipped := 0, 0
	for _, token := range cleaned {
		current, ok := indexed[token]
		if ok {
			skipped++
		} else {
			added++
			current = map[string]any{}
			order = append(order, token)
		}
		normalized := normalizeAccount(mergeMaps(current, map[string]any{"access_token": token, "type": util.ValueOr(current["type"], "Free")}))
		if normalized != nil {
			indexed[token] = normalized
		}
	}
	next := make([]map[string]any, 0, len(order))
	seen := map[string]struct{}{}
	for _, token := range order {
		if _, ok := seen[token]; ok {
			continue
		}
		seen[token] = struct{}{}
		next = append(next, indexed[token])
	}
	s.items = next
	_ = s.saveLocked()
	items := publicAccounts(s.items)
	s.mu.Unlock()
	s.logs.Add(fmt.Sprintf("新增 %d 个账号，跳过 %d 个", added, skipped), map[string]any{
		"module":         "accounts",
		"operation_type": "新增",
		"added":          added,
		"skipped":        skipped,
	})
	return map[string]any{"added": added, "skipped": skipped, "items": items}
}

func (s *AccountService) DeleteAccounts(tokens []string) map[string]any {
	targets := map[string]struct{}{}
	for _, token := range cleanTokens(tokens) {
		targets[token] = struct{}{}
	}
	if len(targets) == 0 {
		return map[string]any{"removed": 0, "items": s.ListAccounts()}
	}
	s.mu.Lock()
	next := s.items[:0]
	removed := 0
	for _, item := range s.items {
		token := util.Clean(item["access_token"])
		if _, ok := targets[token]; ok {
			removed++
			delete(s.imageReservations, token)
			continue
		}
		next = append(next, item)
	}
	s.items = next
	if len(s.items) > 0 {
		s.index %= len(s.items)
	} else {
		s.index = 0
	}
	if removed > 0 {
		_ = s.saveLocked()
	}
	items := publicAccounts(s.items)
	s.mu.Unlock()
	if removed > 0 {
		s.logs.Add(fmt.Sprintf("删除 %d 个账号", removed), map[string]any{
			"module":         "accounts",
			"operation_type": "删除",
			"removed":        removed,
		})
	}
	return map[string]any{"removed": removed, "items": items}
}

func (s *AccountService) RemoveToken(token string) bool {
	return util.ToInt(s.DeleteAccounts([]string{token})["removed"], 0) > 0
}

func (s *AccountService) UpdateAccount(accessToken string, updates map[string]any) map[string]any {
	accessToken = util.Clean(accessToken)
	if accessToken == "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	idx := s.findIndexLocked(accessToken)
	if idx < 0 {
		return nil
	}
	account := normalizeAccount(mergeMaps(s.items[idx], updates, map[string]any{"access_token": accessToken}))
	if account == nil {
		return nil
	}
	if account["status"] == "限流" && s.config.AutoRemoveRateLimitedAccounts() {
		delete(s.imageReservations, accessToken)
		s.items = append(s.items[:idx], s.items[idx+1:]...)
		_ = s.saveLocked()
		s.logs.Add("自动移除限流账号", map[string]any{
			"module":         "accounts",
			"operation_type": "自动移除",
			"token":          util.AnonymizeToken(accessToken),
		})
		return nil
	}
	s.items[idx] = account
	_ = s.saveLocked()
	s.logs.Add("更新账号", map[string]any{
		"module":         "accounts",
		"operation_type": "更新",
		"token":          util.AnonymizeToken(accessToken),
		"status":         account["status"],
	})
	return util.CopyMap(account)
}

func (s *AccountService) GetAccount(accessToken string) map[string]any {
	accessToken = util.Clean(accessToken)
	if accessToken == "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	idx := s.findIndexLocked(accessToken)
	if idx < 0 {
		return nil
	}
	return util.CopyMap(s.items[idx])
}

func (s *AccountService) GetTextAccessToken() string {
	return s.GetTextAccessTokenExcluding(nil)
}

func (s *AccountService) GetTextAccessTokenExcluding(excluded map[string]struct{}) string {
	return s.getTextAccessTokenExcluding(excluded, nil)
}

func (s *AccountService) GetVisionAccessTokenExcluding(excluded map[string]struct{}) string {
	return s.getTextAccessTokenExcluding(excluded, IsImageAccountAvailable)
}

func (s *AccountService) getTextAccessTokenExcluding(excluded map[string]struct{}, allow func(map[string]any) bool) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.items) == 0 {
		return ""
	}
	now := time.Now()
	start := s.textIndex % len(s.items)
	for offset := 0; offset < len(s.items); offset++ {
		idx := (start + offset) % len(s.items)
		item := s.items[idx]
		status := util.Clean(item["status"])
		token := util.Clean(item["access_token"])
		if token == "" {
			continue
		}
		if _, ok := excluded[token]; ok {
			continue
		}
		if accountCheckCoolingDown(item, now) {
			continue
		}
		if allow != nil && !allow(item) {
			continue
		}
		if status != "禁用" && status != "异常" {
			s.textIndex = (idx + 1) % len(s.items)
			return token
		}
	}
	return ""
}

func (s *AccountService) GetAvailableAccessToken(ctx context.Context) (string, error) {
	return s.GetAvailableAccessTokenFor(ctx, nil)
}

func (s *AccountService) GetAvailableImageAccessToken(ctx context.Context, preferPaid bool) (string, error) {
	return s.getAvailableAccessTokenFor(ctx, nil, preferPaid)
}

func (s *AccountService) GetAvailableAccessTokenFor(ctx context.Context, allow func(map[string]any) bool) (string, error) {
	return s.getAvailableAccessTokenFor(ctx, allow, false)
}

func (s *AccountService) getAvailableAccessTokenFor(ctx context.Context, allow func(map[string]any) bool, preferPaid bool) (string, error) {
	select {
	case <-ctx.Done():
		return "", ctx.Err()
	default:
	}
	reservations, err := s.reserveCandidateBatchWithPreference(nil, allow, accountCandidateBatchSize, preferPaid)
	if err != nil {
		return "", err
	}
	selected := ""
	for _, reservation := range reservations {
		if s.reservedImageSlotAvailable(reservation) {
			selected = reservation.token
			break
		}
	}
	s.releaseImageReservationsExcept(reservations, selected)
	if selected == "" {
		return "", fmt.Errorf("no available image quota")
	}
	return selected, nil
}

func (s *AccountService) HasAvailableAccount() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, item := range s.items {
		if s.availableImageSlotsLocked(item) > 0 {
			return true
		}
	}
	return false
}

func (s *AccountService) RefreshAccountState(ctx context.Context, accessToken string) map[string]any {
	remote, err := s.FetchRemoteInfo(ctx, accessToken)
	if err != nil {
		if ctx.Err() != nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return nil
		}
		if _, handled := s.ApplyAccountError(accessToken, "refresh_account_state", err); handled {
			return s.GetAccount(accessToken)
		}
		s.markAccountCheckFailure(accessToken, err)
		return nil
	}
	remote["checked_at"] = util.NowISO()
	remote["check_error"] = nil
	remote["check_failed_at"] = nil
	remote["check_cooldown_until"] = nil
	return s.UpdateAccount(accessToken, remote)
}

func (s *AccountService) RefreshAccounts(ctx context.Context, accessTokens []string) map[string]any {
	tokens := cleanTokens(accessTokens)
	if len(tokens) == 0 {
		return map[string]any{"refreshed": 0, "errors": []map[string]string{}, "items": s.ListAccounts()}
	}
	type result struct {
		token string
		info  map[string]any
		err   error
	}
	workers := len(tokens)
	if workers > 20 {
		workers = 20
	}
	jobs := make(chan string)
	results := make(chan result, len(tokens))
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for token := range jobs {
				info, err := s.FetchRemoteInfo(ctx, token)
				results <- result{token: token, info: info, err: err}
			}
		}()
	}
	go func() {
		for _, token := range tokens {
			jobs <- token
		}
		close(jobs)
		wg.Wait()
		close(results)
	}()
	refreshed := 0
	removed := 0
	errors := []map[string]string{}
	for res := range results {
		if res.err == nil {
			if s.UpdateAccount(res.token, res.info) != nil {
				refreshed++
			}
			continue
		}
		message := res.err.Error()
		if normalized, handled := s.ApplyAccountError(res.token, "refresh_accounts", res.err); handled {
			message = normalized
		} else {
			s.markAccountCheckFailure(res.token, res.err)
		}
		s.recordHeartbeatFailure(res.token, "refresh_accounts")
		errors = append(errors, map[string]string{"access_token": res.token, "error": message})
	}
	return map[string]any{"refreshed": refreshed, "removed": removed, "errors": errors, "items": s.ListAccounts()}
}

func (s *AccountService) MarkImageResult(accessToken string, success bool) map[string]any {
	accessToken = util.Clean(accessToken)
	if accessToken == "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.releaseImageReservationLocked(accessToken)
	idx := s.findIndexLocked(accessToken)
	if idx < 0 {
		return nil
	}
	next := util.CopyMap(s.items[idx])
	next["last_used_at"] = util.NowLocal()
	unknown := util.ToBool(next["image_quota_unknown"])
	if success {
		next["success"] = util.ToInt(next["success"], 0) + 1
		next["checked_at"] = util.NowISO()
		next["check_error"] = nil
		next["check_failed_at"] = nil
		next["check_cooldown_until"] = nil
		if !unknown {
			quota := util.ToInt(next["quota"], 0) - 1
			if quota < 0 {
				quota = 0
			}
			next["quota"] = quota
			if quota == 0 {
				next["status"] = "限流"
				if _, ok := next["restore_at"]; !ok {
					next["restore_at"] = nil
				}
			} else if next["status"] == "限流" {
				next["status"] = "正常"
			}
		}
	} else {
		next["fail"] = util.ToInt(next["fail"], 0) + 1
	}
	account := normalizeAccount(next)
	if account == nil {
		return nil
	}
	if account["status"] == "限流" && s.config.AutoRemoveRateLimitedAccounts() {
		delete(s.imageReservations, accessToken)
		s.items = append(s.items[:idx], s.items[idx+1:]...)
		_ = s.saveLocked()
		s.logs.Add("自动移除限流账号", map[string]any{
			"module":         "accounts",
			"operation_type": "自动移除",
			"token":          util.AnonymizeToken(accessToken),
		})
		return nil
	}
	s.items[idx] = account
	_ = s.saveLocked()
	return util.CopyMap(account)
}

func (s *AccountService) ReleaseImageSlot(accessToken string) {
	s.releaseImageReservation(accessToken)
}

func (s *AccountService) MarkImageAttemptTimeout(accessToken string) {
	s.MarkImageResult(accessToken, false)
	s.markAccountCooldown(accessToken, imageAttemptTimeoutErrorMessage, accountRefreshFailureCooldown)
}

func (s *AccountService) RemoveInvalidToken(accessToken, event string) bool {
	if !s.config.AutoRemoveInvalidAccounts() {
		return false
	}
	removed := s.RemoveToken(accessToken)
	if removed {
		s.logs.Add("自动移除异常账号", map[string]any{
			"module":         "accounts",
			"operation_type": "自动移除",
			"source":         event,
			"token":          util.AnonymizeToken(accessToken),
		})
	}
	return removed
}

func (s *AccountService) ApplyAccountError(accessToken, event string, err error) (string, bool) {
	if err == nil {
		return "", false
	}
	return s.ApplyAccountErrorMessage(accessToken, event, err.Error())
}

func (s *AccountService) ApplyAccountErrorMessage(accessToken, event, message string) (string, bool) {
	if IsAccountInvalidErrorMessage(message) {
		if isHeartbeatRefreshEvent(event) || !s.RemoveInvalidToken(accessToken, event) {
			s.UpdateAccount(accessToken, map[string]any{"status": "异常", "quota": 0, "image_quota_unknown": false})
		}
		return "检测到封号", true
	}
	if IsAccountRateLimitedErrorMessage(message) {
		s.UpdateAccount(accessToken, map[string]any{"status": "限流", "quota": 0, "image_quota_unknown": false})
		return "检测到限流", true
	}
	if event == "image_stream" && isBootstrapTransportErrorMessage(message) {
		s.markAccountCooldown(accessToken, message, accountErrorCooldownDuration(message))
		if detail, ok := util.SummarizeUpstreamConnectionError(message); ok {
			return detail, true
		}
		return "检测到临时网络异常，账号已短暂冷却", true
	}
	if accountShouldCooldownOnErrorMessage(message) {
		s.markAccountCooldown(accessToken, message, accountErrorCooldownDuration(message))
		if detail, ok := util.SummarizeCloudflareError(message); ok {
			return detail, true
		}
		if detail, ok := util.SummarizeUpstreamConnectionError(message); ok {
			return detail, true
		}
		return "检测到临时网络异常，账号已短暂冷却", true
	}
	return message, false
}

func (s *AccountService) markAccountCheckFailure(accessToken string, err error) {
	accessToken = util.Clean(accessToken)
	if accessToken == "" || err == nil {
		return
	}
	s.markAccountCooldown(accessToken, err.Error(), accountRefreshFailureCooldown)
}

func (s *AccountService) recordHeartbeatFailure(accessToken, event string) bool {
	account := s.GetAccount(accessToken)
	if util.Clean(account["check_error"]) == "" {
		return false
	}
	s.logs.Add("账号心跳失败，已标记冷却", map[string]any{
		"module":         "accounts",
		"operation_type": "心跳失败",
		"source":         event,
		"token":          util.AnonymizeToken(accessToken),
		"error":          util.Clean(account["check_error"]),
	})
	return true
}

func isHeartbeatRefreshEvent(event string) bool {
	switch event {
	case "refresh_accounts", "refresh_account_state":
		return true
	default:
		return false
	}
}

func (s *AccountService) markAccountCooldown(accessToken, message string, duration time.Duration) {
	accessToken = util.Clean(accessToken)
	if accessToken == "" {
		return
	}
	if duration <= 0 {
		duration = accountRefreshFailureCooldown
	}
	now := time.Now().UTC()
	s.mu.Lock()
	defer s.mu.Unlock()
	idx := s.findIndexLocked(accessToken)
	if idx < 0 {
		return
	}
	next := util.CopyMap(s.items[idx])
	next["check_error"] = message
	next["check_failed_at"] = now.Format(time.RFC3339Nano)
	next["check_cooldown_until"] = now.Add(duration).Format(time.RFC3339Nano)
	if normalized := normalizeAccount(next); normalized != nil {
		s.items[idx] = normalized
		_ = s.saveLocked()
	}
}

func (s *AccountService) FetchRemoteInfo(ctx context.Context, accessToken string) (map[string]any, error) {
	accessToken = util.Clean(accessToken)
	if accessToken == "" {
		return nil, fmt.Errorf("access_token is required")
	}
	baseURL := strings.TrimRight(firstNonEmpty(s.remoteBaseURL, "https://chatgpt.com"), "/")
	headers := s.remoteHeaders(accessToken)
	client := s.browserHTTPClient(s.remoteImpersonation(accessToken), accountRefreshHTTPTimeout)
	if client == nil {
		client = &http.Client{Timeout: accountRefreshHTTPTimeout}
	}
	if err := s.bootstrapRemote(ctx, client, baseURL, accessToken); err != nil {
		return nil, err
	}
	type response struct {
		payload map[string]any
		err     error
	}
	fetch := func(method, urlPath string, body any, extra map[string]string) response {
		var reader io.Reader
		if body != nil {
			data, _ := json.Marshal(body)
			reader = bytes.NewReader(data)
		}
		req, _ := http.NewRequestWithContext(ctx, method, baseURL+urlPath, reader)
		for key, value := range headers {
			req.Header.Set(key, value)
		}
		req.Header.Set("x-openai-target-path", urlPath)
		req.Header.Set("x-openai-target-route", urlPath)
		for key, value := range extra {
			req.Header.Set(key, value)
		}
		resp, err := client.Do(req)
		if err != nil {
			return response{err: err}
		}
		defer resp.Body.Close()
		data, readErr := io.ReadAll(resp.Body)
		if readErr != nil {
			return response{err: readErr}
		}
		if resp.StatusCode != http.StatusOK {
			return response{err: refreshHTTPError(urlPath, resp.StatusCode, data)}
		}
		var payload map[string]any
		if err := json.Unmarshal(data, &payload); err != nil {
			return response{err: err}
		}
		return response{payload: payload}
	}
	me := fetch(http.MethodGet, "/backend-api/me", nil, nil)
	if me.err != nil {
		return nil, me.err
	}
	init := fetch(http.MethodPost, "/backend-api/conversation/init", map[string]any{
		"gizmo_id": nil, "requested_default_model": nil, "conversation_id": nil, "timezone_offset_min": -480,
	}, nil)
	if init.err != nil {
		return nil, init.err
	}
	limits := anyList(init.payload["limits_progress"])
	accountType := s.detectAccountType(accessToken, me.payload, init.payload)
	quota, restoreAt, unknown := extractQuotaAndRestoreAt(limits)
	status := "正常"
	if !unknown && quota == 0 {
		status = "限流"
	}
	if unknown && accountType == "Free" {
		status = "限流"
	}
	return map[string]any{
		"email":                me.payload["email"],
		"user_id":              me.payload["id"],
		"type":                 accountType,
		"quota":                quota,
		"image_quota_unknown":  unknown,
		"limits_progress":      limits,
		"default_model_slug":   init.payload["default_model_slug"],
		"restore_at":           restoreAt,
		"status":               status,
		"checked_at":           util.NowISO(),
		"check_error":          nil,
		"check_failed_at":      nil,
		"check_cooldown_until": nil,
	}, nil
}

func (s *AccountService) bootstrapRemote(ctx context.Context, client *http.Client, baseURL, accessToken string) error {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/", nil)
	for key, value := range s.remoteBootstrapHeaders(accessToken) {
		req.Header.Set(key, value)
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return refreshHTTPError("bootstrap", resp.StatusCode, data)
	}
	return nil
}

func (s *AccountService) StartLimitedWatcher(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = 5 * time.Minute
	}
	go func() {
		timer := time.NewTimer(0)
		defer timer.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-timer.C:
				tokens := s.ListLimitedTokens()
				if len(tokens) > 0 {
					s.RefreshAccounts(ctx, tokens)
				}
				timer.Reset(interval)
			}
		}
	}()
}

type imageTokenReservation struct {
	token string
	slot  int
}

type imageAccountCandidate struct {
	token string
	score int
	paid  bool
}

const imageAccountScoreRoundRobinWindow = 150

func (s *AccountService) reserveCandidateBatch(excluded map[string]struct{}, allow func(map[string]any) bool, limit int) ([]imageTokenReservation, error) {
	return s.reserveCandidateBatchWithPreference(excluded, allow, limit, false)
}

func (s *AccountService) reserveCandidateBatchWithPreference(excluded map[string]struct{}, allow func(map[string]any) bool, limit int, preferPaid bool) ([]imageTokenReservation, error) {
	if limit < 1 {
		limit = 1
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var candidates []imageAccountCandidate
	for _, item := range s.items {
		token := util.Clean(item["access_token"])
		if token == "" {
			continue
		}
		if _, ok := excluded[token]; ok {
			continue
		}
		if !IsImageAccountAvailable(item) {
			continue
		}
		if allow != nil && !allow(item) {
			continue
		}
		if s.availableImageSlotsLocked(item) > 0 {
			score := imageAccountHealthScore(item)
			candidates = append(candidates, imageAccountCandidate{token: token, score: score, paid: IsPaidImageAccount(item)})
		}
	}
	if len(candidates) == 0 {
		return nil, fmt.Errorf("no available image quota")
	}
	start := s.index % len(candidates)
	if start > 0 {
		candidates = append(candidates[start:], candidates[:start]...)
	}
	candidates = imageAccountHealthyRoundRobinPool(candidates)
	if preferPaid {
		candidates = preferPaidCandidates(candidates)
	}
	if limit > len(candidates) {
		limit = len(candidates)
	}
	s.ensureImageReservationsLocked()
	reservations := make([]imageTokenReservation, 0, limit)
	for offset := 0; offset < limit; offset++ {
		token := candidates[offset].token
		s.imageReservations[token]++
		reservations = append(reservations, imageTokenReservation{token: token, slot: s.imageReservations[token]})
	}
	// Advance by one leader, not by the whole batch. Otherwise candidate counts that
	// divide the batch size (for example 2 or 3 paid image accounts) keep selecting
	// the same first account forever.
	s.index = start + 1
	return reservations, nil
}

func imageAccountHealthyRoundRobinPool(candidates []imageAccountCandidate) []imageAccountCandidate {
	if len(candidates) == 0 {
		return candidates
	}
	best := candidates[0].score
	for _, candidate := range candidates[1:] {
		if candidate.score < best {
			best = candidate.score
		}
	}
	threshold := best + imageAccountScoreRoundRobinWindow
	pool := make([]imageAccountCandidate, 0, len(candidates))
	for _, candidate := range candidates {
		if candidate.score <= threshold {
			pool = append(pool, candidate)
		}
	}
	if len(pool) == 0 {
		return candidates
	}
	return pool
}

func preferPaidCandidates(candidates []imageAccountCandidate) []imageAccountCandidate {
	if len(candidates) == 0 {
		return candidates
	}
	paid := make([]imageAccountCandidate, 0, len(candidates))
	other := make([]imageAccountCandidate, 0, len(candidates))
	for _, candidate := range candidates {
		if candidate.paid {
			paid = append(paid, candidate)
		} else {
			other = append(other, candidate)
		}
	}
	if len(paid) == 0 {
		return candidates
	}
	return append(paid, other...)
}

func imageAccountHealthScore(account map[string]any) int {
	success := util.ToInt(account["success"], 0)
	fail := util.ToInt(account["fail"], 0)
	total := success + fail
	score := 100
	if total > 0 {
		score = fail * 1000 / total
		if success > 0 && fail == 0 {
			score -= 20
		}
	}
	if fail >= success && fail >= 10 {
		score += 500
	}
	if fail > 0 && success == 0 {
		score += 300
	}
	if util.ToBool(account["image_quota_unknown"]) {
		score += 25
	}
	if usedAt, ok := parseAccountTime(account["last_used_at"]); ok {
		age := time.Since(usedAt)
		switch {
		case age < 0:
			score += 250
		case age < 30*time.Second:
			score += 250
		case age < 2*time.Minute:
			score += 150
		case age < 10*time.Minute:
			score += 50
		}
	}
	return score
}

func (s *AccountService) reserveNextCandidateToken(excluded map[string]struct{}, allow func(map[string]any) bool) (imageTokenReservation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var tokens []string
	for _, item := range s.items {
		token := util.Clean(item["access_token"])
		if token == "" {
			continue
		}
		if _, ok := excluded[token]; ok {
			continue
		}
		if !IsImageAccountAvailable(item) {
			continue
		}
		if allow != nil && !allow(item) {
			continue
		}
		if s.availableImageSlotsLocked(item) > 0 {
			tokens = append(tokens, token)
		}
	}
	if len(tokens) == 0 {
		return imageTokenReservation{}, fmt.Errorf("no available image quota")
	}
	token := tokens[s.index%len(tokens)]
	s.index++
	s.ensureImageReservationsLocked()
	s.imageReservations[token]++
	return imageTokenReservation{token: token, slot: s.imageReservations[token]}, nil
}

func (s *AccountService) releaseImageReservationsExcept(reservations []imageTokenReservation, keepToken string) {
	for _, reservation := range reservations {
		if keepToken != "" && reservation.token == keepToken {
			continue
		}
		s.releaseImageReservation(reservation.token)
	}
}

func (s *AccountService) reservedImageSlotAvailable(reservation imageTokenReservation) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	idx := s.findIndexLocked(reservation.token)
	if idx < 0 {
		return false
	}
	return reservation.slot > 0 && reservation.slot <= imageAccountCapacity(s.items[idx])
}

func (s *AccountService) availableImageSlotsLocked(account map[string]any) int {
	capacity := imageAccountCapacity(account)
	if capacity <= 0 {
		return 0
	}
	token := util.Clean(account["access_token"])
	if token == "" {
		return 0
	}
	inFlight := s.imageReservations[token]
	if inFlight >= capacity {
		return 0
	}
	return capacity - inFlight
}

func (s *AccountService) releaseImageReservation(accessToken string) {
	accessToken = util.Clean(accessToken)
	if accessToken == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.releaseImageReservationLocked(accessToken)
}

func (s *AccountService) releaseImageReservationLocked(accessToken string) {
	s.ensureImageReservationsLocked()
	count := s.imageReservations[accessToken]
	if count <= 1 {
		delete(s.imageReservations, accessToken)
		return
	}
	s.imageReservations[accessToken] = count - 1
}

func (s *AccountService) ensureImageReservationsLocked() {
	if s.imageReservations == nil {
		s.imageReservations = map[string]int{}
	}
}

func imageAccountCapacity(account map[string]any) int {
	if !IsImageAccountAvailable(account) {
		return 0
	}
	// Quota is the remaining total allowance, not a safe per-account concurrency
	// limit. Reusing one token for parallel image workers makes failures cluster on
	// the same account and starves the rest of the pool.
	return 1
}

func (s *AccountService) findIndexLocked(accessToken string) int {
	for index, item := range s.items {
		if util.Clean(item["access_token"]) == accessToken {
			return index
		}
	}
	return -1
}

func (s *AccountService) loadAccounts() []map[string]any {
	items, err := s.storage.LoadAccounts()
	if err != nil {
		return []map[string]any{}
	}
	out := make([]map[string]any, 0, len(items))
	for _, item := range items {
		if normalized := normalizeAccount(item); normalized != nil {
			out = append(out, normalized)
		}
	}
	return out
}

func (s *AccountService) saveLocked() error {
	return s.storage.SaveAccounts(s.items)
}

func (s *AccountService) remoteHeaders(accessToken string) map[string]string {
	account := s.GetAccount(accessToken)
	clean := func(keys ...string) string {
		for _, key := range keys {
			if raw, ok := account["fp"].(map[string]any); ok {
				if value := util.Clean(raw[key]); value != "" {
					return value
				}
			}
			if value := util.Clean(account[key]); value != "" {
				return value
			}
		}
		return ""
	}
	headers := map[string]string{
		"authorization":      "Bearer " + accessToken,
		"accept":             "*/*",
		"accept-language":    "zh-CN,zh;q=0.9,en;q=0.8",
		"content-type":       "application/json",
		"oai-language":       "zh-CN",
		"origin":             "https://chatgpt.com",
		"referer":            "https://chatgpt.com/",
		"sec-fetch-dest":     "empty",
		"sec-fetch-mode":     "cors",
		"sec-fetch-site":     "same-origin",
		"user-agent":         firstNonEmpty(clean("user-agent", "user_agent"), defaultRemoteUserAgent),
		"sec-ch-ua":          firstNonEmpty(clean("sec-ch-ua"), defaultRemoteSecCHUA),
		"sec-ch-ua-mobile":   firstNonEmpty(clean("sec-ch-ua-mobile"), "?0"),
		"sec-ch-ua-platform": firstNonEmpty(clean("sec-ch-ua-platform"), `"Windows"`),
	}
	if deviceID := clean("oai-device-id", "oai_device_id"); deviceID != "" {
		headers["oai-device-id"] = deviceID
	}
	if sessionID := clean("oai-session-id", "oai_session_id"); sessionID != "" {
		headers["oai-session-id"] = sessionID
	}
	return headers
}

func (s *AccountService) remoteBootstrapHeaders(accessToken string) map[string]string {
	account := s.GetAccount(accessToken)
	clean := func(keys ...string) string {
		for _, key := range keys {
			if raw, ok := account["fp"].(map[string]any); ok {
				if value := util.Clean(raw[key]); value != "" {
					return value
				}
			}
			if value := util.Clean(account[key]); value != "" {
				return value
			}
		}
		return ""
	}
	return map[string]string{
		"user-agent":                firstNonEmpty(clean("user-agent", "user_agent"), defaultRemoteUserAgent),
		"accept":                    "text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,*/*;q=0.8",
		"accept-language":           "zh-CN,zh;q=0.9,en;q=0.8",
		"sec-ch-ua":                 firstNonEmpty(clean("sec-ch-ua"), defaultRemoteSecCHUA),
		"sec-ch-ua-mobile":          firstNonEmpty(clean("sec-ch-ua-mobile"), "?0"),
		"sec-ch-ua-platform":        firstNonEmpty(clean("sec-ch-ua-platform"), `"Windows"`),
		"sec-fetch-dest":            "document",
		"sec-fetch-mode":            "navigate",
		"sec-fetch-site":            "none",
		"sec-fetch-user":            "?1",
		"upgrade-insecure-requests": "1",
	}
}

func (s *AccountService) remoteImpersonation(accessToken string) string {
	account := s.GetAccount(accessToken)
	if raw, ok := account["fp"].(map[string]any); ok {
		if value := util.Clean(raw["impersonate"]); value != "" {
			return value
		}
	}
	return firstNonEmpty(util.Clean(account["impersonate"]), defaultRemoteProfile)
}

func (s *AccountService) detectAccountType(accessToken string, mePayload, initPayload map[string]any) string {
	tokenPayload := decodeAccessTokenPayload(accessToken)
	if authPayload, ok := tokenPayload["https://api.openai.com/auth"].(map[string]any); ok {
		if matched := normalizeAccountType(authPayload["chatgpt_plan_type"]); matched != "" {
			return matched
		}
	}
	for _, payload := range []any{mePayload, initPayload, tokenPayload} {
		if matched := searchAccountType(payload); matched != "" {
			return matched
		}
	}
	return "Free"
}

func IsImageAccountAvailable(account map[string]any) bool {
	if account == nil {
		return false
	}
	if accountCheckCoolingDown(account, time.Now()) {
		return false
	}
	status := util.Clean(account["status"])
	if status == "禁用" || status == "限流" || status == "异常" {
		return false
	}
	if util.ToBool(account["image_quota_unknown"]) {
		return true
	}
	return util.ToInt(account["quota"], 0) > 0
}

func accountRefreshFresh(account map[string]any, now time.Time) bool {
	if account == nil || accountCheckCoolingDown(account, now) {
		return false
	}
	checkedAt, ok := parseAccountTime(account["checked_at"])
	if !ok {
		return false
	}
	if checkedAt.After(now.Add(5 * time.Second)) {
		return false
	}
	return now.Sub(checkedAt) <= accountRefreshTTL
}

func accountCheckCoolingDown(account map[string]any, now time.Time) bool {
	if account == nil {
		return false
	}
	until, ok := parseAccountTime(account["check_cooldown_until"])
	return ok && until.After(now)
}

func accountShouldCooldownOnErrorMessage(message string) bool {
	text := strings.TrimSpace(message)
	if text == "" || isBootstrapErrorMessage(text) || IsAccountInvalidErrorMessage(text) || IsAccountRateLimitedErrorMessage(text) {
		return false
	}
	lower := strings.ToLower(text)
	if _, ok := util.SummarizeUpstreamConnectionError(text); ok {
		return true
	}
	if _, ok := util.SummarizeCloudflareError(text); ok {
		return true
	}
	return strings.Contains(lower, "cloudflare challenge") ||
		strings.Contains(lower, "cf_chl") ||
		strings.Contains(lower, "challenge-platform") ||
		strings.Contains(lower, "enable javascript and cookies to continue") ||
		strings.Contains(lower, "upstream returned html error page") ||
		strings.Contains(lower, imageAttemptTimeoutErrorMessage) ||
		strings.Contains(lower, "tool choice 'required' must be specified with 'tools' parameter") ||
		strings.Contains(lower, "tool choice 'image_generation' not found in 'tools' parameter") ||
		strings.Contains(lower, "image generation produced no image result") ||
		strings.Contains(lower, "connection reset by peer") ||
		strings.Contains(lower, "unexpected eof")
}

func accountErrorCooldownDuration(message string) time.Duration {
	lower := strings.ToLower(strings.TrimSpace(message))
	if strings.Contains(lower, "image generation produced no image result") {
		return accountImageFailureCooldown
	}
	if _, ok := util.SummarizeCloudflareError(message); ok {
		return accountImageFailureCooldown
	}
	return accountRefreshFailureCooldown
}

func parseAccountTime(value any) (time.Time, bool) {
	text := util.Clean(value)
	if text == "" {
		return time.Time{}, false
	}
	if parsed, err := time.Parse(time.RFC3339Nano, text); err == nil {
		return parsed, true
	}
	if parsed, err := time.Parse(time.RFC3339, text); err == nil {
		return parsed, true
	}
	if parsed, err := time.ParseInLocation("2006-01-02 15:04:05", text, time.Local); err == nil {
		return parsed, true
	}
	return time.Time{}, false
}

func IsPaidImageAccount(account map[string]any) bool {
	switch util.Clean(account["type"]) {
	case "Plus", "ProLite", "Pro", "Team":
		return true
	default:
		return false
	}
}

func IsAccountInvalidErrorMessage(message string) bool {
	text := strings.ToLower(strings.TrimSpace(message))
	if text == "" || isBootstrapErrorMessage(text) {
		return false
	}
	return strings.Contains(text, "token_invalidated") ||
		strings.Contains(text, "token_revoked") ||
		strings.Contains(text, "token_expired") ||
		strings.Contains(text, "expired_access_token") ||
		strings.Contains(text, "authentication token has been invalidated") ||
		strings.Contains(text, "invalidated oauth token") ||
		strings.Contains(text, "access token expired") ||
		strings.Contains(text, "oauth token expired") ||
		strings.Contains(text, "expired oauth token") ||
		strings.Contains(text, "jwt expired") ||
		strings.Contains(text, "token has expired") ||
		strings.Contains(text, "bearer token expired") ||
		hasAccountHTTPStatus(text, http.StatusUnauthorized)
}

func IsAccountRateLimitedErrorMessage(message string) bool {
	text := strings.ToLower(strings.TrimSpace(message))
	if text == "" || isBootstrapErrorMessage(text) {
		return false
	}
	if hasAccountHTTPStatus(text, http.StatusTooManyRequests) ||
		strings.Contains(text, "rate_limit") ||
		strings.Contains(text, "rate limit") ||
		strings.Contains(text, "too many requests") ||
		strings.Contains(text, "too_many_requests") ||
		strings.Contains(text, "insufficient_quota") ||
		strings.Contains(text, "limit reached") ||
		strings.Contains(text, "usage limit") ||
		strings.Contains(text, "image generation limit") ||
		strings.Contains(text, "you've reached") ||
		strings.Contains(text, "you have reached") ||
		strings.Contains(text, "限流") ||
		strings.Contains(text, "额度已用尽") ||
		strings.Contains(text, "生成上限") ||
		strings.Contains(text, "已达上限") {
		return true
	}
	return false
}

func normalizeAccount(item map[string]any) map[string]any {
	if item == nil {
		return nil
	}
	accessToken := util.Clean(item["access_token"])
	if accessToken == "" {
		return nil
	}
	normalized := util.CopyMap(item)
	normalized["access_token"] = accessToken
	normalized["type"] = firstNonEmpty(util.Clean(normalized["type"]), "Free")
	normalized["status"] = firstNonEmpty(util.Clean(normalized["status"]), "正常")
	quota := util.ToInt(normalized["quota"], 0)
	if quota < 0 {
		quota = 0
	}
	normalized["quota"] = quota
	normalized["image_quota_unknown"] = util.ToBool(normalized["image_quota_unknown"])
	if email := util.Clean(normalized["email"]); email != "" {
		normalized["email"] = email
	} else {
		normalized["email"] = nil
	}
	if userID := util.Clean(normalized["user_id"]); userID != "" {
		normalized["user_id"] = userID
	} else {
		normalized["user_id"] = nil
	}
	limits := anyList(normalized["limits_progress"])
	normalized["limits_progress"] = limits
	if model := util.Clean(normalized["default_model_slug"]); model != "" {
		normalized["default_model_slug"] = model
	} else {
		normalized["default_model_slug"] = nil
	}
	if restore := util.Clean(normalized["restore_at"]); restore != "" {
		normalized["restore_at"] = restore
	} else {
		normalized["restore_at"] = nil
	}
	for _, key := range []string{"checked_at", "check_failed_at", "check_cooldown_until", "check_error"} {
		if value := util.Clean(normalized[key]); value != "" {
			normalized[key] = value
		} else {
			normalized[key] = nil
		}
	}
	normalized["success"] = util.ToInt(normalized["success"], 0)
	normalized["fail"] = util.ToInt(normalized["fail"], 0)
	return normalized
}

func publicAccounts(accounts []map[string]any) []map[string]any {
	out := make([]map[string]any, 0, len(accounts))
	for _, account := range accounts {
		token := util.Clean(account["access_token"])
		if token == "" {
			continue
		}
		out = append(out, map[string]any{
			"id":                 accountIDFromToken(token),
			"token_preview":      util.AnonymizeToken(token),
			"access_token":       token,
			"type":               util.ValueOr(account["type"], "Free"),
			"status":             util.ValueOr(account["status"], "正常"),
			"quota":              util.ValueOr(account["quota"], 0),
			"imageQuotaUnknown":  util.ToBool(account["image_quota_unknown"]),
			"email":              account["email"],
			"user_id":            account["user_id"],
			"limits_progress":    util.ValueOr(account["limits_progress"], []any{}),
			"default_model_slug": account["default_model_slug"],
			"restoreAt":          account["restore_at"],
			"checkedAt":          account["checked_at"],
			"checkFailedAt":      account["check_failed_at"],
			"checkCooldownUntil": account["check_cooldown_until"],
			"checkError":         account["check_error"],
			"success":            util.ToInt(account["success"], 0),
			"fail":               util.ToInt(account["fail"], 0),
			"lastUsedAt":         account["last_used_at"],
		})
	}
	return out
}

func accountIDFromToken(token string) string {
	return util.SHA1Short(token, 16)
}

func cleanAccountIDs(ids []string) map[string]struct{} {
	out := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id != "" {
			out[id] = struct{}{}
		}
	}
	return out
}

func cleanTokens(tokens []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(tokens))
	for _, token := range tokens {
		token = strings.TrimSpace(token)
		if token == "" {
			continue
		}
		if _, ok := seen[token]; ok {
			continue
		}
		seen[token] = struct{}{}
		out = append(out, token)
	}
	return out
}

func decodeAccessTokenPayload(accessToken string) map[string]any {
	parts := strings.Split(util.Clean(accessToken), ".")
	if len(parts) < 2 {
		return map[string]any{}
	}
	payload := parts[1]
	data, err := base64.RawURLEncoding.DecodeString(payload)
	if err != nil {
		data, err = base64.URLEncoding.DecodeString(payload)
	}
	if err != nil {
		return map[string]any{}
	}
	var out map[string]any
	if json.Unmarshal(data, &out) != nil {
		return map[string]any{}
	}
	return out
}

func normalizeAccountType(value any) string {
	switch strings.ToLower(util.Clean(value)) {
	case "free":
		return "Free"
	case "plus", "personal":
		return "Plus"
	case "prolite", "pro_lite":
		return "ProLite"
	case "team", "business", "enterprise":
		return "Team"
	case "pro":
		return "Pro"
	default:
		return ""
	}
}

func searchAccountType(value any) string {
	switch x := value.(type) {
	case map[string]any:
		for key, item := range x {
			keyText := strings.ToLower(util.Clean(key))
			if strings.Contains(keyText, "plan") || strings.Contains(keyText, "type") || strings.Contains(keyText, "subscription") || strings.Contains(keyText, "workspace") || strings.Contains(keyText, "tier") {
				if matched := normalizeAccountType(item); matched != "" {
					return matched
				}
				if matched := searchAccountType(item); matched != "" {
					return matched
				}
			}
		}
	case []any:
		for _, item := range x {
			if matched := searchAccountType(item); matched != "" {
				return matched
			}
		}
	}
	return ""
}

func extractQuotaAndRestoreAt(limits []any) (int, any, bool) {
	for _, raw := range limits {
		item, ok := raw.(map[string]any)
		if !ok || item["feature_name"] != "image_gen" {
			continue
		}
		restore := any(nil)
		if value := util.Clean(item["reset_after"]); value != "" {
			restore = value
		}
		return util.ToInt(item["remaining"], 0), restore, false
	}
	return 0, nil, true
}

func anyList(value any) []any {
	if list, ok := value.([]any); ok {
		return list
	}
	if list, ok := value.([]map[string]any); ok {
		out := make([]any, len(list))
		for i, item := range list {
			out[i] = item
		}
		return out
	}
	return []any{}
}

func mergeMaps(items ...map[string]any) map[string]any {
	out := map[string]any{}
	for _, item := range items {
		for key, value := range item {
			out[key] = value
		}
	}
	return out
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func hasHTTPStatus(message string, status int) bool {
	code := fmt.Sprint(status)
	return strings.Contains(message, "http "+code) ||
		strings.Contains(message, "status="+code) ||
		strings.Contains(message, "status: "+code) ||
		strings.Contains(message, `"status":`+code)
}

func hasAccountHTTPStatus(message string, status int) bool {
	if !hasHTTPStatus(message, status) {
		return false
	}
	return strings.Contains(message, "/backend-api/") ||
		strings.Contains(message, "auth_chat_requirements") ||
		strings.Contains(message, "authorization") ||
		strings.Contains(message, "token")
}

func isBootstrapErrorMessage(message string) bool {
	return strings.HasPrefix(strings.TrimSpace(message), "bootstrap failed")
}

func isBootstrapTransportErrorMessage(message string) bool {
	text := strings.TrimSpace(message)
	if !isBootstrapErrorMessage(text) {
		return false
	}
	if _, ok := util.SummarizeUpstreamConnectionError(text); ok {
		return true
	}
	return false
}

func refreshHTTPError(context string, status int, body []byte) error {
	detail := summarizeRefreshErrorBody(body)
	if detail == "" {
		return fmt.Errorf("%s failed: HTTP %d", context, status)
	}
	return fmt.Errorf("%s failed: HTTP %d, %s", context, status, detail)
}

func summarizeRefreshErrorBody(body []byte) string {
	text := strings.TrimSpace(string(body))
	if text == "" {
		return ""
	}
	lower := strings.ToLower(text)
	if strings.Contains(lower, "cf_chl") ||
		strings.Contains(lower, "challenge-platform") ||
		strings.Contains(lower, "enable javascript and cookies to continue") ||
		strings.Contains(lower, "cloudflare") {
		if detail, ok := util.SummarizeCloudflareError(text); ok {
			return detail
		}
		return util.UpstreamCloudflareChallengeMessage
	}
	if strings.Contains(lower, "<html") || strings.Contains(lower, "<!doctype html") || strings.Contains(lower, "<body") {
		return "upstream returned HTML error page"
	}
	const maxBodyDetail = 2048
	if len(text) > maxBodyDetail {
		return "body=" + text[:maxBodyDetail] + "...(truncated)"
	}
	return "body=" + text
}
