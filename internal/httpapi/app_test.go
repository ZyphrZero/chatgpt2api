package httpapi

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"image"
	"image/color"
	"image/png"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"chatgpt2api/internal/backend"
	"chatgpt2api/internal/protocol"
	"chatgpt2api/internal/service"
	"chatgpt2api/internal/util"
	"chatgpt2api/internal/version"
)

func TestAppAuthAndSPACompatibility(t *testing.T) {
	originalVersion := version.Version
	version.Version = "test-build"
	t.Cleanup(func() { version.Version = originalVersion })

	app := newTestApp(t)
	defer app.Close()

	user, rawKey, err := app.auth.CreateAPIKey(service.AuthRoleUser, "frontend", service.AuthOwner{})
	if err != nil {
		t.Fatalf("CreateAPIKey() error = %v", err)
	}
	if user["role"] != "user" {
		t.Fatalf("created user = %#v", user)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/auth/users/"+user["id"].(string)+"/key", nil)
	req.Header.Set("Authorization", adminAuthHeader(t, app))
	res := httptest.NewRecorder()
	app.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("reveal user key status = %d body = %s", res.Code, res.Body.String())
	}
	var revealed map[string]any
	if err := json.Unmarshal(res.Body.Bytes(), &revealed); err != nil {
		t.Fatalf("reveal json: %v", err)
	}
	if revealed["key"] != rawKey {
		t.Fatalf("revealed key = %#v, want raw key", revealed["key"])
	}

	req = httptest.NewRequest(http.MethodGet, "/auth/session", nil)
	req.Header.Set("Authorization", "Bearer "+rawKey)
	res = httptest.NewRecorder()
	app.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("/auth/session status = %d body = %s", res.Code, res.Body.String())
	}
	var login map[string]any
	if err := json.Unmarshal(res.Body.Bytes(), &login); err != nil {
		t.Fatalf("login json: %v", err)
	}
	if login["role"] != "user" {
		t.Fatalf("login role = %#v", login)
	}
	if login["version"] != "test-build" {
		t.Fatalf("login version = %#v", login["version"])
	}

	req = httptest.NewRequest(http.MethodGet, "/version", nil)
	res = httptest.NewRecorder()
	app.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("/version status = %d body = %s", res.Code, res.Body.String())
	}
	var versionBody map[string]any
	if err := json.Unmarshal(res.Body.Bytes(), &versionBody); err != nil {
		t.Fatalf("version json: %v", err)
	}
	if versionBody["version"] != "test-build" {
		t.Fatalf("/version body = %#v", versionBody)
	}

	req = httptest.NewRequest(http.MethodGet, "/health", nil)
	res = httptest.NewRecorder()
	app.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("/health status = %d body = %s", res.Code, res.Body.String())
	}

	req = httptest.NewRequest(http.MethodGet, "/api/announcements?target=login", nil)
	res = httptest.NewRecorder()
	app.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("/api/announcements status = %d body = %s", res.Code, res.Body.String())
	}
	var announcementsBody map[string]any
	if err := json.Unmarshal(res.Body.Bytes(), &announcementsBody); err != nil {
		t.Fatalf("announcements json: %v", err)
	}
	if items := logItems(announcementsBody); len(items) != 0 {
		t.Fatalf("unexpected initial announcements = %#v", announcementsBody)
	}

	req = httptest.NewRequest(http.MethodPost, "/api/admin/announcements", strings.NewReader(`{"title":"通知 A","content":"今晚维护","show_login":true,"show_image":false}`))
	req.Header.Set("Authorization", adminAuthHeader(t, app))
	res = httptest.NewRecorder()
	app.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("create login announcement status = %d body = %s", res.Code, res.Body.String())
	}
	var createBody map[string]any
	if err := json.Unmarshal(res.Body.Bytes(), &createBody); err != nil {
		t.Fatalf("create announcement json: %v", err)
	}
	createdItem, _ := createBody["item"].(map[string]any)
	createdID, _ := createdItem["id"].(string)
	if createdID == "" {
		t.Fatalf("missing created announcement id: %#v", createBody)
	}

	req = httptest.NewRequest(http.MethodPost, "/api/admin/announcements", strings.NewReader(`{"title":"通知 B","content":"画图页公告","show_login":false,"show_image":true}`))
	req.Header.Set("Authorization", adminAuthHeader(t, app))
	res = httptest.NewRecorder()
	app.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("create image announcement status = %d body = %s", res.Code, res.Body.String())
	}

	req = httptest.NewRequest(http.MethodGet, "/api/admin/announcements", nil)
	req.Header.Set("Authorization", adminAuthHeader(t, app))
	res = httptest.NewRecorder()
	app.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("admin list announcements status = %d body = %s", res.Code, res.Body.String())
	}
	if err := json.Unmarshal(res.Body.Bytes(), &announcementsBody); err != nil {
		t.Fatalf("admin announcements json: %v", err)
	}
	if items := logItems(announcementsBody); len(items) != 2 {
		t.Fatalf("admin announcements length = %d body = %#v", len(items), announcementsBody)
	}

	req = httptest.NewRequest(http.MethodGet, "/api/announcements?target=login", nil)
	res = httptest.NewRecorder()
	app.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("public login announcements status = %d body = %s", res.Code, res.Body.String())
	}
	if err := json.Unmarshal(res.Body.Bytes(), &announcementsBody); err != nil {
		t.Fatalf("public login announcements json: %v", err)
	}
	items := logItems(announcementsBody)
	if len(items) != 1 || items[0]["title"] != "通知 A" {
		t.Fatalf("unexpected public login announcements = %#v", announcementsBody)
	}

	req = httptest.NewRequest(http.MethodPost, "/api/admin/announcements/"+createdID, strings.NewReader(`{"enabled":false}`))
	req.Header.Set("Authorization", adminAuthHeader(t, app))
	res = httptest.NewRecorder()
	app.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("disable announcement status = %d body = %s", res.Code, res.Body.String())
	}
	req = httptest.NewRequest(http.MethodGet, "/api/announcements?target=login", nil)
	res = httptest.NewRecorder()
	app.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("public login announcements after disable status = %d body = %s", res.Code, res.Body.String())
	}
	if err := json.Unmarshal(res.Body.Bytes(), &announcementsBody); err != nil {
		t.Fatalf("public login announcements after disable json: %v", err)
	}
	if items := logItems(announcementsBody); len(items) != 0 {
		t.Fatalf("disabled announcement should be hidden: %#v", announcementsBody)
	}

	msgReq := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader("{"))
	msgReq.Header.Set("x-api-key", rawKey)
	msgRes := httptest.NewRecorder()
	app.Handler().ServeHTTP(msgRes, msgReq)
	if msgRes.Code != http.StatusBadRequest {
		t.Fatalf("x-api-key auth did not reach JSON validation, status = %d body = %s", msgRes.Code, msgRes.Body.String())
	}

	for _, path := range []string{"/", "/settings"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		res := httptest.NewRecorder()
		app.Handler().ServeHTTP(res, req)
		if res.Code != http.StatusOK || !isSPAHTML(res.Body.String()) {
			t.Fatalf("%s status/body = %d %q", path, res.Code, res.Body.String())
		}
	}
	req = httptest.NewRequest(http.MethodGet, "/assets/missing.js", nil)
	res = httptest.NewRecorder()
	app.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusNotFound {
		t.Fatalf("missing asset status = %d", res.Code)
	}
}

func TestImageShareResponsesUseCacheHeadersAndPreload(t *testing.T) {
	app := newTestApp(t)
	defer app.Close()

	var imageBuf bytes.Buffer
	img := image.NewRGBA(image.Rect(0, 0, 2, 2))
	img.Set(0, 0, color.RGBA{R: 255, A: 255})
	if err := png.Encode(&imageBuf, img); err != nil {
		t.Fatalf("encode png: %v", err)
	}
	payload := map[string]any{
		"image":  "data:image/png;base64," + base64.StdEncoding.EncodeToString(imageBuf.Bytes()),
		"prompt": "share speed",
	}
	body, _ := json.Marshal(payload)
	req := httptest.NewRequest(http.MethodPost, "/api/image-shares", bytes.NewReader(body))
	req.Header.Set("Authorization", adminAuthHeader(t, app))
	res := httptest.NewRecorder()
	app.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("create share status = %d body = %s", res.Code, res.Body.String())
	}
	var share map[string]any
	if err := json.Unmarshal(res.Body.Bytes(), &share); err != nil {
		t.Fatalf("share json: %v", err)
	}
	id := util.Clean(share["id"])
	imageURL := util.Clean(share["image_url"])
	if id == "" || imageURL == "" {
		t.Fatalf("share response missing id/image_url: %#v", share)
	}

	req = httptest.NewRequest(http.MethodGet, "/api/image-shares?id="+url.QueryEscape(id), nil)
	res = httptest.NewRecorder()
	app.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("get share status = %d body = %s", res.Code, res.Body.String())
	}
	if got := res.Header().Get("Cache-Control"); got != imageShareAPICacheControl {
		t.Fatalf("share api Cache-Control = %q, want %q", got, imageShareAPICacheControl)
	}

	req = httptest.NewRequest(http.MethodGet, imageURL, nil)
	res = httptest.NewRecorder()
	app.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("share image status = %d body = %s", res.Code, res.Body.String())
	}
	if got := res.Header().Get("Cache-Control"); got != imageShareCacheControl {
		t.Fatalf("share image Cache-Control = %q, want %q", got, imageShareCacheControl)
	}

	req = httptest.NewRequest(http.MethodGet, "/share?id="+url.QueryEscape(id), nil)
	res = httptest.NewRecorder()
	app.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("share page status = %d body = %s", res.Code, res.Body.String())
	}
	if got := res.Header().Get("Cache-Control"); got == "private, no-store" || got == "" {
		t.Fatalf("share page Cache-Control = %q, want public short cache", got)
	}
	htmlBody := res.Body.String()
	if !strings.Contains(htmlBody, `rel="preload" as="image"`) || !strings.Contains(htmlBody, imageURL) {
		t.Fatalf("share page missing image preload for %q", imageURL)
	}
}

func TestImageShareCreationRequiresImageOwnership(t *testing.T) {
	app := newTestApp(t)
	defer app.Close()

	aliceOwner := service.AuthOwner{ID: "linuxdo:alice", Name: "Alice", Provider: service.AuthProviderLinuxDo}
	bobOwner := service.AuthOwner{ID: "linuxdo:bob", Name: "Bob", Provider: service.AuthProviderLinuxDo}
	_, aliceKey, err := app.auth.CreateAPIKey(service.AuthRoleUser, "alice", aliceOwner)
	if err != nil {
		t.Fatalf("CreateAPIKey(alice) error = %v", err)
	}
	_, bobKey, err := app.auth.CreateAPIKey(service.AuthRoleUser, "bob", bobOwner)
	if err != nil {
		t.Fatalf("CreateAPIKey(bob) error = %v", err)
	}
	aliceRel := "2026/05/18/share-alice.png"
	bobRel := "2026/05/18/share-bob.png"
	for _, rel := range []string{aliceRel, bobRel} {
		imagePath := filepath.Join(app.config.ImagesDir(), filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(imagePath), 0o755); err != nil {
			t.Fatalf("mkdir image dir for %s: %v", rel, err)
		}
		if err := writeHTTPTestPNG(imagePath); err != nil {
			t.Fatalf("write image %s: %v", rel, err)
		}
	}
	app.images.RecordGeneratedImages([]string{aliceRel}, aliceOwner.ID, aliceOwner.Name, service.ImageVisibilityPrivate)
	app.images.RecordGeneratedImages([]string{bobRel}, bobOwner.ID, bobOwner.Name, service.ImageVisibilityPrivate)

	body, _ := json.Marshal(map[string]any{"image": "/images/" + bobRel, "prompt": "borrowed private image"})
	req := httptest.NewRequest(http.MethodPost, "/api/image-shares", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+aliceKey)
	res := httptest.NewRecorder()
	app.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusNotFound {
		t.Fatalf("alice sharing bob private image status = %d body = %s", res.Code, res.Body.String())
	}

	body, _ = json.Marshal(map[string]any{"image": "/images/" + aliceRel, "prompt": "own private image"})
	req = httptest.NewRequest(http.MethodPost, "/api/image-shares", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+aliceKey)
	res = httptest.NewRecorder()
	app.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("alice sharing own private image status = %d body = %s", res.Code, res.Body.String())
	}

	body, _ = json.Marshal(map[string]any{"image": "/images/" + bobRel, "prompt": "bob own private image"})
	req = httptest.NewRequest(http.MethodPost, "/api/image-shares", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+bobKey)
	res = httptest.NewRecorder()
	app.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("bob sharing own private image status = %d body = %s", res.Code, res.Body.String())
	}
}

func TestImageQuotaChargeForPayloadDetectsResolutionKeywords(t *testing.T) {
	app := newTestApp(t)
	defer app.Close()

	tests := []struct {
		name       string
		payload    map[string]any
		requested  int
		wantQuota  int
		wantFixed  bool
		wantPreset string
	}{
		{name: "normal", payload: map[string]any{"prompt": "生成海报"}, requested: 1, wantQuota: 1, wantFixed: false},
		{name: "prompt 2k", payload: map[string]any{"prompt": "生成 2K 高清海报"}, requested: 1, wantQuota: 2, wantFixed: true, wantPreset: "2k"},
		{name: "prompt 4k", payload: map[string]any{"prompt": "生成 4k 超清商品图"}, requested: 1, wantQuota: 5, wantFixed: true, wantPreset: "4k"},
		{name: "size 8k", payload: map[string]any{"prompt": "生成商品图", "size": "8K"}, requested: 2, wantQuota: 10, wantFixed: true, wantPreset: "4k"},
		{name: "explicit resolution", payload: map[string]any{"prompt": "生成商品图", "image_resolution": "4k"}, requested: 3, wantQuota: 15, wantFixed: true, wantPreset: "4k"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotQuota, gotFixed := app.imageQuotaChargeForPayload(tt.payload, tt.requested)
			if gotQuota != tt.wantQuota || gotFixed != tt.wantFixed {
				t.Fatalf("imageQuotaChargeForPayload() = (%d, %v), want (%d, %v)", gotQuota, gotFixed, tt.wantQuota, tt.wantFixed)
			}
			if gotPreset := imageResolutionPresetForQuota(tt.payload); gotPreset != tt.wantPreset {
				t.Fatalf("imageResolutionPresetForQuota() = %q, want %q", gotPreset, tt.wantPreset)
			}
		})
	}
}

func TestWriteProtocolFixedChargeDoesNotRefundResolutionQuota(t *testing.T) {
	app := newTestApp(t)
	defer app.Close()

	_, rawKey, err := app.auth.CreateAPIKey(service.AuthRoleUser, "quota user", service.AuthOwner{})
	if err != nil {
		t.Fatalf("CreateAPIKey() error = %v", err)
	}
	identity := app.auth.Authenticate(rawKey)
	if identity == nil {
		t.Fatal("Authenticate() returned nil")
	}
	if _, err := app.profiles.AdjustUserQuota(identityScope(*identity), 5); err != nil {
		t.Fatalf("AdjustUserQuota() error = %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/v1/images/generations", nil)
	res := httptest.NewRecorder()
	result := map[string]any{"data": []map[string]any{{"url": "https://example.test/generated.png"}}}
	app.writeProtocol(res, req, result, nil, nil, "openai", "/v1/images/generations", util.ImageModelAuto, *identity, "文生图", service.ImageVisibilityPrivate, 5, true)
	if res.Code != http.StatusOK {
		t.Fatalf("writeProtocol status = %d body = %s", res.Code, res.Body.String())
	}
	profile := app.profiles.PublicForUser(identityScope(*identity))
	if used := util.ToInt(profile["image_quota_used"], 0); used != 5 {
		t.Fatalf("image_quota_used = %d, want 5 in %#v", used, profile)
	}
}

func TestAdminSystemCheckUpdates(t *testing.T) {
	releaseAPI := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repos/ZyphrZero/chatgpt2api/releases/latest" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"tag_name": "v1.2.0",
			"name": "v1.2.0",
			"body": "release notes",
			"html_url": "https://github.com/ZyphrZero/chatgpt2api/releases/tag/v1.2.0",
			"published_at": "2026-01-01T00:00:00Z",
			"assets": [
				{"name":"chatgpt2api_1.2.0_linux_amd64.tar.gz","browser_download_url":"https://github.com/ZyphrZero/chatgpt2api/releases/download/v1.2.0/chatgpt2api_1.2.0_linux_amd64.tar.gz","size":123},
				{"name":"checksums.txt","browser_download_url":"https://github.com/ZyphrZero/chatgpt2api/releases/download/v1.2.0/checksums.txt","size":64}
			]
		}`))
	}))
	defer releaseAPI.Close()

	originalVersion := version.Version
	originalBuildType := version.BuildType
	version.Version = "1.1.0"
	version.BuildType = "release"
	t.Cleanup(func() {
		version.Version = originalVersion
		version.BuildType = originalBuildType
	})

	app := newTestApp(t)
	defer app.Close()
	app.update = service.NewUpdateService(service.UpdateOptions{
		APIBaseURL:     releaseAPI.URL,
		CurrentVersion: version.Get(),
		BuildType:      version.GetBuildType(),
	})

	req := httptest.NewRequest(http.MethodGet, "/api/admin/system/check-updates?force=true", nil)
	req.Header.Set("Authorization", adminAuthHeader(t, app))
	res := httptest.NewRecorder()
	app.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("check updates status = %d body = %s", res.Code, res.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(res.Body.Bytes(), &body); err != nil {
		t.Fatalf("check updates json: %v", err)
	}
	if body["current_version"] != "1.1.0" || body["latest_version"] != "1.2.0" || body["has_update"] != true || body["build_type"] != "release" {
		t.Fatalf("unexpected check updates body = %#v", body)
	}
}

func TestPasswordAccountLoginAndRegistrationToggle(t *testing.T) {
	app := newTestApp(t)
	defer app.Close()

	req := httptest.NewRequest(http.MethodPost, "/auth/login", strings.NewReader(`{"username":"admin","password":"AdminPass123!"}`))
	res := httptest.NewRecorder()
	app.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("admin password login status = %d body = %s", res.Code, res.Body.String())
	}
	var login map[string]any
	if err := json.Unmarshal(res.Body.Bytes(), &login); err != nil {
		t.Fatalf("login json: %v", err)
	}
	adminToken, _ := login["token"].(string)
	if adminToken == "" || login["role"] != service.AuthRoleAdmin || login["subject_id"] != "admin" {
		t.Fatalf("admin login body = %#v", login)
	}

	req = httptest.NewRequest(http.MethodPost, "/auth/register", strings.NewReader(`{"username":"alice","password":"Password123","name":"Alice"}`))
	res = httptest.NewRecorder()
	app.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusForbidden {
		t.Fatalf("disabled registration status = %d body = %s", res.Code, res.Body.String())
	}

	req = httptest.NewRequest(http.MethodPost, "/api/settings", strings.NewReader(`{"registration_enabled":true}`))
	req.Header.Set("Authorization", "Bearer "+adminToken)
	res = httptest.NewRecorder()
	app.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("enable registration status = %d body = %s", res.Code, res.Body.String())
	}

	req = httptest.NewRequest(http.MethodPost, "/auth/register", strings.NewReader(`{"username":"alice","password":"Password123","name":"Alice"}`))
	res = httptest.NewRecorder()
	app.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("enabled registration status = %d body = %s", res.Code, res.Body.String())
	}
	var registered map[string]any
	if err := json.Unmarshal(res.Body.Bytes(), &registered); err != nil {
		t.Fatalf("register json: %v", err)
	}
	userToken, _ := registered["token"].(string)
	if userToken == "" || registered["role"] != service.AuthRoleUser || registered["name"] != "Alice" {
		t.Fatalf("register body = %#v", registered)
	}
	if registered["role_id"] != service.DefaultManagedRoleID {
		t.Fatalf("registered role fields = %#v", registered)
	}

	req = httptest.NewRequest(http.MethodGet, "/auth/session", nil)
	req.Header.Set("Authorization", "Bearer "+userToken)
	res = httptest.NewRecorder()
	app.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("registered session status = %d body = %s", res.Code, res.Body.String())
	}
}

func TestSubscriptionPaymentNotifyCreditsUserQuota(t *testing.T) {
	app := newTestApp(t)
	defer app.Close()

	req := httptest.NewRequest(http.MethodPost, "/auth/login", strings.NewReader(`{"username":"admin","password":"AdminPass123!"}`))
	res := httptest.NewRecorder()
	app.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("admin login status = %d body = %s", res.Code, res.Body.String())
	}
	var adminLogin map[string]any
	if err := json.Unmarshal(res.Body.Bytes(), &adminLogin); err != nil {
		t.Fatalf("admin login json: %v", err)
	}
	adminToken, _ := adminLogin["token"].(string)

	req = httptest.NewRequest(http.MethodPost, "/api/settings", strings.NewReader(`{"registration_enabled":true}`))
	req.Header.Set("Authorization", "Bearer "+adminToken)
	res = httptest.NewRecorder()
	app.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("enable registration status = %d body = %s", res.Code, res.Body.String())
	}

	req = httptest.NewRequest(http.MethodPost, "/auth/register", strings.NewReader(`{"username":"buyer","password":"Password123","name":"Buyer"}`))
	res = httptest.NewRecorder()
	app.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("register buyer status = %d body = %s", res.Code, res.Body.String())
	}
	var buyerLogin map[string]any
	if err := json.Unmarshal(res.Body.Bytes(), &buyerLogin); err != nil {
		t.Fatalf("buyer login json: %v", err)
	}
	buyerToken, _ := buyerLogin["token"].(string)
	buyerID, _ := buyerLogin["subject_id"].(string)
	if buyerToken == "" || buyerID == "" {
		t.Fatalf("buyer login body = %#v", buyerLogin)
	}

	req = httptest.NewRequest(http.MethodPost, "/api/admin/subscription", strings.NewReader(`{
		"enabled": true,
		"payment": {
			"enabled": true,
			"gateway_url": "https://pay.example.com",
			"merchant_id": "1000",
			"merchant_key": "secret",
			"pay_types": ["alipay"],
			"site_name": "1818.pro"
		},
		"plans": [{"id":"creator","name":"创作版","price":"69.00","quota":1500,"enabled":true}]
	}`))
	req.Header.Set("Authorization", "Bearer "+adminToken)
	res = httptest.NewRecorder()
	app.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("subscription config status = %d body = %s", res.Code, res.Body.String())
	}

	req = httptest.NewRequest(http.MethodPost, "/api/subscription/checkout", strings.NewReader(`{"plan_id":"creator","pay_type":"alipay"}`))
	req.Header.Set("Authorization", "Bearer "+buyerToken)
	res = httptest.NewRecorder()
	app.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("checkout status = %d body = %s", res.Code, res.Body.String())
	}
	var checkout map[string]any
	if err := json.Unmarshal(res.Body.Bytes(), &checkout); err != nil {
		t.Fatalf("checkout json: %v", err)
	}
	order, _ := checkout["order"].(map[string]any)
	orderID := util.Clean(order["id"])
	if orderID == "" {
		t.Fatalf("checkout order = %#v", checkout)
	}
	checkoutFields := util.StringMap(checkout["fields"])
	replayValues := url.Values{}
	for key, value := range checkoutFields {
		replayValues.Set(key, util.Clean(value))
	}
	req = httptest.NewRequest(http.MethodPost, "/api/subscription/payment/notify", strings.NewReader(replayValues.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	res = httptest.NewRecorder()
	app.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusOK || strings.TrimSpace(res.Body.String()) != "fail" {
		t.Fatalf("checkout-field replay notify status = %d body = %s", res.Code, res.Body.String())
	}
	profile := app.profiles.PublicForUser(buyerID)
	if util.ToInt(profile["image_quota_total"], 0) != 10 {
		t.Fatalf("quota after checkout-field replay = %#v", profile)
	}

	notify := map[string]string{
		"pid":          "1000",
		"out_trade_no": orderID,
		"trade_no":     "remote-trade-1",
		"trade_status": "TRADE_SUCCESS",
		"money":        "69.00",
	}
	notify["sign"] = service.EPayMD5Sign(notify, "secret")
	notifyValues := url.Values{}
	for key, value := range notify {
		notifyValues.Set(key, value)
	}
	notifyValues.Set("sign_type", "MD5")
	req = httptest.NewRequest(http.MethodPost, "/api/subscription/payment/notify", strings.NewReader(notifyValues.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	res = httptest.NewRecorder()
	app.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusOK || strings.TrimSpace(res.Body.String()) != "success" {
		t.Fatalf("notify status = %d body = %s", res.Code, res.Body.String())
	}

	profile = app.profiles.PublicForUser(buyerID)
	if util.ToInt(profile["image_quota_total"], 0) != 1510 {
		t.Fatalf("quota after notify = %#v", profile)
	}

	req = httptest.NewRequest(http.MethodPost, "/api/subscription/payment/notify", strings.NewReader(notifyValues.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	res = httptest.NewRecorder()
	app.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusOK || strings.TrimSpace(res.Body.String()) != "success" {
		t.Fatalf("second notify status = %d body = %s", res.Code, res.Body.String())
	}
	profile = app.profiles.PublicForUser(buyerID)
	if util.ToInt(profile["image_quota_total"], 0) != 1510 {
		t.Fatalf("quota after duplicate notify = %#v", profile)
	}
}

func TestAdminUserQuotaRouteEmitsUserNotification(t *testing.T) {
	app := newTestApp(t)
	defer app.Close()

	user, err := app.auth.CreatePasswordUser("quota_user", "Password123", "Quota User", "", true)
	if err != nil {
		t.Fatalf("CreatePasswordUser() error = %v", err)
	}
	userID := util.Clean(user["id"])
	if _, err := app.profiles.EnsureUser(userID, nil); err != nil {
		t.Fatalf("EnsureUser() error = %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/admin/users/"+userID+"/quota", strings.NewReader(`{"delta":12}`))
	req.Header.Set("Authorization", adminAuthHeader(t, app))
	res := httptest.NewRecorder()
	app.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("quota route status = %d body = %s", res.Code, res.Body.String())
	}
	var response map[string]any
	if err := json.Unmarshal(res.Body.Bytes(), &response); err != nil {
		t.Fatalf("quota route json: %v", err)
	}
	item, _ := response["item"].(map[string]any)
	if got := util.ToInt(item["image_quota_total"], 0); got != 12 {
		t.Fatalf("image_quota_total = %d, want 12 body = %#v", got, response)
	}
	notification, _ := response["notification"].(map[string]any)
	if notification["category"] != service.NotificationCategoryAdminQuotaAdjustment {
		t.Fatalf("notification = %#v", notification)
	}

	items := app.notifications.ListForOwner(userID, 10)
	if len(items) != 1 {
		t.Fatalf("notifications len = %d items=%#v", len(items), items)
	}
	if items[0]["category"] != service.NotificationCategoryAdminQuotaAdjustment {
		t.Fatalf("notification category = %#v", items[0])
	}
	if app.notifications.UnreadCountForOwner(userID) != 1 {
		t.Fatalf("unread count = %d, want 1", app.notifications.UnreadCountForOwner(userID))
	}
}

func TestSubscriptionUserRoutesSkipStaticAPIPermission(t *testing.T) {
	app := newTestApp(t)
	defer app.Close()

	if _, err := app.subscriptions.UpdateConfig(map[string]any{
		"enabled": true,
		"payment": map[string]any{
			"enabled":      true,
			"gateway_url":  "https://pay.example.com",
			"merchant_id":  "1000",
			"merchant_key": "secret",
			"pay_types":    []string{"alipay"},
			"site_name":    "1818.pro",
		},
		"plans": []map[string]any{{
			"id":          "creator",
			"name":        "创作版",
			"price":       "69.00",
			"quota":       1500,
			"enabled":     true,
			"recommended": true,
		}},
	}); err != nil {
		t.Fatalf("UpdateConfig() error = %v", err)
	}
	role, err := app.auth.CreateRole(map[string]any{
		"name":            "subscription visitor",
		"menu_paths":      []string{"/subscription"},
		"api_permissions": []string{service.APIPermissionKey(http.MethodGet, "/v1/models")},
	})
	if err != nil {
		t.Fatalf("CreateRole() error = %v", err)
	}
	user, rawKey, err := app.auth.CreateAPIKey(service.AuthRoleUser, "subscriber", service.AuthOwner{ID: "sub-user", Name: "Subscriber"})
	if err != nil {
		t.Fatalf("CreateAPIKey() error = %v", err)
	}
	if updated := app.auth.UpdateUser(util.Clean(user["owner_id"]), map[string]any{"role_id": role["id"]}); updated == nil {
		t.Fatal("UpdateUser() returned nil")
	}

	req := httptest.NewRequest(http.MethodGet, "/api/subscription/config", nil)
	req.Header.Set("Authorization", "Bearer "+rawKey)
	res := httptest.NewRecorder()
	app.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("subscription config status = %d body = %s", res.Code, res.Body.String())
	}

	req = httptest.NewRequest(http.MethodPost, "/api/subscription/checkout", strings.NewReader(`{"plan_id":"creator","pay_type":"alipay"}`))
	req.Header.Set("Authorization", "Bearer "+rawKey)
	res = httptest.NewRecorder()
	app.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("subscription checkout status = %d body = %s", res.Code, res.Body.String())
	}
	var checkout map[string]any
	if err := json.Unmarshal(res.Body.Bytes(), &checkout); err != nil {
		t.Fatalf("checkout json: %v", err)
	}
	orderID := util.Clean(util.StringMap(checkout["order"])["id"])
	if orderID == "" {
		t.Fatalf("checkout order = %#v", checkout)
	}

	req = httptest.NewRequest(http.MethodGet, "/api/subscription/order?id="+url.QueryEscape(orderID), nil)
	req.Header.Set("Authorization", "Bearer "+rawKey)
	res = httptest.NewRecorder()
	app.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("own subscription order status = %d body = %s", res.Code, res.Body.String())
	}

	other, otherKey, err := app.auth.CreateAPIKey(service.AuthRoleUser, "other subscriber", service.AuthOwner{ID: "other-user", Name: "Other"})
	if err != nil {
		t.Fatalf("CreateAPIKey(other) error = %v", err)
	}
	if updated := app.auth.UpdateUser(util.Clean(other["owner_id"]), map[string]any{"role_id": role["id"]}); updated == nil {
		t.Fatal("UpdateUser(other) returned nil")
	}
	req = httptest.NewRequest(http.MethodGet, "/api/subscription/order?id="+url.QueryEscape(orderID), nil)
	req.Header.Set("Authorization", "Bearer "+otherKey)
	res = httptest.NewRecorder()
	app.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusForbidden {
		t.Fatalf("other subscription order status = %d body = %s", res.Code, res.Body.String())
	}
}

func TestProfileAccountNameAndPasswordUpdates(t *testing.T) {
	app := newTestApp(t)
	defer app.Close()

	user, token, err := app.auth.RegisterPasswordUser("alice", "Password123", "Alice")
	if err != nil {
		t.Fatalf("RegisterPasswordUser() error = %v", err)
	}
	if user.Name != "Alice" || token == "" {
		t.Fatalf("registered identity=%#v token=%q", user, token)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/profile", strings.NewReader(`{"name":"Alice Updated"}`))
	req.Header.Set("Authorization", "Bearer "+token)
	res := httptest.NewRecorder()
	app.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("profile name update status = %d body = %s", res.Code, res.Body.String())
	}
	var profile map[string]any
	if err := json.Unmarshal(res.Body.Bytes(), &profile); err != nil {
		t.Fatalf("profile update json: %v", err)
	}
	if profile["name"] != "Alice Updated" || profile["subject_id"] != user.ID {
		t.Fatalf("profile update body = %#v", profile)
	}

	req = httptest.NewRequest(http.MethodGet, "/auth/session", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	res = httptest.NewRecorder()
	app.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("session after profile update status = %d body = %s", res.Code, res.Body.String())
	}
	if err := json.Unmarshal(res.Body.Bytes(), &profile); err != nil {
		t.Fatalf("session after profile update json: %v", err)
	}
	if profile["name"] != "Alice Updated" {
		t.Fatalf("session did not reflect updated name: %#v", profile)
	}

	req = httptest.NewRequest(http.MethodPost, "/api/profile/password", strings.NewReader(`{"current_password":"wrong-password","new_password":"NewPassword123"}`))
	req.Header.Set("Authorization", "Bearer "+token)
	res = httptest.NewRecorder()
	app.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusBadRequest {
		t.Fatalf("wrong current password status = %d body = %s", res.Code, res.Body.String())
	}

	req = httptest.NewRequest(http.MethodPost, "/api/profile/password", strings.NewReader(`{"current_password":"Password123","new_password":"NewPassword123"}`))
	req.Header.Set("Authorization", "Bearer "+token)
	res = httptest.NewRecorder()
	app.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("password update status = %d body = %s", res.Code, res.Body.String())
	}

	req = httptest.NewRequest(http.MethodPost, "/auth/login", strings.NewReader(`{"username":"alice","password":"Password123"}`))
	res = httptest.NewRecorder()
	app.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusBadRequest {
		t.Fatalf("old password login status = %d body = %s", res.Code, res.Body.String())
	}

	req = httptest.NewRequest(http.MethodPost, "/auth/login", strings.NewReader(`{"username":"alice","password":"NewPassword123"}`))
	res = httptest.NewRecorder()
	app.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("new password login status = %d body = %s", res.Code, res.Body.String())
	}
	if err := json.Unmarshal(res.Body.Bytes(), &profile); err != nil {
		t.Fatalf("new password login json: %v", err)
	}
	if profile["name"] != "Alice Updated" || profile["subject_id"] != user.ID {
		t.Fatalf("new password login body = %#v", profile)
	}
}

func TestCreationTaskFailureWritesCallLog(t *testing.T) {
	app := newTestApp(t)
	defer app.Close()

	_, rawKey, err := app.auth.CreateAPIKey(service.AuthRoleUser, "frontend", service.AuthOwner{})
	if err != nil {
		t.Fatalf("CreateAPIKey() error = %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/creation-tasks/image-generations", strings.NewReader(`{"client_task_id":"task-log-test","prompt":"test image"}`))
	req.Header.Set("Authorization", "Bearer "+rawKey)
	res := httptest.NewRecorder()
	app.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("submit creation task status = %d body = %s", res.Code, res.Body.String())
	}

	var logs map[string]any
	var item map[string]any
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		req = httptest.NewRequest(http.MethodGet, "/api/logs", nil)
		req.Header.Set("Authorization", adminAuthHeader(t, app))
		res = httptest.NewRecorder()
		app.Handler().ServeHTTP(res, req)
		if res.Code != http.StatusOK {
			t.Fatalf("logs status = %d body = %s", res.Code, res.Body.String())
		}
		if err := json.Unmarshal(res.Body.Bytes(), &logs); err != nil {
			t.Fatalf("logs json: %v", err)
		}
		item = findLogBySummary(logItems(logs), "Responses 作画调用失败")
		if item != nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if item == nil {
		t.Fatalf("expected creation task failure to write a log event, got %#v", logs)
	}
	if _, ok := item["type"]; ok {
		t.Fatalf("log item should not expose type: %#v", item)
	}
	detail, _ := item["detail"].(map[string]any)
	if detail["endpoint"] != "/api/creation-tasks/response-image-generations" ||
		detail["path"] != "/api/creation-tasks/response-image-generations" ||
		detail["method"] != http.MethodPost ||
		detail["module"] != "creation-tasks" ||
		detail["outcome"] != "failed" {
		t.Fatalf("unexpected log detail: %#v", detail)
	}
	if _, ok := detail["status"].(float64); !ok {
		t.Fatalf("log status should use numeric HTTP-style status: %#v", detail)
	}
	if detail["key_name"] != "frontend" || detail["key_role"] != "user" {
		t.Fatalf("call log did not include user key identity: %#v", detail)
	}
}

func TestResponsesImageTaskRouteBuildsTaskPayload(t *testing.T) {
	app := newTestApp(t)
	defer app.Close()

	called := make(chan map[string]any, 1)
	app.tasks.SetResponseImageHandler(func(ctx context.Context, identity service.Identity, payload map[string]any) (map[string]any, error) {
		called <- payload
		return map[string]any{"data": []map[string]any{{"url": "https://example.test/generated.png"}}}, nil
	})
	_, rawKey, err := app.auth.CreateAPIKey(service.AuthRoleUser, "frontend", service.AuthOwner{})
	if err != nil {
		t.Fatalf("CreateAPIKey() error = %v", err)
	}

	body := `{"client_task_id":"response-image-route","prompt":"生成封面","model":"gpt-5.5","size":"2048x2048","image_resolution":"2k","quality":"high","output_format":"jpeg","output_compression":42,"n":2,"images":["data:image/png;base64,cG5n"],"messages":[{"role":"user","content":"生成封面"}],"visibility":"public"}`
	req := httptest.NewRequest(http.MethodPost, "/api/creation-tasks/response-image-generations", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+rawKey)
	res := httptest.NewRecorder()
	app.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("submit response image task status = %d body = %s", res.Code, res.Body.String())
	}
	var task map[string]any
	if err := json.Unmarshal(res.Body.Bytes(), &task); err != nil {
		t.Fatalf("task json: %v", err)
	}
	if task["mode"] != "response-image" || task["model"] != "gpt-5.5" || task["quality"] != "high" || task["visibility"] != "public" {
		t.Fatalf("unexpected submitted task = %#v", task)
	}
	if task["output_format"] != "jpeg" || util.ToInt(task["output_compression"], -1) != 42 {
		t.Fatalf("unexpected task output options = %#v", task)
	}
	select {
	case payload := <-called:
		if payload["model"] != "gpt-5.5" || payload["quality"] != "high" {
			t.Fatalf("unexpected response image payload = %#v", payload)
		}
		if payload["output_format"] != "jpeg" || util.ToInt(payload["output_compression"], -1) != 42 {
			t.Fatalf("unexpected response image output options = %#v", payload)
		}
		if images, ok := payload["images"].([]any); !ok || len(images) != 1 {
			t.Fatalf("payload images = %#v", payload["images"])
		}
		if payload["image_resolution"] != "2k" || payload["requested_size"] != "2048x2048" {
			t.Fatalf("payload request metadata = %#v", payload)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for response image handler")
	}
}

func TestImageGenerationTaskRouteAutoUsesResponsesImageHandler(t *testing.T) {
	app := newTestApp(t)
	defer app.Close()

	called := make(chan map[string]any, 1)
	app.tasks.SetResponseImageHandler(func(ctx context.Context, identity service.Identity, payload map[string]any) (map[string]any, error) {
		called <- payload
		return map[string]any{"data": []map[string]any{{"url": "https://example.test/generated.png"}}}, nil
	})
	_, rawKey, err := app.auth.CreateAPIKey(service.AuthRoleUser, "frontend", service.AuthOwner{})
	if err != nil {
		t.Fatalf("CreateAPIKey() error = %v", err)
	}

	body := `{"client_task_id":"auto-response-route","prompt":"画图迪士尼风格街道","model":"auto","quality":"medium","output_format":"webp","n":1}`
	req := httptest.NewRequest(http.MethodPost, "/api/creation-tasks/image-generations", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+rawKey)
	res := httptest.NewRecorder()
	app.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("submit image generation task status = %d body = %s", res.Code, res.Body.String())
	}
	var task map[string]any
	if err := json.Unmarshal(res.Body.Bytes(), &task); err != nil {
		t.Fatalf("task json: %v", err)
	}
	if task["mode"] != "response-image" || task["model"] != "auto" || task["output_format"] != "webp" {
		t.Fatalf("unexpected submitted task = %#v", task)
	}
	select {
	case payload := <-called:
		if payload["model"] != "auto" || payload["output_format"] != "webp" {
			t.Fatalf("unexpected response image payload = %#v", payload)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for response image handler")
	}
}

func TestRunResponsesImageGenerationTaskUsesResponsesPipelineForAutoModel(t *testing.T) {
	app := newTestApp(t)
	defer app.Close()

	called := make(chan protocol.ConversationRequest, 4)
	app.engine.ImageTokenProvider = func(context.Context) (string, error) { return "test-token", nil }
	app.engine.ImageClientFactory = func(string) *backend.Client { return nil }
	app.engine.StreamImageOutputsFunc = func(ctx context.Context, client *backend.Client, request protocol.ConversationRequest, index, total int) (<-chan protocol.ImageOutput, <-chan error) {
		called <- request
		out := make(chan protocol.ImageOutput, 1)
		errCh := make(chan error, 1)
		var encoded bytes.Buffer
		if err := encodeHTTPTestPNG(&encoded); err != nil {
			t.Errorf("encodeHTTPTestPNG() error = %v", err)
		}
		result := app.engine.FormatImageResultWithOptions(
			[]map[string]any{{"b64_json": base64.StdEncoding.EncodeToString(encoded.Bytes())}},
			request.Prompt,
			"url",
			"https://example.test",
			request.OwnerID,
			request.OwnerName,
			time.Now().Unix(),
			"",
			protocol.ImageOutputOptions{Format: request.OutputFormat, Compression: request.OutputCompression},
		)
		out <- protocol.ImageOutput{Kind: "result", Index: index, Total: total, Created: time.Now().Unix(), Data: util.AsMapSlice(result["data"])}
		close(out)
		errCh <- nil
		close(errCh)
		return out, errCh
	}

	result, err := app.runResponsesImageGenerationTask(context.Background(), map[string]any{
		"prompt":        "画图迪士尼风格街道",
		"model":         util.ImageModelAuto,
		"quality":       "medium",
		"output_format": "webp",
		"n":             1,
		"owner_id":      "test-owner",
	})
	if err != nil {
		t.Fatalf("runResponsesImageGenerationTask() error = %v", err)
	}
	if data := util.AsMapSlice(result["data"]); len(data) != 1 || util.Clean(data[0]["url"]) == "" {
		t.Fatalf("result data = %#v, result = %#v", data, result)
	}
	if files, _ := filepath.Glob(filepath.Join(app.config.ImagesDir(), "**", "*")); len(files) != 1 {
		t.Fatalf("stored files = %d, want exactly one saved image", len(files))
	}
	select {
	case request := <-called:
		if !request.ResponsesImageTool {
			t.Fatalf("ResponsesImageTool = false, request = %#v", request)
		}
		if request.Model != util.ImageModelAuto || request.OutputFormat != "webp" || request.Quality != "medium" {
			t.Fatalf("unexpected response image request = %#v", request)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for responses image pipeline")
	}
}

func TestResponsesImageTaskResultExtractsImagesAndText(t *testing.T) {
	app := newTestApp(t)
	defer app.Close()

	imageData := "cG5n"
	completed := map[string]any{
		"created_at": float64(1700000000),
		"output": []map[string]any{
			{"type": "image_generation_call", "result": imageData, "revised_prompt": "封面"},
		},
	}
	result := responsesImageTaskResult(app.engine, completed, map[string]any{
		"prompt":          "生成封面",
		"response_format": "b64_json",
		"base_url":        "https://example.test",
		"owner_id":        "owner-1",
		"owner_name":      "Alice",
	})
	data := util.AsMapSlice(result["data"])
	if len(data) != 1 || data[0]["b64_json"] != imageData || data[0]["revised_prompt"] != "封面" {
		t.Fatalf("response image result data = %#v", result)
	}
	if urlValue := util.Clean(data[0]["url"]); !strings.HasPrefix(urlValue, "https://example.test/images/") {
		t.Fatalf("stored image url = %#v in %#v", urlValue, result)
	}

	text := responseOutputText([]map[string]any{
		{"type": "message", "content": []map[string]any{{"type": "output_text", "text": "模型返回文本"}}},
	})
	if text != "模型返回文本" {
		t.Fatalf("responseOutputText() = %q", text)
	}
}

func TestRunLoggedImageTaskLogsTextOutputAsFailure(t *testing.T) {
	app := newTestApp(t)
	defer app.Close()

	identity := service.Identity{ID: "admin", Role: service.AuthRoleAdmin, Name: "Admin"}
	result, err := app.runLoggedImageTask(
		context.Background(),
		identity,
		map[string]any{"model": "gpt-image-2"},
		"/api/creation-tasks/response-image-generations",
		"Responses 作画",
		func(context.Context, map[string]any) (map[string]any, error) {
			return map[string]any{"output_type": "text", "message": "模型返回文本", "data": []map[string]any{}}, nil
		},
	)
	if err != nil {
		t.Fatalf("runLoggedImageTask() error = %v", err)
	}
	if result["output_type"] != "text" || result["message"] != "模型返回文本" {
		t.Fatalf("runLoggedImageTask() result = %#v", result)
	}
	logs := app.logs.Search(service.LogQuery{Limit: 10})
	item := findLogBySummary(logs, "Responses 作画调用失败")
	if item == nil {
		t.Fatalf("expected text-only image result to write failure log, got %#v", logs)
	}
	detail := util.StringMap(item["detail"])
	if detail["outcome"] != "failed" || util.ToInt(detail["status"], 0) != http.StatusBadGateway {
		t.Fatalf("failure log detail = %#v", detail)
	}
}

func TestResponsesImageTaskTextOutputError(t *testing.T) {
	result := map[string]any{"data": []map[string]any{}}
	completed := map[string]any{
		"output": []map[string]any{
			{"type": "message", "content": []map[string]any{{"type": "output_text", "text": "Got it! What image would you like me to generate?"}}},
		},
	}
	err := responsesImageTaskTextOutputError(result, completed)
	if err == nil {
		t.Fatalf("responsesImageTaskTextOutputError() err = nil, result = %#v", result)
	}
	var imageErr *protocol.ImageGenerationError
	if !errors.As(err, &imageErr) || imageErr.Code != "image_generation_text_response" {
		t.Fatalf("err = %T %v, want image_generation_text_response", err, err)
	}
	if result["output_type"] != "text" {
		t.Fatalf("result output_type = %#v, want text in %#v", result["output_type"], result)
	}
}

func TestResponseImageTaskBodyPassesSizeToImageTool(t *testing.T) {
	body := responseImageTaskBody(map[string]any{
		"prompt":             "生成 4K 正方形封面",
		"model":              "gpt-5.5",
		"size":               "2880x2880",
		"quality":            "high",
		"output_format":      "webp",
		"output_compression": 35,
	})
	tools := body["tools"].([]map[string]any)
	if len(tools) != 1 {
		t.Fatalf("tools = %#v", body["tools"])
	}
	tool := tools[0]
	if tool["size"] != "2880x2880" {
		t.Fatalf("tool size = %#v, want 2880x2880", tool["size"])
	}
	if tool["quality"] != "high" {
		t.Fatalf("tool quality = %#v, want high", tool["quality"])
	}
	if tool["output_format"] != "webp" {
		t.Fatalf("tool output_format = %#v, want webp", tool["output_format"])
	}
	if tool["output_compression"] != 35 {
		t.Fatalf("tool output_compression = %#v, want 35", tool["output_compression"])
	}
}

func TestResponseImageTaskBodyUsesAutoToolSizeForAspectRatio(t *testing.T) {
	body := responseImageTaskBody(map[string]any{
		"prompt":  "生成竖版海报",
		"model":   "gpt-5.5",
		"size":    "9:16",
		"quality": "high",
	})
	tools := body["tools"].([]map[string]any)
	tool := tools[0]
	if tool["size"] != "auto" {
		t.Fatalf("tool size = %#v, want auto", tool["size"])
	}
	input := body["input"].(string)
	if !strings.Contains(input, "9:16") {
		t.Fatalf("input should keep aspect ratio hint: %q", input)
	}
}

func TestResponseImageTaskBodyOmitsPngCompression(t *testing.T) {
	body := responseImageTaskBody(map[string]any{
		"prompt":             "生成封面",
		"model":              "gpt-5.5",
		"output_format":      "png",
		"output_compression": 35,
	})
	tools := body["tools"].([]map[string]any)
	tool := tools[0]
	if tool["output_format"] != "png" {
		t.Fatalf("tool output_format = %#v, want png", tool["output_format"])
	}
	if _, ok := tool["output_compression"]; ok {
		t.Fatalf("png tool should omit output_compression: %#v", tool)
	}
}

func TestResponseImageTaskBodyDefaultsAutoAndOmitsCodexQuality(t *testing.T) {
	body := responseImageTaskBody(map[string]any{
		"prompt":  "生成封面",
		"model":   "codex-gpt-image-2",
		"quality": "high",
	})
	tools := body["tools"].([]map[string]any)
	tool := tools[0]
	if tool["size"] != "auto" {
		t.Fatalf("tool size = %#v, want auto", tool["size"])
	}
	if _, ok := tool["quality"]; ok {
		t.Fatalf("codex tool should not include quality: %#v", tool)
	}
}

func TestEmptyCollectionEndpointsReturnArrays(t *testing.T) {
	app := newTestApp(t)
	defer app.Close()

	for _, tc := range []struct {
		name string
		path string
		keys []string
	}{
		{name: "accounts", path: "/api/accounts", keys: []string{"items"}},
		{name: "images", path: "/api/images", keys: []string{"items", "groups"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, tc.path, nil)
			req.Header.Set("Authorization", adminAuthHeader(t, app))
			res := httptest.NewRecorder()
			app.Handler().ServeHTTP(res, req)
			if res.Code != http.StatusOK {
				t.Fatalf("%s status = %d body = %s", tc.path, res.Code, res.Body.String())
			}
			var payload map[string]any
			if err := json.Unmarshal(res.Body.Bytes(), &payload); err != nil {
				t.Fatalf("%s json: %v", tc.path, err)
			}
			for _, key := range tc.keys {
				items, ok := payload[key].([]any)
				if !ok || items == nil || len(items) != 0 {
					t.Fatalf("%s %q = %#v, want empty array", tc.path, key, payload[key])
				}
			}
		})
	}
}

func TestRBACPermissionsGateManagementAPIs(t *testing.T) {
	app := newTestApp(t)
	defer app.Close()

	user, rawKey, err := app.auth.CreateAPIKey(service.AuthRoleUser, "operator", service.AuthOwner{})
	if err != nil {
		t.Fatalf("CreateAPIKey() error = %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/accounts", nil)
	req.Header.Set("Authorization", "Bearer "+rawKey)
	res := httptest.NewRecorder()
	app.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusForbidden {
		t.Fatalf("default user accounts status = %d body = %s", res.Code, res.Body.String())
	}

	role, err := app.auth.CreateRole(map[string]any{
		"name":            "accounts viewer",
		"menu_paths":      []string{"/accounts"},
		"api_permissions": []string{service.APIPermissionKey(http.MethodGet, "/api/accounts")},
	})
	if err != nil {
		t.Fatalf("CreateRole() error = %v", err)
	}
	userID := user["id"].(string)
	updated := app.auth.UpdateUser(userID, map[string]any{"role_id": role["id"]})
	if updated == nil {
		t.Fatal("UpdateUser() returned nil")
	}

	req = httptest.NewRequest(http.MethodGet, "/auth/session", nil)
	req.Header.Set("Authorization", "Bearer "+rawKey)
	res = httptest.NewRecorder()
	app.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("login after permission update status = %d body = %s", res.Code, res.Body.String())
	}
	var login map[string]any
	if err := json.Unmarshal(res.Body.Bytes(), &login); err != nil {
		t.Fatalf("login json: %v", err)
	}
	if paths := util.AsStringSlice(login["menu_paths"]); len(paths) != 1 || paths[0] != "/accounts" {
		t.Fatalf("login menu_paths = %#v", login["menu_paths"])
	}
	if login["role_id"] != role["id"] || login["role_name"] != "accounts viewer" {
		t.Fatalf("login role fields = %#v", login)
	}

	req = httptest.NewRequest(http.MethodGet, "/api/accounts", nil)
	req.Header.Set("Authorization", "Bearer "+rawKey)
	res = httptest.NewRecorder()
	app.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("granted user accounts status = %d body = %s", res.Code, res.Body.String())
	}

	app.accounts.AddAccounts([]string{"pool-token"})
	req = httptest.NewRequest(http.MethodGet, "/api/accounts", nil)
	req.Header.Set("Authorization", "Bearer "+rawKey)
	res = httptest.NewRecorder()
	app.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("granted user accounts with token status = %d body = %s", res.Code, res.Body.String())
	}
	var accountsBody map[string]any
	if err := json.Unmarshal(res.Body.Bytes(), &accountsBody); err != nil {
		t.Fatalf("accounts json: %v", err)
	}
	accountItems := logItems(accountsBody)
	if len(accountItems) != 1 {
		t.Fatalf("accounts body = %#v", accountsBody)
	}
	if _, ok := accountItems[0]["access_token"]; ok {
		t.Fatalf("account list should not expose access_token without export permission: %#v", accountItems[0])
	}
	accountID, _ := accountItems[0]["id"].(string)
	if accountID == "" || accountItems[0]["token_preview"] == "" {
		t.Fatalf("account list missing id/token preview: %#v", accountItems[0])
	}

	req = httptest.NewRequest(http.MethodGet, "/api/accounts/tokens", nil)
	req.Header.Set("Authorization", "Bearer "+rawKey)
	res = httptest.NewRecorder()
	app.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusForbidden {
		t.Fatalf("ungranted account token export status = %d body = %s", res.Code, res.Body.String())
	}

	req = httptest.NewRequest(http.MethodPost, "/api/accounts", strings.NewReader(`{"tokens":["x"]}`))
	req.Header.Set("Authorization", "Bearer "+rawKey)
	res = httptest.NewRecorder()
	app.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusForbidden {
		t.Fatalf("ungranted write accounts status = %d body = %s", res.Code, res.Body.String())
	}

	if _, err := app.auth.UpdateRole(role["id"].(string), map[string]any{
		"api_permissions": []string{
			service.APIPermissionKey(http.MethodGet, "/api/accounts"),
			service.APIPermissionKey(http.MethodDelete, "/api/accounts"),
		},
	}); err != nil {
		t.Fatalf("UpdateRole(delete accounts) error = %v", err)
	}
	req = httptest.NewRequest(http.MethodDelete, "/api/accounts", strings.NewReader(`{"account_ids":["`+accountID+`"]}`))
	req.Header.Set("Authorization", "Bearer "+rawKey)
	res = httptest.NewRecorder()
	app.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("delete account by id status = %d body = %s", res.Code, res.Body.String())
	}

	app.accounts.AddAccounts([]string{"pool-token"})
	if _, err := app.auth.UpdateRole(role["id"].(string), map[string]any{
		"api_permissions": []string{
			service.APIPermissionKey(http.MethodGet, "/api/accounts"),
			service.APIPermissionKey(http.MethodGet, "/api/accounts/tokens"),
		},
	}); err != nil {
		t.Fatalf("UpdateRole(export tokens) error = %v", err)
	}
	req = httptest.NewRequest(http.MethodGet, "/api/accounts/tokens", nil)
	req.Header.Set("Authorization", "Bearer "+rawKey)
	res = httptest.NewRecorder()
	app.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("granted account token export status = %d body = %s", res.Code, res.Body.String())
	}
	var tokenExport map[string]any
	if err := json.Unmarshal(res.Body.Bytes(), &tokenExport); err != nil {
		t.Fatalf("token export json: %v", err)
	}
	tokens := util.AsStringSlice(tokenExport["tokens"])
	if len(tokens) != 1 || tokens[0] != "pool-token" {
		t.Fatalf("exported tokens = %#v", tokenExport["tokens"])
	}
}

func TestRBACImageDeletePermissionStaysOwnerScoped(t *testing.T) {
	app := newTestApp(t)
	defer app.Close()

	user, rawKey, err := app.auth.CreateAPIKey(service.AuthRoleUser, "image-operator", service.AuthOwner{})
	if err != nil {
		t.Fatalf("CreateAPIKey() error = %v", err)
	}
	userID := user["id"].(string)
	ownRel := "delegated-own-delete.png"
	otherRel := "delegated-other-delete.png"
	ownPath := filepath.Join(app.config.ImagesDir(), filepath.FromSlash(ownRel))
	otherPath := filepath.Join(app.config.ImagesDir(), filepath.FromSlash(otherRel))
	for _, imagePath := range []string{ownPath, otherPath} {
		if err := writeHTTPTestPNG(imagePath); err != nil {
			t.Fatalf("write test image: %v", err)
		}
	}
	app.images.RecordGeneratedImages([]string{ownRel}, userID, "Image Operator", service.ImageVisibilityPrivate)
	app.images.RecordGeneratedImages([]string{otherRel}, "another-owner", "Another Owner", service.ImageVisibilityPrivate)

	req := httptest.NewRequest(http.MethodDelete, "/api/images", strings.NewReader(`{"paths":["`+ownRel+`"]}`))
	req.Header.Set("Authorization", "Bearer "+rawKey)
	res := httptest.NewRecorder()
	app.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusForbidden {
		t.Fatalf("default user delete status = %d body = %s", res.Code, res.Body.String())
	}

	role, err := app.auth.CreateRole(map[string]any{
		"name":            "image manager",
		"menu_paths":      []string{"/image-manager"},
		"api_permissions": []string{service.APIPermissionKey(http.MethodDelete, "/api/images")},
	})
	if err != nil {
		t.Fatalf("CreateRole() error = %v", err)
	}
	updated := app.auth.UpdateUser(user["id"].(string), map[string]any{"role_id": role["id"]})
	if updated == nil {
		t.Fatal("UpdateUser() returned nil")
	}

	req = httptest.NewRequest(http.MethodDelete, "/api/images", strings.NewReader(`{"paths":["`+otherRel+`"]}`))
	req.Header.Set("Authorization", "Bearer "+rawKey)
	res = httptest.NewRecorder()
	app.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("granted user delete other status = %d body = %s", res.Code, res.Body.String())
	}
	var payload map[string]any
	if err := json.Unmarshal(res.Body.Bytes(), &payload); err != nil {
		t.Fatalf("delete json: %v", err)
	}
	if deleted, _ := payload["deleted"].(float64); int(deleted) != 0 {
		t.Fatalf("deleted other = %#v body = %#v", payload["deleted"], payload)
	}
	if missing, _ := payload["missing"].(float64); int(missing) != 1 {
		t.Fatalf("missing other = %#v body = %#v", payload["missing"], payload)
	}
	if _, err := os.Stat(otherPath); err != nil {
		t.Fatalf("other image should not be deleted, stat error = %v", err)
	}

	req = httptest.NewRequest(http.MethodDelete, "/api/images", strings.NewReader(`{"paths":["`+ownRel+`"]}`))
	req.Header.Set("Authorization", "Bearer "+rawKey)
	res = httptest.NewRecorder()
	app.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("granted user delete own status = %d body = %s", res.Code, res.Body.String())
	}
	payload = nil
	if err := json.Unmarshal(res.Body.Bytes(), &payload); err != nil {
		t.Fatalf("delete own json: %v", err)
	}
	if deleted, _ := payload["deleted"].(float64); int(deleted) != 1 {
		t.Fatalf("deleted own = %#v body = %#v", payload["deleted"], payload)
	}
	if _, err := os.Stat(ownPath); !os.IsNotExist(err) {
		t.Fatalf("own image still exists or stat failed unexpectedly: %v", err)
	}
}

func TestLoginPageImageUploadSettings(t *testing.T) {
	app := newTestApp(t)
	defer app.Close()

	req := httptest.NewRequest(http.MethodGet, "/api/app-meta", nil)
	res := httptest.NewRecorder()
	app.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("app meta status = %d body = %s", res.Code, res.Body.String())
	}
	var meta map[string]any
	if err := json.Unmarshal(res.Body.Bytes(), &meta); err != nil {
		t.Fatalf("app meta json: %v", err)
	}
	if meta["login_page_image_url"] != "" || meta["login_page_image_mode"] != "contain" {
		t.Fatalf("initial app meta = %#v", meta)
	}

	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	_ = writer.WriteField("login_page_image_action", "replace")
	_ = writer.WriteField("login_page_image_mode", "cover")
	_ = writer.WriteField("login_page_image_zoom", "1.25")
	_ = writer.WriteField("login_page_image_position_x", "40")
	_ = writer.WriteField("login_page_image_position_y", "60")
	part, err := writer.CreateFormFile("login_page_image_file", "panel.png")
	if err != nil {
		t.Fatalf("CreateFormFile() error = %v", err)
	}
	if err := encodeHTTPTestPNG(part); err != nil {
		t.Fatalf("encode upload png: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("multipart close: %v", err)
	}

	req = httptest.NewRequest(http.MethodPost, "/api/settings/login-page-image", body)
	req.Header.Set("Authorization", adminAuthHeader(t, app))
	req.Header.Set("Content-Type", writer.FormDataContentType())
	res = httptest.NewRecorder()
	app.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("upload status = %d body = %s", res.Code, res.Body.String())
	}
	var payload map[string]any
	if err := json.Unmarshal(res.Body.Bytes(), &payload); err != nil {
		t.Fatalf("upload json: %v", err)
	}
	config, _ := payload["config"].(map[string]any)
	imageURL, _ := config["login_page_image_url"].(string)
	if !strings.HasPrefix(imageURL, "/login-page-images/") {
		t.Fatalf("uploaded image url = %#v in %#v", imageURL, payload)
	}
	if config["login_page_image_mode"] != "cover" || config["login_page_image_zoom"] != float64(1.25) {
		t.Fatalf("login page image config = %#v", config)
	}

	req = httptest.NewRequest(http.MethodGet, imageURL, nil)
	res = httptest.NewRecorder()
	app.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("uploaded image static status = %d body = %s", res.Code, res.Body.String())
	}

	req = httptest.NewRequest(http.MethodGet, "/api/app-meta", nil)
	res = httptest.NewRecorder()
	app.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("app meta after upload status = %d body = %s", res.Code, res.Body.String())
	}
	if err := json.Unmarshal(res.Body.Bytes(), &meta); err != nil {
		t.Fatalf("app meta after upload json: %v", err)
	}
	if meta["login_page_image_url"] != imageURL || meta["login_page_image_mode"] != "cover" {
		t.Fatalf("app meta after upload = %#v", meta)
	}
}

func TestImageManagementIsScopedByOwner(t *testing.T) {
	app := newTestApp(t)
	defer app.Close()

	owner := service.AuthOwner{ID: "linuxdo:123", Name: "alice", Provider: service.AuthProviderLinuxDo}
	_, sessionKey, err := app.auth.UpsertLinuxDoSession(owner)
	if err != nil {
		t.Fatalf("UpsertLinuxDoSession() error = %v", err)
	}
	aliceRel := "2026/04/29/alice.png"
	bobRel := "2026/04/29/bob.png"
	legacyRel := "2026/04/29/legacy.png"
	for _, rel := range []string{aliceRel, bobRel, legacyRel} {
		path := filepath.Join(app.config.ImagesDir(), filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("mkdir image dir: %v", err)
		}
		if err := writeHTTPTestPNG(path); err != nil {
			t.Fatalf("write image %s: %v", rel, err)
		}
	}
	app.images.RecordImageOwners([]string{aliceRel}, owner.ID)
	app.images.RecordImageOwners([]string{bobRel}, "linuxdo:456")

	req := httptest.NewRequest(http.MethodGet, "/api/images", nil)
	req.Header.Set("Authorization", "Bearer "+sessionKey)
	res := httptest.NewRecorder()
	app.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("linuxdo images status = %d body = %s", res.Code, res.Body.String())
	}
	var list map[string]any
	if err := json.Unmarshal(res.Body.Bytes(), &list); err != nil {
		t.Fatalf("linuxdo images json: %v", err)
	}
	items := logItems(list)
	if len(items) != 1 || items[0]["path"] != aliceRel {
		t.Fatalf("linuxdo scoped images = %#v", list)
	}
	if items[0]["owner_name"] != owner.Name || items[0]["visibility"] != service.ImageVisibilityPrivate {
		t.Fatalf("linuxdo image metadata = %#v", items[0])
	}

	req = httptest.NewRequest(http.MethodPatch, "/api/images/visibility", strings.NewReader(`{"path":"`+aliceRel+`","visibility":"public"}`))
	req.Header.Set("Authorization", "Bearer "+sessionKey)
	res = httptest.NewRecorder()
	app.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("linuxdo publish image status = %d body = %s", res.Code, res.Body.String())
	}
	var visibilityBody map[string]any
	if err := json.Unmarshal(res.Body.Bytes(), &visibilityBody); err != nil {
		t.Fatalf("visibility json: %v", err)
	}
	updatedItem, _ := visibilityBody["item"].(map[string]any)
	if updatedItem["visibility"] != service.ImageVisibilityPublic || updatedItem["owner_name"] != owner.Name {
		t.Fatalf("publish image response = %#v", visibilityBody)
	}

	req = httptest.NewRequest(http.MethodPatch, "/api/images/visibility", strings.NewReader(`{"path":"`+bobRel+`","visibility":"public"}`))
	req.Header.Set("Authorization", "Bearer "+sessionKey)
	res = httptest.NewRecorder()
	app.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusNotFound {
		t.Fatalf("linuxdo publish other image status = %d body = %s", res.Code, res.Body.String())
	}

	req = httptest.NewRequest(http.MethodGet, "/api/images?scope=public", nil)
	req.Header.Set("Authorization", "Bearer "+sessionKey)
	res = httptest.NewRecorder()
	app.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("public images status = %d body = %s", res.Code, res.Body.String())
	}
	if err := json.Unmarshal(res.Body.Bytes(), &list); err != nil {
		t.Fatalf("public images json: %v", err)
	}
	if items := logItems(list); len(items) != 1 || items[0]["path"] != aliceRel || items[0]["owner_name"] != owner.Name {
		t.Fatalf("public scoped images = %#v", list)
	}

	req = httptest.NewRequest(http.MethodPatch, "/api/images/visibility", strings.NewReader(`{"path":"`+aliceRel+`","visibility":"private"}`))
	req.Header.Set("Authorization", "Bearer "+sessionKey)
	res = httptest.NewRecorder()
	app.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("linuxdo unpublish image status = %d body = %s", res.Code, res.Body.String())
	}

	req = httptest.NewRequest(http.MethodGet, "/api/images?scope=public", nil)
	req.Header.Set("Authorization", "Bearer "+sessionKey)
	res = httptest.NewRecorder()
	app.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("public images after unpublish status = %d body = %s", res.Code, res.Body.String())
	}
	if err := json.Unmarshal(res.Body.Bytes(), &list); err != nil {
		t.Fatalf("public images after unpublish json: %v", err)
	}
	if items := logItems(list); len(items) != 0 {
		t.Fatalf("unpublished image should leave public gallery: %#v", list)
	}

	req = httptest.NewRequest(http.MethodGet, "/api/images?scope=public", nil)
	req.Header.Set("Authorization", adminAuthHeader(t, app))
	res = httptest.NewRecorder()
	app.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("admin public gallery status = %d body = %s", res.Code, res.Body.String())
	}
	if err := json.Unmarshal(res.Body.Bytes(), &list); err != nil {
		t.Fatalf("admin public gallery json: %v", err)
	}
	items = logItems(list)
	if len(items) != 3 {
		t.Fatalf("admin public gallery should see all images, got %#v", list)
	}
	seenPaths := make(map[string]bool, len(items))
	for _, item := range items {
		path, _ := item["path"].(string)
		seenPaths[path] = true
	}
	if !seenPaths[aliceRel] || !seenPaths[bobRel] || !seenPaths[legacyRel] {
		t.Fatalf("admin public gallery paths = %#v", items)
	}

	req = httptest.NewRequest(http.MethodDelete, "/api/images", strings.NewReader(`{"paths":["`+bobRel+`","`+aliceRel+`"]}`))
	req.Header.Set("Authorization", "Bearer "+sessionKey)
	res = httptest.NewRecorder()
	app.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusForbidden {
		t.Fatalf("linuxdo delete images status = %d body = %s", res.Code, res.Body.String())
	}
	if _, err := os.Stat(filepath.Join(app.config.ImagesDir(), filepath.FromSlash(aliceRel))); err != nil {
		t.Fatalf("alice image should not be deleted by Linuxdo user, stat error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(app.config.ImagesDir(), filepath.FromSlash(bobRel))); err != nil {
		t.Fatalf("bob image should not be deleted, stat error = %v", err)
	}

	_, localKey, err := app.auth.CreateAPIKey(service.AuthRoleUser, "local user", service.AuthOwner{})
	if err != nil {
		t.Fatalf("CreateAPIKey(local) error = %v", err)
	}
	req = httptest.NewRequest(http.MethodDelete, "/api/images", strings.NewReader(`{"paths":["`+aliceRel+`"]}`))
	req.Header.Set("Authorization", "Bearer "+localKey)
	res = httptest.NewRecorder()
	app.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusForbidden {
		t.Fatalf("local user delete images status = %d body = %s", res.Code, res.Body.String())
	}

	req = httptest.NewRequest(http.MethodGet, "/api/images", nil)
	req.Header.Set("Authorization", adminAuthHeader(t, app))
	res = httptest.NewRecorder()
	app.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("admin images status = %d body = %s", res.Code, res.Body.String())
	}
	if err := json.Unmarshal(res.Body.Bytes(), &list); err != nil {
		t.Fatalf("admin images json: %v", err)
	}
	if items := logItems(list); len(items) != 3 {
		t.Fatalf("admin should see owned and legacy images, got %#v", list)
	}

	req = httptest.NewRequest(http.MethodDelete, "/api/images", strings.NewReader(`{"paths":["`+aliceRel+`"]}`))
	req.Header.Set("Authorization", adminAuthHeader(t, app))
	res = httptest.NewRecorder()
	app.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("admin delete images status = %d body = %s", res.Code, res.Body.String())
	}
	var deleteBody map[string]any
	if err := json.Unmarshal(res.Body.Bytes(), &deleteBody); err != nil {
		t.Fatalf("admin delete images json: %v", err)
	}
	if deleteBody["deleted"] != float64(1) || deleteBody["missing"] != float64(0) {
		t.Fatalf("admin delete images body = %#v", deleteBody)
	}
	if _, err := os.Stat(filepath.Join(app.config.ImagesDir(), filepath.FromSlash(aliceRel))); !os.IsNotExist(err) {
		t.Fatalf("alice image should be deleted by admin, stat error = %v", err)
	}
}

func TestManagedImageFilesRequireOwnerOrPublicAccess(t *testing.T) {
	app := newTestApp(t)
	defer app.Close()

	owner := service.AuthOwner{ID: "linuxdo:123", Name: "alice", Provider: service.AuthProviderLinuxDo}
	_, aliceKey, err := app.auth.UpsertLinuxDoSession(owner)
	if err != nil {
		t.Fatalf("UpsertLinuxDoSession(alice) error = %v", err)
	}
	_, bobKey, err := app.auth.CreateAPIKey(service.AuthRoleUser, "bob", service.AuthOwner{ID: "linuxdo:456", Name: "bob", Provider: service.AuthProviderLinuxDo})
	if err != nil {
		t.Fatalf("CreateAPIKey(bob) error = %v", err)
	}

	rel := "2026/05/01/1777664437_f5b9d1d2cd2a380307ca9fb32c1a84d1.png"
	imagePath := filepath.Join(app.config.ImagesDir(), filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(imagePath), 0o755); err != nil {
		t.Fatalf("mkdir image dir: %v", err)
	}
	if err := writeHTTPTestPNG(imagePath); err != nil {
		t.Fatalf("write image: %v", err)
	}
	app.images.RecordGeneratedImages([]string{rel}, owner.ID, owner.Name, service.ImageVisibilityPrivate)

	req := httptest.NewRequest(http.MethodGet, "/images/2026/05/01", nil)
	res := httptest.NewRecorder()
	app.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusNotFound {
		t.Fatalf("image directory listing status = %d body = %q, want 404", res.Code, res.Body.String())
	}

	req = httptest.NewRequest(http.MethodGet, "/images/"+rel, nil)
	res = httptest.NewRecorder()
	app.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous private image status = %d body = %q, want 401", res.Code, res.Body.String())
	}

	req = httptest.NewRequest(http.MethodGet, "/images/"+rel, nil)
	req.Header.Set("Authorization", "Bearer "+bobKey)
	res = httptest.NewRecorder()
	app.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusNotFound {
		t.Fatalf("other user private image status = %d body = %q, want 404", res.Code, res.Body.String())
	}

	req = httptest.NewRequest(http.MethodGet, "/images/"+rel, nil)
	req.Header.Set("Authorization", "Bearer "+aliceKey)
	res = httptest.NewRecorder()
	app.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("owner private image status = %d body = %q", res.Code, res.Body.String())
	}
	if got := res.Header().Get("Content-Type"); !strings.Contains(got, "image/png") {
		t.Fatalf("owner private image Content-Type = %q, want image/png", got)
	}

	req = httptest.NewRequest(http.MethodGet, "/images/"+rel, nil)
	req.AddCookie(&http.Cookie{Name: authSessionCookieName, Value: aliceKey})
	res = httptest.NewRecorder()
	app.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("owner private image cookie status = %d body = %q", res.Code, res.Body.String())
	}

	req = httptest.NewRequest(http.MethodHead, "/images/"+rel, nil)
	req.Header.Set("Authorization", "Bearer "+aliceKey)
	res = httptest.NewRecorder()
	app.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("owner private image HEAD status = %d body = %q", res.Code, res.Body.String())
	}
	if res.Body.Len() != 0 {
		t.Fatalf("owner private image HEAD body length = %d, want 0", res.Body.Len())
	}

	if _, err := app.images.UpdateImageVisibility(rel, service.ImageVisibilityPublic, service.ImageAccessScope{OwnerID: owner.ID}); err != nil {
		t.Fatalf("publish image: %v", err)
	}
	req = httptest.NewRequest(http.MethodGet, "/images/"+rel, nil)
	res = httptest.NewRecorder()
	app.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("anonymous public image status = %d body = %q", res.Code, res.Body.String())
	}
}

func TestImageThumbnailsAreGeneratedOnDemand(t *testing.T) {
	app := newTestApp(t)
	defer app.Close()

	rel := "2026/04/29/sample.png"
	imagePath := filepath.Join(app.config.ImagesDir(), filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(imagePath), 0o755); err != nil {
		t.Fatalf("mkdir image dir: %v", err)
	}
	if err := writeHTTPTestPNG(imagePath); err != nil {
		t.Fatalf("write image: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/images", nil)
	req.Header.Set("Authorization", adminAuthHeader(t, app))
	res := httptest.NewRecorder()
	app.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("/api/images status = %d body = %s", res.Code, res.Body.String())
	}
	var list map[string]any
	if err := json.Unmarshal(res.Body.Bytes(), &list); err != nil {
		t.Fatalf("/api/images json: %v", err)
	}
	items := logItems(list)
	if len(items) != 1 {
		t.Fatalf("/api/images items = %#v", list)
	}
	thumbnailURL, _ := items[0]["thumbnail_url"].(string)
	if !strings.Contains(thumbnailURL, "/image-thumbnails/") {
		t.Fatalf("thumbnail_url = %q, want lazy thumbnail route", thumbnailURL)
	}
	parsedThumbnailURL, err := url.Parse(thumbnailURL)
	if err != nil {
		t.Fatalf("parse thumbnail URL: %v", err)
	}
	if !strings.HasSuffix(parsedThumbnailURL.Path, ".jpg") {
		t.Fatalf("thumbnail path = %q, want .jpg suffix", parsedThumbnailURL.Path)
	}
	if parsedThumbnailURL.Query().Get("v") == "" {
		t.Fatalf("thumbnail URL = %q, want cache-busting query", thumbnailURL)
	}
	thumbPath := filepath.Join(app.config.ImageThumbnailsDir(), filepath.FromSlash(rel)+".jpg")
	if _, err := os.Stat(thumbPath); !os.IsNotExist(err) {
		t.Fatalf("/api/images should not create thumbnail synchronously, stat error = %v", err)
	}

	req = httptest.NewRequest(http.MethodGet, parsedThumbnailURL.Path, nil)
	req.Header.Set("Authorization", adminAuthHeader(t, app))
	res = httptest.NewRecorder()
	app.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("thumbnail status = %d body = %s", res.Code, res.Body.String())
	}
	if res.Body.Len() == 0 {
		t.Fatal("thumbnail body is empty")
	}
	if got := res.Header().Get("Cache-Control"); got != imageThumbnailCacheControl {
		t.Fatalf("thumbnail Cache-Control = %q, want %q", got, imageThumbnailCacheControl)
	}
	if got := res.Header().Get("Content-Type"); !strings.Contains(got, "image/jpeg") {
		t.Fatalf("thumbnail Content-Type = %q, want image/jpeg", got)
	}
	if _, err := os.Stat(thumbPath); err != nil {
		t.Fatalf("thumbnail was not created on demand: %v", err)
	}
}

func TestManagedImageThumbnailsRequireOwnerOrPublicAccess(t *testing.T) {
	app := newTestApp(t)
	defer app.Close()

	owner := service.AuthOwner{ID: "linuxdo:123", Name: "alice", Provider: service.AuthProviderLinuxDo}
	_, aliceKey, err := app.auth.UpsertLinuxDoSession(owner)
	if err != nil {
		t.Fatalf("UpsertLinuxDoSession(alice) error = %v", err)
	}
	_, bobKey, err := app.auth.CreateAPIKey(service.AuthRoleUser, "bob", service.AuthOwner{ID: "linuxdo:456", Name: "bob", Provider: service.AuthProviderLinuxDo})
	if err != nil {
		t.Fatalf("CreateAPIKey(bob) error = %v", err)
	}

	rel := "2026/05/01/private.png"
	imagePath := filepath.Join(app.config.ImagesDir(), filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(imagePath), 0o755); err != nil {
		t.Fatalf("mkdir image dir: %v", err)
	}
	if err := writeHTTPTestPNG(imagePath); err != nil {
		t.Fatalf("write image: %v", err)
	}
	app.images.RecordGeneratedImages([]string{rel}, owner.ID, owner.Name, service.ImageVisibilityPrivate)
	thumbnailPath := "/image-thumbnails/" + rel + ".jpg"

	req := httptest.NewRequest(http.MethodGet, thumbnailPath, nil)
	res := httptest.NewRecorder()
	app.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous private thumbnail status = %d body = %q, want 401", res.Code, res.Body.String())
	}

	req = httptest.NewRequest(http.MethodGet, thumbnailPath, nil)
	req.Header.Set("Authorization", "Bearer "+bobKey)
	res = httptest.NewRecorder()
	app.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusNotFound {
		t.Fatalf("other user private thumbnail status = %d body = %q, want 404", res.Code, res.Body.String())
	}

	req = httptest.NewRequest(http.MethodGet, thumbnailPath, nil)
	req.Header.Set("Authorization", "Bearer "+aliceKey)
	res = httptest.NewRecorder()
	app.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("owner private thumbnail status = %d body = %q", res.Code, res.Body.String())
	}
	if got := res.Header().Get("Content-Type"); !strings.Contains(got, "image/jpeg") {
		t.Fatalf("owner private thumbnail Content-Type = %q, want image/jpeg", got)
	}

	req = httptest.NewRequest(http.MethodGet, thumbnailPath, nil)
	req.AddCookie(&http.Cookie{Name: authSessionCookieName, Value: aliceKey})
	res = httptest.NewRecorder()
	app.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("owner private thumbnail cookie status = %d body = %q", res.Code, res.Body.String())
	}

	if _, err := app.images.UpdateImageVisibility(rel, service.ImageVisibilityPublic, service.ImageAccessScope{OwnerID: owner.ID}); err != nil {
		t.Fatalf("publish image: %v", err)
	}
	req = httptest.NewRequest(http.MethodGet, thumbnailPath, nil)
	res = httptest.NewRecorder()
	app.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("anonymous public thumbnail status = %d body = %q", res.Code, res.Body.String())
	}
}

func TestAuthSessionCookieLifecycle(t *testing.T) {
	app := newTestApp(t)
	defer app.Close()

	req := httptest.NewRequest(http.MethodPost, "/auth/login", strings.NewReader(`{"username":"`+testAdminUsername+`","password":"`+testAdminPassword+`"}`))
	res := httptest.NewRecorder()
	app.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("login status = %d body = %s", res.Code, res.Body.String())
	}
	cookie := findResponseCookie(res.Result(), authSessionCookieName)
	if cookie == nil || cookie.Value == "" || cookie.Path != "/" || !cookie.HttpOnly {
		t.Fatalf("login cookie = %#v", cookie)
	}
	if got := cookie.SameSite; got != http.SameSiteLaxMode {
		t.Fatalf("login cookie SameSite = %v, want Lax", got)
	}

	req = httptest.NewRequest(http.MethodPost, "/auth/logout", nil)
	req.AddCookie(cookie)
	res = httptest.NewRecorder()
	app.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("logout status = %d body = %s", res.Code, res.Body.String())
	}
	cleared := findResponseCookie(res.Result(), authSessionCookieName)
	if cleared == nil || cleared.MaxAge >= 0 || cleared.Value != "" {
		t.Fatalf("logout cookie = %#v", cleared)
	}
}

func TestBrowserCookieSessionTakesPriorityOverStaleBearer(t *testing.T) {
	app := newTestApp(t)
	defer app.Close()

	oldItem, oldKey, err := app.auth.CreateAPIKey(service.AuthRoleUser, "old mobile token", service.AuthOwner{ID: "user-old", Name: "Old"})
	if err != nil {
		t.Fatalf("CreateAPIKey old error = %v", err)
	}
	newItem, newKey, err := app.auth.CreateAPIKey(service.AuthRoleUser, "new cookie session", service.AuthOwner{ID: "user-new", Name: "New"})
	if err != nil {
		t.Fatalf("CreateAPIKey new error = %v", err)
	}
	oldID := util.Clean(oldItem["owner_id"])
	newID := util.Clean(newItem["owner_id"])
	if _, err := app.profiles.AdjustUserQuota(oldID, 5); err != nil {
		t.Fatalf("old quota setup error = %v", err)
	}
	if _, err := app.profiles.AdjustUserQuota(newID, 5); err != nil {
		t.Fatalf("new quota setup error = %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/checkin", nil)
	req.Header.Set("Authorization", "Bearer "+oldKey)
	req.AddCookie(&http.Cookie{Name: authSessionCookieName, Value: newKey})
	res := httptest.NewRecorder()
	app.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("checkin status = %d body = %s", res.Code, res.Body.String())
	}
	oldProfile := app.profiles.PublicForUser(oldID)
	newProfile := app.profiles.PublicForUser(newID)
	if util.Clean(oldProfile["last_checkin_date"]) != "" {
		t.Fatalf("stale bearer owner was checked in: %#v", oldProfile)
	}
	if util.Clean(newProfile["last_checkin_date"]) == "" {
		t.Fatalf("cookie owner was not checked in: %#v", newProfile)
	}

	req = httptest.NewRequest(http.MethodGet, "/auth/session", nil)
	req.Header.Set("Authorization", "Bearer "+oldKey)
	req.AddCookie(&http.Cookie{Name: authSessionCookieName, Value: newKey})
	res = httptest.NewRecorder()
	app.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("session status = %d body = %s", res.Code, res.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(res.Body.Bytes(), &body); err != nil {
		t.Fatalf("session json: %v", err)
	}
	if got := util.Clean(body["subject_id"]); got != newID {
		t.Fatalf("/auth/session should refresh the browser cookie identity, got %q want %q body=%#v", got, newID, body)
	}
	cookie := findResponseCookie(res.Result(), authSessionCookieName)
	if cookie == nil || cookie.Value != newKey {
		t.Fatalf("/auth/session should keep the authenticated browser cookie token: %#v", cookie)
	}
	if got := util.Clean(body["token"]); got != newKey {
		t.Fatalf("/auth/session token = %q, want cookie token", got)
	}
}

func TestSocialLoginCookieUsesSocialPath(t *testing.T) {
	res := httptest.NewRecorder()
	setSocialLoginCookie(res, socialLoginStateCookieName, "state", socialLoginCookieMaxAgeSec, true)
	cookie := findResponseCookie(res.Result(), socialLoginStateCookieName)
	if cookie == nil || cookie.Path != socialLoginCookiePath || !cookie.HttpOnly || !cookie.Secure || cookie.SameSite != http.SameSiteLaxMode {
		t.Fatalf("social login cookie = %#v", cookie)
	}
	res = httptest.NewRecorder()
	clearSocialLoginCookie(res, socialLoginStateCookieName, true)
	cookie = findResponseCookie(res.Result(), socialLoginStateCookieName)
	if cookie == nil || cookie.Path != socialLoginCookiePath || cookie.Value != "" || cookie.MaxAge >= 0 {
		t.Fatalf("cleared social login cookie = %#v", cookie)
	}
}

func TestResolveImageBaseURLSanitizesRequestHeaders(t *testing.T) {
	app := newTestApp(t)
	defer app.Close()

	req := httptest.NewRequest(http.MethodGet, "/api/subscription/checkout", nil)
	req.Host = "images.dfmcn.com"
	req.Header.Set("X-Forwarded-Proto", "javascript")
	if got := app.resolveImageBaseURL(req); got != "http://images.dfmcn.com" {
		t.Fatalf("resolveImageBaseURL invalid proto = %q", got)
	}

	req = httptest.NewRequest(http.MethodGet, "/api/subscription/checkout", nil)
	req.Host = "images.dfmcn.com:443"
	req.Header.Set("X-Forwarded-Proto", "https, http")
	if got := app.resolveImageBaseURL(req); got != "https://images.dfmcn.com:443" {
		t.Fatalf("resolveImageBaseURL forwarded https = %q", got)
	}

	req = httptest.NewRequest(http.MethodGet, "/api/subscription/checkout", nil)
	req.Host = "evil.test/path"
	if got := app.resolveImageBaseURL(req); got != "http://localhost" {
		t.Fatalf("resolveImageBaseURL invalid host = %q", got)
	}
}

func TestResolveImageBaseURLPrefersConfiguredBaseURL(t *testing.T) {
	t.Setenv("CHATGPT2API_BASE_URL", "https://images.dfmcn.com/root/")
	app := newTestApp(t)
	defer app.Close()

	req := httptest.NewRequest(http.MethodGet, "/api/subscription/checkout", nil)
	req.Host = "evil.test"
	req.Header.Set("X-Forwarded-Proto", "http")
	if got := app.resolveImageBaseURL(req); got != "https://images.dfmcn.com/root" {
		t.Fatalf("resolveImageBaseURL configured base = %q", got)
	}
}

func TestLoginAllowsCredentialedLoopbackFrontend(t *testing.T) {
	app := newTestApp(t)
	defer app.Close()

	req := httptest.NewRequest(http.MethodPost, "/auth/login", strings.NewReader(`{"username":"`+testAdminUsername+`","password":"`+testAdminPassword+`"}`))
	req.Host = "127.0.0.1:8000"
	req.Header.Set("Origin", "http://localhost:5173")
	res := httptest.NewRecorder()
	app.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("login status = %d body = %s", res.Code, res.Body.String())
	}
	if got := res.Header().Get("Access-Control-Allow-Origin"); got != "http://localhost:5173" {
		t.Fatalf("Access-Control-Allow-Origin = %q, want frontend origin", got)
	}
	if got := res.Header().Get("Access-Control-Allow-Credentials"); got != "true" {
		t.Fatalf("Access-Control-Allow-Credentials = %q, want true", got)
	}
	if cookie := findResponseCookie(res.Result(), authSessionCookieName); cookie == nil || cookie.Value == "" {
		t.Fatalf("login cookie = %#v", cookie)
	}
}

func TestCredentialedLoginPreflightAllowsContentType(t *testing.T) {
	app := newTestApp(t)
	defer app.Close()

	req := httptest.NewRequest(http.MethodOptions, "/auth/login", nil)
	req.Host = "127.0.0.1:8000"
	req.Header.Set("Origin", "http://127.0.0.1:5173")
	req.Header.Set("Access-Control-Request-Method", http.MethodPost)
	req.Header.Set("Access-Control-Request-Headers", "content-type")
	res := httptest.NewRecorder()
	app.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusNoContent {
		t.Fatalf("preflight status = %d body = %s", res.Code, res.Body.String())
	}
	if got := res.Header().Get("Access-Control-Allow-Origin"); got != "http://127.0.0.1:5173" {
		t.Fatalf("Access-Control-Allow-Origin = %q, want request origin", got)
	}
	if got := res.Header().Get("Access-Control-Allow-Credentials"); got != "true" {
		t.Fatalf("Access-Control-Allow-Credentials = %q, want true", got)
	}
	if got := res.Header().Get("Access-Control-Allow-Headers"); got != "content-type" {
		t.Fatalf("Access-Control-Allow-Headers = %q, want content-type", got)
	}
}

func TestImageThumbnailRejectsTraversal(t *testing.T) {
	app := newTestApp(t)
	defer app.Close()

	outsideThumbnailRoot := filepath.Join(app.config.DataDir, "secret.png.jpg")
	if err := os.WriteFile(outsideThumbnailRoot, []byte("secret"), 0o644); err != nil {
		t.Fatalf("write outside thumbnail root: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/image-thumbnails/../secret.png.jpg", nil)
	res := httptest.NewRecorder()
	app.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusNotFound {
		t.Fatalf("thumbnail traversal status = %d body = %q, want 404", res.Code, res.Body.String())
	}
}

func TestLinuxDoUserCanManageOwnKeys(t *testing.T) {
	app := newTestApp(t)
	defer app.Close()

	owner := service.AuthOwner{ID: "linuxdo:123", Name: "linuxdo_user", Provider: service.AuthProviderLinuxDo, LinuxDoLevel: "3"}
	_, sessionKey, err := app.auth.UpsertLinuxDoSession(owner)
	if err != nil {
		t.Fatalf("UpsertLinuxDoSession() error = %v", err)
	}
	_, otherKey, err := app.auth.CreateAPIKey(service.AuthRoleUser, "other user key", service.AuthOwner{ID: "linuxdo:456", Name: "other", Provider: service.AuthProviderLinuxDo})
	if err != nil || otherKey == "" {
		t.Fatalf("CreateAPIKey(other) key=%q err=%v", otherKey, err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/auth/users", nil)
	req.Header.Set("Authorization", "Bearer "+sessionKey)
	res := httptest.NewRecorder()
	app.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("linuxdo initial list status = %d body = %s", res.Code, res.Body.String())
	}
	var list map[string]any
	if err := json.Unmarshal(res.Body.Bytes(), &list); err != nil {
		t.Fatalf("initial list json: %v", err)
	}
	if rawItems, ok := list["items"].([]any); !ok || len(rawItems) != 0 {
		t.Fatalf("linuxdo initial list should be empty array, got %#v", list)
	}

	req = httptest.NewRequest(http.MethodPost, "/api/auth/users", strings.NewReader(`{"name":"linuxdo api"}`))
	req.Header.Set("Authorization", "Bearer "+sessionKey)
	res = httptest.NewRecorder()
	app.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("linuxdo create key status = %d body = %s", res.Code, res.Body.String())
	}
	var created map[string]any
	if err := json.Unmarshal(res.Body.Bytes(), &created); err != nil {
		t.Fatalf("create key json: %v", err)
	}
	item, _ := created["item"].(map[string]any)
	if item["owner_id"] != owner.ID || item["provider"] != service.AuthProviderLinuxDo {
		t.Fatalf("created key owner = %#v", item)
	}
	firstKey, _ := created["key"].(string)
	firstID, _ := item["id"].(string)

	req = httptest.NewRequest(http.MethodPost, "/api/auth/users", strings.NewReader(`{"name":"linuxdo api refreshed"}`))
	req.Header.Set("Authorization", "Bearer "+sessionKey)
	res = httptest.NewRecorder()
	app.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("linuxdo reset key status = %d body = %s", res.Code, res.Body.String())
	}
	if err := json.Unmarshal(res.Body.Bytes(), &created); err != nil {
		t.Fatalf("reset key json: %v", err)
	}
	item, _ = created["item"].(map[string]any)
	resetKey, _ := created["key"].(string)
	if item["id"] != firstID || resetKey == "" || resetKey == firstKey {
		t.Fatalf("reset key did not rotate in place: item=%#v key=%q first=%q", item, resetKey, firstKey)
	}
	if app.auth.Authenticate(firstKey) != nil {
		t.Fatal("old Linuxdo API key still authenticated after reset")
	}

	req = httptest.NewRequest(http.MethodGet, "/api/auth/users", nil)
	req.Header.Set("Authorization", "Bearer "+sessionKey)
	res = httptest.NewRecorder()
	app.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("linuxdo list keys status = %d body = %s", res.Code, res.Body.String())
	}
	if err := json.Unmarshal(res.Body.Bytes(), &list); err != nil {
		t.Fatalf("list keys json: %v", err)
	}
	if items := logItems(list); len(items) != 1 || items[0]["owner_id"] != owner.ID {
		t.Fatalf("linuxdo scoped list = %#v", list)
	}

	req = httptest.NewRequest(http.MethodGet, "/api/auth/users", nil)
	req.Header.Set("Authorization", adminAuthHeader(t, app))
	res = httptest.NewRecorder()
	app.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("admin list keys status = %d body = %s", res.Code, res.Body.String())
	}
	if err := json.Unmarshal(res.Body.Bytes(), &list); err != nil {
		t.Fatalf("admin list json: %v", err)
	}
	if items := logItems(list); len(items) != 2 {
		t.Fatalf("admin should see all API keys, got %#v", list)
	}

	_, unownedKey, err := app.auth.CreateAPIKey(service.AuthRoleUser, "legacy user", service.AuthOwner{})
	if err != nil {
		t.Fatalf("CreateAPIKey(unowned) error = %v", err)
	}
	req = httptest.NewRequest(http.MethodPost, "/api/auth/users", strings.NewReader(`{"name":"should fail"}`))
	req.Header.Set("Authorization", "Bearer "+unownedKey)
	res = httptest.NewRecorder()
	app.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusForbidden {
		t.Fatalf("unowned user key manage status = %d body = %s", res.Code, res.Body.String())
	}
}

func TestProfileAPIKeyIsPersonalAndPermissionIndependent(t *testing.T) {
	app := newTestApp(t)
	defer app.Close()

	user, _, err := app.auth.RegisterPasswordUser("alice", "Password123", "Alice")
	if err != nil {
		t.Fatalf("RegisterPasswordUser() error = %v", err)
	}
	role, err := app.auth.CreateRole(map[string]any{
		"name":            "creative only",
		"menu_paths":      []string{"/image"},
		"api_permissions": []string{service.APIPermissionKey("GET", "/v1/models")},
	})
	if err != nil {
		t.Fatalf("CreateRole() error = %v", err)
	}
	if updated := app.auth.UpdateUser(user.ID, map[string]any{"role_id": role["id"]}); updated == nil {
		t.Fatal("UpdateUser(role) returned nil")
	}
	_, userSession, err := app.auth.LoginPassword("alice", "Password123")
	if err != nil {
		t.Fatalf("LoginPassword(user) error = %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/auth/users", nil)
	req.Header.Set("Authorization", "Bearer "+userSession)
	res := httptest.NewRecorder()
	app.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusForbidden {
		t.Fatalf("restricted user /api/auth/users status = %d body = %s", res.Code, res.Body.String())
	}

	req = httptest.NewRequest(http.MethodGet, "/api/profile/api-key", nil)
	req.Header.Set("Authorization", "Bearer "+userSession)
	res = httptest.NewRecorder()
	app.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("profile key list status = %d body = %s", res.Code, res.Body.String())
	}
	var list map[string]any
	if err := json.Unmarshal(res.Body.Bytes(), &list); err != nil {
		t.Fatalf("profile key list json: %v", err)
	}
	if items := logItems(list); len(items) != 0 {
		t.Fatalf("new profile key list should be empty: %#v", list)
	}

	req = httptest.NewRequest(http.MethodPost, "/api/profile/api-key", strings.NewReader(`{"name":"Alice API"}`))
	req.Header.Set("Authorization", "Bearer "+userSession)
	res = httptest.NewRecorder()
	app.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("profile key create status = %d body = %s", res.Code, res.Body.String())
	}
	var created map[string]any
	if err := json.Unmarshal(res.Body.Bytes(), &created); err != nil {
		t.Fatalf("profile key create json: %v", err)
	}
	item, _ := created["item"].(map[string]any)
	firstID, _ := item["id"].(string)
	firstKey, _ := created["key"].(string)
	if firstID == "" || firstKey == "" || item["owner_id"] != user.ID || item["role"] != service.AuthRoleUser {
		t.Fatalf("profile key create body = %#v", created)
	}

	req = httptest.NewRequest(http.MethodPost, "/api/profile/api-key", strings.NewReader(`{"name":"Alice API rotated"}`))
	req.Header.Set("Authorization", "Bearer "+userSession)
	res = httptest.NewRecorder()
	app.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("profile key rotate status = %d body = %s", res.Code, res.Body.String())
	}
	if err := json.Unmarshal(res.Body.Bytes(), &created); err != nil {
		t.Fatalf("profile key rotate json: %v", err)
	}
	item, _ = created["item"].(map[string]any)
	rotatedKey, _ := created["key"].(string)
	if item["id"] != firstID || rotatedKey == "" || rotatedKey == firstKey {
		t.Fatalf("profile key rotate body = %#v first=%q", created, firstKey)
	}
	if app.auth.Authenticate(firstKey) != nil {
		t.Fatal("old profile API key still authenticated after rotation")
	}
	if identity := app.auth.Authenticate(rotatedKey); identity == nil || identity.ID != user.ID || identity.RoleID != role["id"] {
		t.Fatalf("rotated profile API identity = %#v", identity)
	}

	req = httptest.NewRequest(http.MethodPost, "/api/profile/api-key", strings.NewReader(`{"name":"Admin API"}`))
	req.Header.Set("Authorization", adminAuthHeader(t, app))
	res = httptest.NewRecorder()
	app.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("admin profile key create status = %d body = %s", res.Code, res.Body.String())
	}
	if err := json.Unmarshal(res.Body.Bytes(), &created); err != nil {
		t.Fatalf("admin profile key create json: %v", err)
	}
	adminKey, _ := created["key"].(string)
	item, _ = created["item"].(map[string]any)
	if adminKey == "" || item["role"] != service.AuthRoleAdmin || item["owner_id"] != service.AuthRoleAdmin {
		t.Fatalf("admin profile key body = %#v", created)
	}
	if identity := app.auth.Authenticate(adminKey); identity == nil || identity.Role != service.AuthRoleAdmin {
		t.Fatalf("admin profile API identity = %#v", identity)
	}
}

func TestProfilePromptFavoritesArePersonalAndPermissionIndependent(t *testing.T) {
	app := newTestApp(t)
	defer app.Close()

	user, _, err := app.auth.RegisterPasswordUser("alice", "Password123", "Alice")
	if err != nil {
		t.Fatalf("RegisterPasswordUser(alice) error = %v", err)
	}
	role, err := app.auth.CreateRole(map[string]any{
		"name":            "models only",
		"menu_paths":      []string{"/image"},
		"api_permissions": []string{service.APIPermissionKey("GET", "/v1/models")},
	})
	if err != nil {
		t.Fatalf("CreateRole() error = %v", err)
	}
	if updated := app.auth.UpdateUser(user.ID, map[string]any{"role_id": role["id"]}); updated == nil {
		t.Fatal("UpdateUser(role) returned nil")
	}
	_, aliceToken, err := app.auth.LoginPassword("alice", "Password123")
	if err != nil {
		t.Fatalf("LoginPassword(alice) error = %v", err)
	}

	other, otherToken, err := app.auth.RegisterPasswordUser("bob", "Password123", "Bob")
	if err != nil {
		t.Fatalf("RegisterPasswordUser(bob) error = %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/profile/prompt-favorites", nil)
	req.Header.Set("Authorization", "Bearer "+aliceToken)
	res := httptest.NewRecorder()
	app.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("initial list status = %d body = %s", res.Code, res.Body.String())
	}
	var list map[string]any
	if err := json.Unmarshal(res.Body.Bytes(), &list); err != nil {
		t.Fatalf("initial list json: %v", err)
	}
	if items := logItems(list); len(items) != 0 {
		t.Fatalf("initial list should be empty: %#v", list)
	}

	body := `{
		"prompt_id":"banana-prompt-quicker:title:author:1",
		"source":"banana-prompt-quicker",
		"title":"Prompt A",
		"preview":"https://example.test/a.png",
		"reference_image_urls":["https://example.test/ref.png"],
		"prompt":"draw a cat",
		"author":"Alice",
		"mode":"edit",
		"category":"Animals",
		"sub_category":"Cats",
		"source_label":"banana-prompt-quicker",
		"is_nsfw":false,
		"localizations":{"zh-CN":{"title":"提示词 A","prompt":"画猫","category":"动物","sub_category":"猫"}}
	}`
	req = httptest.NewRequest(http.MethodPost, "/api/profile/prompt-favorites", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+aliceToken)
	res = httptest.NewRecorder()
	app.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("create favorite status = %d body = %s", res.Code, res.Body.String())
	}
	var created map[string]any
	if err := json.Unmarshal(res.Body.Bytes(), &created); err != nil {
		t.Fatalf("create favorite json: %v", err)
	}
	item, _ := created["item"].(map[string]any)
	favoriteID, _ := item["id"].(string)
	if favoriteID == "" || item["title"] != "Prompt A" || item["prompt_id"] != "banana-prompt-quicker:title:author:1" {
		t.Fatalf("create favorite body = %#v", created)
	}
	if items := logItems(created); len(items) != 1 {
		t.Fatalf("created items length = %d body = %#v", len(items), created)
	}

	req = httptest.NewRequest(http.MethodPost, "/api/profile/prompt-favorites", strings.NewReader(strings.Replace(body, "Prompt A", "Prompt A Updated", 1)))
	req.Header.Set("Authorization", "Bearer "+aliceToken)
	res = httptest.NewRecorder()
	app.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("duplicate favorite status = %d body = %s", res.Code, res.Body.String())
	}
	if err := json.Unmarshal(res.Body.Bytes(), &created); err != nil {
		t.Fatalf("duplicate favorite json: %v", err)
	}
	if items := logItems(created); len(items) != 1 || items[0]["title"] != "Prompt A Updated" {
		t.Fatalf("duplicate favorite should update in place: %#v", created)
	}

	req = httptest.NewRequest(http.MethodGet, "/api/profile/prompt-favorites", nil)
	req.Header.Set("Authorization", "Bearer "+otherToken)
	res = httptest.NewRecorder()
	app.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("other list status = %d body = %s", res.Code, res.Body.String())
	}
	if err := json.Unmarshal(res.Body.Bytes(), &list); err != nil {
		t.Fatalf("other list json: %v", err)
	}
	if items := logItems(list); len(items) != 0 {
		t.Fatalf("other user saw favorites, user=%s other=%s list=%#v", user.ID, other.ID, list)
	}

	req = httptest.NewRequest(http.MethodDelete, "/api/profile/prompt-favorites/"+favoriteID, nil)
	req.Header.Set("Authorization", "Bearer "+otherToken)
	res = httptest.NewRecorder()
	app.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusNotFound {
		t.Fatalf("other delete status = %d body = %s", res.Code, res.Body.String())
	}

	req = httptest.NewRequest(http.MethodDelete, "/api/profile/prompt-favorites/"+favoriteID, nil)
	req.Header.Set("Authorization", "Bearer "+aliceToken)
	res = httptest.NewRecorder()
	app.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("delete favorite status = %d body = %s", res.Code, res.Body.String())
	}
	if err := json.Unmarshal(res.Body.Bytes(), &list); err != nil {
		t.Fatalf("delete favorite json: %v", err)
	}
	if items := logItems(list); len(items) != 0 {
		t.Fatalf("favorite remained after delete: %#v", list)
	}

	_, unownedKey, err := app.auth.CreateAPIKey(service.AuthRoleUser, "legacy user", service.AuthOwner{})
	if err != nil {
		t.Fatalf("CreateAPIKey(unowned) error = %v", err)
	}
	req = httptest.NewRequest(http.MethodGet, "/api/profile/prompt-favorites", nil)
	req.Header.Set("Authorization", "Bearer "+unownedKey)
	res = httptest.NewRecorder()
	app.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusForbidden {
		t.Fatalf("unowned key list status = %d body = %s", res.Code, res.Body.String())
	}
}

func TestAdminUsersManageLinuxDoUsers(t *testing.T) {
	app := newTestApp(t)
	defer app.Close()

	owner := service.AuthOwner{ID: "linuxdo:123", Name: "linuxdo_user", Provider: service.AuthProviderLinuxDo, LinuxDoLevel: "3"}
	_, sessionKey, err := app.auth.UpsertLinuxDoSession(owner)
	if err != nil {
		t.Fatalf("UpsertLinuxDoSession() error = %v", err)
	}
	_, ownerAPIKey, err := app.auth.UpsertAPIKeyForOwner("", owner)
	if err != nil {
		t.Fatalf("UpsertAPIKeyForOwner() error = %v", err)
	}
	local, localKey, err := app.auth.CreateAPIKey(service.AuthRoleUser, "local user", service.AuthOwner{})
	if err != nil {
		t.Fatalf("CreateAPIKey(local) error = %v", err)
	}
	localID, _ := local["id"].(string)
	app.logs.Add("文生图调用完成", map[string]any{
		"subject_id":  owner.ID,
		"key_id":      "linuxdo-session",
		"status":      "success",
		"endpoint":    "/v1/images/generations",
		"duration_ms": 120,
		"urls":        []string{"https://example.test/a.png", "https://example.test/b.png"},
	})
	app.logs.Add("文生图调用失败", map[string]any{
		"subject_id": owner.ID,
		"key_id":     "linuxdo-session",
		"status":     "failed",
		"endpoint":   "/v1/images/generations",
	})
	app.logs.Add("图生图调用完成", map[string]any{
		"key_id":   localID,
		"status":   "success",
		"endpoint": "/api/creation-tasks/image-edits",
	})

	req := httptest.NewRequest(http.MethodGet, "/api/admin/users", nil)
	req.Header.Set("Authorization", "Bearer "+sessionKey)
	res := httptest.NewRecorder()
	app.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusForbidden {
		t.Fatalf("linuxdo admin users status = %d body = %s", res.Code, res.Body.String())
	}

	req = httptest.NewRequest(http.MethodGet, "/api/admin/users", nil)
	req.Header.Set("Authorization", adminAuthHeader(t, app))
	res = httptest.NewRecorder()
	app.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("admin users status = %d body = %s", res.Code, res.Body.String())
	}
	var list map[string]any
	if err := json.Unmarshal(res.Body.Bytes(), &list); err != nil {
		t.Fatalf("admin users json: %v", err)
	}
	linuxdoUser := findHTTPItem(logItems(list), owner.ID)
	if linuxdoUser == nil || linuxdoUser["provider"] != service.AuthProviderLinuxDo || linuxdoUser["has_session"] != true || linuxdoUser["has_api_key"] != true {
		t.Fatalf("linuxdo managed user = %#v in %#v", linuxdoUser, list)
	}
	if linuxdoUser["linuxdo_level"] != "3" {
		t.Fatalf("linuxdo level = %#v", linuxdoUser)
	}
	localUser := findHTTPItem(logItems(list), localID)
	if localUser == nil || localUser["provider"] != service.AuthProviderLocal || localUser["has_api_key"] != true {
		t.Fatalf("local managed user = %#v in %#v", localUser, list)
	}
	if linuxdoUser["call_count"] != float64(2) || linuxdoUser["success_count"] != float64(1) || linuxdoUser["failure_count"] != float64(1) || linuxdoUser["quota_used"] != float64(2) {
		t.Fatalf("linuxdo usage stats = %#v", linuxdoUser)
	}
	if curve, ok := linuxdoUser["usage_curve"].([]any); !ok || len(curve) != 14 {
		t.Fatalf("linuxdo usage curve = %#v", linuxdoUser["usage_curve"])
	}
	if localUser["call_count"] != float64(1) || localUser["quota_used"] != float64(1) {
		t.Fatalf("local usage stats = %#v", localUser)
	}

	req = httptest.NewRequest(http.MethodPost, "/api/admin/users", strings.NewReader(`{"username":"created_local","name":"Created Local","password":"Password123","enabled":true}`))
	req.Header.Set("Authorization", adminAuthHeader(t, app))
	res = httptest.NewRecorder()
	app.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("create password user status = %d body = %s", res.Code, res.Body.String())
	}
	var created map[string]any
	if err := json.Unmarshal(res.Body.Bytes(), &created); err != nil {
		t.Fatalf("create password user json: %v", err)
	}
	createdItem, _ := created["item"].(map[string]any)
	if createdItem["username"] != "created_local" || createdItem["name"] != "Created Local" || createdItem["has_api_key"] != false || createdItem["has_session"] != false {
		t.Fatalf("create password user body = %#v", created)
	}
	if _, ok := created["key"]; ok {
		t.Fatalf("password user creation should not issue an API key: %#v", created)
	}
	createdID, _ := createdItem["id"].(string)
	createdPath := "/api/admin/users/" + url.PathEscape(createdID)

	req = httptest.NewRequest(http.MethodPost, "/auth/login", strings.NewReader(`{"username":"created_local","password":"Password123"}`))
	res = httptest.NewRecorder()
	app.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("created password user login status = %d body = %s", res.Code, res.Body.String())
	}
	var createdLogin map[string]any
	if err := json.Unmarshal(res.Body.Bytes(), &createdLogin); err != nil {
		t.Fatalf("created password user login json: %v", err)
	}
	if createdLogin["subject_id"] != createdID || createdLogin["name"] != "Created Local" {
		t.Fatalf("created password user login body = %#v", createdLogin)
	}

	req = httptest.NewRequest(http.MethodGet, createdPath+"/key", nil)
	req.Header.Set("Authorization", adminAuthHeader(t, app))
	res = httptest.NewRecorder()
	app.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusNotFound {
		t.Fatalf("initial password user key reveal status = %d body = %s", res.Code, res.Body.String())
	}

	req = httptest.NewRequest(http.MethodPost, createdPath+"/reset-key", strings.NewReader(`{"name":"rotated local"}`))
	req.Header.Set("Authorization", adminAuthHeader(t, app))
	res = httptest.NewRecorder()
	app.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("reset local managed key status = %d body = %s", res.Code, res.Body.String())
	}
	var reset map[string]any
	if err := json.Unmarshal(res.Body.Bytes(), &reset); err != nil {
		t.Fatalf("reset local managed key json: %v", err)
	}
	rotatedLocalKey, _ := reset["key"].(string)
	if rotatedLocalKey == "" {
		t.Fatalf("reset local managed key body = %#v", reset)
	}
	if identity := app.auth.Authenticate(rotatedLocalKey); identity == nil || identity.ID != createdID {
		t.Fatalf("rotated local managed key identity = %#v", identity)
	}

	ownerPath := "/api/admin/users/" + url.PathEscape(owner.ID)
	req = httptest.NewRequest(http.MethodPost, ownerPath+"/reset-key", strings.NewReader(`{"name":"managed token"}`))
	req.Header.Set("Authorization", adminAuthHeader(t, app))
	res = httptest.NewRecorder()
	app.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusForbidden {
		t.Fatalf("reset linuxdo managed key status = %d body = %s", res.Code, res.Body.String())
	}

	req = httptest.NewRequest(http.MethodGet, ownerPath+"/key", nil)
	req.Header.Set("Authorization", adminAuthHeader(t, app))
	res = httptest.NewRecorder()
	app.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusForbidden {
		t.Fatalf("reveal linuxdo managed key status = %d body = %s", res.Code, res.Body.String())
	}

	req = httptest.NewRequest(http.MethodPost, ownerPath, strings.NewReader(`{"enabled":false}`))
	req.Header.Set("Authorization", adminAuthHeader(t, app))
	res = httptest.NewRecorder()
	app.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("disable managed user status = %d body = %s", res.Code, res.Body.String())
	}
	if app.auth.Authenticate(sessionKey) != nil || app.auth.Authenticate(ownerAPIKey) != nil {
		t.Fatal("disabled linuxdo user credentials still authenticate")
	}
	if app.auth.Authenticate(localKey) == nil {
		t.Fatal("disabling linuxdo user should not affect local user")
	}
	disabledLoginItem, disabledLoginKey, err := app.auth.UpsertLinuxDoSession(owner)
	if err != nil {
		t.Fatalf("UpsertLinuxDoSession(disabled) error = %v", err)
	}
	if disabledLoginItem["enabled"] != false {
		t.Fatalf("disabled linuxdo login item = %#v", disabledLoginItem)
	}
	if app.auth.Authenticate(disabledLoginKey) != nil {
		t.Fatal("disabled linuxdo user authenticated after a new login")
	}

	req = httptest.NewRequest(http.MethodPost, ownerPath, strings.NewReader(`{"enabled":true}`))
	req.Header.Set("Authorization", adminAuthHeader(t, app))
	res = httptest.NewRecorder()
	app.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("enable managed user status = %d body = %s", res.Code, res.Body.String())
	}
	if app.auth.Authenticate(disabledLoginKey) == nil || app.auth.Authenticate(ownerAPIKey) == nil {
		t.Fatal("enabled linuxdo user credentials should authenticate")
	}

	req = httptest.NewRequest(http.MethodDelete, ownerPath, nil)
	req.Header.Set("Authorization", adminAuthHeader(t, app))
	res = httptest.NewRecorder()
	app.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("delete managed user status = %d body = %s", res.Code, res.Body.String())
	}
	if app.auth.Authenticate(disabledLoginKey) != nil || app.auth.Authenticate(ownerAPIKey) != nil {
		t.Fatal("deleted linuxdo user credentials still authenticate")
	}
	if app.auth.Authenticate(localKey) == nil {
		t.Fatal("deleting linuxdo user should not affect local user")
	}
	if err := json.Unmarshal(res.Body.Bytes(), &list); err != nil {
		t.Fatalf("delete managed user json: %v", err)
	}
	if findHTTPItem(logItems(list), owner.ID) != nil {
		t.Fatalf("deleted linuxdo user still listed: %#v", list)
	}
}

func TestLinuxDoOAuthCallbackCreatesSession(t *testing.T) {
	oauthServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/token":
			if err := r.ParseForm(); err != nil {
				t.Fatalf("ParseForm() error = %v", err)
			}
			if r.Form.Get("code") != "oauth-code" || r.Form.Get("client_id") != "client-id" || r.Form.Get("client_secret") != "client-secret" {
				t.Fatalf("unexpected token form = %#v", r.Form)
			}
			util.WriteJSON(w, http.StatusOK, map[string]any{"access_token": "linuxdo-access", "token_type": "Bearer", "expires_in": 3600})
		case "/user":
			if r.Header.Get("Authorization") != "Bearer linuxdo-access" {
				t.Fatalf("userinfo authorization = %q", r.Header.Get("Authorization"))
			}
			util.WriteJSON(w, http.StatusOK, map[string]any{"id": 123, "username": "linuxdo_user", "trust_level": 2})
		default:
			http.NotFound(w, r)
		}
	}))
	defer oauthServer.Close()

	t.Setenv("CHATGPT2API_LINUXDO_ENABLED", "true")
	t.Setenv("CHATGPT2API_LINUXDO_CLIENT_ID", "client-id")
	t.Setenv("CHATGPT2API_LINUXDO_CLIENT_SECRET", "client-secret")
	t.Setenv("CHATGPT2API_LINUXDO_AUTHORIZE_URL", oauthServer.URL+"/authorize")
	t.Setenv("CHATGPT2API_LINUXDO_TOKEN_URL", oauthServer.URL+"/token")
	t.Setenv("CHATGPT2API_LINUXDO_USERINFO_URL", oauthServer.URL+"/user")
	t.Setenv("CHATGPT2API_LINUXDO_REDIRECT_URL", "http://chatgpt2api.test/auth/linuxdo/oauth/callback")
	t.Setenv("CHATGPT2API_LINUXDO_FRONTEND_REDIRECT_URL", "/auth/linuxdo/callback")

	app := newTestApp(t)
	defer app.Close()

	req := httptest.NewRequest(http.MethodGet, "/auth/linuxdo/start?redirect=/settings", nil)
	res := httptest.NewRecorder()
	app.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusFound {
		t.Fatalf("start status = %d body = %s", res.Code, res.Body.String())
	}
	authorizeURL, err := url.Parse(res.Header().Get("Location"))
	if err != nil {
		t.Fatalf("parse authorize location: %v", err)
	}
	state := authorizeURL.Query().Get("state")
	if state == "" || authorizeURL.Query().Get("client_id") != "client-id" {
		t.Fatalf("authorize location = %s", authorizeURL.String())
	}

	req = httptest.NewRequest(http.MethodGet, "/auth/linuxdo/oauth/callback?code=oauth-code&state="+url.QueryEscape(state), nil)
	for _, cookie := range res.Result().Cookies() {
		req.AddCookie(cookie)
	}
	res = httptest.NewRecorder()
	app.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusFound {
		t.Fatalf("callback status = %d body = %s", res.Code, res.Body.String())
	}
	callbackLocation := res.Header().Get("Location")
	if strings.Contains(callbackLocation, "%25") {
		t.Fatalf("callback location double-encoded fragment values: %s", callbackLocation)
	}
	callbackURL, err := url.Parse(callbackLocation)
	if err != nil {
		t.Fatalf("parse callback location: %v", err)
	}
	fragment, err := url.ParseQuery(callbackURL.Fragment)
	if err != nil {
		t.Fatalf("parse callback fragment: %v", err)
	}
	sessionKey := fragment.Get("key")
	if sessionKey == "" || fragment.Get("subject_id") != "linuxdo:123" || fragment.Get("redirect") != "/settings" {
		t.Fatalf("callback fragment = %#v", fragment)
	}

	req = httptest.NewRequest(http.MethodGet, "/auth/session", nil)
	req.Header.Set("Authorization", "Bearer "+sessionKey)
	res = httptest.NewRecorder()
	app.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("login with linuxdo session status = %d body = %s", res.Code, res.Body.String())
	}
	var login map[string]any
	if err := json.Unmarshal(res.Body.Bytes(), &login); err != nil {
		t.Fatalf("login json: %v", err)
	}
	if login["subject_id"] != "linuxdo:123" || login["provider"] != service.AuthProviderLinuxDo || login["name"] != "linuxdo_user" {
		t.Fatalf("login response = %#v", login)
	}

	req = httptest.NewRequest(http.MethodGet, "/api/admin/users", nil)
	req.Header.Set("Authorization", adminAuthHeader(t, app))
	res = httptest.NewRecorder()
	app.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("admin users after linuxdo oauth status = %d body = %s", res.Code, res.Body.String())
	}
	var users map[string]any
	if err := json.Unmarshal(res.Body.Bytes(), &users); err != nil {
		t.Fatalf("admin users json: %v", err)
	}
	linuxdoUser := findHTTPItem(logItems(users), "linuxdo:123")
	if linuxdoUser == nil || linuxdoUser["linuxdo_level"] != "2" {
		t.Fatalf("oauth linuxdo user level = %#v", linuxdoUser)
	}
}

func TestCreationTaskPollingDisablesCaching(t *testing.T) {
	app := newTestApp(t)
	defer app.Close()

	_, rawKey, err := app.auth.CreateAPIKey(service.AuthRoleUser, "frontend", service.AuthOwner{})
	if err != nil {
		t.Fatalf("CreateAPIKey() error = %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/creation-tasks?ids=missing", nil)
	req.Header.Set("Authorization", "Bearer "+rawKey)
	res := httptest.NewRecorder()
	app.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("creation task list status = %d body = %s", res.Code, res.Body.String())
	}
	if got := res.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("Cache-Control = %q, want no-store", got)
	}
	if got := res.Header().Get("Pragma"); got != "no-cache" {
		t.Fatalf("Pragma = %q, want no-cache", got)
	}
}

func TestModelsCallLogIncludesUserKeyName(t *testing.T) {
	app := newTestApp(t)
	defer app.Close()

	_, rawKey, err := app.auth.CreateAPIKey(service.AuthRoleUser, "frontend", service.AuthOwner{})
	if err != nil {
		t.Fatalf("CreateAPIKey() error = %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	req.Header.Set("Authorization", "Bearer "+rawKey)
	res := httptest.NewRecorder()
	app.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("models status = %d body = %s", res.Code, res.Body.String())
	}

	req = httptest.NewRequest(http.MethodGet, "/api/logs", nil)
	req.Header.Set("Authorization", adminAuthHeader(t, app))
	res = httptest.NewRecorder()
	app.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("logs status = %d body = %s", res.Code, res.Body.String())
	}
	var logs map[string]any
	if err := json.Unmarshal(res.Body.Bytes(), &logs); err != nil {
		t.Fatalf("logs json: %v", err)
	}
	items := logItems(logs)
	if len(items) == 0 {
		t.Fatalf("expected models call to write a log event, got %#v", logs)
	}
	item := findLogByDetails(items, map[string]any{
		"endpoint": "/v1/models",
		"outcome":  "success",
	})
	if item == nil {
		t.Fatalf("expected models call log event, got %#v", items)
	}
	if _, ok := item["type"]; ok {
		t.Fatalf("log item should not expose type: %#v", item)
	}
	detail, _ := item["detail"].(map[string]any)
	if detail["endpoint"] != "/v1/models" ||
		detail["path"] != "/v1/models" ||
		detail["method"] != http.MethodGet ||
		detail["status"] != float64(http.StatusOK) ||
		detail["outcome"] != "success" ||
		detail["key_name"] != "frontend" ||
		detail["key_role"] != "user" {
		t.Fatalf("models call log did not include user key identity: %#v", detail)
	}
}

func TestAPIAuditLogCapturesRequestMetadata(t *testing.T) {
	app := newTestApp(t)
	defer app.Close()

	req := httptest.NewRequest(http.MethodGet, "/api/settings?section=logging", nil)
	req.Header.Set("Authorization", adminAuthHeader(t, app))
	req.Header.Set("User-Agent", "chatgpt2api-test")
	req.RemoteAddr = "203.0.113.10:12345"
	res := httptest.NewRecorder()
	app.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("settings status = %d body = %s", res.Code, res.Body.String())
	}

	req = httptest.NewRequest(http.MethodGet, "/api/logs?username=admin&method=GET&status=200&summary=%2Fapi%2Fsettings", nil)
	req.Header.Set("Authorization", adminAuthHeader(t, app))
	res = httptest.NewRecorder()
	app.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("audit logs status = %d body = %s", res.Code, res.Body.String())
	}
	var logs map[string]any
	if err := json.Unmarshal(res.Body.Bytes(), &logs); err != nil {
		t.Fatalf("audit logs json: %v", err)
	}
	items := logItems(logs)
	if len(items) == 0 {
		t.Fatalf("expected audit log, got %#v", logs)
	}
	item := findLogByDetail(items, "path", "/api/settings")
	if item == nil {
		t.Fatalf("expected audit log for /api/settings, got %#v", items)
	}
	if _, ok := item["type"]; ok {
		t.Fatalf("log item should not expose type: %#v", item)
	}
	detail, _ := item["detail"].(map[string]any)
	if detail["method"] != http.MethodGet || detail["status"] != float64(http.StatusOK) || detail["log_level"] != "info" {
		t.Fatalf("unexpected audit detail = %#v", detail)
	}
	if detail["operation_type"] != "查询" || detail["subject_id"] != testAdminUsername || detail["user_agent"] != "chatgpt2api-test" {
		t.Fatalf("missing audit identity/request fields = %#v", detail)
	}
	if _, ok := detail["duration_ms"].(float64); !ok {
		t.Fatalf("duration_ms not numeric in audit detail = %#v", detail)
	}
}

func TestLogGovernanceEndpointCleansOldLogs(t *testing.T) {
	app := newTestApp(t)
	defer app.Close()

	logDir := filepath.Join(app.config.DataDir, "logs")
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		t.Fatalf("mkdir logs: %v", err)
	}
	logData := []byte(`{"time":"2000-01-01 00:00:00","type":"event","summary":"旧日志","detail":{"status":"success"}}` + "\n" +
		`{"time":"` + time.Now().Format("2006-01-02 15:04:05") + `","type":"event","summary":"新日志","detail":{"status":200}}` + "\n")
	if err := os.WriteFile(filepath.Join(logDir, "events.jsonl"), logData, 0o644); err != nil {
		t.Fatalf("write log data: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/logs/governance", nil)
	req.Header.Set("Authorization", adminAuthHeader(t, app))
	res := httptest.NewRecorder()
	app.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("governance status = %d body = %s", res.Code, res.Body.String())
	}
	var payload map[string]any
	if err := json.Unmarshal(res.Body.Bytes(), &payload); err != nil {
		t.Fatalf("governance json: %v", err)
	}
	governance, _ := payload["governance"].(map[string]any)
	if governance["total"] != float64(2) {
		t.Fatalf("governance total = %#v, want 2 in %#v", governance["total"], payload)
	}

	req = httptest.NewRequest(http.MethodPost, "/api/logs/governance", strings.NewReader(`{"retention_days":1}`))
	req.Header.Set("Authorization", adminAuthHeader(t, app))
	res = httptest.NewRecorder()
	app.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("cleanup status = %d body = %s", res.Code, res.Body.String())
	}
	payload = map[string]any{}
	if err := json.Unmarshal(res.Body.Bytes(), &payload); err != nil {
		t.Fatalf("cleanup json: %v", err)
	}
	cleanup, _ := payload["cleanup"].(map[string]any)
	if cleanup["deleted"] != float64(1) || cleanup["remaining"] != float64(2) {
		t.Fatalf("cleanup result = %#v, want deleted 1 remaining 2", cleanup)
	}
}

func logItems(payload map[string]any) []map[string]any {
	rawItems, _ := payload["items"].([]any)
	items := make([]map[string]any, 0, len(rawItems))
	for _, raw := range rawItems {
		if item, ok := raw.(map[string]any); ok {
			items = append(items, item)
		}
	}
	return items
}

func isSPAHTML(body string) bool {
	return strings.Contains(body, `id="root"`) ||
		strings.Contains(body, `id=root`) ||
		strings.Contains(body, `id="app"`) ||
		strings.Contains(body, `id=app`)
}

func findLogBySummary(items []map[string]any, summary string) map[string]any {
	for _, item := range items {
		if item["summary"] == summary {
			return item
		}
	}
	return nil
}

func findHTTPItem(items []map[string]any, id string) map[string]any {
	for _, item := range items {
		if item["id"] == id {
			return item
		}
	}
	return nil
}

func findResponseCookie(res *http.Response, name string) *http.Cookie {
	for _, cookie := range res.Cookies() {
		if cookie.Name == name {
			return cookie
		}
	}
	return nil
}

func findLogByDetail(items []map[string]any, key, value string) map[string]any {
	return findLogByDetails(items, map[string]any{key: value})
}

func findLogByDetails(items []map[string]any, values map[string]any) map[string]any {
	for _, item := range items {
		detail, _ := item["detail"].(map[string]any)
		matches := true
		for key, value := range values {
			if detail[key] != value {
				matches = false
				break
			}
		}
		if matches {
			return item
		}
	}
	return nil
}

const (
	testAdminUsername = "admin"
	testAdminPassword = "AdminPass123!"
)

func adminAuthHeader(t *testing.T, app *App) string {
	t.Helper()
	identity, token, err := app.auth.LoginPassword(testAdminUsername, testAdminPassword)
	if err != nil {
		t.Fatalf("admin LoginPassword() error = %v", err)
	}
	if identity == nil || identity.Role != service.AuthRoleAdmin || token == "" {
		t.Fatalf("admin LoginPassword() identity=%#v token=%q", identity, token)
	}
	return "Bearer " + token
}

func newTestApp(t *testing.T) *App {
	t.Helper()
	root := t.TempDir()
	t.Setenv("CHATGPT2API_ROOT", root)
	t.Setenv("CHATGPT2API_ADMIN_USERNAME", testAdminUsername)
	t.Setenv("CHATGPT2API_ADMIN_PASSWORD", testAdminPassword)
	unsetTestEnv(t, "CHATGPT2API_REGISTRATION_ENABLED")
	t.Setenv("STORAGE_BACKEND", "json")
	t.Setenv("DATABASE_URL", "")
	app, err := NewApp()
	if err != nil {
		t.Fatalf("NewApp() error = %v", err)
	}
	app.engine.ListModelsFunc = func(context.Context) (map[string]any, error) {
		return map[string]any{"object": "list", "data": []map[string]any{}}, nil
	}
	return app
}

func unsetTestEnv(t *testing.T, key string) {
	t.Helper()
	original, existed := os.LookupEnv(key)
	if err := os.Unsetenv(key); err != nil {
		t.Fatalf("Unsetenv(%s): %v", key, err)
	}
	t.Cleanup(func() {
		if existed {
			_ = os.Setenv(key, original)
			return
		}
		_ = os.Unsetenv(key)
	})
}

func writeHTTPTestPNG(path string) error {
	file, err := os.Create(path)
	if err != nil {
		return err
	}
	defer file.Close()
	return encodeHTTPTestPNG(file)
}

func encodeHTTPTestPNG(file interface {
	Write([]byte) (int, error)
}) error {
	img := image.NewRGBA(image.Rect(0, 0, 12, 12))
	for y := 0; y < 12; y++ {
		for x := 0; x < 12; x++ {
			img.Set(x, y, color.RGBA{R: uint8(x * 16), G: uint8(y * 16), B: 180, A: 255})
		}
	}
	return png.Encode(file, img)
}
