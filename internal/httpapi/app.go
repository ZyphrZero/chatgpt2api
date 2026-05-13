package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
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
	imageThumbnailCacheControl = "public, max-age=31536000, immutable"
	authSessionCookieName      = "chatgpt2api_session"
)

type App struct {
	config     *config.Store
	auth       *service.AuthService
	accounts   *service.AccountService
	billing    *service.BillingService
	logs       *service.LogService
	logger     *service.Logger
	proxy      *service.ProxyService
	engine     *protocol.Engine
	images     *service.ImageService
	tasks      *service.ImageTaskService
	announce   *service.AnnouncementService
	prompts    *service.PromptFavoriteService
	cpa        *service.CPAConfig
	cpaImport  *service.CPAImportService
	sub2       *service.Sub2APIConfig
	sub2Import *service.Sub2APIService
	register   *service.RegisterService
	update     *service.UpdateService
	cancel     context.CancelFunc
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
	billing := service.NewBillingService(cfg.DataDir, storageBackend, cfg)
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
	app := &App{config: cfg, auth: auth, accounts: accounts, billing: billing, logs: logs, logger: logger, proxy: proxy, engine: engine, images: service.NewImageService(cfg, storageBackend), announce: service.NewAnnouncementService(cfg.DataDir, storageBackend), prompts: service.NewPromptFavoriteService(cfg.DataDir, storageBackend), cpa: service.NewCPAConfig(cfg.DataDir, storageBackend), sub2: service.NewSub2APIConfig(cfg.DataDir, storageBackend), update: newUpdateService(cfg), cancel: cancel}
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
		cfg.UserDefaultConcurrentLimit,
		cfg.UserDefaultRPMLimit,
	)
	app.tasks.SetBillingService(billing)
	app.tasks.SetTaskTimeoutGetter(func() time.Duration {
		return time.Duration(app.config.ImageTaskTimeoutSeconds()) * time.Second
	})
	accounts.StartLimitedWatcher(ctx, time.Duration(cfg.RefreshAccountIntervalMinute())*time.Minute)
	_, _ = app.images.CleanupStorage(service.ImageStorageCleanupOptions{
		RetentionDays: cfg.ImageRetentionDays(),
		MaxBytes:      cfg.ImageStorageLimitBytes(),
	})
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
	a.writeProtocol(w, r, result, nil, err, "openai", "/v1/models", "models", identity, "模型列表", service.ImageVisibilityPrivate, service.BillingReference{})
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
	a.attachCreationTaskLimiter(body, identity)
	visibility, err := service.NormalizeImageVisibility(util.Clean(body["visibility"]))
	if err != nil {
		util.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	model := firstNonEmpty(util.Clean(body["model"]), util.ImageModelAuto)
	if err := a.checkProtocolBilling(identity, protocolBillableUnits("/v1/images/generations", body)); err != nil {
		a.writeProtocol(w, r, nil, nil, err, "openai", "/v1/images/generations", model, identity, "文生图", visibility, service.BillingReference{})
		return
	}
	billingRef := a.protocolBillingReference(identity, "/v1/images/generations", model)
	a.attachProtocolBillingCharger(body, identity, billingRef)
	result, stream, err := a.engine.HandleImageGenerations(r.Context(), body)
	a.writeProtocol(w, r, result, stream, err, "openai", "/v1/images/generations", model, identity, "文生图", visibility, billingRef, body)
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
	a.attachCreationTaskLimiter(body, identity)
	body["images"] = images
	visibility, err := service.NormalizeImageVisibility(util.Clean(body["visibility"]))
	if err != nil {
		util.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	model := firstNonEmpty(util.Clean(body["model"]), util.ImageModelAuto)
	if err := a.checkProtocolBilling(identity, protocolBillableUnits("/v1/images/edits", body)); err != nil {
		a.writeProtocol(w, r, nil, nil, err, "openai", "/v1/images/edits", model, identity, "图生图", visibility, service.BillingReference{})
		return
	}
	billingRef := a.protocolBillingReference(identity, "/v1/images/edits", model)
	a.attachProtocolBillingCharger(body, identity, billingRef)
	result, stream, err := a.engine.HandleImageEdits(r.Context(), body, images)
	a.writeProtocol(w, r, result, stream, err, "openai", "/v1/images/edits", model, identity, "图生图", visibility, billingRef, body)
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
	a.attachCreationTaskLimiter(body, identity)
	model := firstNonEmpty(util.Clean(body["model"]), "auto")
	if err := a.checkProtocolBilling(identity, protocolBillableUnits("/v1/chat/completions", body)); err != nil {
		a.writeProtocol(w, r, nil, nil, err, "openai", "/v1/chat/completions", model, identity, "文本生成", service.ImageVisibilityPrivate, service.BillingReference{})
		return
	}
	billingRef := a.protocolBillingReference(identity, "/v1/chat/completions", model)
	a.attachProtocolBillingCharger(body, identity, billingRef)
	result, stream, err := a.engine.HandleChatCompletions(r.Context(), body)
	a.writeProtocol(w, r, result, stream, err, "openai", "/v1/chat/completions", model, identity, "文本生成", service.ImageVisibilityPrivate, billingRef)
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
	a.attachCreationTaskLimiter(body, identity)
	model := firstNonEmpty(util.Clean(body["model"]), "auto")
	if err := a.checkProtocolBilling(identity, protocolBillableUnits("/v1/responses", body)); err != nil {
		a.writeProtocol(w, r, nil, nil, err, "openai", "/v1/responses", model, identity, "Responses", service.ImageVisibilityPrivate, service.BillingReference{})
		return
	}
	billingRef := a.protocolBillingReference(identity, "/v1/responses", model)
	a.attachProtocolBillingCharger(body, identity, billingRef)
	result, stream, err := a.engine.HandleResponsesScoped(r.Context(), body, identityScope(identity))
	a.writeProtocol(w, r, result, stream, err, "openai", "/v1/responses", model, identity, "Responses", service.ImageVisibilityPrivate, billingRef)
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
	a.writeProtocol(w, r, result, stream, err, "anthropic", "/v1/messages", model, identity, "Messages", service.ImageVisibilityPrivate, service.BillingReference{})
}

func (a *App) writeProtocol(w http.ResponseWriter, r *http.Request, result map[string]any, stream *protocol.StreamResult, err error, sseKind, endpoint, model string, identity service.Identity, summary, visibility string, billingRef service.BillingReference, imagePayloads ...map[string]any) {
	start := time.Now()
	requestCapture := requestAuditCapture(r.Context())
	if err != nil {
		a.logCall(identity, summary, r.Method, endpoint, model, start, "failed", protocolErrorHTTPStatus(err), err.Error(), nil, requestCapture)
		markRequestBusinessLogged(r)
		a.writeProtocolError(w, err)
		return
	}
	if stream == nil {
		urls := collectURLs(result)
		a.recordProtocolGeneratedImages(identity, urls, visibility, imagePayloads...)
		a.logCall(identity, summary, r.Method, endpoint, model, start, "success", http.StatusOK, "", urls, requestCapture)
		markRequestBusinessLogged(r)
		util.WriteJSON(w, http.StatusOK, result)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
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
			a.recordProtocolGeneratedImages(identity, urls, visibility, imagePayloads...)
			a.logCall(identity, summary, r.Method, endpoint, model, start, "failed", protocolErrorHTTPStatus(err), err.Error(), urls, requestCapture)
			markRequestBusinessLogged(r)
			fmt.Fprintf(w, "event: error\n")
			fmt.Fprintf(w, "data: %s\n\n", jsonString(map[string]any{"type": "error", "error": map[string]any{"type": fmt.Sprintf("%T", err), "message": err.Error()}}))
			return
		}
		a.recordProtocolGeneratedImages(identity, urls, visibility, imagePayloads...)
		a.logCall(identity, summary, r.Method, endpoint, model, start, "success", http.StatusOK, "", urls, requestCapture)
		markRequestBusinessLogged(r)
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
		a.recordProtocolGeneratedImages(identity, urls, visibility, imagePayloads...)
		a.logCall(identity, summary, r.Method, endpoint, model, start, "failed", protocolErrorHTTPStatus(err), err.Error(), urls, requestCapture)
		markRequestBusinessLogged(r)
		fmt.Fprintf(w, "data: %s\n\n", jsonString(openAIErrorForStream(err)))
	} else {
		a.recordProtocolGeneratedImages(identity, urls, visibility, imagePayloads...)
		a.logCall(identity, summary, r.Method, endpoint, model, start, "success", http.StatusOK, "", urls, requestCapture)
		markRequestBusinessLogged(r)
	}
	fmt.Fprint(w, "data: [DONE]\n\n")
}

func protocolErrorHTTPStatus(err error) int {
	var httpErr protocol.HTTPError
	if errors.As(err, &httpErr) {
		return httpErr.Status
	}
	var billingErr service.BillingLimitError
	if errors.As(err, &billingErr) {
		return http.StatusTooManyRequests
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
	var billingErr service.BillingLimitError
	if errors.As(err, &billingErr) {
		util.WriteJSON(w, http.StatusTooManyRequests, billingErr.OpenAIError())
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
	body, err := readJSONMap(r)
	if err != nil {
		util.WriteError(w, http.StatusBadRequest, "invalid json body")
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

func (a *App) handleSession(w http.ResponseWriter, r *http.Request) {
	identity, ok := a.requireIdentity(w, r, "")
	if !ok {
		return
	}
	if token := requestBearerToken(r); token != "" {
		setAuthSessionCookie(w, r, token)
	}
	a.writeLoginResponse(w, identity, "")
}

func (a *App) handleAccountRegister(w http.ResponseWriter, r *http.Request) {
	if !a.config.RegistrationEnabled() {
		util.WriteError(w, http.StatusForbidden, "已关闭注册通道")
		return
	}
	body, err := readJSONMap(r)
	if err != nil {
		util.WriteError(w, http.StatusBadRequest, "invalid json body")
		return
	}
	identity, token, err := a.auth.RegisterPasswordUser(util.Clean(body["username"]), util.Clean(body["password"]), util.Clean(body["name"]))
	if err != nil {
		util.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	setAuthSessionCookie(w, r, token)
	a.writeLoginResponse(w, *identity, token)
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
	payload := map[string]any{
		"ok":                        true,
		"version":                   version.Get(),
		"token":                     token,
		"role":                      identity.Role,
		"role_id":                   identity.RoleID,
		"role_name":                 identity.RoleName,
		"subject_id":                identity.ID,
		"name":                      identity.Name,
		"provider":                  identity.Provider,
		"credential_id":             identity.CredentialID,
		"credential_name":           identity.CredentialName,
		"creation_concurrent_limit": a.identityCreationConcurrentLimit(identity),
		"creation_rpm_limit":        a.identityCreationRPMLimit(identity),
		"billing":                   a.identityBillingState(identity),
		"menu_paths":                permissions.MenuPaths,
		"api_permissions":           permissions.APIPermissions,
		"menus":                     service.FilterMenuPermissions(permissions.MenuPaths),
	}
	if token == "" {
		delete(payload, "token")
	}
	util.WriteJSON(w, http.StatusOK, payload)
}

func (a *App) identityCreationConcurrentLimit(identity service.Identity) int {
	if identity.Role != service.AuthRoleUser {
		return 0
	}
	return a.config.UserDefaultConcurrentLimit()
}

func (a *App) identityCreationRPMLimit(identity service.Identity) int {
	if identity.Role != service.AuthRoleUser {
		return 0
	}
	return a.config.UserDefaultRPMLimit()
}

func (a *App) identityBillingState(identity service.Identity) map[string]any {
	if identity.Role != service.AuthRoleUser {
		return map[string]any{
			"type":         service.BillingTypeStandard,
			"unit":         service.BillingUnitImage,
			"unlimited":    true,
			"available":    0,
			"standard":     nil,
			"subscription": nil,
			"limit_state":  "unlimited",
		}
	}
	if a == nil || a.billing == nil {
		return nil
	}
	return a.billing.Get(identityScope(identity))
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
		"app_title":                   "chatgpt2api",
		"project_name":                "chatgpt2api",
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
		result, err := a.images.DeleteImages(util.AsStringSlice(body["paths"]), service.ImageAccessScope{All: true})
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
	sharePromptParams := util.ToBool(body["share_prompt_parameters"])
	shareReferences := sharePromptParams && util.ToBool(body["share_reference_images"])
	scope := service.ImageAccessScope{OwnerID: identityScope(identity)}
	if identity.Role == service.AuthRoleAdmin {
		scope = service.ImageAccessScope{All: true}
	}
	item, err := a.images.UpdateImageVisibility(path, visibility, scope, service.ImageVisibilityUpdateOptions{
		SharePromptParams: sharePromptParams,
		ShareReferences:   shareReferences,
	})
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

func (a *App) handleImageReferenceFile(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	rel, err := imageReferenceFileRequestPath(r)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	ref, err := a.images.ImageReferenceFileAccess(rel)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if ref.Visibility == service.ImageVisibilityPublic && ref.Shared {
		if ref.ContentType != "" {
			w.Header().Set("Content-Type", ref.ContentType)
		}
		http.ServeFile(w, r, ref.Path)
		return
	}
	identity, ok := a.imageRequestIdentity(w, r)
	if !ok {
		return
	}
	if identity.Role != service.AuthRoleAdmin && (ref.OwnerID == "" || ref.OwnerID != identityScope(identity)) {
		http.NotFound(w, r)
		return
	}
	if ref.ContentType != "" {
		w.Header().Set("Content-Type", ref.ContentType)
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

func imageReferenceFileRequestPath(r *http.Request) (string, error) {
	raw := strings.TrimPrefix(r.URL.EscapedPath(), "/image-references/")
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

func (a *App) handleImageStorageGovernance(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.requireIdentity(w, r, ""); !ok {
		return
	}
	switch r.Method {
	case http.MethodGet:
		util.WriteJSON(w, http.StatusOK, map[string]any{"governance": a.images.StorageGovernance()})
	case http.MethodPost:
		body, err := readJSONMap(r)
		if err != nil {
			util.WriteError(w, http.StatusBadRequest, "invalid json body")
			return
		}
		action := strings.TrimSpace(util.Clean(body["action"]))
		options := service.ImageStorageCleanupOptions{
			IncludePublic: util.ToBool(body["include_public"]),
		}
		switch action {
		case "retention":
			options.RetentionDays = util.ToInt(body["retention_days"], a.config.ImageRetentionDays())
		case "quota":
			options.MaxBytes = imageCleanupMaxBytes(body["max_bytes"], body["max_mb"], a.config.ImageStorageLimitBytes())
		case "thumbnails":
			options.ClearThumbnails = true
		case "all":
			options.RetentionDays = util.ToInt(body["retention_days"], a.config.ImageRetentionDays())
			options.MaxBytes = imageCleanupMaxBytes(body["max_bytes"], body["max_mb"], a.config.ImageStorageLimitBytes())
			options.ClearThumbnails = util.ToBool(body["clear_thumbnails"])
		default:
			util.WriteError(w, http.StatusBadRequest, "action must be retention, quota, thumbnails, or all")
			return
		}
		result, err := a.images.CleanupStorage(options)
		if err != nil {
			util.WriteError(w, http.StatusBadRequest, err.Error())
			return
		}
		util.WriteJSON(w, http.StatusOK, map[string]any{
			"cleanup":    result,
			"governance": a.images.StorageGovernance(),
		})
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func imageCleanupMaxBytes(rawBytes, rawMB any, fallback int64) int64 {
	if n := int64(util.ToInt(rawBytes, 0)); n > 0 {
		return n
	}
	if mb := util.ToInt(rawMB, 0); mb > 0 {
		return int64(mb) * 1024 * 1024
	}
	return fallback
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
		util.WriteJSON(w, http.StatusOK, map[string]any{"proxy": map[string]any{"url": a.config.Proxy()}})
	case http.MethodPost:
		body, _ := readJSONMap(r)
		url := util.Clean(body["url"])
		updated, err := a.config.Update(map[string]any{"proxy": url})
		if err != nil {
			util.WriteError(w, http.StatusBadRequest, err.Error())
			return
		}
		util.WriteJSON(w, http.StatusOK, map[string]any{"proxy": map[string]any{"url": updated["proxy"]}})
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (a *App) requireIdentity(w http.ResponseWriter, r *http.Request, overrideAuth string) (service.Identity, bool) {
	token := overrideAuthToken(overrideAuth, r)
	if identity := a.auth.Authenticate(token); identity != nil {
		if !a.identityCanAccessRequest(*identity, r) {
			util.WriteError(w, http.StatusForbidden, "permission denied")
			return service.Identity{}, false
		}
		*r = *r.WithContext(withRequestIdentity(r.Context(), *identity))
		return *identity, true
	}
	util.WriteError(w, http.StatusUnauthorized, "authorization is invalid")
	return service.Identity{}, false
}

func overrideAuthToken(overrideAuth string, r *http.Request) string {
	if overrideAuth != "" {
		return extractBearerToken(overrideAuth)
	}
	return requestAuthToken(r)
}

func requestAuthToken(r *http.Request) string {
	if token := requestBearerToken(r); token != "" {
		return token
	}
	return requestAuthCookieToken(r)
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
	token := requestAuthToken(r)
	if token == "" {
		util.WriteError(w, http.StatusUnauthorized, "authorization is invalid")
		return service.Identity{}, false
	}
	if identity := a.auth.Authenticate(token); identity != nil {
		return *identity, true
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
	default:
		return strings.HasPrefix(path, "/api/profile/api-key/") || strings.HasPrefix(path, "/api/profile/prompt-favorites/")
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
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	if forwarded := r.Header.Get("x-forwarded-proto"); forwarded != "" {
		scheme = strings.Split(forwarded, ",")[0]
	}
	host := r.Host
	if value := r.Header.Get("host"); value != "" {
		host = value
	}
	return scheme + "://" + host
}

func readJSONMap(r *http.Request) (map[string]any, error) {
	var body map[string]any
	err := util.DecodeJSON(r.Body, &body)
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
		"client_task_id":          firstForm(r.MultipartForm, "client_task_id"),
		"prompt":                  firstForm(r.MultipartForm, "prompt"),
		"model":                   firstNonEmpty(firstForm(r.MultipartForm, "model"), util.ImageModelAuto),
		"n":                       util.ToInt(firstForm(r.MultipartForm, "n"), 1),
		"size":                    firstForm(r.MultipartForm, "size"),
		"image_resolution":        firstForm(r.MultipartForm, "image_resolution"),
		"quality":                 firstForm(r.MultipartForm, "quality"),
		"background":              firstForm(r.MultipartForm, "background"),
		"moderation":              firstForm(r.MultipartForm, "moderation"),
		"style":                   firstForm(r.MultipartForm, "style"),
		"partial_images":          firstForm(r.MultipartForm, "partial_images"),
		"input_image_mask":        firstForm(r.MultipartForm, "input_image_mask"),
		"output_format":           firstForm(r.MultipartForm, "output_format"),
		"output_compression":      firstForm(r.MultipartForm, "output_compression"),
		"share_prompt_parameters": firstForm(r.MultipartForm, "share_prompt_parameters"),
		"share_reference_images":  firstForm(r.MultipartForm, "share_reference_images"),
		"visibility":              firstForm(r.MultipartForm, "visibility"),
		"response_format":         firstNonEmpty(firstForm(r.MultipartForm, "response_format"), "b64_json"),
		"stream":                  util.ToBool(firstForm(r.MultipartForm, "stream")),
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
	var billingErr service.BillingLimitError
	if errors.As(err, &billingErr) {
		return billingErr.OpenAIError()
	}
	var imageErr *protocol.ImageGenerationError
	if errors.As(err, &imageErr) {
		return imageErr.OpenAIError()
	}
	return map[string]any{"error": map[string]any{"message": err.Error(), "type": fmt.Sprintf("%T", err)}}
}

func (a *App) logCall(identity service.Identity, summary, method, endpoint, model string, started time.Time, outcome string, status int, errText string, urls []string, requestCapture auditRequestCapture) {
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
	addAuditRequestDetail(detail, requestCapture)
	suffix := "调用完成"
	if outcome == "failed" {
		suffix = "调用失败"
	}
	a.logs.Add(summary+suffix, detail)
}

func addIdentityLogDetail(detail map[string]any, identity service.Identity) {
	kind := util.Clean(identity.Kind)
	if kind != "" {
		detail["auth_kind"] = kind
	}
	credentialName := util.Clean(identity.CredentialName)
	if identity.Kind == service.AuthKindSession {
		if credentialName != "" {
			detail["session_name"] = credentialName
		}
	} else if name := util.Clean(firstNonEmpty(identity.CredentialName, identity.Name)); name != "" {
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

func payloadAuditCapture(payload map[string]any) auditRequestCapture {
	args := cleanAuditPayloadMap(payload)
	if len(args) == 0 {
		return auditRequestCapture{}
	}
	return auditRequestCapture{args: service.SanitizeLogValue(args)}
}

func cleanAuditPayloadMap(payload map[string]any) map[string]any {
	out := make(map[string]any, len(payload))
	for key, value := range payload {
		switch key {
		case "owner_id", "owner_name", "base_url":
			continue
		}
		if isInternalPayloadValue(value) {
			continue
		}
		out[key] = cleanAuditPayloadValue(value)
	}
	return out
}

func cleanAuditPayloadValue(value any) any {
	switch x := value.(type) {
	case []protocol.UploadedImage:
		items := make([]map[string]any, 0, len(x))
		for _, image := range x {
			items = append(items, map[string]any{
				"filename":     image.Filename,
				"content_type": image.ContentType,
				"size_bytes":   len(image.Data),
			})
		}
		return items
	case protocol.UploadedImage:
		return map[string]any{
			"filename":     x.Filename,
			"content_type": x.ContentType,
			"size_bytes":   len(x.Data),
		}
	default:
		return value
	}
}

func isInternalPayloadValue(value any) bool {
	if value == nil {
		return false
	}
	switch value.(type) {
	case func(context.Context, int) (func(), error), func([]map[string]any):
		return true
	default:
		return false
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
	a.cleanupImageStorage()
}

func (a *App) recordProtocolGeneratedImages(identity service.Identity, urls []string, visibility string, payloads ...map[string]any) {
	if len(payloads) > 0 && payloads[0] != nil {
		a.recordGeneratedImagesForPayload(identity, urls, visibility, payloads[0])
		return
	}
	a.recordGeneratedImages(identity, urls, visibility)
}

func (a *App) recordGeneratedImagesForPayload(identity service.Identity, urls []string, visibility string, payload map[string]any) {
	if len(urls) == 0 || a.images == nil {
		return
	}
	ownerID := identityScope(identity)
	outputCompression, hasOutputCompression := imageOutputCompressionFromBody(payload["output_compression"])
	var outputCompressionPtr *int
	if hasOutputCompression {
		outputCompressionPtr = &outputCompression
	}
	var partialImagesPtr *int
	if partialImages := util.ToInt(payload["partial_images"], 0); partialImages > 0 {
		partialImagesPtr = &partialImages
	}
	sharePromptParams := util.ToBool(payload["share_prompt_parameters"])
	a.images.RecordGeneratedImages(urls, ownerID, identityDisplayName(identity), visibility, service.GeneratedImageMetadata{
		Prompt:            util.Clean(payload["prompt"]),
		Model:             firstNonEmpty(util.Clean(payload["model"]), util.ImageModelAuto),
		Quality:           util.Clean(payload["quality"]),
		ResolutionPreset:  util.Clean(payload["image_resolution"]),
		RequestedSize:     util.Clean(payload["size"]),
		OutputFormat:      service.NormalizeImageOutputFormat(util.Clean(payload["output_format"])),
		OutputCompression: outputCompressionPtr,
		Background:        util.Clean(payload["background"]),
		Moderation:        util.Clean(payload["moderation"]),
		Style:             util.Clean(payload["style"]),
		PartialImages:     partialImagesPtr,
		InputImageMask:    util.Clean(payload["input_image_mask"]),
		ReferenceImages:   imageReferenceMetadataFromPayload(payload),
		SharePromptParams: sharePromptParams,
		ShareReferences:   sharePromptParams && util.ToBool(payload["share_reference_images"]),
	})
	a.cleanupImageStorage()
}

func (a *App) cleanupImageStorage() {
	if a == nil || a.images == nil || a.config == nil {
		return
	}
	_, _ = a.images.CleanupStorage(service.ImageStorageCleanupOptions{
		RetentionDays: a.config.ImageRetentionDays(),
		MaxBytes:      a.config.ImageStorageLimitBytes(),
	})
}

func imageReferenceMetadataFromPayload(payload map[string]any) []service.GeneratedImageReference {
	if payload == nil {
		return nil
	}
	images := uploadedImagesFromPayload(payload["images"])
	if len(images) == 0 {
		images = protocol.ExtractChatContextImages(payload)
	}
	if len(images) == 0 {
		return nil
	}
	refs := make([]service.GeneratedImageReference, 0, len(images))
	for _, image := range images {
		if len(image.Data) == 0 {
			continue
		}
		refs = append(refs, service.GeneratedImageReference{
			Filename:    image.Filename,
			ContentType: image.ContentType,
			Data:        append([]byte(nil), image.Data...),
		})
	}
	return refs
}

func uploadedImagesFromPayload(value any) []protocol.UploadedImage {
	switch images := value.(type) {
	case []protocol.UploadedImage:
		return images
	case protocol.UploadedImage:
		return []protocol.UploadedImage{images}
	default:
		return nil
	}
}

func (a *App) checkProtocolBilling(identity service.Identity, amount int) error {
	if amount <= 0 || a == nil || a.billing == nil {
		return nil
	}
	return a.billing.CheckAvailable(identity, amount)
}

func (a *App) protocolBillingReference(identity service.Identity, endpoint, model string) service.BillingReference {
	return service.BillingReference{
		Endpoint:       endpoint,
		Model:          model,
		RequestID:      "req_" + util.NewHex(18),
		CredentialID:   identity.CredentialID,
		CredentialName: identity.CredentialName,
	}
}

func (a *App) chargeProtocolBilling(identity service.Identity, consumed int, ref service.BillingReference) error {
	if a == nil || a.billing == nil || consumed <= 0 {
		return nil
	}
	return a.billing.Charge(identity, consumed, ref)
}

// attachProtocolBillingCharger sets the per-image-output inline charge hook on
// the request body. The hook atomically deducts 1 billing unit before each
// image is persisted to disk, preventing gallery writes when balance/quota is
// insufficient. The chargeIndex counter ensures unique charge keys per output.
func (a *App) attachProtocolBillingCharger(body map[string]any, identity service.Identity, billingRef service.BillingReference) {
	if a == nil || a.billing == nil || body == nil {
		return
	}
	if identity.Role != service.AuthRoleUser {
		return
	}
	var mu sync.Mutex
	chargeIndex := 0
	body[protocol.ImageOutputChargePayloadKey] = func(index int) error {
		mu.Lock()
		idx := chargeIndex
		chargeIndex++
		mu.Unlock()
		ref := protocolChargeReference(billingRef, "inline", idx)
		return a.billing.Charge(identity, 1, ref)
	}
}

func protocolChargeReference(ref service.BillingReference, scope string, index int) service.BillingReference {
	if strings.TrimSpace(ref.ChargeKey) == "" && ref.Endpoint != "" {
		keyID := firstNonEmpty(ref.RequestID, ref.TaskID, util.NewHex(12))
		ref.ChargeKey = strings.Join([]string{"protocol", ref.Endpoint, keyID, scope, fmt.Sprint(index)}, ":")
	}
	ref.OutputIndex = index
	return ref
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
	requestCapture := payloadAuditCapture(payload)
	payload["owner_id"] = identityScope(identity)
	payload["owner_name"] = identityDisplayName(identity)
	model := firstNonEmpty(util.Clean(payload["model"]), util.ImageModelAuto)
	result, err := run(ctx, payload)
	urls := collectURLs(result)
	a.recordGeneratedImagesForPayload(identity, urls, util.Clean(payload["visibility"]), payload)
	if err != nil {
		a.logCall(identity, summary, http.MethodPost, endpoint, model, start, "failed", protocolErrorHTTPStatus(err), err.Error(), urls, requestCapture)
		return result, err
	}
	if len(util.AsMapSlice(result["data"])) == 0 {
		message := firstNonEmpty(util.Clean(result["message"]), "image task returned no image data")
		a.logCall(identity, summary, http.MethodPost, endpoint, model, start, "failed", http.StatusBadGateway, message, urls, requestCapture)
		return result, nil
	}
	a.logCall(identity, summary, http.MethodPost, endpoint, model, start, "success", http.StatusOK, "", urls, requestCapture)
	return result, nil
}

func (a *App) attachCreationTaskLimiter(body map[string]any, identity service.Identity) {
	if a == nil || a.tasks == nil || body == nil {
		return
	}
	body[protocol.ImageOutputSlotAcquirerPayloadKey] = func(ctx context.Context, index int) (func(), error) {
		return a.tasks.AcquireCreationUnit(ctx, identity)
	}
}

func (a *App) runLoggedChatTask(ctx context.Context, identity service.Identity, payload map[string]any) (map[string]any, error) {
	start := time.Now()
	requestCapture := payloadAuditCapture(payload)
	payload["owner_id"] = identityScope(identity)
	payload["owner_name"] = identityDisplayName(identity)
	payload["stream"] = false
	model := firstNonEmpty(util.Clean(payload["model"]), util.ImageModelAuto)
	result, stream, err := a.engine.HandleChatCompletions(ctx, payload)
	if stream != nil {
		err = errors.New("chat task streaming is not supported")
	}
	if err != nil {
		a.logCall(identity, "文本生成", http.MethodPost, "/api/creation-tasks/chat-completions", model, start, "failed", protocolErrorHTTPStatus(err), err.Error(), nil, requestCapture)
		return result, err
	}
	text := chatCompletionResultText(result)
	if text == "" {
		err = errors.New("模型没有返回文本内容")
		a.logCall(identity, "文本生成", http.MethodPost, "/api/creation-tasks/chat-completions", model, start, "failed", http.StatusBadGateway, err.Error(), nil, requestCapture)
		return result, err
	}
	a.logCall(identity, "文本生成", http.MethodPost, "/api/creation-tasks/chat-completions", model, start, "success", http.StatusOK, "", nil, requestCapture)
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

func protocolBillableUnits(endpoint string, body map[string]any) int {
	switch endpoint {
	case "/v1/images/generations", "/v1/images/edits":
		return normalizedProtocolImageCount(body["n"])
	case "/v1/chat/completions":
		if protocol.IsImageChatRequest(body) {
			return normalizedProtocolImageCount(body["n"])
		}
	case "/v1/responses":
		if protocol.HasResponseImageGenerationTool(body) {
			return normalizedProtocolImageCount(body["n"])
		}
	}
	return 0
}

func normalizedProtocolImageCount(value any) int {
	n := util.ToInt(value, 1)
	if n < 1 {
		return 1
	}
	if n > 4 {
		return 4
	}
	return n
}

func billableProtocolOutputCount(endpoint string, result map[string]any) int {
	if len(result) == 0 {
		return 0
	}
	switch endpoint {
	case "/v1/images/generations", "/v1/images/edits":
		return billableImageDataCount(result["data"])
	case "/v1/chat/completions":
		return countChatCompletionImages(result)
	case "/v1/responses":
		return countResponseOutputImages(result)
	default:
		return billableURLCount(collectURLs(result))
	}
}

func billableProtocolStreamItemCount(endpoint string, item map[string]any) int {
	if len(item) == 0 {
		return 0
	}
	switch endpoint {
	case "/v1/images/generations", "/v1/images/edits":
		if util.Clean(item["object"]) == "image.generation.result" {
			return billableImageDataCount(item["data"])
		}
	case "/v1/chat/completions":
		for _, choice := range util.AsMapSlice(item["choices"]) {
			delta := util.StringMap(choice["delta"])
			if len(delta) == 0 {
				delta = util.StringMap(choice["message"])
			}
			if count := countImagesInChatContent(delta["content"]); count > 0 {
				return count
			}
		}
	case "/v1/responses":
		eventType := util.Clean(item["type"])
		switch eventType {
		case "response.output_item.done", "response.output_item.added":
			if count := countResponseOutputItemImages(util.StringMap(item["item"])); count > 0 {
				return count
			}
		}
	}
	return 0
}

func billableImageDataCount(value any) int {
	count := 0
	for _, item := range util.AsMapSlice(value) {
		if util.Clean(item["url"]) != "" || util.Clean(item["b64_json"]) != "" {
			count++
		}
	}
	return count
}

func countChatCompletionImages(result map[string]any) int {
	count := 0
	for _, choice := range util.AsMapSlice(result["choices"]) {
		message := util.StringMap(choice["message"])
		count += countImagesInChatContent(message["content"])
	}
	return count
}

func countImagesInChatContent(content any) int {
	switch value := content.(type) {
	case string:
		return strings.Count(value, "![")
	case []any:
		count := 0
		for _, raw := range value {
			item := util.StringMap(raw)
			if util.Clean(item["type"]) == "image_url" || util.Clean(item["image_url"]) != "" {
				count++
			}
			if util.Clean(item["type"]) == "text" {
				count += strings.Count(util.Clean(item["text"]), "![")
			}
		}
		return count
	default:
		return 0
	}
}

func countResponseOutputImages(result map[string]any) int {
	count := 0
	for _, item := range util.AsMapSlice(result["output"]) {
		count += countResponseOutputItemImages(item)
	}
	return count
}

func countResponseOutputItemImages(item map[string]any) int {
	if util.Clean(item["type"]) == "image_generation_call" && util.Clean(item["result"]) != "" {
		return 1
	}
	return 0
}

func billableURLCount(urls []string) int {
	return len(dedupe(urls))
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

func (a *App) serveWeb(w http.ResponseWriter, r *http.Request) {
	frontend.Handler().ServeHTTP(w, r)
}
