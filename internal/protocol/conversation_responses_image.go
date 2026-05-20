package protocol

import (
	"context"
	"encoding/base64"
	"fmt"
	"strings"
	"time"

	"chatgpt2api/internal/backend"
	"chatgpt2api/internal/util"
)

// nextImageAccessToken returns an access token for image generation requests.
// Defaults to picking the next available account; tests can override via
// Engine.ImageTokenProvider.
func (e *Engine) nextImageAccessToken(ctx context.Context, request ConversationRequest) (string, error) {
	if e.ImageTokenProvider != nil {
		return e.ImageTokenProvider(ctx)
	}
	return e.Accounts.GetAvailableImageAccessToken(ctx, request.RequirePaidAccount)
}

// newImageClient creates a backend client bound to the supplied access token.
// Tests can override via Engine.ImageClientFactory.
func (e *Engine) newImageClient(token string) *backend.Client {
	if e.ImageClientFactory != nil {
		return e.ImageClientFactory(token)
	}
	return backend.NewClient(token, e.Accounts, e.Proxy)
}

// StreamResponsesImageOutputs drives image generation through the upstream
// `responses_image` route (the new `/backend-api/f/conversation` and
// `/backend-api/codex/responses` flows in backend/responses_image.go).
//
// The function mirrors the upstream 0.1.8 implementation and intentionally
// avoids reusing legacy picture_v2 helpers — it consumes ResponsesImageEvent
// payloads directly.
func (e *Engine) StreamResponsesImageOutputs(ctx context.Context, client *backend.Client, request ConversationRequest, index, total int) (<-chan ImageOutput, <-chan error) {
	out := make(chan ImageOutput)
	errCh := make(chan error, 1)
	go func() {
		defer close(out)
		defer close(errCh)
		prompt := buildResponsesImagePrompt(request.Prompt, request.Size, request.Model)
		if strings.TrimSpace(prompt) == "" {
			prompt = request.Prompt
		}
		events, upstreamErr := client.StreamResponsesImage(ctx, backend.ResponsesImageRequest{
			Prompt:              prompt,
			Model:               request.Model,
			Size:                request.Size,
			Quality:             request.Quality,
			Background:          request.Background,
			Moderation:          request.Moderation,
			Style:               request.Style,
			OutputFormat:        request.OutputFormat,
			OutputCompression:   request.OutputCompression,
			PartialImages:       request.PartialImages,
			ResultLimit:         imageRequestResultLimit(request.N),
			InputImages:         responsesInputImages(request.Images),
			InputImageMask:      responsesInputImagePtr(request.InputImageMask),
			AllowCodexFallback:  request.AllowCodexFallback,
			ForceCodexResponses: strings.TrimSpace(request.Model) == util.ImageModelCodex,
		})
		emitted := false
		emittedResults := 0
		resultLimit := imageRequestResultLimit(request.N)
		seen := map[string]struct{}{}
		for event := range events {
			if event.PartialImage != "" {
				out <- ImageOutput{Kind: "progress", Model: request.Model, Index: index, Total: total, Created: firstNonZeroInt64(event.Created, time.Now().Unix()), Text: event.Text, UpstreamEventType: event.Type}
				continue
			}
			if isFinalImageTextEvent(event) {
				out <- ImageOutput{Kind: "message", Model: request.Model, Index: index, Total: total, Created: firstNonZeroInt64(event.Created, time.Now().Unix()), Text: strings.TrimSpace(event.Text), UpstreamEventType: event.Type}
				continue
			}
			if event.Result == "" {
				continue
			}
			if emittedResults >= resultLimit {
				continue
			}
			key := firstNonEmpty(event.ItemID, event.Result)
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			item := map[string]any{
				"b64_json":       event.Result,
				"revised_prompt": firstNonEmpty(event.RevisedPrompt, prompt),
				"output_format":  firstNonEmpty(event.OutputFormat, request.OutputFormat),
			}
			if event.Background != "" {
				item["background"] = event.Background
			}
			created := firstNonZeroInt64(event.Created, time.Now().Unix())
			result := e.FormatImageResultWithOptions([]map[string]any{item}, prompt, request.ResponseFormat, request.BaseURL, request.OwnerID, request.OwnerName, created, "", imageResultOutputOptions(request, event))
			data := util.AsMapSlice(result["data"])
			if remaining := resultLimit - emittedResults; remaining > 0 && len(data) > remaining {
				data = data[:remaining]
			}
			if len(data) > 0 {
				emitted = true
				emittedResults += len(data)
				out <- ImageOutput{Kind: "result", Model: request.Model, Index: index, Total: total, Created: created, Data: data}
			}
		}
		if err := <-upstreamErr; err != nil {
			errCh <- err
			return
		}
		if !emitted {
			errCh <- fmt.Errorf("image generation failed")
			return
		}
		errCh <- nil
	}()
	return out, errCh
}

// imageResultOutputOptions chooses the output options for the formatter.
// User-selected output format has priority over upstream metadata: upstream
// Responses/Codex may return WebP even when the UI asked for PNG, so the local
// formatter must re-encode to the requested format before saving.
func imageResultOutputOptions(request ConversationRequest, event backend.ResponsesImageEvent) ImageOutputOptions {
	return ImageOutputOptions{Format: firstNonEmpty(request.OutputFormat, event.OutputFormat), Compression: request.OutputCompression}
}

func imageRequestResultLimit(n int) int {
	if n < 1 {
		return 1
	}
	if n > 4 {
		return 4
	}
	return n
}

func responsesInputImages(values []string) []backend.ResponsesInputImage {
	out := make([]backend.ResponsesInputImage, 0, len(values))
	for _, value := range values {
		image := responsesInputImage(value)
		if len(image.Data) > 0 {
			out = append(out, image)
		}
	}
	return out
}

func responsesInputImagePtr(value string) *backend.ResponsesInputImage {
	image := responsesInputImage(value)
	if len(image.Data) == 0 {
		return nil
	}
	return &image
}

func responsesInputImage(value string) backend.ResponsesInputImage {
	value = strings.TrimSpace(value)
	if value == "" {
		return backend.ResponsesInputImage{}
	}
	contentType := "image/png"
	dataPart := value
	if strings.HasPrefix(value, "data:") {
		header, data, ok := strings.Cut(value, ",")
		if ok {
			dataPart = data
			if mimeType := strings.TrimPrefix(strings.Split(header, ";")[0], "data:"); mimeType != "" {
				contentType = mimeType
			}
		}
	}
	data, err := base64.StdEncoding.DecodeString(strings.TrimSpace(dataPart))
	if err != nil {
		return backend.ResponsesInputImage{}
	}
	return backend.ResponsesInputImage{Data: data, ContentType: contentType}
}

func firstNonZeroInt64(values ...int64) int64 {
	for _, value := range values {
		if value != 0 {
			return value
		}
	}
	return 0
}

// isFinalImageTextEvent decides whether a streaming event is a terminal
// text-only response (the model declined to invoke the image tool, or a
// moderation/blocked notice). When true the caller should surface the text
// to the user instead of treating the absence of bytes as an error.
func isFinalImageTextEvent(event backend.ResponsesImageEvent) bool {
	if strings.TrimSpace(event.Text) == "" || event.Result != "" {
		return false
	}
	if event.Blocked {
		return true
	}
	if strings.EqualFold(strings.TrimSpace(event.TurnUseCase), "text") {
		return true
	}
	return event.ToolInvoked != nil && !*event.ToolInvoked
}

// buildResponsesImagePrompt prepares the prompt that gets shipped upstream.
// The codex variant forwards the raw prompt; everything else uses our
// existing BuildImagePrompt to add aspect/quality hints.
func buildResponsesImagePrompt(prompt, size, model string) string {
	prompt = strings.TrimSpace(prompt)
	if prompt == "" {
		return ""
	}
	if strings.TrimSpace(model) == util.ImageModelCodex {
		return prompt
	}
	return BuildImagePrompt(prompt, size, "")
}
