package protocol

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"chatgpt2api/internal/backend"
	"chatgpt2api/internal/service"
	"chatgpt2api/internal/storage"
	"chatgpt2api/internal/util"

	"github.com/HugoSmits86/nativewebp"
)

type testProtocolImageConfig struct {
	root string
}

func (c testProtocolImageConfig) ImagesDir() string {
	path := filepath.Join(c.root, "images")
	_ = os.MkdirAll(path, 0o755)
	return path
}

func (c testProtocolImageConfig) ImageMetadataDir() string {
	path := filepath.Join(c.root, "image_metadata")
	_ = os.MkdirAll(path, 0o755)
	return path
}

func (c testProtocolImageConfig) BaseURL() string {
	return "https://example.test"
}

func (c testProtocolImageConfig) CleanupOldImages() int {
	return 0
}

type testProtocolAccountConfig struct{}

func (testProtocolAccountConfig) AutoRemoveInvalidAccounts() bool     { return false }
func (testProtocolAccountConfig) AutoRemoveRateLimitedAccounts() bool { return false }
func (testProtocolAccountConfig) Proxy() string                       { return "" }

func TestFormatImageResultStoresOwnerName(t *testing.T) {
	config := testProtocolImageConfig{root: t.TempDir()}
	engine := &Engine{Config: config}
	imageData := base64.StdEncoding.EncodeToString([]byte("png-bytes"))

	result := engine.FormatImageResult(
		[]map[string]any{{"b64_json": imageData}},
		"draw",
		"url",
		"https://example.test",
		"linuxdo:41499",
		"Cassianvale",
		123,
		"",
	)
	items, _ := result["data"].([]map[string]any)
	if len(items) != 1 {
		t.Fatalf("FormatImageResult() data = %#v", result["data"])
	}
	imageURL, _ := items[0]["url"].(string)
	rel := strings.TrimPrefix(imageURL, "https://example.test/images/")
	if rel == imageURL || rel == "" {
		t.Fatalf("image url = %q", imageURL)
	}

	data, err := os.ReadFile(filepath.Join(config.ImageMetadataDir(), filepath.FromSlash(rel)+".json"))
	if err != nil {
		t.Fatalf("ReadFile(metadata) error = %v", err)
	}
	var meta map[string]any
	if err := json.Unmarshal(data, &meta); err != nil {
		t.Fatalf("Unmarshal(metadata) error = %v", err)
	}
	if meta["owner_id"] != "linuxdo:41499" || meta["owner_name"] != "Cassianvale" {
		t.Fatalf("metadata = %#v", meta)
	}
}

func TestHighResolutionImageRequestsCanUseFreeAccounts(t *testing.T) {
	dir := t.TempDir()
	accounts := service.NewAccountService(
		storage.NewJSONBackend(filepath.Join(dir, "accounts.json"), filepath.Join(dir, "auth_keys.json")),
		testProtocolAccountConfig{},
		nil,
		service.NewLogService(dir),
	)
	accounts.AddAccounts([]string{"free-token"})
	accounts.UpdateAccount("free-token", map[string]any{
		"status":     "正常",
		"type":       "Free",
		"quota":      1,
		"checked_at": time.Now().UTC().Format(time.RFC3339Nano),
	})

	called := false
	engine := &Engine{
		Accounts: accounts,
		Proxy:    service.NewProxyService(testProtocolAccountConfig{}),
		StreamImageOutputsFunc: func(ctx context.Context, client *backend.Client, request ConversationRequest, index, total int) (<-chan ImageOutput, <-chan error) {
			if !request.RequirePaidAccount {
				t.Errorf("RequirePaidAccount = false, want high-resolution marker retained")
			}
			called = true
			out := make(chan ImageOutput, 1)
			errCh := make(chan error, 1)
			out <- ImageOutput{Kind: "result", Data: []map[string]any{{"b64_json": base64.StdEncoding.EncodeToString([]byte("png-bytes"))}}}
			close(out)
			errCh <- nil
			close(errCh)
			return out, errCh
		},
	}

	outputs, errCh := engine.StreamImageOutputsWithPool(context.Background(), ConversationRequest{
		Model:              "gpt-image-2",
		Prompt:             "draw",
		N:                  1,
		Size:               "2048x2048",
		RequirePaidAccount: true,
	})
	var got []ImageOutput
	for output := range outputs {
		got = append(got, output)
	}
	if err := <-errCh; err != nil {
		t.Fatalf("StreamImageOutputsWithPool() error = %v", err)
	}
	if !called || len(got) != 1 || got[0].Kind != "result" {
		t.Fatalf("outputs called=%v got=%#v, want one result from free account", called, got)
	}
}

func TestImagePoolStopsAfterTransientNoImageResultRetryBudget(t *testing.T) {
	dir := t.TempDir()
	accounts := service.NewAccountService(
		storage.NewJSONBackend(filepath.Join(dir, "accounts.json"), filepath.Join(dir, "auth_keys.json")),
		testProtocolAccountConfig{},
		service.NewProxyService(testProtocolAccountConfig{}),
		service.NewLogService(dir),
	)
	tokens := []string{"token-1", "token-2", "token-3", "token-4", "token-5", "token-6"}
	accounts.AddAccounts(tokens)
	for _, token := range tokens {
		accounts.UpdateAccount(token, map[string]any{
			"status":     "正常",
			"type":       "Free",
			"quota":      1,
			"checked_at": time.Now().UTC().Format(time.RFC3339Nano),
		})
	}

	calls := 0
	engine := &Engine{
		Accounts: accounts,
		Proxy:    service.NewProxyService(testProtocolAccountConfig{}),
		StreamImageOutputsFunc: func(ctx context.Context, client *backend.Client, request ConversationRequest, index, total int) (<-chan ImageOutput, <-chan error) {
			calls++
			out := make(chan ImageOutput)
			errCh := make(chan error, 1)
			close(out)
			errCh <- fmt.Errorf("image generation produced no image result")
			close(errCh)
			return out, errCh
		},
	}

	outputs, errCh := engine.StreamImageOutputsWithPool(context.Background(), ConversationRequest{
		Model:   "gpt-image-2",
		Prompt:  "draw",
		N:       1,
		Size:    "1024x1024",
		Quality: "high",
	})
	for range outputs {
	}
	if err := <-errCh; err == nil || !strings.Contains(err.Error(), "临时网络异常") {
		t.Fatalf("err = %v, want transient cooldown message", err)
	}
	if calls != maxTransientImageStreamAttempts+1 {
		t.Fatalf("calls = %d, want retry budget %d", calls, maxTransientImageStreamAttempts+1)
	}
}

func TestImagePoolUsesShorterTransientRetryBudgetForMultiImageRequests(t *testing.T) {
	dir := t.TempDir()
	accounts := service.NewAccountService(
		storage.NewJSONBackend(filepath.Join(dir, "accounts.json"), filepath.Join(dir, "auth_keys.json")),
		testProtocolAccountConfig{},
		service.NewProxyService(testProtocolAccountConfig{}),
		service.NewLogService(dir),
	)
	tokens := []string{"token-1", "token-2", "token-3", "token-4"}
	accounts.AddAccounts(tokens)
	for _, token := range tokens {
		accounts.UpdateAccount(token, map[string]any{
			"status":     "正常",
			"type":       "Free",
			"quota":      1,
			"checked_at": time.Now().UTC().Format(time.RFC3339Nano),
		})
	}

	calls := 0
	engine := &Engine{
		Accounts: accounts,
		Proxy:    service.NewProxyService(testProtocolAccountConfig{}),
		StreamImageOutputsFunc: func(ctx context.Context, client *backend.Client, request ConversationRequest, index, total int) (<-chan ImageOutput, <-chan error) {
			calls++
			out := make(chan ImageOutput)
			errCh := make(chan error, 1)
			close(out)
			errCh <- fmt.Errorf("image generation produced no image result")
			close(errCh)
			return out, errCh
		},
	}

	outputs, errCh := engine.StreamImageOutputsWithPool(context.Background(), ConversationRequest{
		Model:  "gpt-image-2",
		Prompt: "draw",
		N:      2,
		Size:   "1024x1024",
	})
	for range outputs {
	}
	if err := <-errCh; err == nil {
		t.Fatal("StreamImageOutputsWithPool() err = nil, want transient failure")
	}
	if calls < 2 || calls > 4 {
		t.Fatalf("calls = %d, want bounded multi-image retries between 2 and 4 attempts", calls)
	}
}

func TestImagePoolCutsOffSlowDefaultImageAttempt(t *testing.T) {
	oldTimeout := imageStreamAttemptTimeoutForRequest
	imageStreamAttemptTimeoutForRequest = func(ConversationRequest) time.Duration {
		return 10 * time.Millisecond
	}
	defer func() { imageStreamAttemptTimeoutForRequest = oldTimeout }()

	calls := 0
	engine := &Engine{
		ImageTokenProvider: func(context.Context) (string, error) {
			return fmt.Sprintf("token-%d", calls+1), nil
		},
		ImageClientFactory: func(string) *backend.Client { return nil },
	}
	engine.StreamImageOutputsFunc = func(ctx context.Context, client *backend.Client, request ConversationRequest, index, total int) (<-chan ImageOutput, <-chan error) {
		calls++
		out := make(chan ImageOutput)
		errCh := make(chan error, 1)
		go func() {
			defer close(out)
			defer close(errCh)
			<-ctx.Done()
			errCh <- ctx.Err()
		}()
		return out, errCh
	}

	start := time.Now()
	outputs, errCh := engine.StreamImageOutputsWithPool(context.Background(), ConversationRequest{
		Model:        "gpt-image-2",
		Prompt:       "draw",
		N:            1,
		Quality:      "medium",
		OutputFormat: "webp",
	})
	for range outputs {
	}
	err := <-errCh
	if err == nil || !strings.Contains(err.Error(), "响应超时") {
		t.Fatalf("err = %v, want attempt timeout error", err)
	}
	if calls < 4 || calls > 16 {
		t.Fatalf("calls = %d, want bounded racing workers with limited retry", calls)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("elapsed = %s, want fast cutoff", elapsed)
	}
}

func TestFastImagePoolDoesNotRaceMultipleAccountsForSingleImage(t *testing.T) {
	started := make(chan int, 4)
	engine := &Engine{
		ImageTokenProvider: func(context.Context) (string, error) {
			return fmt.Sprintf("token-%d", len(started)+1), nil
		},
		ImageClientFactory: func(string) *backend.Client { return nil },
	}
	engine.StreamImageOutputsFunc = func(ctx context.Context, client *backend.Client, request ConversationRequest, index, total int) (<-chan ImageOutput, <-chan error) {
		started <- index
		out := make(chan ImageOutput, 1)
		errCh := make(chan error, 1)
		out <- ImageOutput{Kind: "result", Index: index, Total: total, Created: time.Now().Unix(), Data: []map[string]any{{"url": "https://example.test/only.webp"}}}
		close(out)
		errCh <- nil
		close(errCh)
		return out, errCh
	}

	outputs, errCh := engine.StreamImageOutputsWithPool(context.Background(), ConversationRequest{
		Model:        "gpt-image-2",
		Prompt:       "draw",
		N:            1,
		Quality:      "medium",
		OutputFormat: "webp",
	})
	var got []ImageOutput
	for output := range outputs {
		got = append(got, output)
	}
	if err := <-errCh; err != nil {
		t.Fatalf("StreamImageOutputsWithPool() error = %v", err)
	}
	if len(started) != 1 {
		t.Fatalf("started workers = %d, want 1", len(started))
	}
	if len(got) != 1 || got[0].Kind != "result" {
		t.Fatalf("outputs = %#v, want first successful result", got)
	}
}

func TestExpensiveImageAttemptTimeoutStaysResponsive(t *testing.T) {
	request := ConversationRequest{
		Model:        "gpt-image-2",
		Prompt:       "edit",
		N:            1,
		Quality:      "medium",
		OutputFormat: "jpeg",
		Images:       []string{"data:image/png;base64,aW1n"},
	}.Normalized()
	if timeout := defaultImageStreamAttemptTimeoutForRequest(request); timeout > fastImageStreamAttemptTimeout {
		t.Fatalf("timeout = %s, want <= %s", timeout, fastImageStreamAttemptTimeout)
	}
}

func TestFastImagePoolDoesNotExpandSingleImageWorkerBudget(t *testing.T) {
	started := make(chan int, 4)
	engine := &Engine{
		ImageTokenProvider: func(context.Context) (string, error) {
			return fmt.Sprintf("token-%d", len(started)+1), nil
		},
		ImageClientFactory: func(string) *backend.Client { return nil },
	}
	engine.StreamImageOutputsFunc = func(ctx context.Context, client *backend.Client, request ConversationRequest, index, total int) (<-chan ImageOutput, <-chan error) {
		started <- index
		out := make(chan ImageOutput, 1)
		errCh := make(chan error, 1)
		out <- ImageOutput{Kind: "result", Index: index, Total: total, Created: time.Now().Unix(), Data: []map[string]any{{"url": "https://example.test/fast.webp"}}}
		close(out)
		errCh <- nil
		close(errCh)
		return out, errCh
	}

	outputs, errCh := engine.StreamImageOutputsWithPool(context.Background(), ConversationRequest{
		Model:           "gpt-image-2",
		Prompt:          "draw",
		N:               1,
		Quality:         "medium",
		OutputFormat:    "webp",
		MaxImageWorkers: 2,
	})
	for range outputs {
	}
	if err := <-errCh; err != nil {
		t.Fatalf("StreamImageOutputsWithPool() error = %v", err)
	}
	if len(started) != 1 {
		t.Fatalf("started workers = %d, want 1", len(started))
	}
}

func TestImagePoolIgnoresTrailingStreamErrorAfterImageResult(t *testing.T) {
	engine := &Engine{
		ImageTokenProvider: func(context.Context) (string, error) { return "test-token", nil },
		ImageClientFactory: func(string) *backend.Client { return nil },
	}
	engine.StreamImageOutputsFunc = func(ctx context.Context, client *backend.Client, request ConversationRequest, index, total int) (<-chan ImageOutput, <-chan error) {
		out := make(chan ImageOutput, 1)
		errCh := make(chan error, 1)
		out <- ImageOutput{Kind: "result", Index: index, Total: total, Created: time.Now().Unix(), Data: []map[string]any{{"url": "https://example.test/image.png"}}}
		close(out)
		errCh <- fmt.Errorf("unexpected EOF")
		close(errCh)
		return out, errCh
	}

	outputs, errCh := engine.StreamImageOutputsWithPool(context.Background(), ConversationRequest{
		Model:  "gpt-image-2",
		Prompt: "draw",
		N:      1,
		Size:   "1024x1024",
	})
	var got []ImageOutput
	for output := range outputs {
		got = append(got, output)
	}
	if err := <-errCh; err != nil {
		t.Fatalf("StreamImageOutputsWithPool() error = %v", err)
	}
	if len(got) != 1 || got[0].Kind != "result" {
		t.Fatalf("outputs = %#v, want one result", got)
	}
}

func TestResponsesInputImagesDoesNotDuplicateReferences(t *testing.T) {
	imageData := base64.StdEncoding.EncodeToString([]byte("png-bytes"))
	images := responsesInputImages([]string{"data:image/png;base64," + imageData})
	if len(images) != 1 {
		t.Fatalf("responsesInputImages() length = %d, want 1", len(images))
	}
}

func TestStreamImageOutputsWithPoolRunsRequestedImagesConcurrently(t *testing.T) {
	started := make(chan int, 3)
	release := make(chan struct{})
	engine := &Engine{
		ImageTokenProvider: func(context.Context) (string, error) { return "test-token", nil },
		ImageClientFactory: func(string) *backend.Client { return nil },
	}
	engine.StreamImageOutputsFunc = func(ctx context.Context, client *backend.Client, request ConversationRequest, index, total int) (<-chan ImageOutput, <-chan error) {
		out := make(chan ImageOutput, 1)
		errCh := make(chan error, 1)
		go func() {
			defer close(out)
			defer close(errCh)
			started <- index
			select {
			case <-release:
				out <- ImageOutput{Kind: "result", Index: index, Total: total, Created: time.Now().Unix(), Data: []map[string]any{{"url": fmt.Sprintf("https://example.test/%d.png", index)}}}
				errCh <- nil
			case <-ctx.Done():
				errCh <- ctx.Err()
			}
		}()
		return out, errCh
	}

	outputs, errCh := engine.StreamImageOutputsWithPool(context.Background(), ConversationRequest{
		Model:  "gpt-image-2",
		Prompt: "draw",
		N:      3,
		Size:   "1024x1024",
	})
	done := make(chan []ImageOutput, 1)
	go func() {
		var got []ImageOutput
		for output := range outputs {
			got = append(got, output)
		}
		done <- got
	}()

	for i := 0; i < 3; i++ {
		select {
		case <-started:
		case <-time.After(300 * time.Millisecond):
			t.Fatalf("only %d image workers started concurrently, want 3", i)
		}
	}
	close(release)
	got := <-done
	if err := <-errCh; err != nil {
		t.Fatalf("StreamImageOutputsWithPool() error = %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("outputs = %d, want 3: %#v", len(got), got)
	}
}

func TestFormatImageResultEncodesRequestedOutputFormat(t *testing.T) {
	config := testProtocolImageConfig{root: t.TempDir()}
	engine := &Engine{Config: config}
	src := image.NewNRGBA(image.Rect(0, 0, 2, 2))
	src.Set(0, 0, color.NRGBA{R: 255, A: 255})
	src.Set(1, 0, color.NRGBA{G: 255, A: 255})
	src.Set(0, 1, color.NRGBA{B: 255, A: 255})
	src.Set(1, 1, color.NRGBA{R: 255, G: 255, B: 255, A: 128})
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, src); err != nil {
		t.Fatalf("png.Encode() error = %v", err)
	}
	compression := 25

	result := engine.FormatImageResultWithOptions(
		[]map[string]any{{"b64_json": base64.StdEncoding.EncodeToString(encoded.Bytes())}},
		"draw",
		"b64_json",
		"https://example.test",
		"owner-1",
		"Alice",
		123,
		"",
		ImageOutputOptions{Format: "jpeg", Compression: &compression},
	)
	items, _ := result["data"].([]map[string]any)
	if len(items) != 1 {
		t.Fatalf("FormatImageResultWithOptions() data = %#v", result["data"])
	}
	if items[0]["output_format"] != "jpeg" {
		t.Fatalf("output_format = %#v, want jpeg", items[0]["output_format"])
	}
	imageURL, _ := items[0]["url"].(string)
	if !strings.HasSuffix(imageURL, ".jpg") {
		t.Fatalf("image url = %q, want .jpg suffix", imageURL)
	}
	b64, _ := items[0]["b64_json"].(string)
	converted, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		t.Fatalf("DecodeString() error = %v", err)
	}
	_, format, err := image.DecodeConfig(bytes.NewReader(converted))
	if err != nil {
		t.Fatalf("DecodeConfig() error = %v", err)
	}
	if format != "jpeg" {
		t.Fatalf("decoded format = %q, want jpeg", format)
	}
	rel := strings.TrimPrefix(imageURL, "https://example.test/images/")
	if _, err := os.Stat(filepath.Join(config.ImagesDir(), filepath.FromSlash(rel))); err != nil {
		t.Fatalf("stored jpeg missing: %v", err)
	}
}

func TestFormatImageResultHonorsRequestedFormatOverUpstreamFormat(t *testing.T) {
	config := testProtocolImageConfig{root: t.TempDir()}
	engine := &Engine{Config: config}
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, image.NewNRGBA(image.Rect(0, 0, 2, 2))); err != nil {
		t.Fatalf("png.Encode() error = %v", err)
	}

	result := engine.FormatImageResultWithOptions(
		[]map[string]any{{"b64_json": base64.StdEncoding.EncodeToString(encoded.Bytes()), "output_format": "png"}},
		"draw",
		"url",
		"https://example.test",
		"owner-1",
		"Alice",
		123,
		"",
		ImageOutputOptions{Format: "webp"},
	)
	items := util.AsMapSlice(result["data"])
	if len(items) != 1 {
		t.Fatalf("FormatImageResultWithOptions() data = %#v", result["data"])
	}
	if items[0]["output_format"] != "webp" {
		t.Fatalf("output_format = %#v, want webp", items[0]["output_format"])
	}
	if imageURL, _ := items[0]["url"].(string); !strings.HasSuffix(imageURL, ".webp") {
		t.Fatalf("image url = %q, want .webp suffix", imageURL)
	}
}

func TestImageResultOutputOptionsHonorsRequestedFormatForCodex(t *testing.T) {
	request := ConversationRequest{Model: util.ImageModelCodex, OutputFormat: "png"}.Normalized()
	options := imageResultOutputOptions(request, backend.ResponsesImageEvent{OutputFormat: "webp"})
	if options.Format != "png" {
		t.Fatalf("Format = %q, want png", options.Format)
	}
	if options.TrustUpstreamFormat {
		t.Fatalf("TrustUpstreamFormat = true, want false")
	}
}

func TestFormatImageResultReencodesUpstreamWebPToRequestedPNG(t *testing.T) {
	config := testProtocolImageConfig{root: t.TempDir()}
	engine := &Engine{Config: config}
	var encoded bytes.Buffer
	if err := nativewebp.Encode(&encoded, image.NewNRGBA(image.Rect(0, 0, 2, 2)), nil); err != nil {
		t.Fatalf("nativewebp.Encode() error = %v", err)
	}

	result := engine.FormatImageResultWithOptions(
		[]map[string]any{{"b64_json": base64.StdEncoding.EncodeToString(encoded.Bytes()), "output_format": "webp"}},
		"draw",
		"url",
		"https://example.test",
		"owner-1",
		"Alice",
		123,
		"",
		ImageOutputOptions{Format: "png"},
	)
	items := util.AsMapSlice(result["data"])
	if len(items) != 1 {
		t.Fatalf("FormatImageResultWithOptions() data = %#v", result["data"])
	}
	if items[0]["output_format"] != "png" {
		t.Fatalf("output_format = %#v, want png", items[0]["output_format"])
	}
	imageURL, _ := items[0]["url"].(string)
	if !strings.HasSuffix(imageURL, ".png") {
		t.Fatalf("image url = %q, want .png suffix", imageURL)
	}
	rel := strings.TrimPrefix(imageURL, "https://example.test/images/")
	stored, err := os.ReadFile(filepath.Join(config.ImagesDir(), filepath.FromSlash(rel)))
	if err != nil {
		t.Fatalf("stored png missing: %v", err)
	}
	if _, format, err := image.Decode(bytes.NewReader(stored)); err != nil || format != "png" {
		t.Fatalf("stored image format = %q err=%v, want png", format, err)
	}
}

func TestImageStreamErrorMessage(t *testing.T) {
	cloudflare := `bootstrap failed: status=403, body=<html><script>window._cf_chl_opt={}</script>Enable JavaScript and cookies to continue</html>`
	if got := imageStreamErrorMessage(cloudflare); got != "upstream returned Cloudflare challenge page; refresh browser fingerprint/session or change proxy" {
		t.Fatalf("cloudflare challenge error = %q", got)
	}
	cloudflareOrigin := `源服务器向 Cloudflare 返回了无效或不完整的响应。`
	if got := imageStreamErrorMessage(cloudflareOrigin); got != "upstream returned Cloudflare origin error page; retry with another account/proxy if it repeats" {
		t.Fatalf("cloudflare origin error = %q", got)
	}

	cases := []string{
		"curl: (35) OpenSSL SSL_connect: SSL_ERROR_SYSCALL",
		"TLS connect error: connection reset by peer",
		"error: OPENSSL_INTERNAL:WRONG_VERSION_NUMBER",
		`Get "https://chatgpt.com/": surf: HTTP/2 request failed: uTLS.HandshakeContext() error: EOF; HTTP/1.1 fallback failed: uTLS.HandshakeContext() error: EOF`,
	}
	for _, input := range cases {
		if got := imageStreamErrorMessage(input); got != "upstream connection failed before TLS handshake completed; check proxy reachability to chatgpt.com or change proxy" {
			t.Fatalf("imageStreamErrorMessage(%q) = %q", input, got)
		}
	}
	if got := imageStreamErrorMessage("upstream returned 500"); got != "upstream returned 500" {
		t.Fatalf("non-connection error = %q", got)
	}
	flowControl := "connection error: FLOW_CONTROL_ERROR"
	if got := imageStreamErrorMessage(flowControl); got != "upstream image stream interrupted by HTTP/2 flow control; retry the request or change proxy if it repeats" {
		t.Fatalf("flow control error = %q", got)
	}
	if got := imageStreamErrorMessage(""); got != "image generation failed" {
		t.Fatalf("empty error = %q", got)
	}
}

func TestIsTransientImageStreamErrorMessage(t *testing.T) {
	transient := []string{
		"responses SSE read error: stream error: stream ID 1; INTERNAL_ERROR; received from peer",
		"connection error: FLOW_CONTROL_ERROR",
		"http2: client connection lost",
		"unexpected EOF",
		"connection reset by peer",
		"stream closed",
		"image generation produced no image result",
		`/backend-api/codex/responses failed: status=400, body={"error":{"message":"Tool choice 'required' must be specified with 'tools' parameter.","param":"tool_choice"}}`,
		`/backend-api/codex/responses failed: status=400, body={"error":{"message":"Tool choice 'image_generation' not found in 'tools' parameter.","param":"tool_choice"}}`,
		"bootstrap failed: upstream connection failed before TLS handshake completed; check proxy reachability to chatgpt.com or change proxy",
		`bootstrap failed: Get "https://chatgpt.com/": surf: HTTP/2 request failed: uTLS.HandshakeContext() error: EOF; HTTP/1.1 fallback failed: uTLS.HandshakeContext() error: EOF`,
	}
	for _, input := range transient {
		if !isTransientImageStreamErrorMessage(input) {
			t.Fatalf("isTransientImageStreamErrorMessage(%q) = false, want true", input)
		}
	}

	stable := []string{
		"upstream returned Cloudflare challenge page",
		"You've reached the image generation limit for now.",
		"invalid size: expected WIDTHxHEIGHT",
		"auth_chat_requirements failed: status=401",
		"image generation failed",
	}
	for _, input := range stable {
		if isTransientImageStreamErrorMessage(input) {
			t.Fatalf("isTransientImageStreamErrorMessage(%q) = true, want false", input)
		}
	}
}
