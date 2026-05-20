package protocol

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"html"
	"regexp"
	"strings"
	"time"

	"chatgpt2api/internal/backend"
	"chatgpt2api/internal/util"
)

type StreamResult struct {
	Items <-chan map[string]any
	Err   <-chan error
	Kind  string
}

const xmlToolRule = "Tool output adapter: when calling tools, output ONLY this XML and no prose/markdown:\n<tool_calls><tool_call><tool_name>TOOL_NAME</tool_name><parameters><PARAM><![CDATA[value]]></PARAM></parameters></tool_call></tool_calls>"

func allowCodexImageFallbackForModel(model string) bool {
	switch strings.TrimSpace(model) {
	case "", util.ImageModelAuto, util.ImageModelGPT:
		return true
	default:
		return false
	}
}

func (e *Engine) HandleImageGenerations(ctx context.Context, body map[string]any) (map[string]any, *StreamResult, error) {
	prompt := util.Clean(body["prompt"])
	if prompt == "" {
		return nil, nil, HTTPError{Status: 400, Message: "prompt is required"}
	}
	rawModel := util.Clean(body["model"])
	model := firstNonEmpty(rawModel, util.ImageModelAuto)
	n, err := ParseImageCount(body["n"])
	if err != nil {
		return nil, nil, err
	}
	size := util.Clean(body["size"])
	quality := util.Clean(body["quality"])
	outputFormat := NormalizeImageOutputFormat(util.Clean(body["output_format"]))
	outputCompression, hasOutputCompression := normalizedImageOutputCompression(body["output_compression"])
	responseFormat := firstNonEmpty(util.Clean(body["response_format"]), "b64_json")
	baseURL := util.Clean(body["base_url"])
	request := ConversationRequest{Prompt: prompt, Model: model, Messages: NormalizeMessages(util.AsMapSlice(body["messages"]), nil), N: n, Size: size, Quality: quality, OutputFormat: outputFormat, ResponseFormat: responseFormat, BaseURL: baseURL, OwnerID: util.Clean(body["owner_id"]), OwnerName: util.Clean(body["owner_name"]), MessageAsError: true, RequirePaidAccount: RequiresPaidImageSize(size), AllowCodexFallback: allowCodexImageFallbackForModel(rawModel)}
	if hasOutputCompression {
		request.OutputCompression = &outputCompression
	}
	request = request.Normalized()
	outputs, errCh := e.StreamImageOutputsWithPool(ctx, request)
	if util.ToBool(body["stream"]) {
		return nil, &StreamResult{Items: StreamImageChunks(outputs), Err: errCh, Kind: "openai"}, nil
	}
	result, err := e.CollectImageOutputsWithLimit(outputs, errCh, request.N)
	return result, nil, err
}

func (e *Engine) HandleImageEdits(ctx context.Context, body map[string]any, images []UploadedImage) (map[string]any, *StreamResult, error) {
	encoded := EncodeImages(images)
	if len(encoded) == 0 {
		return nil, nil, &ImageGenerationError{Message: "image is required", StatusCode: 502, Type: "server_error", Code: "upstream_error"}
	}
	size := util.Clean(body["size"])
	rawModel := util.Clean(body["model"])
	request := ConversationRequest{
		Prompt:             util.Clean(body["prompt"]),
		Model:              firstNonEmpty(rawModel, util.ImageModelAuto),
		N:                  util.ToInt(body["n"], 1),
		Size:               size,
		Quality:            util.Clean(body["quality"]),
		OutputFormat:       NormalizeImageOutputFormat(util.Clean(body["output_format"])),
		ResponseFormat:     firstNonEmpty(util.Clean(body["response_format"]), "b64_json"),
		BaseURL:            util.Clean(body["base_url"]),
		OwnerID:            util.Clean(body["owner_id"]),
		OwnerName:          util.Clean(body["owner_name"]),
		Messages:           NormalizeMessages(util.AsMapSlice(body["messages"]), nil),
		Images:             encoded,
		MessageAsError:     true,
		RequirePaidAccount: RequiresPaidImageSize(size),
		AllowCodexFallback: allowCodexImageFallbackForModel(rawModel),
	}
	if compression, ok := normalizedImageOutputCompression(body["output_compression"]); ok {
		request.OutputCompression = &compression
	}
	request = request.Normalized()
	outputs, errCh := e.StreamImageOutputsWithPool(ctx, request)
	if util.ToBool(body["stream"]) {
		return nil, &StreamResult{Items: StreamImageChunks(outputs), Err: errCh, Kind: "openai"}, nil
	}
	result, err := e.CollectImageOutputsWithLimit(outputs, errCh, request.N)
	return result, nil, err
}

func StreamImageChunks(outputs <-chan ImageOutput) <-chan map[string]any {
	out := make(chan map[string]any)
	go func() {
		defer close(out)
		for output := range outputs {
			out <- output.Chunk()
		}
	}()
	return out
}

func (e *Engine) HandleChatCompletions(ctx context.Context, body map[string]any) (map[string]any, *StreamResult, error) {
	if util.ToBool(body["stream"]) {
		var items <-chan map[string]any
		var errCh <-chan error
		if IsImageChatRequest(body) {
			items, errCh = e.ImageChatEvents(ctx, body)
		} else {
			request, err := TextChatRequestFromBody(body)
			if err != nil {
				return nil, nil, err
			}
			items, errCh = e.StreamTextChatCompletionWithPoolRequest(ctx, request)
		}
		return nil, &StreamResult{Items: items, Err: errCh, Kind: "openai"}, nil
	}
	if IsImageChatRequest(body) {
		return e.ImageChatResponse(ctx, body)
	}
	request, err := TextChatRequestFromBody(body)
	if err != nil {
		return nil, nil, err
	}
	text, err := e.CollectTextWithPool(ctx, request)
	if err != nil {
		return nil, nil, err
	}
	return CompletionResponse(request.Model, text, 0, request.Messages), nil, nil
}

func CompletionChunk(model string, delta map[string]any, finishReason any, completionID string, created int64) map[string]any {
	if completionID == "" {
		completionID = "chatcmpl-" + util.NewHex(32)
	}
	if created == 0 {
		created = time.Now().Unix()
	}
	return map[string]any{"id": completionID, "object": "chat.completion.chunk", "created": created, "model": model, "choices": []map[string]any{{"index": 0, "delta": delta, "finish_reason": finishReason}}}
}

func CompletionResponse(model, content string, created int64, messages []map[string]any) map[string]any {
	if created == 0 {
		created = time.Now().Unix()
	}
	promptTokens, completionTokens := 0, 0
	if len(messages) > 0 {
		promptTokens = CountMessageTokens(messages, model)
		completionTokens = CountTextTokens(content, model)
	}
	return map[string]any{
		"id": "chatcmpl-" + util.NewHex(32), "object": "chat.completion", "created": created, "model": model,
		"choices": []map[string]any{{"index": 0, "message": map[string]any{"role": "assistant", "content": content}, "finish_reason": "stop"}},
		"usage":   map[string]any{"prompt_tokens": promptTokens, "completion_tokens": completionTokens, "total_tokens": promptTokens + completionTokens},
	}
}

func (e *Engine) StreamTextChatCompletion(ctx context.Context, client *backend.Client, messages []map[string]any, model string) (<-chan map[string]any, <-chan error) {
	out := make(chan map[string]any)
	errOut := make(chan error, 1)
	go func() {
		defer close(out)
		defer close(errOut)
		deltas, errCh := e.StreamTextDeltas(ctx, client, ConversationRequest{Model: model, Messages: messages})
		id := "chatcmpl-" + util.NewHex(32)
		created := time.Now().Unix()
		sentRole := false
		for deltaText := range deltas {
			if !sentRole {
				sentRole = true
				out <- CompletionChunk(model, map[string]any{"role": "assistant", "content": deltaText}, nil, id, created)
			} else {
				out <- CompletionChunk(model, map[string]any{"content": deltaText}, nil, id, created)
			}
		}
		if err := <-errCh; err != nil {
			errOut <- err
			return
		}
		if !sentRole {
			out <- CompletionChunk(model, map[string]any{"role": "assistant", "content": ""}, nil, id, created)
		}
		out <- CompletionChunk(model, map[string]any{}, "stop", id, created)
		errOut <- nil
	}()
	return out, errOut
}

func (e *Engine) StreamTextChatCompletionWithPool(ctx context.Context, messages []map[string]any, model string) (<-chan map[string]any, <-chan error) {
	return e.StreamTextChatCompletionWithPoolRequest(ctx, ConversationRequest{Model: model, Messages: messages})
}

func (e *Engine) StreamTextChatCompletionWithPoolRequest(ctx context.Context, request ConversationRequest) (<-chan map[string]any, <-chan error) {
	out := make(chan map[string]any)
	errOut := make(chan error, 1)
	go func() {
		defer close(out)
		defer close(errOut)
		attempted := map[string]struct{}{}
		lastError := ""
		for {
			token := e.textAccessTokenForRequest(request, attempted)
			if token == "" {
				if lastError != "" {
					errOut <- NewImageGenerationError(lastError)
					return
				}
				errOut <- NewImageGenerationError(noTextAccountMessage(request))
				return
			}
			attempted[token] = struct{}{}
			items, itemErr := e.StreamTextChatCompletion(ctx, e.TextBackend(token), request.Messages, request.Model)
			emitted := false
			for item := range items {
				emitted = true
				out <- item
			}
			err := <-itemErr
			if err == nil {
				errOut <- nil
				return
			}
			lastError = err.Error()
			if normalized, handled := e.Accounts.ApplyAccountErrorMessage(token, "text_stream", lastError); handled {
				lastError = normalized
				if !emitted {
					continue
				}
			}
			errOut <- err
			return
		}
	}()
	return out, errOut
}

func (e *Engine) StreamTextDeltasWithPool(ctx context.Context, request ConversationRequest) (<-chan string, <-chan error) {
	out := make(chan string)
	errOut := make(chan error, 1)
	go func() {
		defer close(out)
		defer close(errOut)
		attempted := map[string]struct{}{}
		lastError := ""
		for {
			token := e.textAccessTokenForRequest(request, attempted)
			if token == "" {
				if lastError != "" {
					errOut <- NewImageGenerationError(lastError)
					return
				}
				errOut <- NewImageGenerationError(noTextAccountMessage(request))
				return
			}
			attempted[token] = struct{}{}
			deltas, deltaErr := e.StreamTextDeltas(ctx, e.TextBackend(token), request)
			emitted := false
			for delta := range deltas {
				emitted = true
				out <- delta
			}
			err := <-deltaErr
			if err == nil {
				errOut <- nil
				return
			}
			lastError = err.Error()
			if normalized, handled := e.Accounts.ApplyAccountErrorMessage(token, "text_stream", lastError); handled {
				lastError = normalized
				if !emitted {
					continue
				}
			}
			errOut <- err
			return
		}
	}()
	return out, errOut
}

func (e *Engine) CollectTextWithPool(ctx context.Context, request ConversationRequest) (string, error) {
	attempted := map[string]struct{}{}
	lastError := ""
	for {
		token := e.textAccessTokenForRequest(request, attempted)
		if token == "" {
			if lastError != "" {
				return "", NewImageGenerationError(lastError)
			}
			return "", NewImageGenerationError(noTextAccountMessage(request))
		}
		attempted[token] = struct{}{}
		text, err := e.CollectText(ctx, e.TextBackend(token), request)
		if err == nil {
			return text, nil
		}
		lastError = err.Error()
		if normalized, handled := e.Accounts.ApplyAccountErrorMessage(token, "text_stream", lastError); handled {
			lastError = normalized
			continue
		}
		return "", err
	}
}

func (e *Engine) textAccessTokenForRequest(request ConversationRequest, attempted map[string]struct{}) string {
	if request.VisionRequired {
		return e.Accounts.GetVisionAccessTokenExcluding(attempted)
	}
	return e.Accounts.GetTextAccessTokenExcluding(attempted)
}

func noTextAccountMessage(request ConversationRequest) string {
	if request.VisionRequired {
		return "no available vision account"
	}
	return "no available text account"
}

func ChatMessagesFromBody(body map[string]any) ([]map[string]any, error) {
	if messages := util.AsMapSlice(body["messages"]); len(messages) > 0 {
		return messages, nil
	}
	if prompt := strings.TrimSpace(util.Clean(body["prompt"])); prompt != "" {
		return []map[string]any{{"role": "user", "content": prompt}}, nil
	}
	return nil, HTTPError{Status: 400, Message: "messages or prompt is required"}
}

func TextChatParts(body map[string]any) (string, []map[string]any, error) {
	request, err := TextChatRequestFromBody(body)
	if err != nil {
		return "", nil, err
	}
	return request.Model, request.Messages, nil
}

func TextChatRequestFromBody(body map[string]any) (ConversationRequest, error) {
	model := firstNonEmpty(util.Clean(body["model"]), "auto")
	messages, err := ChatMessagesFromBody(body)
	if err != nil {
		return ConversationRequest{}, err
	}
	return ConversationRequest{Model: model, Messages: NormalizeConversationMessages(messages, nil), VisionRequired: ChatMessagesHaveImages(messages)}, nil
}

func ChatMessagesHaveImages(messages []map[string]any) bool {
	for _, message := range messages {
		if len(ExtractImagesFromMessageContent(message["content"])) > 0 {
			return true
		}
	}
	return false
}

func IsImageChatRequest(body map[string]any) bool {
	if util.IsImageModel(util.Clean(body["model"])) {
		return true
	}
	for _, item := range anyList(body["modalities"]) {
		if strings.ToLower(util.Clean(item)) == "image" {
			return true
		}
	}
	return false
}

func (e *Engine) ImageChatResponse(ctx context.Context, body map[string]any) (map[string]any, *StreamResult, error) {
	model, prompt, n, images, messages, err := ChatImageArgs(body)
	if err != nil {
		return nil, nil, err
	}
	size := util.Clean(body["size"])
	request := ConversationRequest{Prompt: prompt, Model: model, Messages: messages, N: n, Size: size, Quality: util.Clean(body["quality"]), ResponseFormat: "b64_json", OwnerID: util.Clean(body["owner_id"]), OwnerName: util.Clean(body["owner_name"]), Images: EncodeImages(images), RequirePaidAccount: RequiresPaidImageSize(size), AllowCodexFallback: allowCodexImageFallbackForModel(util.Clean(body["model"]))}
	applyImageOutputOptionsToRequest(&request, ImageOutputOptionsFromPayload(body))
	outputs, errCh := e.StreamImageOutputsWithPool(ctx, request.Normalized())
	result, err := e.CollectImageOutputs(outputs, errCh)
	if err != nil {
		return nil, nil, err
	}
	return CompletionResponse(model, ImageResultContent(result), int64(util.ToInt(result["created"], 0)), nil), nil, nil
}

func (e *Engine) ImageChatEvents(ctx context.Context, body map[string]any) (<-chan map[string]any, <-chan error) {
	out := make(chan map[string]any)
	errOut := make(chan error, 1)
	go func() {
		defer close(out)
		defer close(errOut)
		model, prompt, n, images, messages, err := ChatImageArgs(body)
		if err != nil {
			errOut <- err
			return
		}
		size := util.Clean(body["size"])
		request := ConversationRequest{Prompt: prompt, Model: model, Messages: messages, N: n, Size: size, Quality: util.Clean(body["quality"]), ResponseFormat: "b64_json", OwnerID: util.Clean(body["owner_id"]), OwnerName: util.Clean(body["owner_name"]), Images: EncodeImages(images), RequirePaidAccount: RequiresPaidImageSize(size), AllowCodexFallback: allowCodexImageFallbackForModel(util.Clean(body["model"]))}
		applyImageOutputOptionsToRequest(&request, ImageOutputOptionsFromPayload(body))
		outputs, errCh := e.StreamImageOutputsWithPool(ctx, request.Normalized())
		id := "chatcmpl-" + util.NewHex(32)
		created := time.Now().Unix()
		sentRole := false
		sentText := ""
		for output := range outputs {
			content := ""
			switch output.Kind {
			case "progress":
				content = output.Text
				sentText += content
			case "result":
				content = BuildChatImageMarkdownContent(map[string]any{"data": output.Data})
			case "message":
				content = output.Text
				content = strings.TrimPrefix(content, sentText)
			}
			if content == "" {
				continue
			}
			if !sentRole {
				sentRole = true
				out <- CompletionChunk(model, map[string]any{"role": "assistant", "content": content}, nil, id, created)
			} else {
				out <- CompletionChunk(model, map[string]any{"content": content}, nil, id, created)
			}
		}
		if err := <-errCh; err != nil {
			errOut <- err
			return
		}
		if !sentRole {
			out <- CompletionChunk(model, map[string]any{"role": "assistant", "content": ""}, nil, id, created)
		}
		out <- CompletionChunk(model, map[string]any{}, "stop", id, created)
		errOut <- nil
	}()
	return out, errOut
}

func applyImageOutputOptionsToRequest(request *ConversationRequest, options ImageOutputOptions) {
	if request == nil {
		return
	}
	request.OutputFormat = options.Format
	request.OutputCompression = options.Compression
}

func ChatImageArgs(body map[string]any) (string, string, int, []UploadedImage, []map[string]any, error) {
	model := firstNonEmpty(util.Clean(body["model"]), util.ImageModelAuto)
	rawMessages, err := ChatMessagesFromBody(body)
	if err != nil {
		return "", "", 0, nil, nil, err
	}
	messages := NormalizeMessages(rawMessages, nil)
	prompt := LatestUserPrompt(messages)
	if prompt == "" {
		prompt = ExtractChatPrompt(body)
	}
	if prompt == "" {
		return "", "", 0, nil, nil, HTTPError{Status: 400, Message: "prompt is required"}
	}
	n, err := ParseImageCount(body["n"])
	if err != nil {
		return "", "", 0, nil, nil, err
	}
	images := ExtractChatContextImages(body)
	return model, prompt, n, images, messages, nil
}

func ImageResultContent(result map[string]any) string {
	if data := util.AsMapSlice(result["data"]); len(data) > 0 {
		return BuildChatImageMarkdownContent(result)
	}
	return firstNonEmpty(util.Clean(result["message"]), "Image generation completed.")
}

func ParseImageCount(raw any) (int, error) {
	value := util.ToInt(raw, 1)
	if value < 1 || value > 4 {
		return 0, HTTPError{Status: 400, Message: "n must be between 1 and 4"}
	}
	return value, nil
}

func BuildChatImageMarkdownContent(imageResult map[string]any) string {
	var parts []string
	for index, item := range util.AsMapSlice(imageResult["data"]) {
		b64 := util.Clean(item["b64_json"])
		if b64 != "" {
			parts = append(parts, fmt.Sprintf("![image_%d](data:image/png;base64,%s)", index+1, b64))
		}
	}
	if len(parts) == 0 {
		return "Image generation completed."
	}
	return strings.Join(parts, "\n\n")
}

func ExtractChatPrompt(body map[string]any) string {
	if prompt := strings.TrimSpace(util.Clean(body["prompt"])); prompt != "" {
		return prompt
	}
	messages := NormalizeMessages(util.AsMapSlice(body["messages"]), nil)
	if prompt := LatestUserPrompt(messages); prompt != "" {
		return prompt
	}
	for _, message := range util.AsMapSlice(body["messages"]) {
		if strings.ToLower(util.Clean(message["role"])) != "user" {
			continue
		}
		if prompt := ExtractPromptFromMessageContent(message["content"]); prompt != "" {
			return prompt
		}
	}
	return ""
}

func ExtractChatImages(body map[string]any) []UploadedImage {
	messages := util.AsMapSlice(body["messages"])
	for i := len(messages) - 1; i >= 0; i-- {
		if strings.ToLower(util.Clean(messages[i]["role"])) != "user" {
			continue
		}
		images := ExtractImagesFromMessageContent(messages[i]["content"])
		if len(images) > 0 {
			return images
		}
	}
	return nil
}

func ExtractChatContextImages(body map[string]any) []UploadedImage {
	var images []UploadedImage
	for _, message := range util.AsMapSlice(body["messages"]) {
		images = append(images, ExtractImagesFromMessageContent(message["content"])...)
	}
	if len(images) > maxContextImages {
		images = images[len(images)-maxContextImages:]
	}
	return images
}

func ExtractPromptFromMessageContent(content any) string {
	if text, ok := content.(string); ok {
		return strings.TrimSpace(text)
	}
	var parts []string
	for _, raw := range anyList(content) {
		item, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		switch util.Clean(item["type"]) {
		case "text":
			if text := strings.TrimSpace(util.Clean(item["text"])); text != "" {
				parts = append(parts, text)
			}
		case "input_text":
			if text := strings.TrimSpace(firstNonEmpty(util.Clean(item["text"]), util.Clean(item["input_text"]))); text != "" {
				parts = append(parts, text)
			}
		}
	}
	return strings.TrimSpace(strings.Join(parts, "\n"))
}

func ExtractImagesFromMessageContent(content any) []UploadedImage {
	if text, ok := content.(string); ok {
		return ExtractImagesFromText(text)
	}
	var images []UploadedImage
	for _, raw := range anyList(content) {
		item, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		itemType := util.Clean(item["type"])
		imageURL := ""
		if itemType == "image_url" {
			if obj, ok := item["image_url"].(map[string]any); ok {
				imageURL = util.Clean(obj["url"])
			} else {
				imageURL = util.Clean(item["image_url"])
			}
		}
		if itemType == "input_image" {
			imageURL = util.Clean(item["image_url"])
		}
		if strings.HasPrefix(imageURL, "data:") {
			header, data, _ := strings.Cut(imageURL, ",")
			mime := strings.TrimPrefix(strings.Split(header, ";")[0], "data:")
			bytes, err := base64.StdEncoding.DecodeString(data)
			if err == nil {
				images = append(images, UploadedImage{Data: bytes, Filename: "image.png", ContentType: firstNonEmpty(mime, "image/png")})
			}
		}
	}
	return images
}

func ExtractImagesFromText(text string) []UploadedImage {
	var images []UploadedImage
	re := regexp.MustCompile(`data:(image/[A-Za-z0-9.+-]+);base64,([A-Za-z0-9+/=]+)`)
	for _, match := range re.FindAllStringSubmatch(text, -1) {
		if len(match) < 3 {
			continue
		}
		bytes, err := base64.StdEncoding.DecodeString(match[2])
		if err == nil {
			images = append(images, UploadedImage{Data: bytes, Filename: "image.png", ContentType: firstNonEmpty(match[1], "image/png")})
		}
	}
	return images
}

func (e *Engine) HandleResponses(ctx context.Context, body map[string]any) (map[string]any, *StreamResult, error) {
	return e.HandleResponsesScoped(ctx, body, "")
}

func (e *Engine) HandleResponsesScoped(ctx context.Context, body map[string]any, scope string) (map[string]any, *StreamResult, error) {
	events, errCh, err := e.ResponseEventsScoped(ctx, body, scope)
	if err != nil {
		return nil, nil, err
	}
	if util.ToBool(body["stream"]) {
		return nil, &StreamResult{Items: events, Err: errCh, Kind: "openai"}, nil
	}
	completed := map[string]any{}
	for event := range events {
		if event["type"] == "response.completed" {
			if response, ok := event["response"].(map[string]any); ok {
				completed = response
			}
		}
	}
	if err := <-errCh; err != nil {
		return nil, nil, err
	}
	if len(completed) == 0 {
		return nil, nil, fmt.Errorf("response generation failed")
	}
	return completed, nil, nil
}

func (e *Engine) ResponseEvents(ctx context.Context, body map[string]any) (<-chan map[string]any, <-chan error, error) {
	return e.ResponseEventsScoped(ctx, body, "")
}

func (e *Engine) ResponseEventsScoped(ctx context.Context, body map[string]any, scope string) (<-chan map[string]any, <-chan error, error) {
	previous, err := e.responseContextFromPreviousScoped(scope, body["previous_response_id"])
	if err != nil {
		return nil, nil, err
	}
	responseModel := firstNonEmpty(util.Clean(body["model"]), "auto")
	currentMessages := MessagesFromInput(body["input"], body["instructions"])
	baseContext := MergeResponseContext(previous, currentMessages, nil)
	if !HasResponseImageGenerationTool(body) {
		events, errCh := e.StreamTextResponseWithMessages(ctx, responseModel, baseContext.Messages)
		events = e.rememberResponseContextEventsScoped(scope, events, baseContext)
		return events, errCh, nil
	}
	request, prompt, err := ResponseImageGenerationRequest(body, scope, &previous)
	if err != nil {
		return nil, nil, err
	}
	var currentImages []string
	if inputImages := ExtractResponseImages(body["input"]); len(inputImages) > 0 {
		currentImages = EncodeImages(inputImages)
	}
	baseContext = MergeResponseContext(previous, currentMessages, currentImages)
	request.Messages = baseContext.Messages
	outputs, errCh := e.StreamImageOutputsWithPool(ctx, request)
	events, responseErr := StreamImageResponse(outputs, prompt, responseModel)
	events = e.rememberResponseContextEventsScoped(scope, events, baseContext)
	return events, combineErrorChannels(errCh, responseErr), nil
}

func ResponseImageGenerationRequest(body map[string]any, scope string, previous *ResponseContext) (ConversationRequest, string, error) {
	responseModel := firstNonEmpty(util.Clean(body["model"]), util.ImageModelAuto)
	if !util.IsResponsesImageToolModel(responseModel) {
		return ConversationRequest{}, "", HTTPError{Status: 400, Message: "unsupported image_generation model: " + responseModel}
	}
	messages := MessagesFromInput(body["input"], body["instructions"])
	prompt := LatestUserPrompt(messages)
	if prompt == "" {
		return ConversationRequest{}, "", HTTPError{Status: 400, Message: "input text is required"}
	}
	n, err := ParseImageCount(body["n"])
	if err != nil {
		return ConversationRequest{}, "", err
	}
	tool := ResponseImageGenerationTool(body)
	size := firstNonEmpty(util.Clean(tool["size"]), util.Clean(body["size"]), "auto")
	inputImages := ExtractResponseImages(body["input"])
	if len(inputImages) > 0 && util.Clean(tool["size"]) == "" && util.Clean(body["size"]) == "" {
		size = "auto"
	}
	images := []string(nil)
	if previous != nil {
		images = append(images, previous.Images...)
	}
	images = append(images, EncodeImages(inputImages)...)
	if len(images) > maxContextImages {
		images = images[len(images)-maxContextImages:]
	}
	outputFormat := NormalizeImageOutputFormat(firstNonEmpty(util.Clean(tool["output_format"]), util.Clean(body["output_format"])))
	request := ConversationRequest{
		Prompt:             prompt,
		Model:              responseModel,
		Messages:           messages,
		N:                  n,
		Size:               size,
		Quality:            firstNonEmpty(util.Clean(tool["quality"]), util.Clean(body["quality"])),
		OutputFormat:       outputFormat,
		ResponseFormat:     firstNonEmpty(util.Clean(tool["response_format"]), util.Clean(body["response_format"]), "b64_json"),
		OwnerID:            scope,
		OwnerName:          util.Clean(body["owner_name"]),
		Images:             images,
		RequirePaidAccount: RequiresPaidImageSize(size),
		ResponsesImageTool: true,
		AllowCodexFallback: allowCodexImageFallbackForModel(responseModel),
		MaxImageWorkers:    util.ToInt(body["max_image_workers"], 0),
	}
	if outputFormat != "png" {
		if compression, ok := normalizedImageOutputCompression(firstNonNil(tool["output_compression"], body["output_compression"])); ok {
			request.OutputCompression = &compression
		}
	}
	return request.Normalized(), prompt, nil
}

func (e *Engine) StreamTextResponse(ctx context.Context, body map[string]any) (<-chan map[string]any, <-chan error) {
	model := firstNonEmpty(util.Clean(body["model"]), "auto")
	messages := MessagesFromInput(body["input"], body["instructions"])
	return e.StreamTextResponseWithMessages(ctx, model, messages)
}

func (e *Engine) StreamTextResponseWithMessages(ctx context.Context, model string, messages []map[string]any) (<-chan map[string]any, <-chan error) {
	deltas, errCh := e.StreamTextDeltasWithPool(ctx, ConversationRequest{Model: model, Messages: messages})
	return streamTextResponseEvents(ctx, model, deltas, errCh)
}

func streamTextResponseEvents(ctx context.Context, model string, deltas <-chan string, upstreamErr <-chan error) (<-chan map[string]any, <-chan error) {
	out := make(chan map[string]any)
	errOut := make(chan error, 1)
	go func() {
		defer close(out)
		defer close(errOut)
		responseID := "resp_" + util.NewHex(32)
		itemID := "msg_" + util.NewHex(32)
		created := time.Now().Unix()
		full := ""
		send := func(item map[string]any) bool {
			select {
			case out <- item:
				return true
			case <-ctx.Done():
				errOut <- ctx.Err()
				return false
			}
		}
		if !send(ResponseCreated(responseID, model, created)) {
			return
		}
		if !send(map[string]any{"type": "response.output_item.added", "output_index": 0, "item": TextOutputItem("", itemID, "in_progress")}) {
			return
		}
		for delta := range deltas {
			full += delta
			if !send(map[string]any{"type": "response.output_text.delta", "item_id": itemID, "output_index": 0, "content_index": 0, "delta": delta}) {
				return
			}
		}
		if upstreamErr != nil {
			if err := <-upstreamErr; err != nil {
				errOut <- err
				return
			}
		}
		if !send(map[string]any{"type": "response.output_text.done", "item_id": itemID, "output_index": 0, "content_index": 0, "text": full}) {
			return
		}
		item := TextOutputItem(full, itemID, "completed")
		if !send(map[string]any{"type": "response.output_item.done", "output_index": 0, "item": item}) {
			return
		}
		if !send(ResponseCompleted(responseID, model, created, []map[string]any{item})) {
			return
		}
		errOut <- nil
	}()
	return out, errOut
}

func combineErrorChannels(first, second <-chan error) <-chan error {
	out := make(chan error, 1)
	go func() {
		defer close(out)
		var firstErr error
		var secondErr error
		if first != nil {
			firstErr = <-first
		}
		if second != nil {
			secondErr = <-second
		}
		if firstErr != nil {
			out <- firstErr
			return
		}
		out <- secondErr
	}()
	return out
}

func StreamImageResponse(outputs <-chan ImageOutput, prompt, model string) (<-chan map[string]any, <-chan error) {
	out := make(chan map[string]any)
	errCh := make(chan error, 1)
	go func() {
		defer close(out)
		defer close(errCh)
		responseID := "resp_" + util.NewHex(32)
		created := time.Now().Unix()
		out <- ResponseCreated(responseID, model, created)
		for output := range outputs {
			if output.Kind == "message" {
				item := TextOutputItem(output.Text, "", "completed")
				out <- map[string]any{"type": "response.output_text.delta", "item_id": item["id"], "output_index": 0, "content_index": 0, "delta": output.Text}
				out <- map[string]any{"type": "response.output_text.done", "item_id": item["id"], "output_index": 0, "content_index": 0, "text": output.Text}
				out <- map[string]any{"type": "response.output_item.done", "output_index": 0, "item": item}
				out <- ResponseCompleted(responseID, model, created, []map[string]any{item})
				errCh <- nil
				return
			}
			if output.Kind != "result" {
				continue
			}
			items := ImageOutputItems(prompt, output.Data, "")
			if len(items) > 0 {
				item := items[0]
				out <- map[string]any{"type": "response.output_item.done", "output_index": 0, "item": item}
				out <- ResponseCompleted(responseID, model, created, []map[string]any{item})
				errCh <- nil
				return
			}
		}
		errCh <- fmt.Errorf("image generation failed")
	}()
	return out, errCh
}

func ResponseCreated(id, model string, created int64) map[string]any {
	return map[string]any{"type": "response.created", "response": map[string]any{"id": id, "object": "response", "created_at": created, "status": "in_progress", "error": nil, "incomplete_details": nil, "model": model, "output": []any{}, "parallel_tool_calls": false}}
}

func ResponseCompleted(id, model string, created int64, output []map[string]any) map[string]any {
	return map[string]any{"type": "response.completed", "response": map[string]any{"id": id, "object": "response", "created_at": created, "status": "completed", "error": nil, "incomplete_details": nil, "model": model, "output": output, "parallel_tool_calls": false}}
}

func TextOutputItem(text, itemID, status string) map[string]any {
	if itemID == "" {
		itemID = "msg_" + util.NewHex(32)
	}
	return map[string]any{"id": itemID, "type": "message", "status": status, "role": "assistant", "content": []map[string]any{{"type": "output_text", "text": text, "annotations": []any{}}}}
}

func ImageOutputItems(prompt string, data []map[string]any, itemID string) []map[string]any {
	var out []map[string]any
	for _, item := range data {
		b64 := util.Clean(item["b64_json"])
		if b64 == "" {
			continue
		}
		id := itemID
		if id == "" {
			id = fmt.Sprintf("ig_%d", len(out)+1)
		}
		out = append(out, map[string]any{"id": id, "type": "image_generation_call", "status": "completed", "result": b64, "revised_prompt": firstNonEmpty(util.Clean(item["revised_prompt"]), prompt)})
	}
	return out
}

func HasResponseImageGenerationTool(body map[string]any) bool {
	return len(ResponseImageGenerationTool(body)) > 0
}

func ResponseImageGenerationTool(body map[string]any) map[string]any {
	for _, raw := range anyList(body["tools"]) {
		if tool, ok := raw.(map[string]any); ok && util.Clean(tool["type"]) == "image_generation" {
			return tool
		}
	}
	if choice := util.StringMap(body["tool_choice"]); choice != nil && util.Clean(choice["type"]) == "image_generation" {
		return choice
	}
	return nil
}

func ExtractResponsePrompt(input any) string {
	return LatestUserPrompt(responseInputMessages(input))
}

func ExtractResponseImage(input any) *UploadedImage {
	images := ExtractResponseImages(input)
	if len(images) == 0 {
		return nil
	}
	return &images[0]
}

func ExtractResponseImages(input any) []UploadedImage {
	var images []UploadedImage
	var walk func(any)
	walk = func(value any) {
		if text, ok := value.(string); ok {
			images = append(images, ExtractImagesFromText(text)...)
			return
		}
		if list := anyList(value); list != nil {
			for _, raw := range list {
				walk(raw)
			}
			return
		}
		item, ok := value.(map[string]any)
		if !ok {
			return
		}
		switch util.Clean(item["type"]) {
		case "input_image":
			imageURL := util.Clean(item["image_url"])
			if strings.HasPrefix(imageURL, "data:") {
				images = append(images, ExtractImagesFromMessageContent([]any{item})...)
			}
		case "image_generation_call":
			if result := util.Clean(item["result"]); result != "" {
				if data, err := base64.StdEncoding.DecodeString(result); err == nil {
					images = append(images, UploadedImage{Data: data, Filename: "generated.png", ContentType: "image/png"})
				}
			}
		}
		if item["content"] != nil {
			images = append(images, ExtractImagesFromMessageContent(item["content"])...)
		}
	}
	walk(input)
	if len(images) > maxContextImages {
		images = images[len(images)-maxContextImages:]
	}
	return images
}

func MessagesFromInput(input any, instructions any) []map[string]any {
	var messages []map[string]any
	if system := strings.TrimSpace(util.Clean(instructions)); system != "" {
		messages = append(messages, map[string]any{"role": "system", "content": system})
	}
	messages = append(messages, responseInputMessages(input)...)
	return NormalizeMessages(messages, nil)
}

func responseInputMessages(input any) []map[string]any {
	if text, ok := input.(string); ok {
		if strings.TrimSpace(text) != "" {
			return []map[string]any{{"role": "user", "content": strings.TrimSpace(text)}}
		}
		return nil
	}
	if item, ok := input.(map[string]any); ok {
		if message, ok := responseMessageFromItem(item); ok {
			return []map[string]any{message}
		}
		return nil
	}
	list := anyList(input)
	allTyped := len(list) > 0
	for _, raw := range list {
		item, ok := raw.(map[string]any)
		allTyped = allTyped && ok && item["type"] != nil && item["role"] == nil
	}
	if allTyped {
		var parts []string
		for _, raw := range list {
			if item, ok := raw.(map[string]any); ok {
				if text := responseContentText([]any{item}); text != "" {
					parts = append(parts, text)
				}
			}
		}
		if text := strings.TrimSpace(strings.Join(parts, "\n")); text != "" {
			return []map[string]any{{"role": "user", "content": text}}
		}
		return nil
	}
	var messages []map[string]any
	for _, raw := range list {
		if item, ok := raw.(map[string]any); ok {
			if message, ok := responseMessageFromItem(item); ok {
				messages = append(messages, message)
			}
		}
	}
	return messages
}

func responseMessageFromItem(item map[string]any) (map[string]any, bool) {
	switch util.Clean(item["type"]) {
	case "input_text":
		if text := strings.TrimSpace(util.Clean(item["text"])); text != "" {
			return map[string]any{"role": "user", "content": text}, true
		}
	case "output_text":
		if text := strings.TrimSpace(util.Clean(item["text"])); text != "" {
			return map[string]any{"role": "assistant", "content": text}, true
		}
	case "image_generation_call":
		if prompt := strings.TrimSpace(util.Clean(item["revised_prompt"])); prompt != "" {
			return map[string]any{"role": "assistant", "content": "Generated image: " + prompt}, true
		}
	}
	if util.Clean(item["type"]) == "message" || item["role"] != nil || item["content"] != nil {
		role := firstNonEmpty(util.Clean(item["role"]), "user")
		if text := responseContentText(item["content"]); text != "" {
			return map[string]any{"role": role, "content": text}, true
		}
	}
	return nil, false
}

func (e *Engine) HandleMessages(ctx context.Context, body map[string]any) (map[string]any, *StreamResult, error) {
	request := MessageRequestFromBody(e, body)
	if util.ToBool(body["stream"]) {
		items, errCh := e.StreamAnthropicEvents(ctx, request)
		return nil, &StreamResult{Items: items, Err: errCh, Kind: "anthropic"}, nil
	}
	items, errCh := e.StreamTextChatCompletionWithPoolRequest(ctx, ConversationRequest{Model: request.Model, Messages: request.Messages, VisionRequired: request.VisionRequired})
	text := CollectChatContent(items)
	if err := <-errCh; err != nil {
		return nil, nil, err
	}
	return MessageResponse(request.Model, text, CountMessageTokens(request.Messages, request.Model), CountTextTokens(text, request.Model), request.Tools), nil, nil
}

type MessageRequest struct {
	Messages       []map[string]any
	Model          string
	Tools          any
	VisionRequired bool
}

func MessageRequestFromBody(e *Engine, body map[string]any) MessageRequest {
	payload := util.CopyMap(body)
	payload["messages"] = PreprocessMessages(payload["messages"])
	payload["system"] = MergeSystem(payload["system"], BuildToolPrompt(payload["tools"]))
	messages := NormalizeConversationMessages(payload["messages"], payload["system"])
	return MessageRequest{Messages: messages, Model: firstNonEmpty(util.Clean(payload["model"]), "auto"), Tools: payload["tools"], VisionRequired: ChatMessagesHaveImages(messages)}
}

func BuildToolPrompt(tools any) string {
	var blocks []string
	for _, raw := range anyList(tools) {
		tool, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		fn := util.StringMap(tool["function"])
		name := firstNonEmpty(util.Clean(tool["name"]), util.Clean(fn["name"]))
		desc := firstNonEmpty(util.Clean(tool["description"]), util.Clean(fn["description"]))
		schema := firstNonNil(tool["input_schema"], tool["parameters"], fn["input_schema"], fn["parameters"], map[string]any{})
		if name != "" {
			data, _ := json.Marshal(schema)
			blocks = append(blocks, fmt.Sprintf("Tool: %s\nDescription: %s\nParameters: %s", name, desc, string(data)))
		}
	}
	if len(blocks) == 0 {
		return ""
	}
	return "Available tools:\n" + strings.Join(blocks, "\n") + "\n\nTool use rules:\n- If the user asks to list/read/search files, inspect project state, run a command, or answer from local code, you MUST call a suitable tool first. Do not say you cannot access files.\n- To call tools, output ONLY XML and no prose/markdown:\n<tool_calls><tool_call><tool_name>TOOL_NAME</tool_name><parameters><PARAM><![CDATA[value]]></PARAM></parameters></tool_call></tool_calls>\n- Put parameters under <parameters> using the exact schema names."
}

func MergeSystem(system any, extra string) any {
	system = CompactSystem(system)
	if hasClaudeCodeSystem(system) {
		extra = xmlToolRule
	}
	if extra == "" {
		return system
	}
	if text, ok := system.(string); ok && strings.TrimSpace(text) != "" {
		return strings.TrimSpace(text) + "\n\n" + extra
	}
	if list, ok := system.([]any); ok {
		return append(list, map[string]any{"type": "text", "text": extra})
	}
	return extra
}

func CompactSystem(system any) any {
	switch typed := system.(type) {
	case string:
		return compactSystemText(typed)
	case []any:
		result := make([]any, 0, len(typed))
		for _, item := range typed {
			if block, ok := item.(map[string]any); ok && util.Clean(block["type"]) == "text" {
				copied := util.CopyMap(block)
				copied["text"] = compactSystemText(util.Clean(block["text"]))
				result = append(result, copied)
				continue
			}
			result = append(result, item)
		}
		return result
	default:
		return system
	}
}

func compactSystemText(text string) string {
	return text
}

func compactMessageText(text string) string {
	return text
}

func hasClaudeCodeSystem(system any) bool {
	switch typed := system.(type) {
	case string:
		return strings.Contains(typed, "You are Claude Code")
	case []any:
		for _, item := range typed {
			block, ok := item.(map[string]any)
			if ok && strings.Contains(util.Clean(block["text"]), "You are Claude Code") {
				return true
			}
		}
	}
	return false
}

func PreprocessMessages(messages any) any {
	list := anyList(messages)
	if list == nil {
		return messages
	}
	var out []any
	for _, raw := range list {
		message, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		item := util.CopyMap(message)
		if text, ok := item["content"].(string); ok {
			item["content"] = compactMessageText(text)
		} else if blocks := anyList(item["content"]); blocks != nil {
			processed := make([]any, 0, len(blocks))
			for _, block := range blocks {
				processed = append(processed, preprocessBlock(block))
			}
			item["content"] = processed
		}
		out = append(out, item)
	}
	return out
}

func preprocessBlock(block any) any {
	item, ok := block.(map[string]any)
	if !ok {
		return block
	}
	switch util.Clean(item["type"]) {
	case "text":
		copied := util.CopyMap(item)
		copied["text"] = compactMessageText(util.Clean(item["text"]))
		return copied
	case "tool_use":
		data, _ := json.Marshal(item["input"])
		return map[string]any{"type": "text", "text": fmt.Sprintf("<tool_calls><tool_call><tool_name>%s</tool_name><parameters>%s</parameters></tool_call></tool_calls>", util.Clean(item["name"]), string(data))}
	case "tool_result":
		return map[string]any{"type": "text", "text": fmt.Sprintf("Tool result %s: %s", util.Clean(item["tool_use_id"]), util.Clean(item["content"]))}
	default:
		return block
	}
}

func MessageResponse(model, text string, inputTokens, outputTokens int, tools any) map[string]any {
	content, stopReason := ContentBlocks(text, tools)
	return map[string]any{"id": "msg_" + util.NewUUID(), "type": "message", "role": "assistant", "model": model, "content": content, "stop_reason": stopReason, "stop_sequence": nil, "usage": map[string]any{"input_tokens": inputTokens, "output_tokens": outputTokens}}
}

func ContentBlocks(text string, tools any) ([]map[string]any, string) {
	var calls []ToolCall
	if len(anyList(tools)) > 0 {
		calls = ParseToolCalls(text)
	}
	text = StripToolMarkup(text)
	if len(calls) == 0 {
		return []map[string]any{{"type": "text", "text": text}}, "end_turn"
	}
	var content []map[string]any
	if text != "" {
		content = append(content, map[string]any{"type": "text", "text": text})
	}
	for _, call := range calls {
		content = append(content, map[string]any{"type": "tool_use", "id": "toolu_" + util.NewUUID(), "name": call.Name, "input": call.Input})
	}
	return content, "tool_use"
}

func (e *Engine) StreamAnthropicEvents(ctx context.Context, request MessageRequest) (<-chan map[string]any, <-chan error) {
	chunks, errCh := e.StreamTextChatCompletionWithPoolRequest(ctx, ConversationRequest{Model: request.Model, Messages: request.Messages, VisionRequired: request.VisionRequired})
	out := make(chan map[string]any)
	outErr := make(chan error, 1)
	go func() {
		defer close(out)
		defer close(outErr)
		messageID := "msg_" + util.NewUUID()
		current := ""
		streamed := ""
		toolMode := len(anyList(request.Tools)) > 0
		toolStarted := false
		textOpen := false
		out <- map[string]any{"type": "message_start", "message": map[string]any{"id": messageID, "type": "message", "role": "assistant", "model": request.Model, "content": []any{}, "stop_reason": nil, "stop_sequence": nil, "usage": map[string]any{"input_tokens": CountMessageTokens(request.Messages, request.Model), "output_tokens": 0}}}
		if !toolMode {
			textOpen = true
			out <- map[string]any{"type": "content_block_start", "index": 0, "content_block": map[string]any{"type": "text", "text": ""}}
		}
		for chunk := range chunks {
			choice := firstChoice(chunk)
			delta := util.StringMap(choice["delta"])
			textDelta := util.Clean(delta["content"])
			if textDelta != "" {
				current += textDelta
				if !toolStarted {
					visible := current
					if toolMode {
						visible = StreamableText(current)
					}
					if strings.HasPrefix(visible, streamed) {
						next := visible[len(streamed):]
						if next != "" {
							if !textOpen {
								textOpen = true
								out <- map[string]any{"type": "content_block_start", "index": 0, "content_block": map[string]any{"type": "text", "text": ""}}
							}
							streamed = visible
							out <- map[string]any{"type": "content_block_delta", "index": 0, "delta": map[string]any{"type": "text_delta", "text": next}}
						}
					}
					toolStarted = toolMode && visible != current
				}
			}
			if choice["finish_reason"] != nil {
				content, stopReason := ContentBlocks(current, request.Tools)
				if textOpen {
					out <- map[string]any{"type": "content_block_stop", "index": 0}
				}
				if stopReason == "tool_use" {
					startIndex := 0
					if textOpen {
						startIndex = 1
					}
					outBufferedBlocks(out, content, startIndex)
				}
				out <- map[string]any{"type": "message_delta", "delta": map[string]any{"stop_reason": stopReason, "stop_sequence": nil}, "usage": map[string]any{"output_tokens": CountTextTokens(current, request.Model)}}
				break
			}
		}
		if err := <-errCh; err != nil {
			outErr <- err
			return
		}
		out <- map[string]any{"type": "message_stop", "created": time.Now().Unix()}
		outErr <- nil
	}()
	return out, outErr
}

func outBufferedBlocks(out chan<- map[string]any, content []map[string]any, startIndex int) {
	for offset, block := range content {
		index := startIndex + offset
		if block["type"] == "tool_use" {
			data, _ := json.Marshal(block["input"])
			out <- map[string]any{"type": "content_block_start", "index": index, "content_block": map[string]any{"type": "tool_use", "id": block["id"], "name": block["name"], "input": map[string]any{}}}
			out <- map[string]any{"type": "content_block_delta", "index": index, "delta": map[string]any{"type": "input_json_delta", "partial_json": string(data)}}
		} else {
			out <- map[string]any{"type": "content_block_start", "index": index, "content_block": map[string]any{"type": "text", "text": ""}}
			out <- map[string]any{"type": "content_block_delta", "index": index, "delta": map[string]any{"type": "text_delta", "text": block["text"]}}
		}
		out <- map[string]any{"type": "content_block_stop", "index": index}
	}
}

func CollectChatContent(chunks <-chan map[string]any) string {
	var parts []string
	for chunk := range chunks {
		choice := firstChoice(chunk)
		delta := util.StringMap(choice["delta"])
		if content := util.Clean(delta["content"]); content != "" {
			parts = append(parts, content)
		}
	}
	return strings.Join(parts, "")
}

func firstChoice(chunk map[string]any) map[string]any {
	choices := anyList(chunk["choices"])
	if len(choices) == 0 {
		return map[string]any{}
	}
	if choice, ok := choices[0].(map[string]any); ok {
		return choice
	}
	return map[string]any{}
}

type ToolCall struct {
	Name  string
	Input map[string]any
}

func StripToolMarkup(text string) string {
	return strings.TrimSpace(regexp.MustCompile(`(?is)<tool_calls\b[^>]*>.*?</tool_calls>|<tool_call\b[^>]*>.*?</tool_call>|<function_call\b[^>]*>.*?</function_call>|<invoke\b[^>]*>.*?</invoke>`).ReplaceAllString(text, ""))
}

func StreamableText(text string) string {
	loc := regexp.MustCompile(`(?is)<tool_calls\b|<tool_call\b|<function_call\b|<invoke\b`).FindStringIndex(text)
	if loc == nil {
		return text
	}
	return strings.TrimRight(text[:loc[0]], " \t\r\n")
}

func ParseToolCalls(text string) []ToolCall {
	text = regexp.MustCompile("(?is)```.*?```").ReplaceAllString(text, "")
	matches := regexp.MustCompile(`(?is)<tool_call\b[^>]*>(.*?)</tool_call>|<function_call\b[^>]*>(.*?)</function_call>|<invoke\b[^>]*>(.*?)</invoke>`).FindAllStringSubmatch(text, -1)
	var out []ToolCall
	for _, match := range matches {
		block := ""
		for _, part := range match[1:] {
			if part != "" {
				block = part
				break
			}
		}
		name := firstNonEmpty(XMLValue(block, "tool_name"), XMLValue(block, "name"), XMLValue(block, "function"))
		params := firstNonEmpty(XMLValue(block, "parameters"), XMLValue(block, "input"), XMLValue(block, "arguments"), "{}")
		if name != "" {
			out = append(out, ToolCall{Name: name, Input: ParseToolParams(params)})
		}
	}
	return out
}

func XMLValue(text, tag string) string {
	re := regexp.MustCompile(`(?is)<` + regexp.QuoteMeta(tag) + `\b[^>]*>(.*?)</` + regexp.QuoteMeta(tag) + `>`)
	match := re.FindStringSubmatch(text)
	if len(match) < 2 {
		return ""
	}
	value := strings.TrimSpace(match[1])
	if cdata := regexp.MustCompile(`(?is)^<!\[CDATA\[(.*?)]]>$`).FindStringSubmatch(value); len(cdata) > 1 {
		value = cdata[1]
	}
	return strings.TrimSpace(html.UnescapeString(value))
}

func ParseToolParams(raw string) map[string]any {
	raw = strings.TrimSpace(raw)
	var parsed map[string]any
	if json.Unmarshal([]byte(raw), &parsed) == nil {
		return parsed
	}
	out := map[string]any{}
	for _, match := range regexp.MustCompile(`(?is)<([\w.-]+)\b[^>]*>(.*?)</([\w.-]+)>`).FindAllStringSubmatch(raw, -1) {
		if len(match) > 3 && match[1] == match[3] {
			out[match[1]] = ParseToolValue(match[2])
		}
	}
	return out
}

func ParseToolValue(raw string) any {
	value := XMLValue("<x>"+raw+"</x>", "x")
	var parsed any
	if json.Unmarshal([]byte(value), &parsed) == nil {
		return parsed
	}
	return value
}

func firstNonNil(values ...any) any {
	for _, value := range values {
		if value != nil {
			return value
		}
	}
	return nil
}

type HTTPError struct {
	Status  int
	Message string
}

func (e HTTPError) Error() string { return e.Message }
