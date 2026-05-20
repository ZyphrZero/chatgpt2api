package protocol

import (
	"context"
	"encoding/base64"
	"errors"
	"strings"
	"testing"

	"chatgpt2api/internal/util"
)

func ptrInt(value int) *int {
	return &value
}

func TestChatAndResponsesImageParsing(t *testing.T) {
	imageData := base64.StdEncoding.EncodeToString([]byte("png-bytes"))
	body := map[string]any{
		"model": "gpt-image-2",
		"messages": []any{
			map[string]any{"role": "system", "content": "ignore"},
			map[string]any{"role": "user", "content": []any{
				map[string]any{"type": "text", "text": "画一张图"},
				map[string]any{"type": "image_url", "image_url": map[string]any{"url": "data:image/png;base64," + imageData}},
			}},
		},
		"n": 2,
	}

	model, prompt, n, images, messages, err := ChatImageArgs(body)
	if err != nil {
		t.Fatalf("ChatImageArgs() error = %v", err)
	}
	if model != "gpt-image-2" || prompt != "画一张图" || n != 2 {
		t.Fatalf("ChatImageArgs() = model %q prompt %q n %d", model, prompt, n)
	}
	if len(messages) != 2 || messages[1]["content"] != "画一张图" {
		t.Fatalf("messages = %#v", messages)
	}
	if len(images) != 1 || string(images[0].Data) != "png-bytes" || images[0].ContentType != "image/png" {
		t.Fatalf("images = %#v", images)
	}

	responseInput := []any{
		map[string]any{"role": "user", "content": []any{
			map[string]any{"type": "input_text", "text": "生成封面"},
			map[string]any{"type": "input_image", "image_url": "data:image/png;base64," + imageData},
		}},
	}
	if prompt := ExtractResponsePrompt(responseInput); prompt != "生成封面" {
		t.Fatalf("ExtractResponsePrompt() = %q", prompt)
	}
	if image := ExtractResponseImage(responseInput); image == nil || string(image.Data) != "png-bytes" {
		t.Fatalf("ExtractResponseImage() = %#v", image)
	}
}

func TestImageRequestDefaultsToAutoModel(t *testing.T) {
	body := map[string]any{
		"messages": []any{
			map[string]any{"role": "user", "content": "画一张图"},
		},
	}
	model, prompt, n, _, _, err := ChatImageArgs(body)
	if err != nil {
		t.Fatalf("ChatImageArgs() error = %v", err)
	}
	if model != "auto" || prompt != "画一张图" || n != 1 {
		t.Fatalf("ChatImageArgs() = model %q prompt %q n %d", model, prompt, n)
	}
}

func TestTextModelDoesNotForceImageChatRoute(t *testing.T) {
	if IsImageChatRequest(map[string]any{"model": "gpt-5", "messages": []any{map[string]any{"role": "user", "content": "hello"}}}) {
		t.Fatal("gpt-5 text chat should not be routed as an image request without image modality")
	}
	if !IsImageChatRequest(map[string]any{"model": "gpt-5", "modalities": []any{"image"}, "messages": []any{map[string]any{"role": "user", "content": "draw"}}}) {
		t.Fatal("gpt-5 with image modality should be routed as an image request")
	}
}

func TestTextChatRequestWithImageUsesVisionWithoutImageRoute(t *testing.T) {
	body := map[string]any{"model": "gpt-5", "messages": []map[string]any{{
		"role": "user",
		"content": []any{
			map[string]any{"type": "text", "text": "这张图是什么"},
			map[string]any{"type": "image_url", "image_url": map[string]any{"url": "data:image/png;base64," + base64.StdEncoding.EncodeToString([]byte("png"))}},
		},
	}}}
	if IsImageChatRequest(body) {
		t.Fatal("chat messages with images must stay on the text chat route unless image generation is explicitly requested")
	}
	request, err := TextChatRequestFromBody(body)
	if err != nil {
		t.Fatalf("TextChatRequestFromBody() error = %v", err)
	}
	if !request.VisionRequired {
		t.Fatal("VisionRequired = false, want true")
	}
	if _, ok := request.Messages[0]["content"].([]any); !ok {
		t.Fatalf("content = %#v, want multimodal blocks preserved", request.Messages[0]["content"])
	}
}

func TestListModelsUsesInjectedLister(t *testing.T) {
	called := false
	engine := &Engine{
		ListModelsFunc: func(context.Context) (map[string]any, error) {
			called = true
			return map[string]any{
				"object": "list",
				"data": []map[string]any{
					{"id": "custom-model", "object": "model"},
				},
			}, nil
		},
	}

	result, err := engine.ListModels(context.Background())
	if err != nil {
		t.Fatalf("ListModels() error = %v", err)
	}
	if !called {
		t.Fatal("ListModelsFunc was not called")
	}
	data, _ := result["data"].([]map[string]any)
	if len(data) == 0 || data[0]["id"] != "custom-model" {
		t.Fatalf("ListModels() data = %#v", result["data"])
	}
}

func TestImageContextPromptIncludesHistory(t *testing.T) {
	messages := []map[string]any{
		{"role": "system", "content": "保持水彩风格"},
		{"role": "user", "content": "画一只白猫"},
		{"role": "assistant", "content": "Generated image: 白猫坐在窗边"},
		{"role": "user", "content": "把它改成夜晚"},
	}
	prompt := BuildImageContextPrompt(messages, LatestUserPrompt(messages), "1:1", "high")
	for _, want := range []string{"保持水彩风格", "画一只白猫", "白猫坐在窗边", "当前请求:\n把它改成夜晚", "输出为 1:1", "画质使用 High 档"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("context prompt missing %q: %s", want, prompt)
		}
	}
}

func TestBuildImagePromptIncludesThreeTwoAndQualityHints(t *testing.T) {
	prompt := BuildImagePrompt("画一张产品照片", "3:2", "medium")
	for _, want := range []string{"画一张产品照片", "3:2 横版构图", "画质使用 Medium 档"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("image prompt missing %q: %s", want, prompt)
		}
	}
}

func TestBuildImagePromptIncludesExactResolutionHint(t *testing.T) {
	prompt := BuildImagePrompt("画一张城市海报", "3840x2160", "high")
	for _, want := range []string{"画一张城市海报", "3840 x 2160 像素", "画质使用 High 档"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("image prompt missing %q: %s", want, prompt)
		}
	}
}

func TestCodexImageModelDropsQualityHint(t *testing.T) {
	request := ConversationRequest{
		Model:   "codex-gpt-image-2",
		Prompt:  "画一张产品照片",
		Size:    "3:2",
		Quality: "high",
	}.Normalized()
	if request.Quality != "" {
		t.Fatalf("Normalized() quality = %q, want empty for codex image model", request.Quality)
	}
	prompt := BuildImagePrompt(request.Prompt, request.Size, request.Quality)
	if strings.Contains(prompt, "画质使用") {
		t.Fatalf("codex image prompt included quality hint: %s", prompt)
	}
	if !strings.Contains(prompt, "3:2 横版构图") {
		t.Fatalf("codex image prompt should still include size hint: %s", prompt)
	}
}

func TestGPTImageModelKeepsQualityHint(t *testing.T) {
	request := ConversationRequest{
		Model:   "gpt-image-2",
		Prompt:  "画一张产品照片",
		Size:    "3:2",
		Quality: " high ",
	}.Normalized()
	if request.Quality != "high" {
		t.Fatalf("Normalized() quality = %q, want high", request.Quality)
	}
	prompt := BuildImagePrompt(request.Prompt, request.Size, request.Quality)
	if !strings.Contains(prompt, "画质使用 High 档") {
		t.Fatalf("gpt image prompt missing quality hint: %s", prompt)
	}
}

func TestConversationRequestNormalizesAutoImageModel(t *testing.T) {
	request := ConversationRequest{
		Model:   " auto ",
		Size:    "3840x1648",
		Quality: "high",
	}.Normalized()
	if request.Model != "gpt-image-2" {
		t.Fatalf("Normalized() model = %q, want gpt-image-2", request.Model)
	}
	if request.Quality != "high" {
		t.Fatalf("Normalized() quality = %q, want high", request.Quality)
	}
	if !request.RequirePaidAccount {
		t.Fatal("Normalized() RequirePaidAccount = false, want true for high resolution")
	}
}

func TestConversationRequestKeepsResponsesImageToolAutoModel(t *testing.T) {
	request := ConversationRequest{
		Model:              " auto ",
		ResponsesImageTool: true,
	}.Normalized()
	if request.Model != "auto" {
		t.Fatalf("Normalized() responses image model = %q, want auto", request.Model)
	}
}

func TestRequiresPaidImageSize(t *testing.T) {
	tests := []struct {
		name string
		size string
		want bool
	}{
		{name: "empty", size: "", want: false},
		{name: "aspect ratio", size: "16:9", want: false},
		{name: "free pixel budget", size: "1248x1248", want: false},
		{name: "1080p square below paid budget", size: "1080x1080", want: false},
		{name: "1080p tier below paid budget", size: "1080p", want: false},
		{name: "1080p widescreen above paid budget", size: "1920x1080", want: true},
		{name: "2k tier", size: "2k", want: true},
		{name: "2k", size: "2560x1440", want: true},
		{name: "4k tier", size: "4k", want: true},
		{name: "4k", size: "3840x2160", want: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := RequiresPaidImageSize(tt.size); got != tt.want {
				t.Fatalf("RequiresPaidImageSize(%q) = %v, want %v", tt.size, got, tt.want)
			}
		})
	}
}

func TestNormalizeImageGenerationSizeTiers(t *testing.T) {
	tests := []struct {
		size string
		want string
	}{
		{size: "", want: ""},
		{size: "16:9", want: "16:9"},
		{size: "1080p", want: "1080x1080"},
		{size: " 2K ", want: "2048x2048"},
		{size: "4k", want: "2880x2880"},
		{size: "1536x2048", want: "1536x2048"},
	}

	for _, tt := range tests {
		t.Run(tt.size, func(t *testing.T) {
			if got := NormalizeImageGenerationSize(tt.size); got != tt.want {
				t.Fatalf("NormalizeImageGenerationSize(%q) = %q, want %q", tt.size, got, tt.want)
			}
		})
	}
}

func TestConversationRequestNormalizesResolutionTierSize(t *testing.T) {
	request := ConversationRequest{
		Model: "gpt-5.5",
		Size:  "2k",
	}.Normalized()
	if request.Size != "2048x2048" {
		t.Fatalf("Normalized() size = %q, want 2048x2048", request.Size)
	}
	if !request.RequirePaidAccount {
		t.Fatal("Normalized() RequirePaidAccount = false, want true for 2k tier")
	}
}

func TestBuildImagePromptNormalizesResolutionTierHint(t *testing.T) {
	prompt := BuildImagePrompt("画一张城市海报", "2k", "")
	if !strings.Contains(prompt, "2048 x 2048") {
		t.Fatalf("BuildImagePrompt() did not expand 2k tier to exact pixels: %s", prompt)
	}
}

func TestResponsesInputKeepsAssistantAndGeneratedImageContext(t *testing.T) {
	imageData := base64.StdEncoding.EncodeToString([]byte("previous-image"))
	input := []any{
		map[string]any{"type": "message", "role": "assistant", "content": []any{
			map[string]any{"type": "output_text", "text": "上一轮说明"},
		}},
		map[string]any{"type": "image_generation_call", "status": "completed", "result": imageData, "revised_prompt": "一只红色猫"},
		map[string]any{"role": "user", "content": []any{
			map[string]any{"type": "input_text", "text": "把它改成蓝色"},
		}},
	}
	messages := MessagesFromInput(input, "保持同一角色")
	if len(messages) != 4 {
		t.Fatalf("MessagesFromInput() = %#v", messages)
	}
	if messages[0]["role"] != "system" || messages[1]["role"] != "assistant" || messages[2]["role"] != "assistant" || messages[3]["role"] != "user" {
		t.Fatalf("unexpected message roles: %#v", messages)
	}
	if got := LatestUserPrompt(messages); got != "把它改成蓝色" {
		t.Fatalf("LatestUserPrompt() = %q", got)
	}
	images := ExtractResponseImages(input)
	if len(images) != 1 || string(images[0].Data) != "previous-image" {
		t.Fatalf("ExtractResponseImages() = %#v", images)
	}
}

func TestResponseImageGenerationRequestUsesTextModelAndToolParams(t *testing.T) {
	body := map[string]any{
		"model": "gpt-5.5",
		"input": "生成封面",
		"tools": []any{
			map[string]any{"type": "image_generation", "size": "16:9", "quality": "high", "output_format": "webp", "output_compression": 37},
		},
	}
	request, prompt, err := ResponseImageGenerationRequest(body, "linuxdo:1", nil)
	if err != nil {
		t.Fatalf("ResponseImageGenerationRequest() error = %v", err)
	}
	if prompt != "生成封面" {
		t.Fatalf("prompt = %q, want 生成封面", prompt)
	}
	if request.Model != "gpt-5.5" {
		t.Fatalf("model = %q, want gpt-5.5", request.Model)
	}
	if !request.ResponsesImageTool {
		t.Fatal("ResponsesImageTool = false, want true")
	}
	if !request.SupportsImageGenerationModel() {
		t.Fatal("request should support image generation with gpt-5.5 responses image tool")
	}
	if request.Size != "16:9" || request.Quality != "high" {
		t.Fatalf("request size/quality = %q/%q, want 16:9/high", request.Size, request.Quality)
	}
	if request.ResponseFormat != "b64_json" {
		t.Fatalf("response format = %q, want b64_json", request.ResponseFormat)
	}
	if request.OutputFormat != "webp" {
		t.Fatalf("output format = %q, want webp", request.OutputFormat)
	}
	if request.OutputCompression == nil || *request.OutputCompression != 37 {
		t.Fatalf("output compression = %#v, want 37", request.OutputCompression)
	}
}

func TestResponseImageGenerationToolAcceptsTypedToolSlice(t *testing.T) {
	body := map[string]any{
		"model": "gpt-image-2",
		"input": "生成封面",
		"tools": []map[string]any{
			{"type": "image_generation", "size": "2880x2880", "output_format": "png"},
		},
	}
	if !HasResponseImageGenerationTool(body) {
		t.Fatal("typed []map[string]any tools should route as responses image generation")
	}
	request, prompt, err := ResponseImageGenerationRequest(body, "admin", nil)
	if err != nil {
		t.Fatalf("ResponseImageGenerationRequest() error = %v", err)
	}
	if prompt != "生成封面" {
		t.Fatalf("prompt = %q, want 生成封面", prompt)
	}
	if !request.ResponsesImageTool {
		t.Fatal("ResponsesImageTool = false, want true")
	}
	if request.Size != "2880x2880" {
		t.Fatalf("request size = %q, want 2880x2880", request.Size)
	}
	if !request.RequirePaidAccount {
		t.Fatal("RequirePaidAccount = false, want true for 2880x2880")
	}
}

func TestResponseImageGenerationRequestDefaultsImageModelForAuto(t *testing.T) {
	body := map[string]any{
		"model": "auto",
		"input": "生成封面",
		"tools": []any{
			map[string]any{"type": "image_generation"},
		},
	}
	request, _, err := ResponseImageGenerationRequest(body, "", nil)
	if err != nil {
		t.Fatalf("ResponseImageGenerationRequest() error = %v", err)
	}
	if request.Model != "auto" {
		t.Fatalf("model = %q, want auto", request.Model)
	}
	if request.Size != "auto" {
		t.Fatalf("size = %q, want auto", request.Size)
	}
	if !request.AllowCodexFallback {
		t.Fatal("AllowCodexFallback = false, want true for auto response-image")
	}
}

func TestResponseImageGenerationRequestAutoMediumUsesSingleWorker(t *testing.T) {
	body := map[string]any{
		"model": "auto",
		"input": "画图迪士尼风格街道",
		"tools": []any{
			map[string]any{"type": "image_generation", "quality": "medium", "output_format": "webp"},
		},
		"n": 1,
	}
	request, _, err := ResponseImageGenerationRequest(body, "", nil)
	if err != nil {
		t.Fatalf("ResponseImageGenerationRequest() error = %v", err)
	}
	if !isFastImageRequest(request.Normalized()) {
		t.Fatalf("request should use fast image scheduling: %#v", request.Normalized())
	}
	if timeout := defaultImageStreamAttemptTimeoutForRequest(request.Normalized()); timeout != fastImageStreamAttemptTimeout {
		t.Fatalf("timeout = %s, want %s", timeout, fastImageStreamAttemptTimeout)
	}
	if workers := imageStreamWorkerCount(request.Normalized()); workers != 1 {
		t.Fatalf("worker count = %d, want 1", workers)
	}
}

func TestToolCallParsing(t *testing.T) {
	text := `先处理
<tool_calls><tool_call><tool_name>read_file</tool_name><parameters><path><![CDATA[internal/app.go]]></path><limit>5</limit></parameters></tool_call></tool_calls>`
	calls := ParseToolCalls(text)
	if len(calls) != 1 {
		t.Fatalf("ParseToolCalls() = %#v", calls)
	}
	if calls[0].Name != "read_file" {
		t.Fatalf("tool name = %q", calls[0].Name)
	}
	if calls[0].Input["path"] != "internal/app.go" || calls[0].Input["limit"] != float64(5) {
		t.Fatalf("tool input = %#v", calls[0].Input)
	}
	if visible := StreamableText(text); visible != "先处理" {
		t.Fatalf("StreamableText() = %q", visible)
	}
	if stripped := StripToolMarkup(text); stripped != "先处理" {
		t.Fatalf("StripToolMarkup() = %q", stripped)
	}
}

func TestApplyPatchOpExtractsTextFromValueMap(t *testing.T) {
	got := ApplyPatchOp(map[string]any{
		"o": "append",
		"v": map[string]any{"type": "text", "text": "增量文本"},
	}, "已有", "")
	if got != "已有增量文本" {
		t.Fatalf("ApplyPatchOp() = %q", got)
	}

	replaced := ApplyPatchOp(map[string]any{
		"o": "replace",
		"v": map[string]any{"content": map[string]any{"parts": []any{map[string]any{"v": "正文"}}}},
	}, "旧", "")
	if replaced != "正文" {
		t.Fatalf("ApplyPatchOp replace = %q", replaced)
	}
}

func TestAssistantHistoryTextUsesOnlyTextParts(t *testing.T) {
	messages := []map[string]any{{
		"role": "assistant",
		"content": []any{
			map[string]any{"type": "text", "text": "可见文本"},
			map[string]any{"type": "image_url", "image_url": map[string]any{"url": "data:image/png;base64,cG5n"}},
		},
	}}
	if got := AssistantHistoryText(messages); got != "可见文本" {
		t.Fatalf("AssistantHistoryText() = %q", got)
	}
	history := AssistantHistoryMessages(messages)
	if len(history) != 1 || history[0] != "可见文本" {
		t.Fatalf("AssistantHistoryMessages() = %#v", history)
	}
}

func TestStreamImageResponseErrorsWhenNoImageOutput(t *testing.T) {
	outputs := make(chan ImageOutput)
	close(outputs)
	events, errCh := StreamImageResponse(outputs, "draw", "gpt-image-2")
	var count int
	for range events {
		count++
	}
	if count != 1 {
		t.Fatalf("event count = %d, want response.created only", count)
	}
	if err := <-errCh; err == nil || err.Error() != "image generation failed" {
		t.Fatalf("err = %v", err)
	}
}

func TestCollectImageOutputsMarksTextResponse(t *testing.T) {
	outputs := make(chan ImageOutput)
	close(outputs)
	errCh := make(chan error, 1)
	errCh <- &ImageGenerationError{Message: "text response", StatusCode: 400, Type: "invalid_request_error", Code: "image_generation_text_response"}
	close(errCh)

	result, err := (&Engine{}).CollectImageOutputs(outputs, errCh)
	if err == nil {
		t.Fatal("CollectImageOutputs() err = nil, want text response error")
	}
	if result["output_type"] != "text" {
		t.Fatalf("output_type = %#v, want text in %#v", result["output_type"], result)
	}
	if result["message"] != "text response" {
		t.Fatalf("message = %#v, want text response", result["message"])
	}
}

func TestCollectImageOutputsWithLimitClampsExtraImageResults(t *testing.T) {
	outputs := make(chan ImageOutput, 2)
	outputs <- ImageOutput{Kind: "result", Created: 123, Data: []map[string]any{
		{"url": "https://example.test/1.png"},
		{"url": "https://example.test/2.png"},
	}}
	outputs <- ImageOutput{Kind: "result", Created: 123, Data: []map[string]any{
		{"url": "https://example.test/3.png"},
		{"url": "https://example.test/4.png"},
	}}
	close(outputs)
	errCh := make(chan error, 1)
	errCh <- nil
	close(errCh)

	result, err := (&Engine{}).CollectImageOutputsWithLimit(outputs, errCh, 3)
	if err != nil {
		t.Fatalf("CollectImageOutputsWithLimit() error = %v", err)
	}
	data := util.AsMapSlice(result["data"])
	if len(data) != 3 {
		t.Fatalf("data length = %d, want 3: %#v", len(data), data)
	}
	if data[2]["url"] != "https://example.test/3.png" {
		t.Fatalf("third result = %#v", data[2])
	}
}

func TestStreamTextResponseEventsPropagatesUpstreamError(t *testing.T) {
	deltas := make(chan string, 1)
	upstreamErr := make(chan error, 1)
	deltas <- "partial"
	close(deltas)
	upstreamErr <- errors.New("upstream failed")
	close(upstreamErr)

	events, errCh := streamTextResponseEvents(context.Background(), "auto", deltas, upstreamErr)
	var types []string
	for event := range events {
		if eventType, ok := event["type"].(string); ok {
			types = append(types, eventType)
		}
	}
	if err := <-errCh; err == nil || err.Error() != "upstream failed" {
		t.Fatalf("err = %v, want upstream failed", err)
	}
	for _, eventType := range types {
		if eventType == "response.completed" || eventType == "response.output_text.done" {
			t.Fatalf("unexpected completion event after upstream error: %v", types)
		}
	}
}

func TestHandleImageGenerationsValidatesPromptAndCount(t *testing.T) {
	engine := &Engine{}
	for _, tc := range []struct {
		name string
		body map[string]any
		want string
	}{
		{name: "empty prompt", body: map[string]any{"n": 1}, want: "prompt is required"},
		{name: "too many images", body: map[string]any{"prompt": "draw", "n": 5}, want: "n must be between 1 and 4"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := engine.HandleImageGenerations(context.Background(), tc.body)
			var httpErr HTTPError
			if !errors.As(err, &httpErr) {
				t.Fatalf("err = %T %v, want HTTPError", err, err)
			}
			if httpErr.Status != 400 || httpErr.Message != tc.want {
				t.Fatalf("HTTPError = %#v, want status 400 message %q", httpErr, tc.want)
			}
		})
	}
}

func TestAllowCodexImageFallbackForAutoModel(t *testing.T) {
	for _, model := range []string{"", util.ImageModelAuto, util.ImageModelGPT} {
		if !allowCodexImageFallbackForModel(model) {
			t.Fatalf("allowCodexImageFallbackForModel(%q) = false, want true", model)
		}
	}
	for _, model := range []string{util.ImageModelCodex, util.ImageModelGPT54, "other"} {
		if allowCodexImageFallbackForModel(model) {
			t.Fatalf("allowCodexImageFallbackForModel(%q) = true, want false", model)
		}
	}
}

func TestApplyImageOutputOptionsToRequest(t *testing.T) {
	jpegRequest := ConversationRequest{}
	applyImageOutputOptionsToRequest(&jpegRequest, ImageOutputOptions{Format: "jpeg", Compression: ptrInt(25)})
	jpegRequest = jpegRequest.Normalized()
	if jpegRequest.OutputFormat != "jpeg" || jpegRequest.OutputCompression == nil || *jpegRequest.OutputCompression != 25 {
		t.Fatalf("jpeg output options = %#v/%#v, want jpeg/25", jpegRequest.OutputFormat, jpegRequest.OutputCompression)
	}

	pngRequest := ConversationRequest{}
	applyImageOutputOptionsToRequest(&pngRequest, ImageOutputOptions{Format: "png", Compression: ptrInt(25)})
	pngRequest = pngRequest.Normalized()
	if pngRequest.OutputFormat != "png" || pngRequest.OutputCompression != nil {
		t.Fatalf("png output options = %#v/%#v, want png/nil", pngRequest.OutputFormat, pngRequest.OutputCompression)
	}
}

func TestMergeSystemUsesCompactToolRuleForClaudeCode(t *testing.T) {
	merged := MergeSystem("You are Claude Code, an agent.", "Available tools:\nTool: read_file\n\nTool use rules:\nverbose")
	text, ok := merged.(string)
	if !ok {
		t.Fatalf("MergeSystem() = %T, want string", merged)
	}
	if strings.Contains(text, "Available tools:") {
		t.Fatalf("MergeSystem() kept verbose tool prompt: %q", text)
	}
	if !strings.Contains(text, "Tool output adapter") || !strings.Contains(text, "<tool_calls>") {
		t.Fatalf("MergeSystem() missing compact XML rule: %q", text)
	}
}
