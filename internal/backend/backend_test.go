package backend

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"chatgpt2api/internal/util"
)

func TestUpstreamHTTPErrorSummarizesCloudflareChallenge(t *testing.T) {
	err := upstreamHTTPError("bootstrap", 403, []byte(`<html><body><script>window._cf_chl_opt={}</script>Enable JavaScript and cookies to continue</body></html>`))
	got := err.Error()
	if !strings.Contains(got, "bootstrap failed: status=403") {
		t.Fatalf("error missing context/status: %q", got)
	}
	if !strings.Contains(got, "upstream returned Cloudflare challenge page") {
		t.Fatalf("error missing challenge summary: %q", got)
	}
	if strings.Contains(got, "<html>") || strings.Contains(got, "window._cf_chl_opt") {
		t.Fatalf("error leaked challenge HTML: %q", got)
	}
}

func TestUpstreamHTTPErrorSummarizesGenericHTML(t *testing.T) {
	err := upstreamHTTPError("auth_models", 502, []byte(`<!doctype html><html><body>bad gateway</body></html>`))
	got := err.Error()
	if got != "auth_models failed: status=502, upstream returned HTML error page" {
		t.Fatalf("upstreamHTTPError() = %q", got)
	}
}

func TestUpstreamHTTPErrorSummarizesCloudflareOriginError(t *testing.T) {
	err := upstreamHTTPError("image_stream", 520, []byte(`<!doctype html><html><body>源服务器向 Cloudflare 返回了无效或不完整的响应。</body></html>`))
	got := err.Error()
	if got != "image_stream failed: status=520, upstream returned Cloudflare origin error page; retry with another account/proxy if it repeats" {
		t.Fatalf("upstreamHTTPError() = %q", got)
	}
}

func TestUpstreamHTTPErrorKeepsPlainBodyDetail(t *testing.T) {
	err := upstreamHTTPError("auth_models", 400, []byte(`{"error":"bad request"}`))
	got := err.Error()
	if got != `auth_models failed: status=400, body={"error":"bad request"}` {
		t.Fatalf("upstreamHTTPError() = %q", got)
	}
}

func TestUpstreamTransportErrorSummarizesSurfHandshakeFailure(t *testing.T) {
	err := upstreamTransportError("bootstrap", errString(`Get "https://chatgpt.com/": surf: HTTP/2 request failed: uTLS.HandshakeContext() error: EOF; HTTP/1.1 fallback failed: uTLS.HandshakeContext() error: EOF`))
	got := err.Error()
	want := "bootstrap failed: upstream connection failed before TLS handshake completed; check proxy reachability to chatgpt.com or change proxy"
	if got != want {
		t.Fatalf("upstreamTransportError() = %q, want %q", got, want)
	}
}

func TestIterSSEPayloadsStopsBlockedReaderOnContextCancel(t *testing.T) {
	reader, writer := io.Pipe()
	defer writer.Close()
	payloads := make(chan string)
	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() {
		defer close(payloads)
		errCh <- iterSSEPayloads(ctx, reader, payloads)
	}()

	cancel()
	select {
	case err := <-errCh:
		if err == nil || err != context.Canceled {
			t.Fatalf("iterSSEPayloads() error = %v, want context.Canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("iterSSEPayloads() did not return after context cancellation")
	}
}

func TestApplyBrowserFingerprintPreservesAccountProfile(t *testing.T) {
	client := &Client{fp: map[string]string{
		"user-agent":     "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/143.0.0.0 Safari/537.36 Edg/143.0.0.0",
		"impersonate":    "edge101",
		"oai-device-id":  "device-1",
		"oai-session-id": "session-1",
	}}
	client.applyBrowserFingerprint()
	if client.fp["user-agent"] != "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/143.0.0.0 Safari/537.36 Edg/143.0.0.0" {
		t.Fatalf("user-agent = %q", client.fp["user-agent"])
	}
	if client.fp["sec-ch-ua"] != `"Microsoft Edge";v="143", "Chromium";v="143", "Not A(Brand";v="24"` {
		t.Fatalf("sec-ch-ua = %q", client.fp["sec-ch-ua"])
	}
	if client.fp["sec-ch-ua-full-version"] != `"143.0.0.0"` {
		t.Fatalf("sec-ch-ua-full-version = %q", client.fp["sec-ch-ua-full-version"])
	}
	if client.fp["impersonate"] != "edge101" {
		t.Fatalf("impersonate = %q", client.fp["impersonate"])
	}
	if client.fp["oai-device-id"] != "device-1" || client.fp["oai-session-id"] != "session-1" {
		t.Fatalf("device/session should be preserved: %#v", client.fp)
	}
}

func TestConversationPayloadEmbedsOpenAIMessageHistoryInSingleUserMessage(t *testing.T) {
	client := &Client{}
	payload := client.conversationPayload([]map[string]any{
		{"role": "user", "content": "你好，你是什么模型？"},
		{"role": "assistant", "content": "你好！我是一个由OpenAI开发的语言模型，叫做GPT-4。"},
		{"role": "user", "content": "我之前说了什么？"},
	}, "auto", "Asia/Shanghai")

	if payload["parent_message_id"] != "client-created-root" {
		t.Fatalf("parent_message_id = %q, want client-created-root", payload["parent_message_id"])
	}
	messages, ok := payload["messages"].([]map[string]any)
	if !ok {
		t.Fatalf("messages = %T, want []map[string]any", payload["messages"])
	}
	if len(messages) != 1 {
		t.Fatalf("messages length = %d, want 1", len(messages))
	}
	author := messages[0]["author"].(map[string]any)
	if author["role"] != "user" {
		t.Fatalf("message role = %q, want user", author["role"])
	}
	content := messages[0]["content"].(map[string]any)
	parts := content["parts"].([]any)
	if len(parts) != 1 {
		t.Fatalf("parts length = %d, want 1", len(parts))
	}
	prompt, ok := parts[0].(string)
	if !ok {
		t.Fatalf("prompt = %T, want string", parts[0])
	}
	for _, want := range []string{
		"Conversation history:",
		"User: 你好，你是什么模型？",
		"Assistant: 你好！我是一个由OpenAI开发的语言模型，叫做GPT-4。",
		"Current user message:\n我之前说了什么？",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt missing %q: %s", want, prompt)
		}
	}
}

func TestConversationPayloadKeepsSingleUserMessagePrompt(t *testing.T) {
	client := &Client{}
	payload := client.conversationPayload([]map[string]any{
		{"role": "user", "content": "hello"},
	}, "auto", "Asia/Shanghai")

	messages := payload["messages"].([]map[string]any)
	content := messages[0]["content"].(map[string]any)
	parts := content["parts"].([]any)
	if parts[0] != "hello" {
		t.Fatalf("prompt = %q, want hello", parts[0])
	}
}

func TestConversationPayloadWithRefsBuildsMultimodalMessage(t *testing.T) {
	client := &Client{}
	payload := client.conversationPayloadWithRefs([]map[string]any{
		{"role": "user", "content": []any{
			map[string]any{"type": "text", "text": "描述这张图"},
			map[string]any{"type": "image_url", "image_url": map[string]any{"url": "data:image/png;base64,cG5n"}},
		}},
	}, "gpt-5", "Asia/Shanghai", []uploadedImageRef{{
		FileID:   "file_123",
		FileName: "ref.png",
		FileSize: 3,
		MIMEType: "image/png",
		Width:    64,
		Height:   32,
	}})

	messages := payload["messages"].([]map[string]any)
	content := messages[0]["content"].(map[string]any)
	if content["content_type"] != "multimodal_text" {
		t.Fatalf("content_type = %#v, want multimodal_text", content["content_type"])
	}
	parts := content["parts"].([]any)
	if len(parts) != 2 {
		t.Fatalf("parts = %#v, want image pointer and prompt", parts)
	}
	pointer := parts[0].(map[string]any)
	if pointer["asset_pointer"] != "file-service://file_123" || pointer["width"] != 64 || pointer["height"] != 32 {
		t.Fatalf("image pointer = %#v", pointer)
	}
	if parts[1] != "描述这张图" {
		t.Fatalf("prompt part = %#v, want text-only prompt", parts[1])
	}
	metadata := messages[0]["metadata"].(map[string]any)
	attachments := metadata["attachments"].([]map[string]any)
	if len(attachments) != 1 || attachments[0]["id"] != "file_123" {
		t.Fatalf("attachments = %#v", attachments)
	}
	if payload["force_use_sse"] != true {
		t.Fatalf("force_use_sse = %#v, want true", payload["force_use_sse"])
	}
}

func TestConversationImageInputsExtractDataURL(t *testing.T) {
	inputs := conversationImageInputs([]map[string]any{{
		"role": "user",
		"content": []any{
			map[string]any{"type": "text", "text": "look"},
			map[string]any{"type": "image_url", "image_url": map[string]any{"url": "data:image/png;base64,cG5n"}},
		},
	}})
	if len(inputs) != 1 {
		t.Fatalf("inputs = %#v, want one image", inputs)
	}
	if string(inputs[0].Data) != "png" || inputs[0].ContentType != "image/png" {
		t.Fatalf("input = %#v", inputs[0])
	}
}

func TestResponsesImagePayloadRequiresImageToolWithoutNamedToolChoice(t *testing.T) {
	payload, err := buildResponsesImagePayload(ResponsesImageRequest{
		Prompt: "生成海报",
		Model:  util.ImageModelCodex,
		Size:   "16:9",
	})
	if err != nil {
		t.Fatalf("buildResponsesImagePayload() error = %v", err)
	}
	var body map[string]any
	if err := json.Unmarshal(payload, &body); err != nil {
		t.Fatalf("payload json: %v", err)
	}
	if body["tool_choice"] != "required" {
		t.Fatalf("tool_choice = %#v, want required", body["tool_choice"])
	}
	tools, ok := body["tools"].([]any)
	if !ok || len(tools) != 1 {
		t.Fatalf("tools = %#v", body["tools"])
	}
	tool, ok := tools[0].(map[string]any)
	if !ok || tool["type"] != "image_generation" {
		t.Fatalf("tool = %#v", tools[0])
	}
}

func TestResponsesImageGPTModelUsesOfficialRouteWithCodexFallback(t *testing.T) {
	if usesCodexResponsesImageRoute(util.ImageModelAuto) {
		t.Fatal("auto should use official responses image route before codex fallback")
	}
	if usesCodexResponsesImageRoute(util.ImageModelGPT) {
		t.Fatal("gpt-image-2 should not force codex responses image route")
	}
	if !usesCodexResponsesImageRoute(util.ImageModelCodex) {
		t.Fatal("codex-gpt-image-2 should force codex responses image route")
	}
	payload, err := buildResponsesImagePayload(ResponsesImageRequest{
		Prompt: "生成海报",
		Model:  util.ImageModelGPT,
		Size:   "3840x1648",
	})
	if err != nil {
		t.Fatalf("buildResponsesImagePayload() error = %v", err)
	}
	var body map[string]any
	if err := json.Unmarshal(payload, &body); err != nil {
		t.Fatalf("payload json: %v", err)
	}
	tools, ok := body["tools"].([]any)
	if !ok || len(tools) != 1 {
		t.Fatalf("tools = %#v", body["tools"])
	}
	tool, ok := tools[0].(map[string]any)
	if !ok {
		t.Fatalf("tool = %#v", tools[0])
	}
	if _, ok := tool["model"]; ok {
		t.Fatalf("tool model = %#v, want omitted for default gpt-image-2 fallback", tool["model"])
	}
	if tool["size"] == "" {
		t.Fatalf("tool size should be normalized for exact resolution: %#v", tool)
	}
	fallback := codexFallbackImageRequest(ResponsesImageRequest{Model: util.ImageModelGPT, Quality: "high"})
	if fallback.Model != util.ImageModelCodex {
		t.Fatalf("fallback model = %q, want %s", fallback.Model, util.ImageModelCodex)
	}
	if fallback.Quality != "" {
		t.Fatalf("fallback quality = %q, want empty", fallback.Quality)
	}
}

func TestCodexFallbackDecisionSkipsBlockedOfficialText(t *testing.T) {
	if shouldUseCodexFallbackForOfficialImageEvents(ResponsesImageEvent{Blocked: true, Text: "blocked"}) {
		t.Fatal("blocked official image text should not fall back")
	}
	if !shouldUseCodexFallbackForOfficialImageEvents(ResponsesImageEvent{Text: "I cannot generate", TurnUseCase: "text"}) {
		t.Fatal("official text turn should fall back")
	}
	if !shouldUseCodexFallbackForOfficialImageError(errString("image generation produced no image result")) {
		t.Fatal("no image result should fall back")
	}
}

func TestOfficialImageModelSlugUsesStableWebModel(t *testing.T) {
	if got := officialImageModelSlug(util.ImageModelAuto); got != "gpt-5-5-thinking" {
		t.Fatalf("officialImageModelSlug(auto) = %q", got)
	}
	if got := officialImageModelSlug(util.ImageModelGPT); got != "gpt-5-5-thinking" {
		t.Fatalf("officialImageModelSlug(gpt-image-2) = %q", got)
	}
}

func TestResolveOfficialImageResultsUsesDirectDataURLs(t *testing.T) {
	raw := []byte("fake-png")
	encoded := base64.StdEncoding.EncodeToString(raw)
	client := &Client{BaseURL: "https://chatgpt.com", httpClient: &http.Client{}}
	events, err := client.resolveOfficialImageResults(context.Background(), ResponsesImageRequest{Prompt: "draw", ResultLimit: 1}, ResponsesImageEvent{
		ConversationID: "conv_1",
		DirectURLs:     []string{"data:image/png;base64," + encoded, "data:image/png;base64," + base64.StdEncoding.EncodeToString([]byte("extra-png"))},
	})
	if err != nil {
		t.Fatalf("resolveOfficialImageResults() error = %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("events = %#v, want one direct image result", events)
	}
	if events[0].Result != encoded {
		t.Fatalf("result = %q, want direct data b64", events[0].Result)
	}
}

func TestOfficialImageDirectURLsIgnoreInputAttachments(t *testing.T) {
	inputURL := "https://files.oaiusercontent.com/file-input/original.png"
	outputURL := "https://files.oaiusercontent.com/file-output/generated.png"
	payload := map[string]any{
		"type": "input_message",
		"v": map[string]any{
			"message": map[string]any{
				"author": map[string]any{"role": "user"},
				"content": map[string]any{
					"parts": []any{map[string]any{"asset_pointer": inputURL}},
				},
			},
		},
	}
	raw, _ := json.Marshal(payload)
	state := &imageConversationState{}
	event, ok, err := parseOfficialImagePayload(string(raw), state)
	if err != nil || !ok {
		t.Fatalf("parseOfficialImagePayload() ok=%v err=%v", ok, err)
	}
	if len(event.DirectURLs) != 0 || len(state.DirectURLs) != 0 {
		t.Fatalf("input attachment URL leaked into direct results: event=%#v state=%#v", event.DirectURLs, state.DirectURLs)
	}

	toolPayload := map[string]any{
		"type": "message",
		"v": map[string]any{
			"message": map[string]any{
				"author":   map[string]any{"role": "tool"},
				"metadata": map[string]any{"async_task_type": "image_gen"},
				"content": map[string]any{
					"parts": []any{map[string]any{"asset_pointer": outputURL}},
				},
			},
		},
	}
	raw, _ = json.Marshal(toolPayload)
	event, ok, err = parseOfficialImagePayload(string(raw), state)
	if err != nil || !ok {
		t.Fatalf("parseOfficialImagePayload(tool) ok=%v err=%v", ok, err)
	}
	if len(event.DirectURLs) != 1 || event.DirectURLs[0] != outputURL {
		t.Fatalf("tool output direct URLs = %#v, want output URL", event.DirectURLs)
	}
}

func TestFetchOfficialConversationImageIDsOnlyUsesToolMessages(t *testing.T) {
	inputURL := "https://files.oaiusercontent.com/file-input/original.png"
	outputURL := "https://files.oaiusercontent.com/file-output/generated.png"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/backend-api/conversation/conv_1" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"mapping": map[string]any{
				"user": map[string]any{
					"message": map[string]any{
						"author": map[string]any{"role": "user"},
						"content": map[string]any{
							"content_type": "multimodal_text",
							"parts":        []any{map[string]any{"asset_pointer": inputURL}},
						},
					},
				},
				"tool": map[string]any{
					"message": map[string]any{
						"author":   map[string]any{"role": "tool"},
						"metadata": map[string]any{"async_task_type": "image_gen"},
						"content": map[string]any{
							"content_type": "multimodal_text",
							"parts":        []any{map[string]any{"asset_pointer": outputURL}},
						},
					},
				},
			},
		})
	}))
	defer server.Close()

	client := &Client{BaseURL: server.URL, AccessToken: "token", httpClient: server.Client()}
	fileIDs, sedimentIDs, directURLs, err := client.fetchOfficialConversationImageIDs(context.Background(), "conv_1")
	if err != nil {
		t.Fatalf("fetchOfficialConversationImageIDs() error = %v", err)
	}
	if len(fileIDs) != 0 || len(sedimentIDs) != 0 {
		t.Fatalf("unexpected file IDs: files=%#v sediments=%#v", fileIDs, sedimentIDs)
	}
	if len(directURLs) != 1 || directURLs[0] != outputURL {
		t.Fatalf("direct URLs = %#v, want only tool output URL", directURLs)
	}
}

func TestSolveTurnstileTokenInterpretsEncodedProgram(t *testing.T) {
	program := `[[3,"ok"]]`
	key := "secret"
	dx := base64.StdEncoding.EncodeToString([]byte(xorTurnstileString(program, key)))
	if got := solveTurnstileToken(dx, key); got != "b2s=" {
		t.Fatalf("solveTurnstileToken() = %q", got)
	}
}

func TestOfficialImageTransportErrorsUseCodexFallback(t *testing.T) {
	cases := []error{
		errString("bootstrap failed: " + util.UpstreamConnectionFailureMessage),
		errString("image_upload failed: " + util.UpstreamConnectionFailureMessage),
		errString("image_upload failed: TLS handshake EOF"),
	}
	for _, err := range cases {
		if !shouldUseCodexFallbackForOfficialImageError(err) {
			t.Fatalf("shouldUseCodexFallbackForOfficialImageError(%q) = false, want true", err)
		}
	}
	if shouldUseCodexFallbackForOfficialImageError(context.Canceled) {
		t.Fatal("context.Canceled fallback = true, want false")
	}
}

func TestOfficialImagePollTimeoutIsBoundedForResponsiveRetry(t *testing.T) {
	if officialImagePollTimeout > time.Minute {
		t.Fatalf("officialImagePollTimeout = %s, want <= 1m", officialImagePollTimeout)
	}
}

type errString string

func (e errString) Error() string { return string(e) }

func serverURL(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	return scheme + "://" + r.Host
}
