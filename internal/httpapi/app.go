package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"mime/multipart"
	"net"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"chatgpt2api/internal/config"
	"chatgpt2api/internal/protocol"
	"chatgpt2api/internal/service"
	"chatgpt2api/internal/storage"
	"chatgpt2api/internal/util"
	"chatgpt2api/internal/version"
	frontend "chatgpt2api/internal/web"

	_ "github.com/HugoSmits86/nativewebp"
)

const (
	maxLoginPageImageSize      = 10 << 20
	maxJSONBodySize            = 20 << 20
	imageThumbnailCacheControl = "public, max-age=31536000, immutable"
	imageShareCacheControl     = "public, max-age=31536000, immutable"
	imageShareAPICacheControl  = "public, max-age=60, stale-while-revalidate=300"
	authSessionCookieName      = "chatgpt2api_session"
)

var defaultShareImageMetaPattern = regexp.MustCompile(`(?is)\s*(<meta\s+(?:property|name)=["'](?:og:image|twitter:image)["']\s+content=["']/share-card\.png["']\s*/?>|<link\s+rel=["']image_src["']\s+href=["']/share-card\.png["']\s*/?>)`)

type App struct {
	config        *config.Store
	auth          *service.AuthService
	accounts      *service.AccountService
	logs          *service.LogService
	logger        *service.Logger
	proxy         *service.ProxyService
	engine        *protocol.Engine
	images        *service.ImageService
	profiles      *service.UserProfileService
	notifications *service.NotificationService
	callStats     *service.ImageCallStatsService
	shares        *service.ImageShareService
	tasks         *service.ImageTaskService
	announce      *service.AnnouncementService
	prompts       *service.PromptFavoriteService
	cpa           *service.CPAConfig
	cpaImport     *service.CPAImportService
	sub2          *service.Sub2APIConfig
	sub2Import    *service.Sub2APIService
	subscriptions *service.SubscriptionService
	register      *service.RegisterService
	update        *service.UpdateService
	authLimiter   *authIPLimiter
	cancel        context.CancelFunc
}

func NewApp() (*App, error) {
	cfg, err := config.NewStore()
	if err != nil {
		return nil, err
	}
	storageBackend, err := cfg.StorageBackend()
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	logs := service.NewLogService(cfg.DataDir, storageBackend)
	logger, err := service.NewLogger(cfg.DataDir, cfg.LogLevels)
	if err != nil {
		cancel()
		return nil, err
	}
	proxy := service.NewProxyService(cfg)
	accounts := service.NewAccountService(storageBackend, cfg, proxy, logs)
	auth := service.NewAuthService(storageBackend)
	bootstrap, err := auth.EnsureBootstrapAdmin(cfg.AdminUsername(), cfg.AdminPassword())
	if err != nil {
		cancel()
		return nil, err
	}
	if bootstrap.Created && bootstrap.Generated {
		fmt.Fprintf(os.Stderr, "bootstrap admin password generated: username=%s password=%s\n", bootstrap.Username, bootstrap.Password)
		logger.Warning("bootstrap admin password generated", "username", bootstrap.Username)
	}
	documentStore, _ := storageBackend.(storage.JSONDocumentBackend)
	engine := &protocol.Engine{Accounts: accounts, Config: cfg, Storage: documentStore, Proxy: proxy, Logger: logger}
	app := &App{config: cfg, auth: auth, accounts: accounts, logs: logs, logger: logger, proxy: proxy, engine: engine, images: service.NewImageService(cfg, storageBackend), profiles: service.NewUserProfileService(cfg.DataDir, storageBackend), notifications: service.NewNotificationService(cfg.DataDir, storageBackend), callStats: service.NewImageCallStatsService(cfg.DataDir, storageBackend), shares: service.NewImageShareService(cfg.DataDir, storageBackend), announce: service.NewAnnouncementService(cfg.DataDir, storageBackend), prompts: service.NewPromptFavoriteService(cfg.DataDir, storageBackend), cpa: service.NewCPAConfig(cfg.DataDir, storageBackend), sub2: service.NewSub2APIConfig(cfg.DataDir, storageBackend), subscriptions: service.NewSubscriptionService(cfg.DataDir, storageBackend), update: newUpdateService(cfg), authLimiter: newAuthIPLimiter(), cancel: cancel}
	app.cpaImport = service.NewCPAImportService(app.cpa, accounts, proxy)
	app.sub2Import = service.NewSub2APIService(app.sub2, accounts)
	app.register = service.NewRegisterService(cfg.DataDir, accounts, storageBackend)
	app.tasks = service.NewStoredImageTaskService(filepath.Join(cfg.DataDir, "image_tasks.json"), storageBackend,
		func(ctx context.Context, identity service.Identity, payload map[string]any) (map[string]any, error) {
			return app.runLoggedImageTask(ctx, identity, payload, "/api/creation-tasks/image-generations", "文生图", func(ctx context.Context, payload map[string]any) (map[string]any, error) {
				result, _, err := engine.HandleImageGenerations(ctx, payload)
				return result, err
			})
		},
		func(ctx context.Context, identity service.Identity, payload map[string]any) (map[string]any, error) {
			return app.runLoggedImageTask(ctx, identity, payload, "/api/creation-tasks/image-edits", "图生图", func(ctx context.Context, payload map[string]any) (map[string]any, error) {
				images, _ := payload["images"].([]protocol.UploadedImage)
				result, _, err := engine.HandleImageEdits(ctx, payload, images)
				return result, err
			})
		},
		func(ctx context.Context, identity service.Identity, payload map[string]any) (map[string]any, error) {
			return app.runLoggedChatTask(ctx, identity, payload)
		},
		cfg.ImageRetentionDays,
		cfg.ImageConcurrentLimit,
		cfg.UserDefaultConcurrentLimit,
		cfg.UserDefaultRPMLimit,
	)
	app.tasks.SetTaskTimeoutGetter(func() time.Duration {
		return time.Duration(app.config.ImageTaskTimeoutSeconds()) * time.Second
	})
	app.tasks.SetResponseImageHandler(func(ctx context.Context, identity service.Identity, payload map[string]any) (map[string]any, error) {
		return app.runLoggedImageTask(ctx, identity, payload, "/api/creation-tasks/response-image-generations", "Responses 作画", func(ctx context.Context, payload map[string]any) (map[string]any, error) {
			return app.runResponsesImageGenerationTask(ctx, payload)
		})
	})
	accounts.StartLimitedWatcher(ctx, time.Duration(cfg.RefreshAccountIntervalMinute())*time.Minute)
	cfg.CleanupOldImages()
	return app, nil
}

func newUpdateService(cfg *config.Store) *service.UpdateService {
	return service.NewUpdateService(service.UpdateOptions{
		CurrentVersion: version.Get(),
		BuildType:      version.GetBuildType(),
		Repo:           cfg.UpdateRepo(),
		ProxyURL:       cfg.UpdateProxyURL(),
		GitHubToken:    cfg.UpdateGitHubToken(),
	})
}

func (a *App) Close() {
	if a.cancel != nil {
		a.cancel()
	}
	if a.logger != nil {
		_ = a.logger.Close()
	}
}

func (a *App) Logger() *service.Logger {
	return a.logger
}

func (a *App) handleModels(w http.ResponseWriter, r *http.Request) {
	identity, ok := a.requireIdentity(w, r, "")
	if !ok {
		return
	}
	result, err := a.engine.ListModels(r.Context())
	a.writeProtocol(w, r, result, nil, err, "openai", "/v1/models", "models", identity, "模型列表", service.ImageVisibilityPrivate, 0, false)
}

func (a *App) handleImageGenerations(w http.ResponseWriter, r *http.Request) {
	identity, ok := a.requireIdentity(w, r, "")
	if !ok {
		return
	}
	body, err := readJSONMap(r)
	if err != nil {
		util.WriteError(w, http.StatusBadRequest, "invalid json body")
		return
	}
	body["owner_id"] = identityScope(identity)
	body["owner_name"] = identityDisplayName(identity)
	body["base_url"] = a.resolveImageBaseURL(r)

	requested := maxInt(1, util.ToInt(body["n"], 1))
	quotaCount, fixedCharge := a.imageQuotaChargeForPayload(body, requested)
	if util.Clean(body["image_resolution"]) == "" {
		if preset := imageResolutionPresetForQuota(body); preset != "" {
			body["image_resolution"] = preset
		}
	}
	body["n"] = requested

	visibility, err := service.NormalizeImageVisibility(util.Clean(body["visibility"]))
	if err != nil {
		util.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	model := firstNonEmpty(util.Clean(body["model"]), util.ImageModelAuto)
	result, stream, err := a.engine.HandleImageGenerations(r.Context(), body)
	a.writeProtocol(w, r, result, stream, err, "openai", "/v1/images/generations", model, identity, "文生图", visibility, quotaCount, fixedCharge)
}

func (a *App) handleImageEdits(w http.ResponseWriter, r *http.Request) {
	identity, ok := a.requireIdentity(w, r, "")
	if !ok {
		return
	}
	body, images, err := readMultipartImageBody(r)
	if err != nil {
		util.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	if n := util.ToInt(body["n"], 1); n < 1 || n > 4 {
		util.WriteError(w, http.StatusBadRequest, "n must be between 1 and 4")
		return
	}
	if len(images) == 0 {
		util.WriteError(w, http.StatusBadRequest, "image file is required")
		return
	}
	body["owner_id"] = identityScope(identity)
	body["owner_name"] = identityDisplayName(identity)
	body["base_url"] = a.resolveImageBaseURL(r)
	requested := maxInt(1, util.ToInt(body["n"], 1))
	quotaCount, fixedCharge := a.imageQuotaChargeForPayload(body, requested)
	if preset := service.NormalizeImageResolutionPreset(util.Clean(body["image_resolution"])); preset != "" {
		body["image_resolution"] = preset
		switch preset {
		case "2k":
			if util.Clean(body["size"]) == "" {
				body["size"] = "2048x2048"
			}
		case "4k":
			if util.Clean(body["size"]) == "" {
				body["size"] = "2880x2880"
			}
		case "1080p":
			if util.Clean(body["size"]) == "" {
				body["size"] = "1080x1080"
			}
		}
	}
	body["n"] = requested
	visibility, err := service.NormalizeImageVisibility(util.Clean(body["visibility"]))
	if err != nil {
		util.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	model := firstNonEmpty(util.Clean(body["model"]), util.ImageModelAuto)
	result, stream, err := a.engine.HandleImageEdits(r.Context(), body, images)
	a.writeProtocol(w, r, result, stream, err, "openai", "/v1/images/edits", model, identity, "图生图", visibility, quotaCount, fixedCharge)
}

func (a *App) handleChatCompletions(w http.ResponseWriter, r *http.Request) {
	identity, ok := a.requireIdentity(w, r, "")
	if !ok {
		return
	}
	body, err := readJSONMap(r)
	if err != nil {
		util.WriteError(w, http.StatusBadRequest, "invalid json body")
		return
	}
	body["owner_id"] = identityScope(identity)
	body["owner_name"] = identityDisplayName(identity)
	model := firstNonEmpty(util.Clean(body["model"]), "auto")
	result, stream, err := a.engine.HandleChatCompletions(r.Context(), body)
	a.writeProtocol(w, r, result, stream, err, "openai", "/v1/chat/completions", model, identity, "文本生成", service.ImageVisibilityPrivate, 0, false)
}

func (a *App) handleResponses(w http.ResponseWriter, r *http.Request) {
	identity, ok := a.requireIdentity(w, r, "")
	if !ok {
		return
	}
	body, err := readJSONMap(r)
	if err != nil {
		util.WriteError(w, http.StatusBadRequest, "invalid json body")
		return
	}
	body["owner_id"] = identityScope(identity)
	body["owner_name"] = identityDisplayName(identity)
	model := firstNonEmpty(util.Clean(body["model"]), "auto")
	result, stream, err := a.engine.HandleResponsesScoped(r.Context(), body, identityScope(identity))
	a.writeProtocol(w, r, result, stream, err, "openai", "/v1/responses", model, identity, "Responses", service.ImageVisibilityPrivate, 0, false)
}

func (a *App) handleMessages(w http.ResponseWriter, r *http.Request) {
	authHeader := r.Header.Get("Authorization")
	if authHeader == "" && r.Header.Get("x-api-key") != "" {
		authHeader = "Bearer " + r.Header.Get("x-api-key")
	}
	identity, ok := a.requireIdentity(w, r, authHeader)
	if !ok {
		return
	}
	body, err := readJSONMap(r)
	if err != nil {
		util.WriteError(w, http.StatusBadRequest, "invalid json body")
		return
	}
	model := firstNonEmpty(util.Clean(body["model"]), "auto")
	result, stream, err := a.engine.HandleMessages(r.Context(), body)
	a.writeProtocol(w, r, result, stream, err, "anthropic", "/v1/messages", model, identity, "Messages", service.ImageVisibilityPrivate, 0, false)
}

func (a *App) writeProtocol(w http.ResponseWriter, r *http.Request, result map[string]any, stream *protocol.StreamResult, err error, sseKind, endpoint, model string, identity service.Identity, summary, visibility string, quotaCount int, fixedCharge bool) {
	start := time.Now()
	refund := func(int) {}
	if quotaCount > 0 && a.profiles != nil {
		if reserved, reserveErr := a.profiles.ReserveQuota(identity, quotaCount); reserveErr == nil && reserved != nil {
			refund = reserved
		} else if reserveErr != nil {
			a.logCall(identity, summary, r.Method, endpoint, model, start, "failed", http.StatusTooManyRequests, reserveErr.Error(), nil)
			util.WriteError(w, http.StatusTooManyRequests, reserveErr.Error())
			return
		}
	}
	if err != nil {
		if quotaCount > 0 {
			refund(quotaCount)
		}
		a.logCall(identity, summary, r.Method, endpoint, model, start, "failed", protocolErrorHTTPStatus(err), err.Error(), nil)
		a.writeProtocolError(w, err)
		return
	}
	if stream == nil {
		urls := collectURLs(result)
		if quotaCount > 0 && !fixedCharge {
			if used := len(util.AsMapSlice(result["data"])); used < quotaCount {
				refund(quotaCount - used)
			}
		}
		a.recordGeneratedImages(identity, urls, visibility)
		a.logCall(identity, summary, r.Method, endpoint, model, start, "success", http.StatusOK, "", urls)
		util.WriteJSON(w, http.StatusOK, result)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	flusher, _ := w.(http.Flusher)
	if stream.Kind == "anthropic" || sseKind == "anthropic" {
		var urls []string
		for item := range stream.Items {
			urls = append(urls, collectURLs(item)...)
			event := firstNonEmpty(util.Clean(item["type"]), "message_delta")
			fmt.Fprintf(w, "event: %s\n", event)
			fmt.Fprintf(w, "data: %s\n\n", jsonString(item))
			if flusher != nil {
				flusher.Flush()
			}
		}
		if err := <-stream.Err; err != nil {
			a.recordGeneratedImages(identity, urls, visibility)
			a.logCall(identity, summary, r.Method, endpoint, model, start, "failed", protocolErrorHTTPStatus(err), err.Error(), urls)
			fmt.Fprintf(w, "event: error\n")
			fmt.Fprintf(w, "data: %s\n\n", jsonString(map[string]any{"type": "error", "error": map[string]any{"type": fmt.Sprintf("%T", err), "message": err.Error()}}))
			return
		}
		a.recordGeneratedImages(identity, urls, visibility)
		a.logCall(identity, summary, r.Method, endpoint, model, start, "success", http.StatusOK, "", urls)
		return
	}
	fmt.Fprint(w, ": stream-open\n\n")
	if flusher != nil {
		flusher.Flush()
	}
	var urls []string
	for item := range stream.Items {
		urls = append(urls, collectURLs(item)...)
		fmt.Fprintf(w, "data: %s\n\n", jsonString(item))
		if flusher != nil {
			flusher.Flush()
		}
	}
	if err := <-stream.Err; err != nil {
		if quotaCount > 0 && !fixedCharge {
			if remaining := quotaCount - len(urls); remaining > 0 {
				refund(remaining)
			}
		}
		a.recordGeneratedImages(identity, urls, visibility)
		a.logCall(identity, summary, r.Method, endpoint, model, start, "failed", protocolErrorHTTPStatus(err), err.Error(), urls)
		fmt.Fprintf(w, "data: %s\n\n", jsonString(openAIErrorForStream(err)))
	} else {
		if quotaCount > 0 && !fixedCharge {
			if remaining := quotaCount - len(urls); remaining > 0 {
				refund(remaining)
			}
		}
		a.recordGeneratedImages(identity, urls, visibility)
		a.logCall(identity, summary, r.Method, endpoint, model, start, "success", http.StatusOK, "", urls)
	}
	fmt.Fprint(w, "data: [DONE]\n\n")
}

func protocolErrorHTTPStatus(err error) int {
	var httpErr protocol.HTTPError
	if errors.As(err, &httpErr) {
		return httpErr.Status
	}
	var imageErr *protocol.ImageGenerationError
	if errors.As(err, &imageErr) {
		return imageErr.StatusCode
	}
	message := err.Error()
	if strings.Contains(strings.ToLower(message), "no available image quota") {
		return http.StatusTooManyRequests
	}
	return http.StatusBadGateway
}

func (a *App) writeProtocolError(w http.ResponseWriter, err error) {
	var httpErr protocol.HTTPError
	if errors.As(err, &httpErr) {
		util.WriteError(w, httpErr.Status, httpErr.Message)
		return
	}
	var imageErr *protocol.ImageGenerationError
	if errors.As(err, &imageErr) {
		util.WriteJSON(w, imageErr.StatusCode, imageErr.OpenAIError())
		return
	}
	message := err.Error()
	if strings.Contains(strings.ToLower(message), "no available image quota") {
		util.WriteJSON(w, http.StatusTooManyRequests, map[string]any{"error": map[string]any{"message": "no available image quota", "type": "insufficient_quota", "param": nil, "code": "insufficient_quota"}})
		return
	}
	util.WriteJSON(w, http.StatusBadGateway, map[string]any{"detail": map[string]any{"error": message}})
}

func (a *App) handleLogin(w http.ResponseWriter, r *http.Request) {
	if !a.requireAuthIPAllowed(w, r, "login") {
		return
	}
	body, err := readJSONMap(r)
	if err != nil {
		util.WriteError(w, http.StatusBadRequest, "invalid json body")
		return
	}
	if !a.requireTurnstile(w, r, body) {
		return
	}
	identity, token, err := a.auth.LoginPassword(util.Clean(body["username"]), util.Clean(body["password"]))
	if err != nil {
		util.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	setAuthSessionCookie(w, r, token)
	a.writeLoginResponse(w, *identity, token)
}

func (a *App) handleAdminKeyLogin(w http.ResponseWriter, r *http.Request) {
	if !a.requireAuthIPAllowed(w, r, "login") {
		return
	}
	body, err := readJSONMap(r)
	if err != nil {
		util.WriteError(w, http.StatusBadRequest, "invalid json body")
		return
	}
	key := util.Clean(body["key"])
	if key == "" {
		util.WriteError(w, http.StatusBadRequest, "admin key is required")
		return
	}
	if identity := a.auth.Authenticate(key); identity != nil && identity.Role == service.AuthRoleAdmin {
		setAuthSessionCookie(w, r, key)
		a.writeLoginResponse(w, *identity, key)
		return
	}
	identity, token, err := a.auth.LoginPassword(a.config.AdminUsername(), key)
	if err != nil || identity == nil || identity.Role != service.AuthRoleAdmin {
		util.WriteError(w, http.StatusForbidden, "管理员密钥无效")
		return
	}
	setAuthSessionCookie(w, r, token)
	a.writeLoginResponse(w, *identity, token)
}

func (a *App) handleSession(w http.ResponseWriter, r *http.Request) {
	identity, token, ok := a.requireIdentityWithToken(w, r, "")
	if !ok {
		return
	}
	if token != "" {
		setAuthSessionCookie(w, r, token)
	}
	a.writeLoginResponse(w, identity, token)
}

func (a *App) handleAccountRegister(w http.ResponseWriter, r *http.Request) {
	if !a.config.RegistrationEnabled() {
		util.WriteError(w, http.StatusForbidden, "registration is disabled")
		return
	}
	if !a.requireAuthIPAllowed(w, r, "register") {
		return
	}
	body, err := readJSONMap(r)
	if err != nil {
		util.WriteError(w, http.StatusBadRequest, "invalid json body")
		return
	}
	if !a.requireTurnstile(w, r, body) {
		return
	}
	identity, token, err := a.auth.RegisterPasswordUser(util.Clean(body["username"]), util.Clean(body["password"]), util.Clean(body["name"]))
	if err != nil {
		util.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	if a.profiles != nil {
		outcome, err := a.profiles.RegisterUserOutcome(identity.OwnerID, util.Clean(body["invite_code"]), a.config.UserFreeQuota(), a.config.InviteRewardQuota(), a.config.InviteeBonusQuota())
		if err != nil {
			_ = a.auth.DeleteUser(identity.OwnerID)
			util.WriteError(w, http.StatusBadRequest, err.Error())
			return
		}
		a.emitRegistrationNotifications(identity.OwnerID, outcome)
	}
	setAuthSessionCookie(w, r, token)
	a.writeLoginResponse(w, *identity, token)
}

func (a *App) handleInviteLanding(w http.ResponseWriter, r *http.Request) {
	code := strings.TrimSpace(r.URL.Query().Get("code"))
	if code == "" {
		code = strings.TrimPrefix(strings.TrimPrefix(strings.TrimPrefix(r.URL.Path, "/auth/invite/"), "/auth/invite"), "/")
	}
	if a.profiles == nil {
		http.NotFound(w, r)
		return
	}
	result := a.profiles.PublicInvite(code, a.resolveImageBaseURL(r), a.config.InviteeBonusQuota())
	if result == nil {
		http.NotFound(w, r)
		return
	}
	util.WriteJSON(w, http.StatusOK, result)
}

func (a *App) handleImageShares(w http.ResponseWriter, r *http.Request) {
	if a.shares == nil {
		http.NotFound(w, r)
		return
	}
	switch r.Method {
	case http.MethodPost:
		identity, ok := a.requireIdentity(w, r, "")
		if !ok {
			return
		}
		body, err := readJSONMap(r)
		if err != nil {
			util.WriteError(w, http.StatusBadRequest, "invalid json body")
			return
		}
		if !a.canCreateImageShare(identity, util.Clean(body["image"])) {
			util.WriteError(w, http.StatusNotFound, "image not found")
			return
		}
		item, err := a.shares.Create(identity, body, a.resolveImageBaseURL(r), a.config.ImagesDir())
		if err != nil {
			util.WriteError(w, http.StatusBadRequest, err.Error())
			return
		}
		if a.profiles != nil {
			ownerID := identityScope(identity)
			if code := a.profiles.InviteCodeOf(ownerID); code != "" {
				item["inviter_invite_code"] = code
				if shareURL, ok := item["share_url"].(string); ok && shareURL != "" {
					item["share_url"] = appendQueryParam(shareURL, "ref", code)
				}
			}
		}
		util.WriteJSON(w, http.StatusOK, item)
	case http.MethodGet:
		id := strings.TrimSpace(r.URL.Query().Get("id"))
		if id == "" {
			id = strings.TrimPrefix(strings.TrimPrefix(r.URL.Path, "/api/image-shares/"), "/api/image-shares")
			id = strings.Trim(id, "/")
		}
		if id == "" {
			util.WriteError(w, http.StatusBadRequest, "id is required")
			return
		}
		item := a.shares.Get(id)
		if item == nil {
			util.WriteError(w, http.StatusNotFound, "share image not found")
			return
		}
		ownerID := a.shares.OwnerIDOf(id)
		inviterCode := ""
		if a.profiles != nil {
			inviterCode = a.profiles.InviteCodeOf(ownerID)
		}
		if inviterCode != "" {
			item["inviter_invite_code"] = inviterCode
			if shareURL, ok := item["share_url"].(string); ok && shareURL != "" {
				item["share_url"] = appendQueryParam(shareURL, "ref", inviterCode)
			}
		}
		w.Header().Set("Cache-Control", imageShareAPICacheControl)
		util.WriteJSON(w, http.StatusOK, item)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (a *App) canCreateImageShare(identity service.Identity, imageValue string) bool {
	imageValue = strings.TrimSpace(imageValue)
	if imageValue == "" {
		return false
	}
	if strings.HasPrefix(imageValue, "data:image/") {
		return true
	}
	scope := service.ImageAccessScope{OwnerID: identityScope(identity)}
	if identity.Role == service.AuthRoleAdmin {
		scope = service.ImageAccessScope{All: true}
	}
	_, err := a.images.ImageFileAccess(imageValue, scope)
	return err == nil
}

// appendQueryParam adds key=value to the URL while preserving any existing query string.
// If key already exists it overwrites the previous value to avoid duplicate ref params.
func appendQueryParam(rawURL, key, value string) string {
	if rawURL == "" || key == "" || value == "" {
		return rawURL
	}
	if idx := strings.Index(rawURL, "#"); idx >= 0 {
		base := rawURL[:idx]
		fragment := rawURL[idx:]
		return appendQueryParam(base, key, value) + fragment
	}
	separator := "?"
	if strings.Contains(rawURL, "?") {
		separator = "&"
	}
	return rawURL + separator + key + "=" + value
}

func (a *App) handleImageCallStats(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.requireIdentity(w, r, ""); !ok {
		return
	}
	if a.callStats == nil {
		util.WriteJSON(w, http.StatusOK, map[string]any{"today_calls": 0, "total_calls": 0})
		return
	}
	util.WriteJSON(w, http.StatusOK, a.callStats.Snapshot())
}

func (a *App) handleShareImageFile(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	rel, err := imageShareRequestPath(r)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	root := filepath.Join(a.config.DataDir, "share_images")
	if a.shares != nil {
		root = a.shares.ImageDir()
	}
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	full := filepath.Join(rootAbs, filepath.FromSlash(rel))
	fullAbs, err := filepath.Abs(full)
	if err != nil || !pathInsideRoot(rootAbs, fullAbs) {
		http.NotFound(w, r)
		return
	}
	if info, err := os.Stat(fullAbs); err == nil && !info.IsDir() {
		w.Header().Set("Cache-Control", imageShareCacheControl)
		http.ServeFile(w, r, fullAbs)
		return
	}
	http.NotFound(w, r)
}

func (a *App) handleLogout(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	clearAuthSessionCookie(w, r)
	util.WriteJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (a *App) writeLoginResponse(w http.ResponseWriter, identity service.Identity, token string) {
	permissions := a.identityPermissions(identity)
	subjectID := identityScope(identity)
	payload := map[string]any{
		"ok":              true,
		"version":         version.Get(),
		"token":           token,
		"role":            identity.Role,
		"role_id":         identity.RoleID,
		"role_name":       identity.RoleName,
		"subject_id":      subjectID,
		"name":            identity.Name,
		"provider":        identity.Provider,
		"credential_id":   identity.CredentialID,
		"credential_name": identity.CredentialName,
		"menu_paths":      permissions.MenuPaths,
		"api_permissions": permissions.APIPermissions,
		"menus":           service.FilterMenuPermissions(permissions.MenuPaths),
	}
	if a.profiles != nil && identity.Role == service.AuthRoleUser {
		for key, value := range a.profiles.PublicForUser(subjectID) {
			payload[key] = value
		}
	}
	if a.notifications != nil && identity.Role == service.AuthRoleUser {
		payload["notifications_unread"] = a.notifications.UnreadCountForOwner(subjectID)
	}
	if token == "" {
		delete(payload, "token")
	}
	util.WriteJSON(w, http.StatusOK, payload)
}

func (a *App) handleInviteMe(w http.ResponseWriter, r *http.Request) {
	identity, ok := a.requireIdentity(w, r, "")
	if !ok {
		return
	}
	if a.profiles == nil {
		util.WriteJSON(w, http.StatusOK, map[string]any{})
		return
	}
	util.WriteJSON(w, http.StatusOK, a.profiles.InviteSummary(identity, a.resolveImageBaseURL(r), a.config.InviteRewardQuota(), a.config.InviteeBonusQuota()))
}

func (a *App) handleCheckinStatus(w http.ResponseWriter, r *http.Request) {
	identity, ok := a.requireIdentity(w, r, "")
	if !ok {
		return
	}
	if a.profiles == nil {
		util.WriteJSON(w, http.StatusOK, map[string]any{"enabled": false})
		return
	}
	util.WriteJSON(w, http.StatusOK, a.profiles.CheckinStatus(identity, a.config.CheckinRewards(), a.config.CheckinEnabled()))
}

func (a *App) handleCheckin(w http.ResponseWriter, r *http.Request) {
	identity, ok := a.requireIdentity(w, r, "")
	if !ok {
		return
	}
	if a.profiles == nil {
		util.WriteError(w, http.StatusBadRequest, "checkin service unavailable")
		return
	}
	result, err := a.profiles.Checkin(identity, a.config.CheckinRewards(), a.config.CheckinEnabled())
	if err != nil {
		if strings.Contains(err.Error(), "今日已签到") {
			util.WriteJSON(w, http.StatusOK, map[string]any{"already_checked": true, "reward": 0, "state": result})
			return
		}
		util.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	reward := util.ToInt(result["reward"], 0)
	day := util.ToInt(result["day"], util.ToInt(result["current_streak"], 0))
	quotaAfter := util.ToInt(result["image_quota_total"], 0)
	a.emitCheckinNotification(identityScope(identity), day, reward, quotaAfter)
	util.WriteJSON(w, http.StatusOK, map[string]any{"already_checked": false, "reward": reward, "state": result})
}

func (a *App) handleCheckinAdminConfig(w http.ResponseWriter, r *http.Request) {
	identity, ok := a.requireIdentity(w, r, "")
	if !ok {
		return
	}
	if identity.Role != service.AuthRoleAdmin {
		util.WriteError(w, http.StatusForbidden, "permission denied")
		return
	}
	switch r.Method {
	case http.MethodGet:
		util.WriteJSON(w, http.StatusOK, map[string]any{"enabled": a.config.CheckinEnabled(), "rewards": a.config.CheckinRewards()})
	case http.MethodPut, http.MethodPost:
		body, err := readJSONMap(r)
		if err != nil {
			util.WriteError(w, http.StatusBadRequest, "invalid json body")
			return
		}
		updates := map[string]any{}
		if value, ok := body["enabled"]; ok {
			updates["checkin_enabled"] = util.ToBool(value)
		}
		if value, ok := body["rewards"]; ok {
			updates["checkin_rewards"] = value
		}
		if len(updates) == 0 {
			util.WriteError(w, http.StatusBadRequest, "no updates provided")
			return
		}
		if _, err := a.config.Update(updates); err != nil {
			util.WriteError(w, http.StatusBadRequest, err.Error())
			return
		}
		util.WriteJSON(w, http.StatusOK, map[string]any{"enabled": a.config.CheckinEnabled(), "rewards": a.config.CheckinRewards()})
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (a *App) handleCheckinAdminLogs(w http.ResponseWriter, r *http.Request) {
	identity, ok := a.requireIdentity(w, r, "")
	if !ok {
		return
	}
	if identity.Role != service.AuthRoleAdmin {
		util.WriteError(w, http.StatusForbidden, "permission denied")
		return
	}
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	limit := util.ToInt(r.URL.Query().Get("limit"), 100)
	result := map[string]any{"items": []map[string]any{}}
	if a.profiles != nil {
		result = a.profiles.CheckinLogs(limit)
	}
	result["enabled"] = a.config.CheckinEnabled()
	result["rewards"] = a.config.CheckinRewards()
	util.WriteJSON(w, http.StatusOK, result)
}

func (a *App) handleSettings(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.requireIdentity(w, r, ""); !ok {
		return
	}
	switch r.Method {
	case http.MethodGet:
		util.WriteJSON(w, http.StatusOK, map[string]any{"config": a.config.Get()})
	case http.MethodPost:
		body, err := readJSONMap(r)
		if err != nil {
			util.WriteError(w, http.StatusBadRequest, "invalid json body")
			return
		}
		updated, err := a.config.Update(body)
		if err != nil {
			util.WriteError(w, http.StatusBadRequest, err.Error())
			return
		}
		a.update = newUpdateService(a.config)
		util.WriteJSON(w, http.StatusOK, map[string]any{"config": updated})
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (a *App) handleAppMeta(w http.ResponseWriter, r *http.Request) {
	util.WriteJSON(w, http.StatusOK, map[string]any{
		"app_title":                   "1818",
		"project_name":                "1818",
		"login_page_image_url":        a.config.LoginPageImageURL(),
		"login_page_image_mode":       a.config.LoginPageImageMode(),
		"login_page_image_zoom":       a.config.LoginPageImageZoom(),
		"login_page_image_position_x": a.config.LoginPageImagePositionX(),
		"login_page_image_position_y": a.config.LoginPageImagePositionY(),
	})
}

func (a *App) handlePermissionCatalog(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.requireIdentity(w, r, ""); !ok {
		return
	}
	util.WriteJSON(w, http.StatusOK, a.auth.PermissionCatalog())
}

func (a *App) handleLoginPageImageSettings(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.requireIdentity(w, r, ""); !ok {
		return
	}
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if err := r.ParseMultipartForm(maxLoginPageImageSize + (1 << 20)); err != nil {
		util.WriteError(w, http.StatusBadRequest, "invalid multipart form")
		return
	}

	currentImageURL := a.config.LoginPageImageURL()
	nextImageURL := strings.TrimSpace(r.FormValue("login_page_image_url"))
	uploadedImageURL := ""
	switch strings.ToLower(strings.TrimSpace(r.FormValue("login_page_image_action"))) {
	case "remove":
		nextImageURL = ""
	case "replace":
		fileHeader := firstMultipartFile(r.MultipartForm, "login_page_image_file")
		if fileHeader == nil {
			util.WriteError(w, http.StatusBadRequest, "login page image file is required")
			return
		}
		storedURL, err := a.storeLoginPageImage(fileHeader)
		if err != nil {
			util.WriteError(w, http.StatusBadRequest, err.Error())
			return
		}
		nextImageURL = storedURL
		uploadedImageURL = storedURL
	}

	updated, err := a.config.Update(map[string]any{
		"login_page_image_url":        nextImageURL,
		"login_page_image_mode":       strings.TrimSpace(r.FormValue("login_page_image_mode")),
		"login_page_image_zoom":       strings.TrimSpace(r.FormValue("login_page_image_zoom")),
		"login_page_image_position_x": strings.TrimSpace(r.FormValue("login_page_image_position_x")),
		"login_page_image_position_y": strings.TrimSpace(r.FormValue("login_page_image_position_y")),
	})
	if err != nil {
		if uploadedImageURL != "" {
			a.deleteLocalLoginPageImage(uploadedImageURL)
		}
		util.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	if currentImageURL != "" && currentImageURL != nextImageURL {
		a.deleteLocalLoginPageImage(currentImageURL)
	}
	util.WriteJSON(w, http.StatusOK, map[string]any{"config": updated})
}

func (a *App) storeLoginPageImage(header *multipart.FileHeader) (string, error) {
	data, ext, err := readLoginPageImageFile(header)
	if err != nil {
		return "", err
	}
	stem := safeUploadStem(header.Filename)
	if stem == "" {
		stem = "login-page"
	}
	filename := fmt.Sprintf("%d-%s%s", time.Now().UnixNano(), stem, ext)
	target := filepath.Join(a.config.LoginPageImagesDir(), filename)
	if err := os.WriteFile(target, data, 0o644); err != nil {
		return "", err
	}
	return "/login-page-images/" + filename, nil
}

func readLoginPageImageFile(header *multipart.FileHeader) ([]byte, string, error) {
	if header == nil {
		return nil, "", fmt.Errorf("image file is required")
	}
	if header.Size > maxLoginPageImageSize {
		return nil, "", fmt.Errorf("login page image cannot exceed 10MB")
	}
	file, err := header.Open()
	if err != nil {
		return nil, "", err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maxLoginPageImageSize+1))
	if err != nil {
		return nil, "", err
	}
	if len(data) == 0 {
		return nil, "", fmt.Errorf("image file is empty")
	}
	if len(data) > maxLoginPageImageSize {
		return nil, "", fmt.Errorf("login page image cannot exceed 10MB")
	}
	if ext := strings.ToLower(filepath.Ext(header.Filename)); ext == ".svg" && bytes.Contains(bytes.ToLower(data[:min(len(data), 512)]), []byte("<svg")) {
		return data, ".svg", nil
	}
	if _, _, err := image.DecodeConfig(bytes.NewReader(data)); err != nil {
		return nil, "", fmt.Errorf("unsupported image file")
	}
	switch http.DetectContentType(data) {
	case "image/jpeg":
		return data, ".jpg", nil
	case "image/gif":
		return data, ".gif", nil
	case "image/webp":
		return data, ".webp", nil
	default:
		return data, ".png", nil
	}
}

func (a *App) deleteLocalLoginPageImage(imageURL string) {
	imagePath, ok := a.localLoginPageImagePath(imageURL)
	if ok {
		_ = os.Remove(imagePath)
	}
}

func (a *App) localLoginPageImagePath(imageURL string) (string, bool) {
	cleanURL := strings.TrimSpace(imageURL)
	if !strings.HasPrefix(cleanURL, "/login-page-images/") {
		return "", false
	}
	rel := strings.TrimPrefix(path.Clean(cleanURL), "/login-page-images/")
	if rel == "." || rel == "" || strings.Contains(rel, "..") {
		return "", false
	}
	root, err := filepath.Abs(a.config.LoginPageImagesDir())
	if err != nil {
		return "", false
	}
	target, err := filepath.Abs(filepath.Join(root, filepath.FromSlash(rel)))
	if err != nil {
		return "", false
	}
	if target != root && !strings.HasPrefix(target, root+string(os.PathSeparator)) {
		return "", false
	}
	return target, true
}

func firstMultipartFile(form *multipart.Form, key string) *multipart.FileHeader {
	if form == nil || len(form.File[key]) == 0 {
		return nil
	}
	return form.File[key][0]
}

func safeUploadStem(filename string) string {
	name := strings.TrimSuffix(filepath.Base(filename), filepath.Ext(filename))
	name = strings.ToLower(strings.TrimSpace(name))
	var builder strings.Builder
	for _, char := range name {
		switch {
		case char >= 'a' && char <= 'z':
			builder.WriteRune(char)
		case char >= '0' && char <= '9':
			builder.WriteRune(char)
		case char == '-' || char == '_':
			builder.WriteRune(char)
		case char == ' ' || char == '.':
			builder.WriteRune('-')
		}
	}
	return strings.Trim(builder.String(), "-_")
}

func (a *App) handleImages(w http.ResponseWriter, r *http.Request) {
	identity, ok := a.requireIdentity(w, r, "")
	if !ok {
		return
	}
	switch r.Method {
	case http.MethodGet:
		scope, status, message := imageListAccessScope(identity, r.URL.Query().Get("scope"))
		if status != 0 {
			util.WriteError(w, status, message)
			return
		}
		payload := a.images.ListImages(a.resolveImageBaseURL(r), strings.TrimSpace(r.URL.Query().Get("start_date")), strings.TrimSpace(r.URL.Query().Get("end_date")), scope)
		a.decorateImageList(payload)
		util.WriteJSON(w, http.StatusOK, payload)
	case http.MethodDelete:
		body, err := readJSONMap(r)
		if err != nil {
			util.WriteError(w, http.StatusBadRequest, "invalid json body")
			return
		}
		scope := service.ImageAccessScope{OwnerID: identityScope(identity)}
		if identity.Role == service.AuthRoleAdmin {
			scope = service.ImageAccessScope{All: true}
		}
		result, err := a.images.DeleteImages(util.AsStringSlice(body["paths"]), scope)
		if err != nil {
			util.WriteError(w, http.StatusBadRequest, err.Error())
			return
		}
		util.WriteJSON(w, http.StatusOK, result)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (a *App) handleImageVisibility(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPatch {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	identity, ok := a.requireIdentity(w, r, "")
	if !ok {
		return
	}
	body, err := readJSONMap(r)
	if err != nil {
		util.WriteError(w, http.StatusBadRequest, "invalid json body")
		return
	}
	path := util.Clean(body["path"])
	if path == "" {
		util.WriteError(w, http.StatusBadRequest, "path is required")
		return
	}
	visibility := util.Clean(body["visibility"])
	scope := service.ImageAccessScope{OwnerID: identityScope(identity)}
	if identity.Role == service.AuthRoleAdmin {
		scope = service.ImageAccessScope{All: true}
	}
	item, err := a.images.UpdateImageVisibility(path, visibility, scope)
	if err != nil {
		status := http.StatusBadRequest
		if err.Error() == "image not found" {
			status = http.StatusNotFound
		}
		util.WriteError(w, status, err.Error())
		return
	}
	a.decorateImageItem(item, a.imageOwnerDisplayNames())
	util.WriteJSON(w, http.StatusOK, map[string]any{"item": item})
}

func (a *App) handleImageFile(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	rel, err := imageFileRequestPath(r)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	ref, ok := a.authorizeImageFileRequest(w, r, rel)
	if !ok {
		return
	}
	http.ServeFile(w, r, ref.Path)
}

func (a *App) authorizeImageFileRequest(w http.ResponseWriter, r *http.Request, rel string) (service.ImageFileAccess, bool) {
	ref, err := a.images.ImageFileAccess(rel, service.ImageAccessScope{All: true})
	if err != nil {
		http.NotFound(w, r)
		return service.ImageFileAccess{}, false
	}
	if ref.Visibility == service.ImageVisibilityPublic {
		return ref, true
	}
	identity, ok := a.imageRequestIdentity(w, r)
	if !ok {
		return service.ImageFileAccess{}, false
	}
	if identity.Role == service.AuthRoleAdmin || (ref.OwnerID != "" && ref.OwnerID == identityScope(identity)) {
		return ref, true
	}
	http.NotFound(w, r)
	return service.ImageFileAccess{}, false
}

func (a *App) handleImageThumbnail(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	thumbnailRel, err := imageThumbnailRequestPath(r)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	sourceRel, sourceErr := a.images.SourceImageRelativePathFromThumbnail(thumbnailRel)
	if sourceErr != nil {
		http.NotFound(w, r)
		return
	}
	if _, ok := a.authorizeImageFileRequest(w, r, sourceRel); !ok {
		return
	}
	_ = a.images.EnsureThumbnail(thumbnailRel)
	thumbPath := filepath.Join(a.config.ImageThumbnailsDir(), filepath.FromSlash(thumbnailRel))
	if info, err := os.Stat(thumbPath); err == nil && !info.IsDir() {
		w.Header().Set("Cache-Control", imageThumbnailCacheControl)
		http.ServeFile(w, r, thumbPath)
		return
	}
	sourcePath := filepath.Join(a.config.ImagesDir(), filepath.FromSlash(sourceRel))
	if info, err := os.Stat(sourcePath); err == nil && !info.IsDir() {
		http.ServeFile(w, r, sourcePath)
		return
	}
	http.NotFound(w, r)
}

func imageFileRequestPath(r *http.Request) (string, error) {
	raw := strings.TrimPrefix(r.URL.EscapedPath(), "/images/")
	if raw == "" || raw == r.URL.EscapedPath() {
		return "", errors.New("invalid image path")
	}
	rel, err := url.PathUnescape(raw)
	if err != nil {
		return "", err
	}
	return rel, nil
}

func imageThumbnailRequestPath(r *http.Request) (string, error) {
	raw := strings.TrimPrefix(r.URL.EscapedPath(), "/image-thumbnails/")
	if raw == "" || raw == r.URL.EscapedPath() {
		return "", errors.New("invalid thumbnail path")
	}
	rel, err := url.PathUnescape(raw)
	if err != nil {
		return "", err
	}
	return rel, nil
}

func imageShareRequestPath(r *http.Request) (string, error) {
	raw := strings.TrimPrefix(r.URL.EscapedPath(), "/share-images/")
	if raw == "" || raw == r.URL.EscapedPath() {
		return "", errors.New("invalid share image path")
	}
	rel, err := url.PathUnescape(raw)
	if err != nil {
		return "", err
	}
	if rel == "" || strings.Contains(rel, "..") {
		return "", errors.New("invalid share image path")
	}
	return rel, nil
}

func pathInsideRoot(root, target string) bool {
	rel, err := filepath.Rel(root, target)
	if err != nil {
		return false
	}
	return rel == "." || (!strings.HasPrefix(rel, "..") && !filepath.IsAbs(rel))
}

func (a *App) handleLogs(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.requireIdentity(w, r, ""); !ok {
		return
	}
	query, err := parseLogQuery(r)
	if err != nil {
		util.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	items := a.logs.Search(query)
	util.WriteJSON(w, http.StatusOK, map[string]any{"items": items, "total": len(items), "page_size": normalizedHTTPLogPageSize(query.Limit)})
}

func (a *App) handleLogGovernance(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.requireIdentity(w, r, ""); !ok {
		return
	}
	switch r.Method {
	case http.MethodGet:
		util.WriteJSON(w, http.StatusOK, map[string]any{"governance": a.logs.GovernanceSummary()})
	case http.MethodPost:
		body, err := readJSONMap(r)
		if err != nil {
			util.WriteError(w, http.StatusBadRequest, "invalid json body")
			return
		}
		retentionDays := util.ToInt(body["retention_days"], a.config.LogRetentionDays())
		result, err := a.logs.CleanupOlderThan(retentionDays)
		if err != nil {
			util.WriteError(w, http.StatusBadRequest, err.Error())
			return
		}
		util.WriteJSON(w, http.StatusOK, map[string]any{
			"cleanup":    result,
			"governance": a.logs.GovernanceSummary(),
		})
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (a *App) handleStorageInfo(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.requireIdentity(w, r, ""); !ok {
		return
	}
	backend, err := a.config.StorageBackend()
	if err != nil {
		util.WriteError(w, http.StatusBadGateway, err.Error())
		return
	}
	util.WriteJSON(w, http.StatusOK, map[string]any{"backend": backend.Info(), "health": backend.HealthCheck()})
}

func (a *App) handleProxy(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.requireIdentity(w, r, ""); !ok {
		return
	}
	if r.URL.Path == "/api/proxy/test" {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		body, _ := readJSONMap(r)
		candidate := strings.TrimSpace(util.Clean(body["url"]))
		if candidate == "" {
			candidate = a.config.Proxy()
		}
		if candidate == "" {
			util.WriteError(w, http.StatusBadRequest, "proxy url is required")
			return
		}
		util.WriteJSON(w, http.StatusOK, map[string]any{"result": a.proxy.Test(candidate, 15*time.Second)})
		return
	}
	switch r.Method {
	case http.MethodGet:
		url := a.config.Proxy()
		util.WriteJSON(w, http.StatusOK, map[string]any{"proxy": map[string]any{"enabled": url != "", "url": url}})
	case http.MethodPost:
		body, _ := readJSONMap(r)
		url := a.config.Proxy()
		if _, ok := body["url"]; ok {
			url = util.Clean(body["url"])
		}
		if _, ok := body["enabled"]; ok {
			if util.ToBool(body["enabled"]) {
				if strings.TrimSpace(url) == "" {
					util.WriteError(w, http.StatusBadRequest, "proxy url is required when proxy is enabled")
					return
				}
			} else {
				url = ""
			}
		}
		updated, err := a.config.Update(map[string]any{"proxy": url})
		if err != nil {
			util.WriteError(w, http.StatusBadRequest, err.Error())
			return
		}
		updatedURL := strings.TrimSpace(fmt.Sprint(updated["proxy"]))
		util.WriteJSON(w, http.StatusOK, map[string]any{"proxy": map[string]any{"enabled": updatedURL != "", "url": updatedURL}})
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (a *App) requireIdentity(w http.ResponseWriter, r *http.Request, overrideAuth string) (service.Identity, bool) {
	identity, _, ok := a.requireIdentityWithToken(w, r, overrideAuth)
	return identity, ok
}

func (a *App) requireIdentityWithToken(w http.ResponseWriter, r *http.Request, overrideAuth string) (service.Identity, string, bool) {
	for _, token := range authTokenCandidates(overrideAuth, r) {
		if identity := a.auth.Authenticate(token); identity != nil {
			if !a.identityCanAccessRequest(*identity, r) {
				util.WriteError(w, http.StatusForbidden, "permission denied")
				return service.Identity{}, "", false
			}
			*r = *r.WithContext(withRequestIdentity(r.Context(), *identity))
			return *identity, token, true
		}
	}
	util.WriteError(w, http.StatusUnauthorized, "authorization is invalid")
	return service.Identity{}, "", false
}

func authTokenCandidates(overrideAuth string, r *http.Request) []string {
	if overrideAuth != "" {
		return appendAuthTokenCandidate(nil, extractBearerToken(overrideAuth))
	}
	return appendAuthTokenCandidate(appendAuthTokenCandidate(nil, requestAuthCookieToken(r)), requestBearerToken(r))
}

func requestAuthToken(r *http.Request) string {
	candidates := authTokenCandidates("", r)
	if len(candidates) == 0 {
		return ""
	}
	return candidates[0]
}

func appendAuthTokenCandidate(tokens []string, token string) []string {
	token = strings.TrimSpace(token)
	if token == "" {
		return tokens
	}
	for _, existing := range tokens {
		if existing == token {
			return tokens
		}
	}
	return append(tokens, token)
}

func requestBearerToken(r *http.Request) string {
	return extractBearerToken(r.Header.Get("Authorization"))
}

func requestAuthCookieToken(r *http.Request) string {
	cookie, err := r.Cookie(authSessionCookieName)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(cookie.Value)
}

func (a *App) imageRequestIdentity(w http.ResponseWriter, r *http.Request) (service.Identity, bool) {
	candidates := authTokenCandidates("", r)
	if len(candidates) == 0 {
		util.WriteError(w, http.StatusUnauthorized, "authorization is invalid")
		return service.Identity{}, false
	}
	for _, token := range candidates {
		if identity := a.auth.Authenticate(token); identity != nil {
			return *identity, true
		}
	}
	util.WriteError(w, http.StatusUnauthorized, "authorization is invalid")
	return service.Identity{}, false
}

func (a *App) identityPermissions(identity service.Identity) service.PermissionSet {
	if identity.Role == service.AuthRoleAdmin {
		return service.DefaultPermissionSetForRole(service.AuthRoleAdmin)
	}
	return service.PermissionSet{
		MenuPaths:      service.NormalizeMenuPermissions(identity.MenuPaths),
		APIPermissions: service.NormalizeAPIPermissions(identity.APIPermissions),
	}
}

func (a *App) identityCanAccessRequest(identity service.Identity, r *http.Request) bool {
	if identity.Role == service.AuthRoleAdmin || isPermissionCheckSkipped(r.URL.Path) {
		return true
	}
	return a.identityCanAccessAPI(identity, r.Method, r.URL.Path)
}

func (a *App) identityCanAccessAPI(identity service.Identity, method, path string) bool {
	if identity.Role == service.AuthRoleAdmin {
		return true
	}
	return service.HasAPIPermission(a.identityPermissions(identity), method, path)
}

func isPermissionCheckSkipped(path string) bool {
	switch path {
	case "/auth/login":
		return true
	case "/auth/logout":
		return true
	case "/auth/register":
		return true
	case "/auth/session":
		return true
	case "/api/profile":
		return true
	case "/api/profile/password":
		return true
	case "/api/profile/api-key":
		return true
	case "/api/profile/prompt-favorites":
		return true
	case "/api/notifications":
		return true
	case "/api/subscription/config":
		return true
	case "/api/subscription/checkout":
		return true
	case "/api/subscription/order":
		return true
	default:
		if strings.HasPrefix(path, "/api/profile/api-key/") || strings.HasPrefix(path, "/api/profile/prompt-favorites/") {
			return true
		}
		// /api/notifications/<id>/read and /api/notifications/<id> are user-scoped and the
		// handler enforces ownership. Skip the static permission check so default users can
		// read their own bell without bespoke role wiring.
		if strings.HasPrefix(path, "/api/notifications/") {
			return true
		}
		return false
	}
}

func extractBearerToken(auth string) string {
	scheme, value, ok := strings.Cut(strings.TrimSpace(auth), " ")
	if !ok || strings.ToLower(scheme) != "bearer" {
		return ""
	}
	return strings.TrimSpace(value)
}

func setAuthSessionCookie(w http.ResponseWriter, r *http.Request, token string) {
	token = strings.TrimSpace(token)
	if token == "" {
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     authSessionCookieName,
		Value:    token,
		Path:     "/",
		MaxAge:   30 * 24 * 60 * 60,
		HttpOnly: true,
		Secure:   isHTTPSRequest(r),
		SameSite: http.SameSiteLaxMode,
	})
}

func clearAuthSessionCookie(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name:     authSessionCookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   isHTTPSRequest(r),
		SameSite: http.SameSiteLaxMode,
	})
}

func (a *App) resolveImageBaseURL(r *http.Request) string {
	if base := a.config.BaseURL(); base != "" {
		return base
	}
	scheme := sanitizedRequestScheme(r)
	host := sanitizedRequestHost(r.Host)
	if host == "" {
		host = "localhost"
	}
	return scheme + "://" + host
}

func sanitizedRequestScheme(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	if forwarded := strings.TrimSpace(r.Header.Get("x-forwarded-proto")); forwarded != "" {
		candidate := strings.ToLower(strings.TrimSpace(strings.Split(forwarded, ",")[0]))
		if candidate == "http" || candidate == "https" {
			scheme = candidate
		}
	}
	return scheme
}

func sanitizedRequestHost(host string) string {
	host = strings.TrimSpace(host)
	if host == "" || strings.ContainsAny(host, "/\\@?#") || hasHTTPControlChar(host) {
		return ""
	}
	if parsedHost, port, err := net.SplitHostPort(host); err == nil {
		if !validHostPort(port) {
			return ""
		}
		cleanHost := sanitizedHostName(parsedHost)
		if cleanHost == "" {
			return ""
		}
		if strings.Contains(cleanHost, ":") {
			return "[" + cleanHost + "]:" + port
		}
		return cleanHost + ":" + port
	}
	if strings.HasPrefix(host, "[") {
		end := strings.Index(host, "]")
		if end < 0 || end != len(host)-1 {
			return ""
		}
		cleanHost := sanitizedHostName(host[1:end])
		if cleanHost == "" || !strings.Contains(cleanHost, ":") {
			return ""
		}
		return "[" + cleanHost + "]"
	}
	cleanHost := sanitizedHostName(host)
	if cleanHost == "" {
		return ""
	}
	if strings.Contains(cleanHost, ":") {
		return "[" + cleanHost + "]"
	}
	return cleanHost
}

func sanitizedHostName(host string) string {
	host = strings.Trim(strings.TrimSpace(host), "[]")
	if host == "" || hasHTTPControlChar(host) {
		return ""
	}
	if ip := net.ParseIP(host); ip != nil {
		return ip.String()
	}
	for _, r := range host {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '.' {
			continue
		}
		return ""
	}
	trimmed := strings.Trim(host, ".")
	if trimmed == "" || strings.Contains(host, "..") {
		return ""
	}
	return strings.ToLower(host)
}

func validHostPort(port string) bool {
	if port == "" {
		return false
	}
	for _, r := range port {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func hasHTTPControlChar(value string) bool {
	for _, r := range value {
		if r <= 31 || r == 127 {
			return true
		}
	}
	return false
}

func readJSONMap(r *http.Request) (map[string]any, error) {
	var body map[string]any
	data, err := io.ReadAll(io.LimitReader(r.Body, maxJSONBodySize+1))
	if err != nil {
		return map[string]any{}, err
	}
	if len(data) > maxJSONBodySize {
		return map[string]any{}, errors.New("request body is too large")
	}
	err = util.DecodeJSON(bytes.NewReader(data), &body)
	if body == nil {
		body = map[string]any{}
	}
	return body, err
}

func readMultipartImageBody(r *http.Request) (map[string]any, []protocol.UploadedImage, error) {
	if err := r.ParseMultipartForm(128 << 20); err != nil {
		return nil, nil, err
	}
	body := map[string]any{
		"client_task_id":     firstForm(r.MultipartForm, "client_task_id"),
		"prompt":             firstForm(r.MultipartForm, "prompt"),
		"model":              firstNonEmpty(firstForm(r.MultipartForm, "model"), util.ImageModelAuto),
		"n":                  util.ToInt(firstForm(r.MultipartForm, "n"), 1),
		"size":               firstForm(r.MultipartForm, "size"),
		"requested_size":     firstForm(r.MultipartForm, "requested_size"),
		"image_resolution":   firstForm(r.MultipartForm, "image_resolution"),
		"quality":            firstForm(r.MultipartForm, "quality"),
		"output_format":      firstForm(r.MultipartForm, "output_format"),
		"output_compression": firstForm(r.MultipartForm, "output_compression"),
		"visibility":         firstForm(r.MultipartForm, "visibility"),
		"response_format":    firstNonEmpty(firstForm(r.MultipartForm, "response_format"), "b64_json"),
		"stream":             util.ToBool(firstForm(r.MultipartForm, "stream")),
	}
	if rawMessages := strings.TrimSpace(firstForm(r.MultipartForm, "messages")); rawMessages != "" {
		var messages any
		if err := json.Unmarshal([]byte(rawMessages), &messages); err != nil {
			return nil, nil, fmt.Errorf("invalid messages")
		}
		body["messages"] = messages
	}
	var images []protocol.UploadedImage
	for _, field := range []string{"image", "image[]"} {
		for _, header := range r.MultipartForm.File[field] {
			image, err := readUpload(header)
			if err != nil {
				return nil, nil, err
			}
			if len(image.Data) == 0 {
				return nil, nil, fmt.Errorf("image file is empty")
			}
			images = append(images, image)
		}
	}
	return body, images, nil
}

func (a *App) imageQuotaChargeForPayload(payload map[string]any, requested int) (int, bool) {
	requested = maxInt(1, requested)
	switch imageResolutionPresetForQuota(payload) {
	case "2k":
		return maxInt(1, a.config.ImageUpscale2KQuotaCost()) * requested, true
	case "4k":
		return maxInt(1, a.config.ImageUpscale4KQuotaCost()) * requested, true
	default:
		return requested, false
	}
}

func imageResolutionPresetForQuota(payload map[string]any) string {
	if preset := service.NormalizeImageResolutionPreset(util.Clean(payload["image_resolution"])); preset != "" {
		return preset
	}
	text := strings.ToLower(strings.Join([]string{
		util.Clean(payload["prompt"]),
		util.Clean(payload["size"]),
		util.Clean(payload["requested_size"]),
	}, " "))
	if strings.Contains(text, "8k") || strings.Contains(text, "4k") {
		return "4k"
	}
	if strings.Contains(text, "2k") {
		return "2k"
	}
	return ""
}

func firstForm(form *multipart.Form, key string) string {
	if form == nil || len(form.Value[key]) == 0 {
		return ""
	}
	return form.Value[key][0]
}

func readUpload(header *multipart.FileHeader) (protocol.UploadedImage, error) {
	file, err := header.Open()
	if err != nil {
		return protocol.UploadedImage{}, err
	}
	defer file.Close()
	data, err := io.ReadAll(file)
	if err != nil {
		return protocol.UploadedImage{}, err
	}
	contentType := header.Header.Get("Content-Type")
	if contentType == "" {
		contentType = "image/png"
	}
	filename := header.Filename
	if filename == "" {
		filename = "image.png"
	}
	return protocol.UploadedImage{Data: data, Filename: filename, ContentType: contentType}, nil
}

func jsonString(v any) string {
	data, _ := json.Marshal(v)
	return string(data)
}

func openAIErrorForStream(err error) map[string]any {
	var imageErr *protocol.ImageGenerationError
	if errors.As(err, &imageErr) {
		return imageErr.OpenAIError()
	}
	return map[string]any{"error": map[string]any{"message": err.Error(), "type": fmt.Sprintf("%T", err)}}
}

func (a *App) logCall(identity service.Identity, summary, method, endpoint, model string, started time.Time, outcome string, status int, errText string, urls []string) {
	method = strings.ToUpper(strings.TrimSpace(method))
	if status <= 0 {
		status = http.StatusOK
		if outcome == "failed" {
			status = http.StatusInternalServerError
		}
	}
	ended := time.Now()
	detail := map[string]any{
		"method":         method,
		"path":           endpoint,
		"endpoint":       endpoint,
		"module":         inferAuditModule(endpoint),
		"model":          model,
		"started_at":     started.Format("2006-01-02 15:04:05"),
		"ended_at":       ended.Format("2006-01-02 15:04:05"),
		"duration_ms":    ended.Sub(started).Milliseconds(),
		"status":         status,
		"outcome":        outcome,
		"operation_type": operationTypeForMethod(method),
		"log_level":      logLevelForStatus(status),
	}
	addIdentityLogDetail(detail, identity)
	if name := identityDisplayName(identity); name != "" {
		detail["username"] = name
	}
	if errText != "" {
		detail["error"] = errText
	}
	if len(urls) > 0 {
		detail["urls"] = dedupe(urls)
	}
	suffix := "调用完成"
	if outcome == "failed" {
		suffix = "调用失败"
	}
	a.logs.Add(summary+suffix, detail)
}

func addIdentityLogDetail(detail map[string]any, identity service.Identity) {
	if name := util.Clean(firstNonEmpty(identity.CredentialName, identity.Name)); name != "" {
		detail["key_name"] = name
	}
	if role := util.Clean(identity.Role); role != "" {
		detail["key_role"] = role
	}
	if id := util.Clean(firstNonEmpty(identity.CredentialID, identity.ID)); id != "" {
		detail["key_id"] = id
	}
	if id := util.Clean(identity.ID); id != "" && id != util.Clean(identity.CredentialID) {
		detail["subject_id"] = id
	}
	if provider := util.Clean(identity.Provider); provider != "" {
		detail["provider"] = provider
	}
}

func identityScope(identity service.Identity) string {
	if owner := util.Clean(identity.OwnerID); owner != "" {
		return owner
	}
	if id := util.Clean(identity.ID); id != "" {
		return id
	}
	return "anonymous"
}

func identityDisplayName(identity service.Identity) string {
	return firstNonEmpty(util.Clean(identity.Name), util.Clean(identity.CredentialName))
}

func imageAccessScope(identity service.Identity) service.ImageAccessScope {
	if identity.Role == service.AuthRoleAdmin {
		return service.ImageAccessScope{All: true}
	}
	return service.ImageAccessScope{OwnerID: identityScope(identity)}
}

func imageListAccessScope(identity service.Identity, value string) (service.ImageAccessScope, int, string) {
	switch strings.TrimSpace(value) {
	case "":
		return imageAccessScope(identity), 0, ""
	case "mine":
		return service.ImageAccessScope{OwnerID: identityScope(identity)}, 0, ""
	case "public":
		if identity.Role == service.AuthRoleAdmin {
			return service.ImageAccessScope{All: true}, 0, ""
		}
		return service.ImageAccessScope{Public: true}, 0, ""
	case "all":
		if identity.Role != service.AuthRoleAdmin {
			return service.ImageAccessScope{}, http.StatusForbidden, "admin permission required"
		}
		return service.ImageAccessScope{All: true}, 0, ""
	default:
		return service.ImageAccessScope{}, http.StatusBadRequest, "scope must be mine, public, or all"
	}
}

func (a *App) recordGeneratedImages(identity service.Identity, urls []string, visibility string) {
	if len(urls) == 0 || a.images == nil {
		return
	}
	ownerID := identityScope(identity)
	a.images.RecordGeneratedImages(urls, ownerID, identityDisplayName(identity), visibility)
	if a.callStats != nil {
		a.callStats.Record(1)
	}
}

func (a *App) recordGeneratedImagesForPayload(identity service.Identity, urls []string, visibility string, payload map[string]any, result ...map[string]any) {
	if len(urls) == 0 || a.images == nil {
		return
	}
	ownerID := identityScope(identity)
	a.images.RecordGeneratedImages(urls, ownerID, identityDisplayName(identity), visibility, service.GeneratedImageMetadata{
		ResolutionPreset: util.Clean(payload["image_resolution"]),
		RequestedSize:    util.Clean(payload["size"]),
		OutputFormat:     service.NormalizeImageOutputFormat(util.Clean(payload["output_format"])),
		Prompt:           util.Clean(payload["prompt"]),
		RevisedPrompt:    firstImageRevisedPrompt(result...),
	})
	if a.callStats != nil {
		a.callStats.Record(1)
	}
}

func firstImageRevisedPrompt(results ...map[string]any) string {
	for _, result := range results {
		for _, item := range util.AsMapSlice(result["data"]) {
			if prompt := util.Clean(item["revised_prompt"]); prompt != "" {
				return prompt
			}
		}
	}
	return ""
}

func (a *App) decorateImageList(payload map[string]any) {
	ownerNames := a.imageOwnerDisplayNames()
	for _, item := range util.AsMapSlice(payload["items"]) {
		a.decorateImageItem(item, ownerNames)
	}
}

func (a *App) decorateImageItem(item map[string]any, ownerNames map[string]string) {
	if item == nil || util.Clean(item["owner_name"]) != "" {
		return
	}
	ownerID := util.Clean(item["owner_id"])
	if ownerID == "" {
		item["owner_name"] = "未知用户"
		return
	}
	if name := ownerNames[ownerID]; name != "" {
		item["owner_name"] = name
		return
	}
	item["owner_name"] = "未知用户"
}

func (a *App) imageOwnerDisplayNames() map[string]string {
	names := map[string]string{"admin": "管理员"}
	for _, item := range a.auth.ListUsers() {
		name := util.Clean(item["name"])
		if name == "" {
			continue
		}
		if id := util.Clean(item["id"]); id != "" {
			names[id] = name
		}
		if ownerID := util.Clean(item["owner_id"]); ownerID != "" {
			names[ownerID] = name
		}
	}
	return names
}

func (a *App) runLoggedImageTask(ctx context.Context, identity service.Identity, payload map[string]any, endpoint, summary string, run func(context.Context, map[string]any) (map[string]any, error)) (map[string]any, error) {
	start := time.Now()
	payload["owner_id"] = identityScope(identity)
	payload["owner_name"] = identityDisplayName(identity)
	model := firstNonEmpty(util.Clean(payload["model"]), util.ImageModelAuto)
	requested := maxInt(1, util.ToInt(payload["n"], 1))
	quotaCount, fixedCharge := a.imageQuotaChargeForPayload(payload, requested)
	refund := func(int) {}
	if a.profiles != nil {
		if reserved, err := a.profiles.ReserveQuota(identity, quotaCount); err == nil && reserved != nil {
			refund = reserved
		} else if err != nil {
			a.logCall(identity, summary, http.MethodPost, endpoint, model, start, "failed", http.StatusTooManyRequests, err.Error(), nil)
			return map[string]any{"message": err.Error()}, err
		}
	}
	result, err := run(ctx, payload)
	urls := collectURLs(result)
	if err != nil {
		refund(quotaCount)
		a.logCall(identity, summary, http.MethodPost, endpoint, model, start, "failed", protocolErrorHTTPStatus(err), err.Error(), urls)
		return result, err
	}
	dataCount := len(util.AsMapSlice(result["data"]))
	if dataCount == 0 {
		message := firstNonEmpty(util.Clean(result["message"]), "image task returned no image data")
		refund(quotaCount)
		a.logCall(identity, summary, http.MethodPost, endpoint, model, start, "failed", http.StatusBadGateway, message, urls)
		return result, nil
	}
	if !fixedCharge && dataCount < requested {
		refund(requested - dataCount)
	}
	a.recordGeneratedImagesForPayload(identity, urls, util.Clean(payload["visibility"]), payload, result)
	a.logCall(identity, summary, http.MethodPost, endpoint, model, start, "success", http.StatusOK, "", urls)
	return result, nil
}

func (a *App) runResponsesImageGenerationTask(ctx context.Context, payload map[string]any) (map[string]any, error) {
	request, err := responseImageTaskConversationRequest(payload)
	if err != nil {
		return nil, err
	}
	outputs, errCh := a.engine.StreamImageOutputsWithPool(ctx, request)
	return a.engine.CollectImageOutputsWithLimit(outputs, errCh, request.N)
}

func responseImageTaskConversationRequest(payload map[string]any) (protocol.ConversationRequest, error) {
	body := responseImageTaskBody(payload)
	request, _, err := protocol.ResponseImageGenerationRequest(body, util.Clean(payload["owner_id"]), nil)
	if err != nil {
		return protocol.ConversationRequest{}, err
	}
	request.ResponseFormat = firstNonEmpty(util.Clean(payload["response_format"]), "url")
	request.BaseURL = util.Clean(payload["base_url"])
	request.OwnerID = util.Clean(payload["owner_id"])
	request.OwnerName = util.Clean(payload["owner_name"])
	request.MessageAsError = true
	return request.Normalized(), nil
}

func responsesImageTaskTextOutputError(result map[string]any, completed map[string]any) error {
	if len(util.AsMapSlice(result["data"])) > 0 {
		return nil
	}
	text := responseOutputText(completed["output"])
	if text == "" {
		return nil
	}
	result["message"] = text
	result["output_type"] = "text"
	return &protocol.ImageGenerationError{
		Message:    firstNonEmpty(text, "Responses image_generation returned text instead of image data."),
		StatusCode: http.StatusBadGateway,
		Type:       "server_error",
		Code:       "image_generation_text_response",
	}
}

func responseImageTaskBody(payload map[string]any) map[string]any {
	size := util.Clean(payload["size"])
	quality := util.Clean(payload["quality"])
	prompt := protocol.BuildImagePrompt(util.Clean(payload["prompt"]), size, quality)
	images := responseImageTaskDataURLs(payload["images"])
	input := any(prompt)
	if len(images) > 0 {
		content := []map[string]any{{"type": "input_text", "text": prompt}}
		for _, imageURL := range images {
			content = append(content, map[string]any{"type": "input_image", "image_url": imageURL})
		}
		input = []map[string]any{{"role": "user", "content": content}}
	}
	tool := map[string]any{
		"type":          "image_generation",
		"action":        responseImageTaskAction(images),
		"size":          protocol.ResponseImageToolSize(size),
		"output_format": service.NormalizeImageOutputFormat(util.Clean(payload["output_format"])),
	}
	if util.Clean(tool["output_format"]) != "png" {
		if compression, ok := imageOutputCompressionFromBody(payload["output_compression"]); ok {
			tool["output_compression"] = compression
		}
	}
	if quality != "" && util.Clean(payload["model"]) != util.ImageModelCodex {
		tool["quality"] = quality
	}
	body := map[string]any{
		"model":           firstNonEmpty(util.Clean(payload["model"]), util.ImageModelAuto),
		"input":           input,
		"tools":           []map[string]any{tool},
		"tool_choice":     "required",
		"n":               util.ToInt(payload["n"], 1),
		"owner_name":      util.Clean(payload["owner_name"]),
		"response_format": "b64_json",
	}
	if maxWorkers := util.ToInt(payload["max_image_workers"], 0); maxWorkers > 0 {
		body["max_image_workers"] = maxWorkers
	}
	if messages := util.AsMapSlice(payload["messages"]); len(messages) > 0 {
		body["instructions"] = responseImageTaskInstructions(messages, prompt)
	}
	return body
}

func responseImageTaskAction(images []string) string {
	if len(images) > 0 {
		return "edit"
	}
	return "generate"
}

func responseImageTaskDataURLs(raw any) []string {
	var out []string
	for _, item := range util.AsStringSlice(raw) {
		if text := util.Clean(item); strings.HasPrefix(text, "data:image/") {
			out = append(out, text)
		}
	}
	return out
}

func responseImageTaskInstructions(messages []map[string]any, prompt string) string {
	var history []string
	for index, message := range messages {
		if index == len(messages)-1 && strings.TrimSpace(util.Clean(message["content"])) == strings.TrimSpace(prompt) {
			continue
		}
		text := strings.TrimSpace(util.Clean(message["content"]))
		if text == "" {
			continue
		}
		history = append(history, firstNonEmpty(util.Clean(message["role"]), "user")+": "+text)
	}
	if len(history) == 0 {
		return ""
	}
	return "Use this conversation history only as context for image generation. Do not render the history text unless the current request explicitly asks for it.\n\n" + strings.Join(history, "\n")
}

func responsesImageTaskResult(engine *protocol.Engine, completed map[string]any, payload map[string]any) map[string]any {
	created := int64(util.ToInt(completed["created_at"], int(time.Now().Unix())))
	items := responseImageOutputItems(completed["output"])
	return engine.FormatImageResultWithOptions(items, util.Clean(payload["prompt"]), util.Clean(payload["response_format"]), util.Clean(payload["base_url"]), util.Clean(payload["owner_id"]), util.Clean(payload["owner_name"]), created, "", protocol.ImageOutputOptionsFromPayload(payload))
}

func responseImageOutputItems(output any) []map[string]any {
	var items []map[string]any
	for _, item := range util.AsMapSlice(output) {
		if util.Clean(item["type"]) != "image_generation_call" {
			continue
		}
		b64 := util.Clean(item["result"])
		if b64 == "" {
			continue
		}
		items = append(items, map[string]any{"b64_json": b64, "revised_prompt": util.Clean(item["revised_prompt"])})
	}
	return items
}

func responseOutputText(output any) string {
	var parts []string
	for _, item := range util.AsMapSlice(output) {
		if util.Clean(item["type"]) != "message" {
			continue
		}
		for _, content := range util.AsMapSlice(item["content"]) {
			if util.Clean(content["type"]) == "output_text" {
				if text := strings.TrimSpace(util.Clean(content["text"])); text != "" {
					parts = append(parts, text)
				}
			}
		}
	}
	return strings.TrimSpace(strings.Join(parts, "\n"))
}

func (a *App) runLoggedChatTask(ctx context.Context, identity service.Identity, payload map[string]any) (map[string]any, error) {
	start := time.Now()
	payload["owner_id"] = identityScope(identity)
	payload["owner_name"] = identityDisplayName(identity)
	payload["stream"] = false
	model := firstNonEmpty(util.Clean(payload["model"]), util.ImageModelAuto)
	result, stream, err := a.engine.HandleChatCompletions(ctx, payload)
	if stream != nil {
		err = errors.New("chat task streaming is not supported")
	}
	if err != nil {
		a.logCall(identity, "文本生成", http.MethodPost, "/api/creation-tasks/chat-completions", model, start, "failed", protocolErrorHTTPStatus(err), err.Error(), nil)
		return result, err
	}
	text := chatCompletionResultText(result)
	if text == "" {
		err = errors.New("模型没有返回文本内容")
		a.logCall(identity, "文本生成", http.MethodPost, "/api/creation-tasks/chat-completions", model, start, "failed", http.StatusBadGateway, err.Error(), nil)
		return result, err
	}
	a.logCall(identity, "文本生成", http.MethodPost, "/api/creation-tasks/chat-completions", model, start, "success", http.StatusOK, "", nil)
	return map[string]any{
		"created":     result["created"],
		"output_type": "text",
		"data":        []map[string]any{{"text_response": text}},
	}, nil
}

func chatCompletionResultText(result map[string]any) string {
	for _, choice := range util.AsMapSlice(result["choices"]) {
		message := util.StringMap(choice["message"])
		if text := chatCompletionContentText(message["content"]); text != "" {
			return text
		}
	}
	return ""
}

func chatCompletionContentText(content any) string {
	if text, ok := content.(string); ok {
		return strings.TrimSpace(text)
	}
	var parts []string
	for _, item := range anyList(content) {
		block := util.StringMap(item)
		if text := util.Clean(block["text"]); text != "" {
			parts = append(parts, text)
		}
	}
	return strings.TrimSpace(strings.Join(parts, "\n"))
}

func collectURLs(v any) []string {
	switch x := v.(type) {
	case map[string]any:
		var urls []string
		for key, value := range x {
			if key == "url" {
				if u := util.Clean(value); u != "" {
					urls = append(urls, u)
				}
			} else if key == "urls" {
				for _, raw := range anyList(value) {
					if u := util.Clean(raw); u != "" {
						urls = append(urls, u)
					}
				}
			} else {
				urls = append(urls, collectURLs(value)...)
			}
		}
		return urls
	case []any:
		var urls []string
		for _, item := range x {
			urls = append(urls, collectURLs(item)...)
		}
		return urls
	case []map[string]any:
		var urls []string
		for _, item := range x {
			urls = append(urls, collectURLs(item)...)
		}
		return urls
	default:
		return nil
	}
}

func dedupe(items []string) []string {
	seen := map[string]struct{}{}
	var out []string
	for _, item := range items {
		if _, ok := seen[item]; ok {
			continue
		}
		seen[item] = struct{}{}
		out = append(out, item)
	}
	return out
}

func anyList(v any) []any {
	if list, ok := v.([]any); ok {
		return list
	}
	if list, ok := v.([]map[string]any); ok {
		out := make([]any, len(list))
		for i, item := range list {
			out[i] = item
		}
		return out
	}
	return nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func (a *App) serveWeb(w http.ResponseWriter, r *http.Request) {
	if a.serveShareWebMeta(w, r) {
		return
	}
	frontend.Handler().ServeHTTP(w, r)
}

func (a *App) serveShareWebMeta(w http.ResponseWriter, r *http.Request) bool {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		return false
	}
	if r.URL.Path != "/share" || a.shares == nil {
		return false
	}
	shareID := strings.TrimSpace(r.URL.Query().Get("id"))
	if shareID == "" {
		return false
	}
	share := a.shares.Get(shareID)
	if share == nil {
		return false
	}
	indexHTML, err := frontend.IndexHTML()
	if err != nil {
		return false
	}
	body := string(indexHTML)
	title := "1818 作品分享"
	description := "1818 AI 商业图片创作台，上传参考图、输入提示词，快速生成商业视觉素材。"
	imageURL := util.Clean(share["image_url"])
	imageType := util.Clean(share["mime_type"])
	if imageType == "" {
		imageType = "image/png"
	}
	shareURL := util.Clean(share["share_url"])
	body = defaultShareImageMetaPattern.ReplaceAllString(body, "")
	meta := strings.Join([]string{
		`<title>` + html.EscapeString(title) + `</title>`,
		`<meta name="description" content="` + html.EscapeString(description) + `">`,
		`<meta property="og:type" content="website">`,
		`<meta property="og:title" content="` + html.EscapeString(title) + `">`,
		`<meta property="og:description" content="` + html.EscapeString(description) + `">`,
		`<meta property="og:url" content="` + html.EscapeString(shareURL) + `">`,
		`<meta property="og:image" content="` + html.EscapeString(imageURL) + `">`,
		`<meta property="og:image:secure_url" content="` + html.EscapeString(imageURL) + `">`,
		`<meta property="og:image:type" content="` + html.EscapeString(imageType) + `">`,
		`<meta name="twitter:card" content="summary_large_image">`,
		`<meta name="twitter:title" content="` + html.EscapeString(title) + `">`,
		`<meta name="twitter:description" content="` + html.EscapeString(description) + `">`,
		`<meta name="twitter:image" content="` + html.EscapeString(imageURL) + `">`,
		`<link rel="image_src" href="` + html.EscapeString(imageURL) + `">`,
		`<link rel="preload" as="image" href="` + html.EscapeString(imageURL) + `">`,
		`<link rel="prefetch" href="/api/image-shares?id=` + html.EscapeString(url.QueryEscape(shareID)) + `">`,
	}, "\n")
	if strings.Contains(body, "<head>") {
		body = strings.Replace(body, "<head>", "<head>\n"+meta, 1)
	} else if strings.Contains(body, "</head>") {
		body = strings.Replace(body, "</head>", meta+"\n</head>", 1)
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=60, stale-while-revalidate=300")
	if r.Method == http.MethodHead {
		return true
	}
	_, _ = io.WriteString(w, body)
	return true
}
