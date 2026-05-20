package protocol

import (
	"bytes"
	"context"
	"crypto/md5"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"chatgpt2api/internal/backend"
	"chatgpt2api/internal/service"
	"chatgpt2api/internal/storage"
	"chatgpt2api/internal/util"

	"github.com/HugoSmits86/nativewebp"
)

type ImageConfig interface {
	ImagesDir() string
	ImageMetadataDir() string
	BaseURL() string
	CleanupOldImages() int
}

type Engine struct {
	Accounts *service.AccountService
	Config   ImageConfig
	Storage  storage.JSONDocumentBackend
	Proxy    *service.ProxyService
	Logger   *service.Logger

	ListModelsFunc         func(context.Context) (map[string]any, error)
	StreamImageOutputsFunc func(context.Context, *backend.Client, ConversationRequest, int, int) (<-chan ImageOutput, <-chan error)
	ImageTokenProvider     func(context.Context) (string, error)
	ImageClientFactory     func(string) *backend.Client

	responseContextMu sync.Mutex
	ResponseContexts  *ResponseContextStore
}

type ImageOutputSlotAcquirer func(context.Context, int) (func(), error)

type ConversationRequest struct {
	Model              string
	Prompt             string
	Messages           []map[string]any
	Images             []string
	InputImageMask     string
	N                  int
	Size               string
	Quality            string
	Background         string
	Moderation         string
	Style              string
	OutputFormat       string
	OutputCompression  *int
	PartialImages      *int
	ResponseFormat     string
	BaseURL            string
	OwnerID            string
	OwnerName          string
	MessageAsError     bool
	RequirePaidAccount bool
	ResponsesImageTool bool
	AllowCodexFallback bool
	VisionRequired     bool
	MaxImageWorkers    int
}

func (r ConversationRequest) Normalized() ConversationRequest {
	if !r.ResponsesImageTool {
		r.Model = NormalizeImageGenerationModel(r.Model)
	} else {
		r.Model = strings.TrimSpace(r.Model)
	}
	r.Size = NormalizeImageGenerationSize(r.Size)
	r.Quality = ImageQualityForModel(r.Model, r.Quality)
	r.OutputFormat = NormalizeImageOutputFormat(r.OutputFormat)
	if !SupportsImageOutputCompression(r.OutputFormat) {
		r.OutputCompression = nil
	} else if r.OutputCompression != nil {
		compression := *r.OutputCompression
		if compression < 0 {
			compression = 0
		} else if compression > 100 {
			compression = 100
		}
		r.OutputCompression = &compression
	}
	r.RequirePaidAccount = r.RequirePaidAccount || RequiresPaidImageSize(r.Size)
	return r
}

func NormalizeImageGenerationModel(model string) string {
	model = strings.TrimSpace(model)
	if model == "" || model == util.ImageModelAuto {
		return util.ImageModelGPT
	}
	return model
}

func ImageQualityForModel(model, quality string) string {
	if strings.TrimSpace(model) == util.ImageModelCodex {
		return ""
	}
	return strings.TrimSpace(quality)
}

func NormalizeImageOutputFormat(format string) string {
	return service.NormalizeImageOutputFormat(format)
}

func SupportsImageOutputCompression(format string) bool {
	switch NormalizeImageOutputFormat(format) {
	case "jpeg", "webp":
		return true
	default:
		return false
	}
}

type ImageOutputOptions struct {
	Format              string
	Compression         *int
	TrustUpstreamFormat bool
}

func ImageOutputOptionsFromPayload(payload map[string]any) ImageOutputOptions {
	rawFormat := util.Clean(payload["output_format"])
	if rawFormat == "" {
		return ImageOutputOptions{}
	}
	format := NormalizeImageOutputFormat(rawFormat)
	options := ImageOutputOptions{Format: format}
	if !SupportsImageOutputCompression(format) {
		return options
	}
	if compression, ok := normalizedImageOutputCompression(payload["output_compression"]); ok {
		options.Compression = &compression
	}
	return options
}

func normalizedImageOutputCompression(value any) (int, bool) {
	if value == nil || strings.TrimSpace(util.Clean(value)) == "" {
		return 0, false
	}
	compression := util.ToInt(value, -1)
	if compression < 0 {
		return 0, false
	}
	if compression > 100 {
		compression = 100
	}
	return compression, true
}

func (r ConversationRequest) SupportsImageGenerationModel() bool {
	return util.IsImageGenerationModel(r.Model) || (r.ResponsesImageTool && util.IsResponsesImageToolModel(r.Model))
}

func (r ConversationRequest) UsesResponsesImageRoute() bool {
	model := strings.TrimSpace(r.Model)
	return model == "" || model == util.ImageModelAuto || model == util.ImageModelGPT || model == util.ImageModelCodex
}

type ConversationState struct {
	Text           string
	ConversationID string
	FileIDs        []string
	SedimentIDs    []string
	Blocked        bool
	ToolInvoked    *bool
	TurnUseCase    string
}

type ConversationEvent map[string]any

type ImageOutput struct {
	Kind              string
	Model             string
	Index             int
	Total             int
	Created           int64
	Text              string
	UpstreamEventType string
	Data              []map[string]any
}

type imageRunResult struct {
	emitted         bool
	returnedMessage bool
	lastError       string
	err             error
}

type ImageGenerationError struct {
	Message    string
	StatusCode int
	Type       string
	Code       string
	Param      any
}

func (e *ImageGenerationError) Error() string { return e.Message }

func (e *ImageGenerationError) OpenAIError() map[string]any {
	return map[string]any{"error": map[string]any{"message": e.Message, "type": e.Type, "param": e.Param, "code": e.Code}}
}

func NewImageGenerationError(message string) *ImageGenerationError {
	return &ImageGenerationError{Message: message, StatusCode: 502, Type: "server_error", Code: "upstream_error"}
}

func isRetriableImageRunError(err error) bool {
	var imageErr *ImageGenerationError
	if errors.As(err, &imageErr) {
		return imageErr.Code == "upstream_error"
	}
	return errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled)
}

const (
	maxTransientImageStreamAttempts    = 3
	fastImageStreamAttemptTimeout      = 90 * time.Second
	regularImageStreamAttemptTimeout   = 120 * time.Second
	expensiveImageStreamAttemptTimeout = 90 * time.Second
)

var imageStreamAttemptTimeoutForRequest = defaultImageStreamAttemptTimeoutForRequest

func isFastImageRequest(request ConversationRequest) bool {
	return request.N == 1 &&
		len(request.Images) == 0 &&
		strings.TrimSpace(request.InputImageMask) == "" &&
		!strings.EqualFold(strings.TrimSpace(request.Quality), "high") &&
		!RequiresPaidImageSize(request.Size)
}

func defaultImageStreamAttemptTimeoutForRequest(request ConversationRequest) time.Duration {
	if len(request.Images) > 0 || strings.TrimSpace(request.InputImageMask) != "" {
		return expensiveImageStreamAttemptTimeout
	}
	if strings.EqualFold(strings.TrimSpace(request.Quality), "high") || RequiresPaidImageSize(request.Size) {
		return expensiveImageStreamAttemptTimeout
	}
	if request.N > 1 {
		return regularImageStreamAttemptTimeout
	}
	return fastImageStreamAttemptTimeout
}

func transientImageStreamAttemptLimit(request ConversationRequest) int {
	if request.N > 1 {
		return 1
	}
	return maxTransientImageStreamAttempts
}

func imageStreamWorkerCount(request ConversationRequest) int {
	workers := request.N
	if workers < 1 {
		return 1
	}
	return workers
}

// isTransientImageStreamErrorMessage matches upstream errors that are usually retriable on
// the same access token within a couple of attempts: HTTP/2 flow control resets, SSE read
// drops, unexpected EOFs and proxied connection resets. Long 2K/4K image generations are
// the main victims, so we retry up to maxTransientImageStreamAttempts before surfacing the
// failure to the caller.
func isTransientImageStreamErrorMessage(message string) bool {
	lower := strings.ToLower(strings.TrimSpace(message))
	if lower == "" {
		return false
	}
	if strings.Contains(lower, strings.ToLower(util.UpstreamConnectionFailureMessage)) {
		return true
	}
	if _, ok := util.SummarizeUpstreamConnectionError(lower); ok {
		return true
	}
	for _, token := range []string{
		"sse read error",
		"responses sse read error",
		"stream error",
		"flow_control_error",
		"internal_error",
		"received from peer",
		"unexpected eof",
		"http2: client connection lost",
		"connection reset by peer",
		"stream closed",
		"context deadline exceeded",
		"deadline exceeded",
		"image generation produced no image result",
		"tool choice 'required' must be specified with 'tools' parameter",
		"tool choice 'image_generation' not found in 'tools' parameter",
	} {
		if strings.Contains(lower, token) {
			return true
		}
	}
	return false
}

func imageStreamErrorMessage(message string) string {
	text := strings.TrimSpace(message)
	if detail, ok := util.SummarizeCloudflareError(text); ok {
		return detail
	}
	lower := strings.ToLower(text)
	if detail, ok := util.SummarizeUpstreamConnectionError(text); ok {
		return detail
	}
	if strings.Contains(lower, "flow_control_error") {
		return "upstream image stream interrupted by HTTP/2 flow control; retry the request or change proxy if it repeats"
	}
	if text == "" {
		return "image generation failed"
	}
	return text
}

func (o ImageOutput) Chunk() map[string]any {
	chunk := map[string]any{
		"object":              "image.generation.chunk",
		"created":             o.Created,
		"model":               o.Model,
		"index":               o.Index,
		"total":               o.Total,
		"progress_text":       o.Text,
		"upstream_event_type": o.UpstreamEventType,
		"data":                []map[string]any{},
	}
	switch o.Kind {
	case "message":
		chunk["object"] = "image.generation.message"
		chunk["message"] = o.Text
		delete(chunk, "progress_text")
		delete(chunk, "upstream_event_type")
	case "result":
		chunk["object"] = "image.generation.result"
		chunk["data"] = o.Data
		delete(chunk, "progress_text")
		delete(chunk, "upstream_event_type")
	}
	return chunk
}

func (e *Engine) TextBackend(accessToken string) *backend.Client {
	return backend.NewClient(accessToken, e.Accounts, e.Proxy)
}

func (e *Engine) ListModels(ctx context.Context) (map[string]any, error) {
	result, err := e.listModels(ctx)
	if err != nil {
		return nil, err
	}
	data := util.AsMapSlice(result["data"])
	seen := map[string]struct{}{}
	for _, item := range data {
		if id := util.Clean(item["id"]); id != "" {
			seen[id] = struct{}{}
		}
	}
	for _, model := range util.ModelList() {
		if _, ok := seen[model]; !ok {
			data = append(data, map[string]any{"id": model, "object": "model", "created": 0, "owned_by": "chatgpt2api", "permission": []any{}, "root": model, "parent": nil})
		}
	}
	result["data"] = data
	return result, nil
}

func (e *Engine) listModels(ctx context.Context) (map[string]any, error) {
	if e != nil && e.ListModelsFunc != nil {
		return e.ListModelsFunc(ctx)
	}
	return backend.NewClient("", e.Accounts, e.Proxy).ListModels(ctx)
}

func (e *Engine) StreamTextDeltas(ctx context.Context, client *backend.Client, request ConversationRequest) (<-chan string, <-chan error) {
	out := make(chan string)
	errCh := make(chan error, 1)
	go func() {
		defer close(out)
		defer close(errCh)
		events, convErr := e.ConversationEvents(ctx, client, request.Messages, request.Model, request.Prompt)
		for event := range events {
			if event["type"] != "conversation.delta" {
				continue
			}
			delta := util.Clean(event["delta"])
			if delta == "" {
				continue
			}
			select {
			case out <- delta:
			case <-ctx.Done():
				errCh <- ctx.Err()
				return
			}
		}
		if err := <-convErr; err != nil {
			errCh <- err
			return
		}
		errCh <- nil
	}()
	return out, errCh
}

func (e *Engine) CollectText(ctx context.Context, client *backend.Client, request ConversationRequest) (string, error) {
	deltas, errCh := e.StreamTextDeltas(ctx, client, request)
	var parts []string
	for delta := range deltas {
		parts = append(parts, delta)
	}
	return strings.Join(parts, ""), <-errCh
}

func (e *Engine) ConversationEvents(ctx context.Context, client *backend.Client, messages []map[string]any, model, prompt string) (<-chan ConversationEvent, <-chan error) {
	out := make(chan ConversationEvent)
	errCh := make(chan error, 1)
	go func() {
		defer close(out)
		defer close(errCh)
		normalized := NormalizeConversationMessages(messages, nil)
		if len(normalized) == 0 && prompt != "" {
			normalized = []map[string]any{{"role": "user", "content": prompt}}
		}
		historyText := AssistantHistoryText(normalized)
		historyMessages := AssistantHistoryMessages(normalized)
		payloads, upstreamErr := client.StreamConversation(ctx, normalized, model, prompt)
		iterErr := IterConversationPayloads(ctx, payloads, historyText, historyMessages, out)
		upErr := <-upstreamErr
		if iterErr != nil {
			errCh <- iterErr
			return
		}
		errCh <- upErr
	}()
	return out, errCh
}

func IterConversationPayloads(ctx context.Context, payloads <-chan string, historyText string, historyMessages []string, out chan<- ConversationEvent) error {
	state := &ConversationState{}
	historyIndex := 0
	for payload := range payloads {
		if payload == "" {
			continue
		}
		if payload == "[DONE]" {
			event := conversationBaseEvent("conversation.done", state)
			event["done"] = true
			select {
			case out <- event:
			case <-ctx.Done():
				return ctx.Err()
			}
			break
		}
		var raw any
		if err := json.Unmarshal([]byte(payload), &raw); err != nil {
			UpdateConversationState(state, payload, nil)
			event := conversationBaseEvent("conversation.raw", state)
			event["payload"] = payload
			out <- event
			continue
		}
		eventMap, ok := raw.(map[string]any)
		if !ok {
			event := conversationBaseEvent("conversation.event", state)
			event["raw"] = raw
			out <- event
			continue
		}
		UpdateConversationState(state, payload, eventMap)
		if historyIndex < len(historyMessages) && EventAssistantText(eventMap, historyText) == historyMessages[historyIndex] {
			historyIndex++
			state.Text = ""
			continue
		}
		nextText := AssistantText(eventMap, state.Text, historyText)
		if nextText != state.Text {
			delta := nextText
			if strings.HasPrefix(nextText, state.Text) {
				delta = nextText[len(state.Text):]
			}
			state.Text = nextText
			event := conversationBaseEvent("conversation.delta", state)
			event["raw"] = eventMap
			event["delta"] = delta
			out <- event
			continue
		}
		event := conversationBaseEvent("conversation.event", state)
		event["raw"] = eventMap
		out <- event
	}
	return nil
}

func (e *Engine) StreamImageOutputsWithPool(ctx context.Context, request ConversationRequest) (<-chan ImageOutput, <-chan error) {
	request = request.Normalized()
	out := make(chan ImageOutput)
	errCh := make(chan error, 1)
	go func() {
		defer close(out)
		defer close(errCh)
		if !request.SupportsImageGenerationModel() {
			errCh <- &ImageGenerationError{Message: "unsupported image model,supported models: " + util.ImageGenerationModelNames(), StatusCode: 502, Type: "server_error", Code: "upstream_error"}
			return
		}
		ctx, cancel := context.WithCancel(ctx)
		defer cancel()

		workerCount := imageStreamWorkerCount(request)
		resultCh := make(chan imageRunResult, workerCount)
		workerOut := make(chan ImageOutput)
		var wg sync.WaitGroup
		for index := 1; index <= workerCount; index++ {
			outputIndex := ((index - 1) % request.N) + 1
			wg.Add(1)
			go func(index int) {
				defer wg.Done()
				result := e.runSingleImageOutput(ctx, workerOut, request, index)
				if result.err != nil {
					if workerCount == request.N || !isRetriableImageRunError(result.err) {
						cancel()
					}
				}
				resultCh <- result
			}(outputIndex)
		}
		go func() {
			wg.Wait()
			close(resultCh)
			close(workerOut)
		}()

		emitted := false
		lastError := ""
		messageOnly := false
		forwardedResults := 0
		var fatalErr error
		resultsDone := false
		outputsDone := false
		for !resultsDone || !outputsDone {
			select {
			case output, ok := <-workerOut:
				if !ok {
					outputsDone = true
					continue
				}
				if fatalErr != nil {
					continue
				}
				if output.Kind == "result" {
					if forwardedResults >= request.N {
						continue
					}
					remaining := request.N - forwardedResults
					if len(output.Data) > remaining {
						output.Data = output.Data[:remaining]
					}
					if len(output.Data) == 0 {
						continue
					}
					forwardedResults += len(output.Data)
				}
				select {
				case out <- output:
				case <-ctx.Done():
					fatalErr = ctx.Err()
					cancel()
					continue
				}
				if output.Kind == "result" && forwardedResults >= request.N {
					cancel()
				}
			case result, ok := <-resultCh:
				if !ok {
					resultsDone = true
					continue
				}
				emitted = emitted || result.emitted
				messageOnly = messageOnly || result.returnedMessage
				if result.lastError != "" {
					lastError = result.lastError
				}
				if result.err != nil {
					if (emitted || forwardedResults >= request.N) && errors.Is(result.err, context.Canceled) {
						continue
					}
					if workerCount != request.N && isRetriableImageRunError(result.err) {
						continue
					}
					fatalErr = result.err
					cancel()
				}
			}
		}
		if fatalErr != nil {
			errCh <- fatalErr
			return
		}
		if messageOnly {
			errCh <- nil
			return
		}
		if !emitted && forwardedResults == 0 {
			errCh <- NewImageGenerationError(imageStreamErrorMessage(lastError))
			return
		}
		errCh <- nil
	}()
	return out, errCh
}

func (e *Engine) runSingleImageOutput(ctx context.Context, out chan<- ImageOutput, request ConversationRequest, index int) imageRunResult {
	result := imageRunResult{}
	transientAttempts := 0
	transientAttemptLimit := transientImageStreamAttemptLimit(request)
	for {
		token, err := e.nextImageAccessToken(ctx, request)
		if err != nil {
			result.lastError = err.Error()
			result.err = NewImageGenerationError(err.Error())
			return result
		}
		emittedForToken := false
		returnedMessage := false
		returnedResult := false
		rateLimitedForToken := false
		rateLimitMessage := ""
		client := e.newImageClient(token)
		attemptCtx := ctx
		attemptCancel := func() {}
		if timeout := imageStreamAttemptTimeoutForRequest(request); timeout > 0 {
			attemptCtx, attemptCancel = context.WithTimeout(ctx, timeout)
		}
		outputs, imageErr := e.StreamImageOutputs(attemptCtx, client, request, index, request.N)
		for output := range outputs {
			if output.Kind == "message" && service.IsAccountRateLimitedErrorMessage(output.Text) {
				rateLimitedForToken = true
				rateLimitMessage = output.Text
				result.lastError = output.Text
				continue
			}
			if output.Kind == "message" && request.MessageAsError {
				if e.Accounts != nil {
					e.Accounts.MarkImageResult(token, false)
				}
				attemptCancel()
				result.err = &ImageGenerationError{Message: firstNonEmpty(output.Text, "Image generation returned a text response instead of image data."), StatusCode: 400, Type: "invalid_request_error", Code: "image_generation_text_response"}
				result.lastError = result.err.Error()
				return result
			}
			result.emitted = true
			emittedForToken = true
			returnedMessage = output.Kind == "message"
			returnedResult = returnedResult || output.Kind == "result"
			select {
			case out <- output:
			case <-ctx.Done():
				attemptCancel()
				result.lastError = ctx.Err().Error()
				result.err = ctx.Err()
				return result
			}
		}
		err = <-imageErr
		attemptTimedOut := err != nil && errors.Is(err, context.DeadlineExceeded) && ctx.Err() == nil && errors.Is(attemptCtx.Err(), context.DeadlineExceeded)
		attemptCancel()
		if returnedResult {
			if e.Accounts != nil {
				e.Accounts.MarkImageResult(token, true)
			}
			return result
		}
		if err == nil {
			if rateLimitedForToken {
				if e.Accounts != nil {
					e.Accounts.MarkImageResult(token, false)
					e.Accounts.ApplyAccountErrorMessage(token, "image_stream", rateLimitMessage)
				}
				continue
			}
			if returnedMessage || !returnedResult {
				if e.Accounts != nil {
					e.Accounts.MarkImageResult(token, false)
				}
				result.returnedMessage = returnedMessage || !returnedResult
				return result
			}
			if e.Accounts != nil {
				e.Accounts.MarkImageResult(token, true)
			}
			return result
		}
		if attemptTimedOut {
			if e.Accounts != nil {
				e.Accounts.MarkImageAttemptTimeout(token)
			}
			result.lastError = "image generation attempt timed out"
			if transientAttempts < transientAttemptLimit {
				transientAttempts++
				continue
			}
			result.err = NewImageGenerationError("图片生成响应超时，已自动切换账号重试，请稍后重试或降低分辨率")
			result.lastError = result.err.Error()
			return result
		}
		if e.Accounts != nil {
			e.Accounts.MarkImageResult(token, false)
		}
		result.lastError = err.Error()
		if e.Accounts != nil {
			if normalized, handled := e.Accounts.ApplyAccountErrorMessage(token, "image_stream", result.lastError); handled {
				originalError := result.lastError
				result.lastError = normalized
				if service.IsAccountRateLimitedErrorMessage(originalError) {
					continue
				}
				if !emittedForToken {
					if IsTokenInvalidError(originalError) {
						continue
					}
					if isTransientImageStreamErrorMessage(originalError) && transientAttempts < transientAttemptLimit {
						transientAttempts++
						continue
					}
					result.err = NewImageGenerationError(imageStreamErrorMessage(result.lastError))
					return result
				}
			}
		}
		if !emittedForToken && IsTokenInvalidError(result.lastError) {
			continue
		}
		if !returnedResult && isTransientImageStreamErrorMessage(result.lastError) && transientAttempts < transientAttemptLimit {
			transientAttempts++
			continue
		}
		result.err = NewImageGenerationError(imageStreamErrorMessage(result.lastError))
		return result
	}
}

func (e *Engine) StreamImageOutputs(ctx context.Context, client *backend.Client, request ConversationRequest, index, total int) (<-chan ImageOutput, <-chan error) {
	if e.StreamImageOutputsFunc != nil {
		return e.StreamImageOutputsFunc(ctx, client, request, index, total)
	}
	return e.StreamResponsesImageOutputs(ctx, client, request, index, total)
}

func (e *Engine) CollectImageOutputs(outputs <-chan ImageOutput, errCh <-chan error) (map[string]any, error) {
	return e.CollectImageOutputsWithLimit(outputs, errCh, 0)
}

func (e *Engine) CollectImageOutputsWithLimit(outputs <-chan ImageOutput, errCh <-chan error, maxResults int) (map[string]any, error) {
	var created int64
	var data []map[string]any
	message := ""
	var progress []string
	for output := range outputs {
		if created == 0 {
			created = output.Created
		}
		switch output.Kind {
		case "progress":
			if output.Text != "" {
				progress = append(progress, output.Text)
			}
		case "message":
			message = output.Text
		case "result":
			for _, item := range output.Data {
				if maxResults > 0 && len(data) >= maxResults {
					break
				}
				data = append(data, item)
			}
		}
	}
	streamErr := <-errCh
	if created == 0 {
		created = time.Now().Unix()
	}
	result := map[string]any{"created": created, "data": data}
	if len(data) == 0 {
		if text := firstNonEmpty(message, strings.TrimSpace(strings.Join(progress, ""))); text != "" {
			result["message"] = text
		}
	}
	if streamErr != nil {
		if imageErr, ok := streamErr.(*ImageGenerationError); ok && imageErr.Code == "image_generation_text_response" {
			result["output_type"] = "text"
		}
		if result["message"] == nil {
			result["message"] = streamErr.Error()
		}
		return result, streamErr
	}
	return result, nil
}

func (e *Engine) FormatImageResult(items []map[string]any, prompt, responseFormat, baseURL, ownerID, ownerName string, created int64, message string) map[string]any {
	return e.FormatImageResultWithOptions(items, prompt, responseFormat, baseURL, ownerID, ownerName, created, message, ImageOutputOptions{})
}

func (e *Engine) FormatImageResultWithOptions(items []map[string]any, prompt, responseFormat, baseURL, ownerID, ownerName string, created int64, message string, options ImageOutputOptions) map[string]any {
	defaultFormat := NormalizeImageOutputFormat(options.Format)
	hasRequestedFormat := strings.TrimSpace(options.Format) != ""
	var data []map[string]any
	for _, item := range items {
		b64 := util.Clean(item["b64_json"])
		if b64 == "" {
			continue
		}
		revised := firstNonEmpty(util.Clean(item["revised_prompt"]), prompt)
		imageBytes, err := base64.StdEncoding.DecodeString(b64)
		if err != nil {
			continue
		}
		itemOptions := options
		if itemFormat := strings.TrimSpace(util.Clean(item["output_format"])); itemFormat != "" && (itemOptions.TrustUpstreamFormat || itemOptions.Format == "") {
			itemOptions.Format = NormalizeImageOutputFormat(itemFormat)
		}
		if itemOptions.Format == "" {
			itemOptions.Format = defaultFormat
		}
		if !SupportsImageOutputCompression(itemOptions.Format) {
			itemOptions.Compression = nil
		}
		if itemOptions.Compression == nil {
			if SupportsImageOutputCompression(itemOptions.Format) {
				if compression, ok := normalizedImageOutputCompression(item["output_compression"]); ok {
					itemOptions.Compression = &compression
				}
			}
		}
		if !itemOptions.TrustUpstreamFormat && hasRequestedFormat {
			imageBytes, err = encodeImageBytes(imageBytes, itemOptions)
			if err != nil {
				continue
			}
		}
		outputFormat := NormalizeImageOutputFormat(itemOptions.Format)
		urlValue := e.SaveImageBytesForOwnerWithFormat(imageBytes, baseURL, ownerID, ownerName, outputFormat)
		responseItem := map[string]any{"url": urlValue, "revised_prompt": revised, "output_format": outputFormat}
		if responseFormat == "b64_json" {
			responseItem["b64_json"] = base64.StdEncoding.EncodeToString(imageBytes)
		}
		data = append(data, responseItem)
	}
	if created == 0 {
		created = time.Now().Unix()
	}
	result := map[string]any{"created": created, "data": data}
	if message != "" && len(data) == 0 {
		result["message"] = message
	}
	return result
}

func (e *Engine) SaveImageBytes(imageData []byte, baseURL string) string {
	return e.SaveImageBytesForOwner(imageData, baseURL, "", "")
}

func (e *Engine) SaveImageBytesForOwner(imageData []byte, baseURL, ownerID, ownerName string) string {
	return e.SaveImageBytesForOwnerWithFormat(imageData, baseURL, ownerID, ownerName, "png")
}

func (e *Engine) SaveImageBytesForOwnerWithFormat(imageData []byte, baseURL, ownerID, ownerName, outputFormat string) string {
	outputFormat = NormalizeImageOutputFormat(outputFormat)
	e.Config.CleanupOldImages()
	sum := md5.Sum(imageData)
	filename := fmt.Sprintf("%d_%s.%s", time.Now().Unix(), hex.EncodeToString(sum[:]), imageFileExtension(outputFormat))
	relativeDir := filepath.Join(time.Now().Format("2006"), time.Now().Format("01"), time.Now().Format("02"))
	rel := filepath.Join(relativeDir, filename)
	filePath := filepath.Join(e.Config.ImagesDir(), rel)
	_ = os.MkdirAll(filepath.Dir(filePath), 0o755)
	_ = os.WriteFile(filePath, imageData, 0o644)
	e.writeImageOwnerMetadata(rel, ownerID, ownerName)
	if baseURL == "" {
		baseURL = e.Config.BaseURL()
	}
	return strings.TrimRight(baseURL, "/") + "/images/" + filepath.ToSlash(rel)
}

func imageFileExtension(outputFormat string) string {
	if NormalizeImageOutputFormat(outputFormat) == "jpeg" {
		return "jpg"
	}
	return NormalizeImageOutputFormat(outputFormat)
}

func encodeImageBytes(data []byte, options ImageOutputOptions) ([]byte, error) {
	format := NormalizeImageOutputFormat(options.Format)
	if format == "png" && isPNGBytes(data) {
		return data, nil
	}
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	switch format {
	case "jpeg":
		quality := 90
		if options.Compression != nil {
			quality = 100 - *options.Compression
			if quality < 1 {
				quality = 1
			} else if quality > 100 {
				quality = 100
			}
		}
		if err := jpeg.Encode(&buf, flattenAlpha(img), &jpeg.Options{Quality: quality}); err != nil {
			return nil, err
		}
	case "webp":
		if err := nativewebp.Encode(&buf, img, nil); err != nil {
			return nil, err
		}
	case "png":
		if err := png.Encode(&buf, img); err != nil {
			return nil, err
		}
	default:
		if err := png.Encode(&buf, img); err != nil {
			return nil, err
		}
	}
	return buf.Bytes(), nil
}

func isPNGBytes(data []byte) bool {
	return len(data) >= 8 &&
		data[0] == 0x89 &&
		data[1] == 'P' &&
		data[2] == 'N' &&
		data[3] == 'G' &&
		data[4] == '\r' &&
		data[5] == '\n' &&
		data[6] == 0x1a &&
		data[7] == '\n'
}

func flattenAlpha(img image.Image) image.Image {
	bounds := img.Bounds()
	out := image.NewRGBA(bounds)
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			r, g, b, a := img.At(x, y).RGBA()
			alpha := int(a)
			out.Set(x, y, color.RGBA{
				R: blendOverWhite(int(r), alpha),
				G: blendOverWhite(int(g), alpha),
				B: blendOverWhite(int(b), alpha),
				A: 255,
			})
		}
	}
	return out
}

func blendOverWhite(channel, alpha int) uint8 {
	value := (channel*alpha + 0xffff*(0xffff-alpha)) / 0xffff
	return uint8(value >> 8)
}

func (e *Engine) writeImageOwnerMetadata(rel, ownerID, ownerName string) {
	ownerID = strings.TrimSpace(ownerID)
	ownerName = strings.TrimSpace(ownerName)
	if e == nil || e.Config == nil || ownerID == "" {
		return
	}
	value := map[string]any{"owner_id": ownerID, "updated_at": time.Now().UTC().Format(time.RFC3339Nano)}
	if ownerName != "" {
		value["owner_name"] = ownerName
	}
	if e.Storage != nil {
		_ = e.Storage.SaveJSONDocument(imageOwnerDocumentName(rel), value)
		return
	}
	metaPath := filepath.Join(e.Config.ImageMetadataDir(), filepath.FromSlash(filepath.ToSlash(rel))+".json")
	_ = os.MkdirAll(filepath.Dir(metaPath), 0o755)
	data, err := json.Marshal(value)
	if err == nil {
		_ = os.WriteFile(metaPath, data, 0o644)
	}
}

func imageOwnerDocumentName(rel string) string {
	return "image_metadata/" + filepath.ToSlash(rel) + ".json"
}

func IsTokenInvalidError(message string) bool {
	return service.IsAccountInvalidErrorMessage(message)
}

func MessageText(content any) string {
	switch v := content.(type) {
	case string:
		return v
	case []any:
		var parts []string
		for _, item := range v {
			switch x := item.(type) {
			case string:
				parts = append(parts, x)
			case map[string]any:
				t := util.Clean(x["type"])
				if t == "text" || t == "input_text" || t == "output_text" {
					parts = append(parts, util.Clean(x["text"]))
				}
			}
		}
		return strings.Join(parts, "")
	default:
		return ""
	}
}

func NormalizeConversationMessages(messages any, system any) []map[string]any {
	var normalized []map[string]any
	if text := MessageText(system); text != "" {
		normalized = append(normalized, map[string]any{"role": "system", "content": text})
	}
	if list, ok := messages.([]map[string]any); ok {
		for _, message := range list {
			normalized = append(normalized, map[string]any{"role": firstNonEmpty(util.Clean(message["role"]), "user"), "content": normalizeConversationContent(message["content"])})
		}
		return normalized
	}
	if list, ok := messages.([]any); ok {
		for _, raw := range list {
			if message, ok := raw.(map[string]any); ok {
				normalized = append(normalized, map[string]any{"role": firstNonEmpty(util.Clean(message["role"]), "user"), "content": normalizeConversationContent(message["content"])})
			}
		}
	}
	return normalized
}

func normalizeConversationContent(content any) any {
	if text, ok := content.(string); ok {
		return text
	}
	parts := anyList(content)
	if len(parts) == 0 {
		return MessageText(content)
	}
	copied := make([]any, 0, len(parts))
	hasImage := false
	for _, part := range parts {
		switch value := part.(type) {
		case string:
			copied = append(copied, value)
		case map[string]any:
			item := util.CopyMap(value)
			copied = append(copied, item)
			switch strings.ToLower(util.Clean(item["type"])) {
			case "image_url", "input_image", "image":
				hasImage = true
			}
		default:
			if text := util.Clean(value); text != "" {
				copied = append(copied, text)
			}
		}
	}
	if hasImage {
		return copied
	}
	return MessageText(copied)
}

func NormalizeMessages(messages any, system any) []map[string]any {
	var normalized []map[string]any
	if text := MessageText(system); text != "" {
		normalized = append(normalized, map[string]any{"role": "system", "content": text})
	}
	if list, ok := messages.([]map[string]any); ok {
		for _, message := range list {
			normalized = append(normalized, map[string]any{"role": firstNonEmpty(util.Clean(message["role"]), "user"), "content": MessageText(message["content"])})
		}
		return normalized
	}
	if list, ok := messages.([]any); ok {
		for _, raw := range list {
			if message, ok := raw.(map[string]any); ok {
				normalized = append(normalized, map[string]any{"role": firstNonEmpty(util.Clean(message["role"]), "user"), "content": MessageText(message["content"])})
			}
		}
	}
	return normalized
}

func AssistantHistoryText(messages []map[string]any) string {
	var parts []string
	for _, item := range messages {
		if item["role"] == "assistant" {
			parts = append(parts, MessageText(item["content"]))
		}
	}
	return strings.Join(parts, "")
}

func AssistantHistoryMessages(messages []map[string]any) []string {
	var out []string
	for _, item := range messages {
		if item["role"] == "assistant" && MessageText(item["content"]) != "" {
			out = append(out, MessageText(item["content"]))
		}
	}
	return out
}

const maxFreeGeneratePixels = 1577536

func NormalizeImageGenerationSize(size string) string {
	switch strings.ToLower(strings.TrimSpace(size)) {
	case "1080p":
		return "1080x1080"
	case "2k":
		return "2048x2048"
	case "4k":
		return "2880x2880"
	default:
		return strings.TrimSpace(size)
	}
}

func ResponseImageToolSize(size string) string {
	normalized := NormalizeImageGenerationSize(size)
	if normalized == "" || strings.EqualFold(normalized, "auto") || isImageAspectRatioSize(normalized) {
		return "auto"
	}
	if _, _, ok := imageSizeDimensions(normalized); ok {
		return normalized
	}
	return "auto"
}

func RequiresPaidImageSize(size string) bool {
	size = NormalizeImageGenerationSize(size)
	width, height, ok := imageSizeDimensions(size)
	return ok && width*height > maxFreeGeneratePixels
}

func isImageAspectRatioSize(size string) bool {
	return regexp.MustCompile(`^\d+(?:\.\d+)?:\d+(?:\.\d+)?$`).MatchString(strings.TrimSpace(size))
}

func imageSizeDimensions(size string) (int, int, bool) {
	matches := regexp.MustCompile(`^(\d+)x(\d+)$`).FindStringSubmatch(strings.ToLower(strings.TrimSpace(size)))
	if len(matches) != 3 {
		return 0, 0, false
	}
	width := util.ToInt(matches[1], 0)
	height := util.ToInt(matches[2], 0)
	if width <= 0 || height <= 0 {
		return 0, 0, false
	}
	return width, height, true
}

func BuildImagePrompt(prompt, size, quality string) string {
	prompt = strings.TrimSpace(prompt)
	size = NormalizeImageGenerationSize(size)
	if strings.EqualFold(size, "auto") {
		size = ""
	}
	var hintsList []string
	hintsList = append(hintsList, "请根据用户请求生成一张图片，必须输出图片结果，不要只回复文字。")
	hints := map[string]string{
		"1:1":  "输出为 1:1 正方形构图，主体居中，适合正方形画幅。",
		"3:2":  "输出为 3:2 横版构图，适合摄影、产品展示和横向叙事画幅。",
		"2:3":  "输出为 2:3 竖版构图，适合海报、人物和纵向叙事画幅。",
		"16:9": "输出为 16:9 横屏构图，适合宽画幅展示。",
		"21:9": "输出为 21:9 超宽横版构图，适合电影感全景和宽银幕画幅。",
		"9:16": "输出为 9:16 竖屏构图，适合竖版画幅展示。",
		"4:3":  "输出为 4:3 比例，兼顾宽度与高度，适合展示画面细节。",
		"3:4":  "输出为 3:4 比例，纵向构图，适合人物肖像或竖向场景。",
	}
	if size != "" {
		if width, height, ok := imageSizeDimensions(size); ok {
			hintsList = append(hintsList, fmt.Sprintf("输出图片目标分辨率为 %d x %d 像素，并严格按该尺寸对应的宽高比构图。", width, height))
		} else if hint, ok := hints[size]; ok {
			hintsList = append(hintsList, hint)
		} else {
			hintsList = append(hintsList, "输出图片，目标尺寸或宽高比为 "+size+"。")
		}
	}
	qualityHints := map[string]string{
		"low":    "画质使用 Low 档，优先更快出图，细节可以适度简化。",
		"medium": "画质使用 Medium 档，在速度、细节和整体完成度之间保持平衡。",
		"high":   "画质使用 High 档，提升细节、纹理、光影和整体完成度。",
	}
	if hint, ok := qualityHints[strings.ToLower(strings.TrimSpace(quality))]; ok {
		hintsList = append(hintsList, hint)
	}
	if len(hintsList) == 0 {
		return prompt
	}
	return prompt + "\n\n" + strings.Join(hintsList, "\n")
}

func CountMessageTokens(messages []map[string]any, model string) int {
	total := 3
	for _, message := range messages {
		total += 3
		for key, value := range message {
			if text, ok := value.(string); ok {
				total += CountTextTokens(text, model)
				if key == "name" {
					total++
				}
			}
		}
	}
	return total
}

func CountTextTokens(text, model string) int {
	runes := []rune(text)
	if len(runes) == 0 {
		return 0
	}
	return (len(runes) + 3) / 4
}

func EncodeImages(images []UploadedImage) []string {
	out := make([]string, 0, len(images))
	for _, image := range images {
		if len(image.Data) > 0 {
			out = append(out, base64.StdEncoding.EncodeToString(image.Data))
		}
	}
	return out
}

type UploadedImage struct {
	Data        []byte
	Filename    string
	ContentType string
}

func AssistantText(event map[string]any, currentText, historyText string) string {
	for _, candidate := range []any{event, event["v"]} {
		m := util.StringMap(candidate)
		message := util.StringMap(m["message"])
		if len(message) == 0 {
			continue
		}
		author := util.StringMap(message["author"])
		if strings.ToLower(util.Clean(author["role"])) != "assistant" {
			continue
		}
		text := AssistantMessageText(message)
		if text != "" {
			return StripHistory(text, historyText)
		}
	}
	return ApplyTextPatch(event, currentText, historyText)
}

func EventAssistantText(event map[string]any, historyText string) string {
	for _, candidate := range []any{event, event["v"]} {
		m := util.StringMap(candidate)
		message := util.StringMap(m["message"])
		author := util.StringMap(message["author"])
		if author["role"] == "assistant" {
			return StripHistory(AssistantMessageText(message), historyText)
		}
	}
	return ""
}

func AssistantMessageText(message map[string]any) string {
	content := util.StringMap(message["content"])
	parts, _ := content["parts"].([]any)
	var out []string
	for _, part := range parts {
		if text := assistantTextValue(part); text != "" {
			out = append(out, text)
		}
	}
	return strings.Join(out, "")
}

func assistantTextValue(value any) string {
	switch typed := value.(type) {
	case string:
		return typed
	case map[string]any:
		for _, key := range []string{"text", "delta", "content", "value"} {
			if text := assistantTextValue(typed[key]); text != "" {
				return text
			}
		}
		if nested := assistantTextValue(typed["v"]); nested != "" {
			return nested
		}
		message := util.StringMap(typed["message"])
		if len(message) > 0 {
			return AssistantMessageText(message)
		}
		if parts := anyList(typed["parts"]); len(parts) > 0 {
			var out []string
			for _, part := range parts {
				if text := assistantTextValue(part); text != "" {
					out = append(out, text)
				}
			}
			return strings.Join(out, "")
		}
		content := util.StringMap(typed["content"])
		if len(content) > 0 {
			var parts []string
			for _, part := range anyList(content["parts"]) {
				if text := assistantTextValue(part); text != "" {
					parts = append(parts, text)
				}
			}
			return strings.Join(parts, "")
		}
	case []any:
		var parts []string
		for _, item := range typed {
			if text := assistantTextValue(item); text != "" {
				parts = append(parts, text)
			}
		}
		return strings.Join(parts, "")
	}
	return ""
}

func StripHistory(text, historyText string) string {
	for historyText != "" && strings.HasPrefix(text, historyText) {
		text = text[len(historyText):]
	}
	return text
}

func ApplyTextPatch(event map[string]any, currentText, historyText string) string {
	if event["p"] == "/message/content/parts/0" {
		return ApplyPatchOp(event, currentText, historyText)
	}
	if value, ok := event["v"].(string); ok && currentText != "" && event["p"] == nil && event["o"] == nil {
		return currentText + value
	}
	if event["o"] == "patch" {
		text := currentText
		for _, raw := range anyList(event["v"]) {
			if op, ok := raw.(map[string]any); ok {
				text = ApplyTextPatch(op, text, historyText)
			}
		}
		return text
	}
	text := currentText
	for _, raw := range anyList(event["v"]) {
		if op, ok := raw.(map[string]any); ok {
			text = ApplyTextPatch(op, text, historyText)
		}
	}
	return text
}

func ApplyPatchOp(operation map[string]any, currentText, historyText string) string {
	value := assistantTextValue(operation["v"])
	switch operation["o"] {
	case "append":
		return currentText + value
	case "replace":
		return StripHistory(value, historyText)
	default:
		return currentText
	}
}

func UpdateConversationState(state *ConversationState, payload string, event map[string]any) {
	conversationID, fileIDs, sedimentIDs := ExtractConversationIDs(payload)
	if conversationID != "" && state.ConversationID == "" {
		state.ConversationID = conversationID
	}
	if event != nil && IsImageToolEvent(event) {
		state.FileIDs = appendUnique(state.FileIDs, fileIDs...)
		state.SedimentIDs = appendUnique(state.SedimentIDs, sedimentIDs...)
	}
	if event == nil {
		return
	}
	if id := util.Clean(event["conversation_id"]); id != "" {
		state.ConversationID = id
	}
	value := util.StringMap(event["v"])
	if id := util.Clean(value["conversation_id"]); id != "" {
		state.ConversationID = id
	}
	if event["type"] == "moderation" {
		moderation := util.StringMap(event["moderation_response"])
		if moderation["blocked"] == true {
			state.Blocked = true
		}
	}
	if event["type"] == "server_ste_metadata" {
		metadata := util.StringMap(event["metadata"])
		if toolInvoked, ok := metadata["tool_invoked"].(bool); ok {
			state.ToolInvoked = &toolInvoked
		}
		if value := util.Clean(metadata["turn_use_case"]); value != "" {
			state.TurnUseCase = value
		}
	}
}

func ExtractConversationIDs(payload string) (string, []string, []string) {
	conversation := ""
	if match := regexp.MustCompile(`"conversation_id"\s*:\s*"([^"]+)"`).FindStringSubmatch(payload); len(match) > 1 {
		conversation = match[1]
	}
	fileIDs := regexp.MustCompile(`(file[-_][A-Za-z0-9]+)`).FindAllString(payload, -1)
	sedimentMatches := regexp.MustCompile(`sediment://([A-Za-z0-9_-]+)`).FindAllStringSubmatch(payload, -1)
	var sediments []string
	for _, match := range sedimentMatches {
		if len(match) > 1 {
			sediments = append(sediments, match[1])
		}
	}
	return conversation, fileIDs, sediments
}

func IsImageToolEvent(event map[string]any) bool {
	value := util.StringMap(event["v"])
	message := util.StringMap(event["message"])
	if len(message) == 0 {
		message = util.StringMap(value["message"])
	}
	metadata := util.StringMap(message["metadata"])
	author := util.StringMap(message["author"])
	return author["role"] == "tool" && metadata["async_task_type"] == "image_gen"
}

func conversationBaseEvent(eventType string, state *ConversationState) ConversationEvent {
	var tool any
	if state.ToolInvoked != nil {
		tool = *state.ToolInvoked
	}
	return ConversationEvent{
		"type":            eventType,
		"text":            state.Text,
		"conversation_id": state.ConversationID,
		"file_ids":        state.FileIDs,
		"sediment_ids":    state.SedimentIDs,
		"blocked":         state.Blocked,
		"tool_invoked":    tool,
		"turn_use_case":   state.TurnUseCase,
	}
}

func anyList(v any) []any {
	switch list := v.(type) {
	case []any:
		return list
	case []map[string]any:
		out := make([]any, 0, len(list))
		for _, item := range list {
			out = append(out, item)
		}
		return out
	}
	return nil
}

func appendUnique(base []string, values ...string) []string {
	seen := map[string]struct{}{}
	for _, item := range base {
		seen[item] = struct{}{}
	}
	for _, value := range values {
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		base = append(base, value)
	}
	return base
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
