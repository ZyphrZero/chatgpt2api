package backend

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"math"
	"net/http"
	urlpkg "net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"chatgpt2api/internal/util"

	_ "github.com/HugoSmits86/nativewebp"
)

const (
	officialImagePreparePath = "/backend-api/f/conversation/prepare"
	officialImageStreamPath  = "/backend-api/f/conversation"

	ResponsesImageMainModel      = "gpt-5.4-mini"
	ResponsesImageCodexToolModel = "gpt-5.4-mini"

	codexResponsesPath       = "/backend-api/codex/responses"
	codexResponsesUserAgent  = "codex-tui/0.128.0 (Mac OS 26.3.1; arm64) iTerm.app/3.6.9 (codex-tui; 0.128.0)"
	codexResponsesOriginator = "codex-tui"

	responsesImageDefaultSize  = "1024x1024"
	responsesImageSizeMultiple = 16
	responsesImageMaxEdge      = 3840
	responsesImageMaxRatio     = 3
	responsesImageMinPixels    = 655360
	responsesImageMaxPixels    = 8294400

	officialImageDownloadAttempts = 3
	officialImagePollTimeout      = 60 * time.Second
)

var officialImageDownloadRetryDelay = 750 * time.Millisecond

const responsesImagePromptGuardPrefix = "Use the following text as the complete prompt. Do not rewrite it:"

const responsesImageToolBridgeMarker = "<chatgpt2api-codex-image-generation>"

const responsesImageToolBridgeText = responsesImageToolBridgeMarker + "\nWhen the user asks for raster image generation or editing, use the OpenAI Responses native `image_generation` tool attached to this request. The local Codex client may not expose an `image_gen` namespace, but that does not mean image generation is unavailable. Do not answer with prose or JSON instead of calling the image_generation tool.\n</chatgpt2api-codex-image-generation>"

type ResponsesInputImage struct {
	Data        []byte
	ContentType string
}

type ResponsesImageRequest struct {
	Prompt              string
	Model               string
	Size                string
	Quality             string
	Background          string
	Moderation          string
	Style               string
	OutputFormat        string
	OutputCompression   *int
	PartialImages       *int
	ResultLimit         int
	InputImages         []ResponsesInputImage
	InputImageMask      *ResponsesInputImage
	AllowCodexFallback  bool
	ForceCodexResponses bool
}

type ResponsesImageEvent struct {
	Type              string
	ItemID            string
	Result            string
	PartialImage      string
	PartialImageIndex int
	RevisedPrompt     string
	OutputFormat      string
	Background        string
	Size              string
	Quality           string
	Model             string
	Created           int64
	ConversationID    string
	FileIDs           []string
	SedimentIDs       []string
	DirectURLs        []string
	Text              string
	Blocked           bool
	ToolInvoked       *bool
	TurnUseCase       string
	Raw               map[string]any
}

type uploadedImageRef struct {
	FileID      string
	FileName    string
	FileSize    int
	MIMEType    string
	Width       int
	Height      int
	IsMaskImage bool
}

type imageConversationState struct {
	Text           string
	ConversationID string
	FileIDs        []string
	SedimentIDs    []string
	DirectURLs     []string
	Blocked        bool
	ToolInvoked    *bool
	TurnUseCase    string
}

func (c *Client) StreamResponsesImage(ctx context.Context, request ResponsesImageRequest) (<-chan ResponsesImageEvent, <-chan error) {
	out := make(chan ResponsesImageEvent)
	errCh := make(chan error, 1)
	go func() {
		defer close(out)
		defer close(errCh)
		if request.ForceCodexResponses || usesCodexResponsesImageRoute(request.Model) {
			errCh <- c.streamCodexResponsesImage(ctx, request, out)
			return
		}
		if request.AllowCodexFallback {
			errCh <- c.streamOfficialResponsesImageWithCodexFallback(ctx, request, out)
			return
		}
		errCh <- c.streamOfficialResponsesImage(ctx, request, out)
	}()
	return out, errCh
}

func usesCodexResponsesImageRoute(model string) bool {
	switch strings.TrimSpace(model) {
	case util.ImageModelCodex, ResponsesImageCodexToolModel:
		return true
	default:
		return false
	}
}

func (c *Client) streamOfficialResponsesImageWithCodexFallback(ctx context.Context, request ResponsesImageRequest, out chan<- ResponsesImageEvent) error {
	bufferedEvents := make(chan ResponsesImageEvent)
	officialErrCh := make(chan error, 1)
	go func() {
		officialErrCh <- c.streamOfficialResponsesImage(ctx, request, bufferedEvents)
		close(bufferedEvents)
	}()
	var events []ResponsesImageEvent
	hasResult := false
	lastEvent := ResponsesImageEvent{}
	for event := range bufferedEvents {
		events = append(events, event)
		lastEvent = event
		if event.Result != "" {
			hasResult = true
		}
	}
	if err := <-officialErrCh; err != nil {
		if shouldUseCodexFallbackForOfficialImageError(err) {
			return c.streamCodexResponsesImage(ctx, codexFallbackImageRequest(request), out)
		}
		return err
	}
	if !hasResult && shouldUseCodexFallbackForOfficialImageEvents(lastEvent) {
		return c.streamCodexResponsesImage(ctx, codexFallbackImageRequest(request), out)
	}
	for _, event := range events {
		select {
		case out <- event:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return nil
}

func codexFallbackImageRequest(request ResponsesImageRequest) ResponsesImageRequest {
	request.Model = util.ImageModelCodex
	request.Quality = ""
	return request
}

func shouldUseCodexFallbackForOfficialImageError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.Canceled) {
		return false
	}
	message := strings.ToLower(strings.TrimSpace(err.Error()))
	if message == "" {
		return false
	}
	if _, ok := util.SummarizeUpstreamConnectionError(message); ok {
		return true
	}
	return strings.Contains(message, "no image result") ||
		strings.Contains(message, "timeout") ||
		strings.Contains(message, "deadline exceeded") ||
		strings.Contains(message, "stream interrupted") ||
		strings.Contains(message, "image_upload failed") ||
		strings.Contains(message, "proxy reachability") ||
		strings.Contains(message, "handshake") ||
		strings.Contains(message, "connection reset") ||
		strings.Contains(message, "unexpected eof") ||
		strings.Contains(message, "eof")
}

func shouldUseCodexFallbackForOfficialImageEvents(event ResponsesImageEvent) bool {
	if event.Result != "" || event.Blocked {
		return false
	}
	return strings.TrimSpace(event.Text) == "" ||
		strings.EqualFold(strings.TrimSpace(event.TurnUseCase), "text") ||
		event.ToolInvoked != nil && !*event.ToolInvoked
}

func (c *Client) streamOfficialResponsesImage(ctx context.Context, request ResponsesImageRequest, out chan<- ResponsesImageEvent) error {
	if strings.TrimSpace(c.AccessToken) == "" {
		return fmt.Errorf("access_token is required for official image conversation route")
	}
	prompt := strings.TrimSpace(request.Prompt)
	if prompt == "" {
		return fmt.Errorf("prompt is required")
	}
	if err := c.bootstrap(ctx); err != nil {
		return err
	}
	reqs, err := c.getChatRequirements(ctx)
	if err != nil {
		return err
	}
	attachments := make([]uploadedImageRef, 0, len(request.InputImages))
	for index, input := range request.InputImages {
		ref, uploadErr := c.uploadResponsesImageRef(ctx, input, fmt.Sprintf("image_%d.%s", index+1, uploadImageExtension(normalizeUploadImageFormat(input.ContentType))))
		if uploadErr != nil {
			return uploadErr
		}
		attachments = append(attachments, ref)
	}
	var maskRef *uploadedImageRef
	if request.InputImageMask != nil && len(request.InputImageMask.Data) > 0 {
		ref, uploadErr := c.uploadResponsesImageRef(ctx, *request.InputImageMask, "mask."+uploadImageExtension(normalizeUploadImageFormat(request.InputImageMask.ContentType)))
		if uploadErr != nil {
			return uploadErr
		}
		ref.IsMaskImage = true
		maskRef = &ref
	}
	streamPrompt := buildOfficialImagePrompt(prompt, request.Size)
	conduitToken, err := c.prepareOfficialImageConversation(ctx, streamPrompt, reqs, request.Model)
	if err != nil {
		return err
	}
	resp, err := c.startOfficialImageConversation(ctx, streamPrompt, reqs, conduitToken, request.Model, attachments, maskRef)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		data, _ := io.ReadAll(resp.Body)
		return upstreamHTTPError(officialImageStreamPath, resp.StatusCode, data)
	}
	return iterOfficialImageSSE(ctx, c, resp.Body, request, out)
}

func (c *Client) streamCodexResponsesImage(ctx context.Context, request ResponsesImageRequest, out chan<- ResponsesImageEvent) error {
	if strings.TrimSpace(c.AccessToken) == "" {
		return fmt.Errorf("access_token is required for codex responses image route")
	}
	accountID := c.chatGPTAccountID()
	headers, err := c.responsesImageHeaders(accountID)
	if err != nil {
		return err
	}
	payload, err := buildResponsesImagePayload(request)
	if err != nil {
		return err
	}
	resp, err := c.postRaw(ctx, codexResponsesPath, payload, headers, true)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		data, _ := io.ReadAll(resp.Body)
		return upstreamHTTPError(codexResponsesPath, resp.StatusCode, data)
	}
	return iterResponsesImageSSE(ctx, resp.Body, out)
}

func (c *Client) responsesImageHeaders(accountID string) (map[string]string, error) {
	if strings.TrimSpace(c.AccessToken) == "" {
		return nil, fmt.Errorf("access_token is required for codex responses image route")
	}
	accountID = strings.TrimSpace(accountID)
	if accountID == "" {
		return nil, fmt.Errorf("chatgpt_account_id is required for codex responses image route")
	}
	if c.sessionID == "" {
		c.sessionID = util.NewUUID()
	}
	return map[string]string{
		"Authorization":         "Bearer " + c.AccessToken,
		"Chatgpt-Account-Id":    accountID,
		"Content-Type":          "application/json",
		"Accept":                "text/event-stream",
		"User-Agent":            codexResponsesUserAgent,
		"Originator":            codexResponsesOriginator,
		"Session_id":            c.sessionID,
		"Connection":            "Keep-Alive",
		"X-OpenAI-Target-Path":  codexResponsesPath,
		"X-OpenAI-Target-Route": codexResponsesPath,
	}, nil
}

func buildResponsesImagePayload(request ResponsesImageRequest) ([]byte, error) {
	prompt := strings.TrimSpace(request.Prompt)
	if prompt == "" {
		return nil, fmt.Errorf("prompt is required")
	}
	content := []any{map[string]any{"type": "input_text", "text": prompt}}
	for _, image := range request.InputImages {
		if len(image.Data) == 0 {
			continue
		}
		content = append(content, map[string]any{"type": "input_image", "image_url": imageDataURL(image)})
	}
	tool := map[string]any{"type": "image_generation", "action": "generate"}
	if len(request.InputImages) > 0 {
		tool["action"] = "edit"
	}
	if model := normalizeResponsesImageToolModel(request.Model); model != "" {
		tool["model"] = model
	}
	if size := normalizeResponsesImageToolSize(request.Size); size != "" {
		tool["size"] = size
	}
	for key, value := range map[string]string{
		"quality":       request.Quality,
		"background":    request.Background,
		"moderation":    request.Moderation,
		"style":         request.Style,
		"output_format": request.OutputFormat,
	} {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			tool[key] = trimmed
		}
	}
	if request.OutputCompression != nil && supportsResponsesImageOutputCompression(request.OutputFormat) {
		tool["output_compression"] = *request.OutputCompression
	}
	if request.PartialImages != nil {
		tool["partial_images"] = *request.PartialImages
	}
	if request.InputImageMask != nil && len(request.InputImageMask.Data) > 0 {
		tool["input_image_mask"] = map[string]any{"image_url": imageDataURL(*request.InputImageMask)}
	}
	payload := map[string]any{
		"model":               ResponsesImageMainModel,
		"input":               []any{map[string]any{"role": "user", "content": content}},
		"tools":               []any{tool},
		"tool_choice":         "required",
		"instructions":        "You generate and edit images for the user.",
		"stream":              true,
		"store":               false,
		"parallel_tool_calls": true,
		"include":             []string{"reasoning.encrypted_content"},
	}
	return json.Marshal(payload)
}

func guardedResponsesImagePrompt(prompt string) string {
	prompt = strings.TrimSpace(prompt)
	if prompt == "" {
		return responsesImagePromptGuardPrefix
	}
	return responsesImagePromptGuardPrefix + "\n" + prompt
}

func supportsResponsesImageOutputCompression(format string) bool {
	switch strings.ToLower(strings.TrimSpace(format)) {
	case "jpg", "jpeg":
		return true
	default:
		return false
	}
}

func normalizeResponsesImageToolModel(model string) string {
	switch strings.ToLower(strings.TrimSpace(model)) {
	case "", util.ImageModelAuto, "gpt-image-1", util.ImageModelGPT:
		return ""
	case util.ImageModelCodex:
		return ResponsesImageCodexToolModel
	case ResponsesImageCodexToolModel:
		return ResponsesImageCodexToolModel
	case util.ImageModelGPT54:
		return util.ImageModelGPT54
	case util.ImageModelGPT55:
		return util.ImageModelGPT55
	case "gpt-5-5-thinking":
		return "gpt-5-5-thinking"
	default:
		return ""
	}
}

func normalizeResponsesImageToolSize(size string) string {
	normalized := strings.ToLower(strings.TrimSpace(size))
	normalized = strings.ReplaceAll(normalized, " ", "")
	normalized = strings.ReplaceAll(normalized, "×", "x")
	if normalized == "" || normalized == "auto" {
		return ""
	}
	switch normalized {
	case "1080p":
		return normalizeResponsesImageDimensions(1080, 1080)
	case "2k":
		return normalizeResponsesImageDimensions(2048, 2048)
	case "4k":
		return normalizeResponsesImageDimensions(3840, 3840)
	}
	if width, height, ok := parseResponsesImageDimensions(normalized); ok {
		if width < 128 && height < 128 {
			return responsesImageSizeFromRatio(float64(width), float64(height))
		}
		return normalizeResponsesImageDimensions(width, height)
	}
	if ratioWidth, ratioHeight, ok := parseResponsesImageRatio(normalized); ok {
		return responsesImageSizeFromRatio(ratioWidth, ratioHeight)
	}
	return ""
}

func parseResponsesImageDimensions(value string) (int, int, bool) {
	match := regexp.MustCompile(`^(\d+)x(\d+)$`).FindStringSubmatch(value)
	if len(match) != 3 {
		return 0, 0, false
	}
	width, err := strconv.Atoi(match[1])
	if err != nil || width <= 0 {
		return 0, 0, false
	}
	height, err := strconv.Atoi(match[2])
	if err != nil || height <= 0 {
		return 0, 0, false
	}
	return width, height, true
}

func parseResponsesImageRatio(value string) (float64, float64, bool) {
	match := regexp.MustCompile(`^(\d+(?:\.\d+)?):(\d+(?:\.\d+)?)$`).FindStringSubmatch(value)
	if len(match) != 3 {
		return 0, 0, false
	}
	width, err := strconv.ParseFloat(match[1], 64)
	if err != nil || width <= 0 {
		return 0, 0, false
	}
	height, err := strconv.ParseFloat(match[2], 64)
	if err != nil || height <= 0 {
		return 0, 0, false
	}
	return width, height, true
}

func responsesImageSizeFromRatio(ratioWidth, ratioHeight float64) string {
	if ratioWidth <= 0 || ratioHeight <= 0 {
		return ""
	}
	if ratioWidth == ratioHeight {
		return responsesImageDefaultSize
	}
	if ratioWidth > ratioHeight {
		return normalizeResponsesImageDimensions(1536, int(float64(1536)*ratioHeight/ratioWidth+0.5))
	}
	return normalizeResponsesImageDimensions(int(float64(1536)*ratioWidth/ratioHeight+0.5), 1536)
}

func normalizeResponsesImageDimensions(width, height int) string {
	if width <= 0 || height <= 0 {
		return ""
	}
	normalizedWidth := roundToResponsesImageMultiple(width)
	normalizedHeight := roundToResponsesImageMultiple(height)

	scaleToFit := func(scale float64) {
		normalizedWidth = floorToResponsesImageMultiple(float64(normalizedWidth) * scale)
		normalizedHeight = floorToResponsesImageMultiple(float64(normalizedHeight) * scale)
	}
	scaleToFill := func(scale float64) {
		normalizedWidth = ceilToResponsesImageMultiple(float64(normalizedWidth) * scale)
		normalizedHeight = ceilToResponsesImageMultiple(float64(normalizedHeight) * scale)
	}

	for range 4 {
		maxEdge := max(normalizedWidth, normalizedHeight)
		if maxEdge > responsesImageMaxEdge {
			scaleToFit(float64(responsesImageMaxEdge) / float64(maxEdge))
		}
		if normalizedWidth > normalizedHeight*responsesImageMaxRatio {
			normalizedWidth = floorToResponsesImageMultiple(float64(normalizedHeight * responsesImageMaxRatio))
		} else if normalizedHeight > normalizedWidth*responsesImageMaxRatio {
			normalizedHeight = floorToResponsesImageMultiple(float64(normalizedWidth * responsesImageMaxRatio))
		}
		pixels := normalizedWidth * normalizedHeight
		if pixels > responsesImageMaxPixels {
			scaleToFit(math.Sqrt(float64(responsesImageMaxPixels) / float64(pixels)))
		} else if pixels < responsesImageMinPixels {
			scaleToFill(math.Sqrt(float64(responsesImageMinPixels) / float64(pixels)))
		}
	}

	return fmt.Sprintf("%dx%d", normalizedWidth, normalizedHeight)
}

func roundToResponsesImageMultiple(value int) int {
	return max(responsesImageSizeMultiple, ((value+responsesImageSizeMultiple/2)/responsesImageSizeMultiple)*responsesImageSizeMultiple)
}

func floorToResponsesImageMultiple(value float64) int {
	return max(responsesImageSizeMultiple, int(value/float64(responsesImageSizeMultiple))*responsesImageSizeMultiple)
}

func ceilToResponsesImageMultiple(value float64) int {
	base := int(value / float64(responsesImageSizeMultiple))
	if float64(base*responsesImageSizeMultiple) < value {
		base++
	}
	return max(responsesImageSizeMultiple, base*responsesImageSizeMultiple)
}

func iterResponsesImageSSE(ctx context.Context, reader io.Reader, out chan<- ResponsesImageEvent) error {
	payloads := make(chan string)
	errCh := make(chan error, 1)
	go func() {
		defer close(payloads)
		errCh <- iterSSEPayloads(ctx, reader, payloads)
	}()
	for payload := range payloads {
		event, ok, err := parseResponsesImagePayload(payload)
		if err != nil {
			return err
		}
		if !ok {
			continue
		}
		select {
		case out <- event:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return <-errCh
}

func parseResponsesImagePayload(payload string) (ResponsesImageEvent, bool, error) {
	payload = strings.TrimSpace(payload)
	if payload == "" || payload == "[DONE]" {
		return ResponsesImageEvent{}, false, nil
	}
	var data map[string]any
	if err := json.Unmarshal([]byte(payload), &data); err != nil {
		return ResponsesImageEvent{}, false, err
	}
	eventType := util.Clean(data["type"])
	event := ResponsesImageEvent{Type: eventType, Created: time.Now().Unix(), Raw: data}
	switch eventType {
	case "response.image_generation_call.partial_image":
		event.PartialImage = util.Clean(data["partial_image_b64"])
		event.PartialImageIndex = util.ToInt(data["partial_image_index"], 0)
		event.OutputFormat = util.Clean(data["output_format"])
		event.Background = util.Clean(data["background"])
		return event, event.PartialImage != "", nil
	case "response.output_item.done":
		item := util.StringMap(data["item"])
		if util.Clean(item["type"]) != "image_generation_call" {
			return event, false, nil
		}
		mergeResponsesImageItem(&event, item)
		return event, event.Result != "", nil
	case "response.completed":
		response := util.StringMap(data["response"])
		for _, raw := range anySlice(response["output"]) {
			item, ok := raw.(map[string]any)
			if !ok || util.Clean(item["type"]) != "image_generation_call" {
				continue
			}
			mergeResponsesImageItem(&event, item)
			return event, event.Result != "", nil
		}
		return event, false, nil
	case "error":
		message := util.Clean(data["message"])
		if message == "" {
			message = util.Clean(util.StringMap(data["error"])["message"])
		}
		if message == "" {
			message = "codex responses image route returned an error"
		}
		return event, false, fmt.Errorf("%s", message)
	default:
		return event, false, nil
	}
}

func mergeResponsesImageItem(event *ResponsesImageEvent, item map[string]any) {
	event.ItemID = util.Clean(item["id"])
	event.Result = firstNonEmpty(util.Clean(item["result"]), util.Clean(item["b64_json"]))
	event.RevisedPrompt = util.Clean(item["revised_prompt"])
	event.OutputFormat = util.Clean(item["output_format"])
	event.Background = util.Clean(item["background"])
	event.Size = util.Clean(item["size"])
	event.Quality = util.Clean(item["quality"])
	event.Model = util.Clean(item["model"])
}

func imageDataURL(image ResponsesInputImage) string {
	contentType := strings.TrimSpace(image.ContentType)
	if contentType == "" {
		contentType = "image/png"
	}
	return "data:" + contentType + ";base64," + base64.StdEncoding.EncodeToString(image.Data)
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func buildOfficialImagePrompt(prompt, size string) string {
	prompt = strings.TrimSpace(prompt)
	if prompt == "" {
		return ""
	}
	var hints []string
	if sizeHint := buildOfficialImageSizeHint(size); sizeHint != "" {
		hints = append(hints, sizeHint)
	}
	if len(hints) == 0 {
		return prompt
	}
	return prompt + "\n\n" + strings.Join(hints, "\n")
}

func buildOfficialImageSizeHint(size string) string {
	size = strings.ToLower(strings.TrimSpace(size))
	if size == "" || size == "auto" {
		return ""
	}
	size = strings.ReplaceAll(size, "×", "x")
	size = strings.ReplaceAll(size, " ", "")
	hints := map[string]string{
		"1:1":   "输出为 1:1 正方形构图，主体居中，适合正方形画幅。",
		"3:2":   "输出为 3:2 横版构图，适合摄影、产品展示和横向叙事画幅。",
		"2:3":   "输出为 2:3 竖版构图，适合海报、人物和纵向叙事画幅。",
		"16:9":  "输出为 16:9 横屏构图，适合宽画幅展示。",
		"9:16":  "输出为 9:16 竖屏构图，适合竖版画幅展示。",
		"4:3":   "输出为 4:3 比例，兼顾宽度与高度，适合展示画面细节。",
		"3:4":   "输出为 3:4 比例，纵向构图，适合人物肖像或竖向场景。",
		"1080p": "以 1080 x 1080 像素对应的正方形画幅作为构图偏好，实际像素以上游返回为准。",
		"2k":    "以 2048 x 2048 像素对应的正方形画幅作为构图偏好，实际像素以上游返回为准。",
		"4k":    "以 2880 x 2880 像素对应的正方形画幅作为构图偏好，实际像素以上游返回为准。",
	}
	if hint, ok := hints[size]; ok {
		return hint
	}
	if width, height, ok := parseOfficialImageDimensions(size); ok {
		return fmt.Sprintf("以 %d x %d 像素对应的宽高比作为构图偏好，实际像素以上游返回为准。", width, height)
	}
	return "输出图片，目标尺寸或宽高比为 " + size + "。"
}

func normalizeUploadImageFormat(contentType string) string {
	contentType = strings.ToLower(strings.TrimSpace(contentType))
	switch contentType {
	case "image/jpeg", "image/jpg":
		return "jpeg"
	case "image/webp":
		return "webp"
	case "image/gif":
		return "gif"
	default:
		return "png"
	}
}

func uploadImageExtension(format string) string {
	switch strings.ToLower(strings.TrimSpace(format)) {
	case "jpeg", "jpg":
		return "jpg"
	case "webp":
		return "webp"
	case "gif":
		return "gif"
	default:
		return "png"
	}
}

func parseOfficialImageDimensions(value string) (int, int, bool) {
	match := regexp.MustCompile(`^(\d+)x(\d+)$`).FindStringSubmatch(value)
	if len(match) != 3 {
		return 0, 0, false
	}
	width, err := strconv.Atoi(match[1])
	if err != nil || width <= 0 {
		return 0, 0, false
	}
	height, err := strconv.Atoi(match[2])
	if err != nil || height <= 0 {
		return 0, 0, false
	}
	return width, height, true
}

func (c *Client) officialImageHeaders(path string, reqs ChatRequirements, conduitToken, accept string) map[string]string {
	extra := map[string]string{
		"Content-Type": "application/json",
		"Accept":       accept,
		"OpenAI-Sentinel-Chat-Requirements-Token": reqs.Token,
	}
	if reqs.ProofToken != "" {
		extra["OpenAI-Sentinel-Proof-Token"] = reqs.ProofToken
	}
	if reqs.TurnstileToken != "" {
		extra["OpenAI-Sentinel-Turnstile-Token"] = reqs.TurnstileToken
	}
	if reqs.SOToken != "" {
		extra["OpenAI-Sentinel-SO-Token"] = reqs.SOToken
	}
	if strings.TrimSpace(conduitToken) != "" {
		extra["X-Conduit-Token"] = conduitToken
	}
	if accept == "text/event-stream" {
		extra["X-Oai-Turn-Trace-Id"] = util.NewUUID()
	}
	return c.headers(path, extra)
}

func (c *Client) prepareOfficialImageConversation(ctx context.Context, prompt string, reqs ChatRequirements, model string) (string, error) {
	payload := map[string]any{
		"action":                "next",
		"fork_from_shared_post": false,
		"parent_message_id":     util.NewUUID(),
		"model":                 officialImageModelSlug(model),
		"client_prepare_state":  "success",
		"timezone_offset_min":   -480,
		"timezone":              "Asia/Shanghai",
		"conversation_mode":     map[string]any{"kind": "primary_assistant"},
		"system_hints":          []any{"picture_v2"},
		"partial_query": map[string]any{
			"id":      util.NewUUID(),
			"author":  map[string]any{"role": "user"},
			"content": map[string]any{"content_type": "text", "parts": []any{prompt}},
		},
		"supports_buffering":  true,
		"supported_encodings": []any{"v1"},
		"client_contextual_info": map[string]any{
			"app_name": "chatgpt.com",
		},
	}
	resp, err := c.postJSON(ctx, officialImagePreparePath, payload, c.officialImageHeaders(officialImagePreparePath, reqs, "", "*/*"), false)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if err := ensureOK(resp, officialImagePreparePath); err != nil {
		return "", err
	}
	var data map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return "", err
	}
	return util.Clean(data["conduit_token"]), nil
}

func officialImageModelSlug(model string) string {
	switch strings.TrimSpace(model) {
	case util.ImageModelCodex:
		return util.ImageModelCodex
	default:
		return "gpt-5-5-thinking"
	}
}

func (c *Client) uploadResponsesImageRef(ctx context.Context, input ResponsesInputImage, fileName string) (uploadedImageRef, error) {
	if len(input.Data) == 0 {
		return uploadedImageRef{}, fmt.Errorf("image data is required")
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(input.Data))
	if err != nil || cfg.Width <= 0 || cfg.Height <= 0 {
		return uploadedImageRef{}, fmt.Errorf("image decode failed: %w", err)
	}
	contentType := strings.TrimSpace(input.ContentType)
	if contentType == "" {
		contentType = "image/png"
	}
	path := "/backend-api/files"
	payload := map[string]any{
		"file_name": fileName,
		"file_size": len(input.Data),
		"use_case":  "multimodal",
		"width":     cfg.Width,
		"height":    cfg.Height,
	}
	resp, err := c.postJSON(ctx, path, payload, c.headers(path, map[string]string{"Content-Type": "application/json", "Accept": "application/json"}), false)
	if err != nil {
		return uploadedImageRef{}, err
	}
	defer resp.Body.Close()
	if err := ensureOK(resp, path); err != nil {
		return uploadedImageRef{}, err
	}
	var uploaded map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&uploaded); err != nil {
		return uploadedImageRef{}, err
	}
	uploadURL := util.Clean(uploaded["upload_url"])
	fileID := util.Clean(uploaded["file_id"])
	if uploadURL == "" || fileID == "" {
		return uploadedImageRef{}, fmt.Errorf("file upload metadata incomplete")
	}
	req, _ := http.NewRequestWithContext(ctx, http.MethodPut, uploadURL, bytes.NewReader(input.Data))
	for key, value := range map[string]string{
		"Content-Type":    contentType,
		"x-ms-blob-type":  "BlockBlob",
		"x-ms-version":    "2020-04-08",
		"Origin":          c.BaseURL,
		"Referer":         c.BaseURL + "/",
		"User-Agent":      c.userAgent,
		"Accept":          "application/json, text/plain, */*",
		"Accept-Language": "zh-CN,zh;q=0.9,en;q=0.8,en-US;q=0.7",
	} {
		req.Header.Set(key, value)
	}
	uploadResp, err := c.httpClient.Do(req)
	if err != nil {
		return uploadedImageRef{}, upstreamTransportError("image_upload", err)
	}
	defer uploadResp.Body.Close()
	if uploadResp.StatusCode < 200 || uploadResp.StatusCode >= 300 {
		data, _ := io.ReadAll(uploadResp.Body)
		return uploadedImageRef{}, upstreamHTTPError("image_upload", uploadResp.StatusCode, data)
	}
	time.Sleep(500 * time.Millisecond)
	finalizePath := "/backend-api/files/" + fileID + "/uploaded"
	finalizeReq, _ := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+finalizePath, strings.NewReader("{}"))
	for key, value := range c.headers(finalizePath, map[string]string{"Content-Type": "application/json", "Accept": "application/json"}) {
		finalizeReq.Header.Set(key, value)
	}
	finalizeResp, err := c.httpClient.Do(finalizeReq)
	if err != nil {
		return uploadedImageRef{}, upstreamTransportError(finalizePath, err)
	}
	defer finalizeResp.Body.Close()
	if err := ensureOK(finalizeResp, finalizePath); err != nil {
		return uploadedImageRef{}, err
	}
	return uploadedImageRef{
		FileID:   fileID,
		FileName: fileName,
		FileSize: len(input.Data),
		MIMEType: contentType,
		Width:    cfg.Width,
		Height:   cfg.Height,
	}, nil
}

func (c *Client) startOfficialImageConversation(ctx context.Context, prompt string, reqs ChatRequirements, conduitToken, model string, refs []uploadedImageRef, maskRef *uploadedImageRef) (*http.Response, error) {
	parts := make([]any, 0, len(refs)+2)
	for _, ref := range refs {
		parts = append(parts, map[string]any{
			"content_type":  "image_asset_pointer",
			"asset_pointer": "file-service://" + ref.FileID,
			"width":         ref.Width,
			"height":        ref.Height,
			"size_bytes":    ref.FileSize,
		})
	}
	parts = append(parts, prompt)
	content := map[string]any{"content_type": "text", "parts": []any{prompt}}
	if len(refs) > 0 {
		content = map[string]any{"content_type": "multimodal_text", "parts": parts}
	}
	metadata := map[string]any{
		"developer_mode_connector_ids": []any{},
		"selected_github_repos":        []any{},
		"selected_all_github_repos":    false,
		"system_hints":                 []any{"picture_v2"},
		"serialization_metadata":       map[string]any{"custom_symbol_offsets": []any{}},
	}
	if len(refs) > 0 {
		attachments := make([]map[string]any, 0, len(refs))
		for _, ref := range refs {
			attachments = append(attachments, map[string]any{
				"id":       ref.FileID,
				"mimeType": ref.MIMEType,
				"name":     ref.FileName,
				"size":     ref.FileSize,
				"width":    ref.Width,
				"height":   ref.Height,
			})
		}
		metadata["attachments"] = attachments
	}
	if maskRef != nil {
		metadata["mask_attachment"] = map[string]any{
			"id":       maskRef.FileID,
			"mimeType": maskRef.MIMEType,
			"name":     maskRef.FileName,
			"size":     maskRef.FileSize,
			"width":    maskRef.Width,
			"height":   maskRef.Height,
		}
		content["mask_pointer"] = "file-service://" + maskRef.FileID
	}
	payload := map[string]any{
		"action": "next",
		"messages": []any{
			map[string]any{
				"id":          util.NewUUID(),
				"author":      map[string]any{"role": "user"},
				"create_time": float64(time.Now().UnixNano()) / 1e9,
				"content":     content,
				"metadata":    metadata,
			},
		},
		"parent_message_id":                    util.NewUUID(),
		"model":                                officialImageModelSlug(model),
		"client_prepare_state":                 "sent",
		"timezone_offset_min":                  -480,
		"timezone":                             "Asia/Shanghai",
		"conversation_mode":                    map[string]any{"kind": "primary_assistant"},
		"enable_message_followups":             true,
		"system_hints":                         []any{"picture_v2"},
		"supports_buffering":                   true,
		"supported_encodings":                  []any{"v1"},
		"force_use_sse":                        true,
		"paragen_cot_summary_display_override": "allow",
		"force_parallel_switch":                "auto",
		"client_contextual_info": map[string]any{
			"is_dark_mode":      false,
			"time_since_loaded": 1200,
			"page_height":       1072,
			"page_width":        1724,
			"pixel_ratio":       1.2,
			"screen_height":     1440,
			"screen_width":      2560,
			"app_name":          "chatgpt.com",
		},
	}
	return c.postJSON(ctx, officialImageStreamPath, payload, c.officialImageHeaders(officialImageStreamPath, reqs, conduitToken, "text/event-stream"), true)
}

func iterOfficialImageSSE(ctx context.Context, client *Client, reader io.Reader, request ResponsesImageRequest, out chan<- ResponsesImageEvent) error {
	state := &imageConversationState{}
	payloads := make(chan string)
	errCh := make(chan error, 1)
	go func() {
		defer close(payloads)
		errCh <- iterSSEPayloads(ctx, reader, payloads)
	}()

	emittedResult := false
	lastEvent := ResponsesImageEvent{}
	for payload := range payloads {
		event, ok, err := parseOfficialImagePayload(payload, state)
		if err != nil {
			return err
		}
		if !ok {
			continue
		}
		lastEvent = event
		select {
		case out <- event:
		case <-ctx.Done():
			return ctx.Err()
		}
		if event.Result != "" {
			emittedResult = true
		}
	}
	if err := <-errCh; err != nil {
		return err
	}
	if emittedResult {
		return nil
	}
	if shouldTreatOfficialImageEventAsFinalText(lastEvent) {
		return nil
	}
	resolved, err := client.resolveOfficialImageResults(ctx, request, lastEvent)
	if err != nil {
		return err
	}
	for _, event := range resolved {
		select {
		case out <- event:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	if len(resolved) == 0 && strings.TrimSpace(lastEvent.Text) == "" {
		return fmt.Errorf("image generation produced no image result")
	}
	return nil
}

func shouldTreatOfficialImageEventAsFinalText(event ResponsesImageEvent) bool {
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

func parseOfficialImagePayload(payload string, state *imageConversationState) (ResponsesImageEvent, bool, error) {
	payload = strings.TrimSpace(payload)
	if payload == "" || payload == "[DONE]" {
		return ResponsesImageEvent{}, false, nil
	}
	var data map[string]any
	if err := json.Unmarshal([]byte(payload), &data); err != nil {
		updateOfficialImageConversationState(state, payload, nil)
		return ResponsesImageEvent{}, false, nil
	}
	updateOfficialImageConversationState(state, payload, data)
	eventType := util.Clean(data["type"])
	event := ResponsesImageEvent{
		Type:           eventType,
		Created:        time.Now().Unix(),
		ConversationID: state.ConversationID,
		FileIDs:        append([]string(nil), state.FileIDs...),
		SedimentIDs:    append([]string(nil), state.SedimentIDs...),
		DirectURLs:     append([]string(nil), state.DirectURLs...),
		Text:           state.Text,
		Blocked:        state.Blocked,
		ToolInvoked:    state.ToolInvoked,
		TurnUseCase:    state.TurnUseCase,
		Raw:            data,
	}
	if message := officialImageTextMessage(data); message != "" && event.Result == "" {
		event.Text = message
	}
	switch eventType {
	case "moderation", "title_generation", "message_stream_complete", "server_ste_metadata", "message_marker", "input_message", "resume_conversation_token":
		return event, true, nil
	case "error":
		message := util.Clean(data["message"])
		if message == "" {
			message = util.Clean(util.StringMap(data["error"])["message"])
		}
		if message == "" {
			message = "official image conversation route returned an error"
		}
		return event, false, fmt.Errorf("%s", message)
	default:
		return event, true, nil
	}
}

func updateOfficialImageConversationState(state *imageConversationState, payload string, event map[string]any) {
	conversationID := officialImageConversationID(payload)
	if conversationID != "" && state.ConversationID == "" {
		state.ConversationID = conversationID
	}
	if event == nil {
		return
	}
	if util.Clean(event["conversation_id"]) != "" {
		state.ConversationID = util.Clean(event["conversation_id"])
	}
	value := util.StringMap(event["v"])
	if util.Clean(value["conversation_id"]) != "" {
		state.ConversationID = util.Clean(value["conversation_id"])
	}
	if event["type"] == "moderation" {
		moderation := util.StringMap(event["moderation_response"])
		if util.ToBool(moderation["blocked"]) {
			state.Blocked = true
		}
	}
	if event["type"] == "server_ste_metadata" {
		metadata := util.StringMap(event["metadata"])
		if toolInvoked, ok := metadata["tool_invoked"].(bool); ok {
			state.ToolInvoked = &toolInvoked
		}
		if turnUseCase := util.Clean(metadata["turn_use_case"]); turnUseCase != "" {
			state.TurnUseCase = turnUseCase
		}
	}
	if isOfficialImageToolEvent(event) {
		fileIDs, sedimentIDs, directURLs := officialImageToolResultRefs(event)
		state.FileIDs = appendUniqueString(state.FileIDs, fileIDs...)
		state.SedimentIDs = appendUniqueString(state.SedimentIDs, sedimentIDs...)
		state.DirectURLs = appendUniqueString(state.DirectURLs, directURLs...)
		if text := officialImageTextMessage(event); text != "" {
			state.Text = text
		}
		return
	}
	if text := officialImageAssistantText(event); text != "" {
		state.Text = text
	}
}

func officialImageConversationID(payload string) string {
	if match := regexp.MustCompile(`"conversation_id"\s*:\s*"([^"]+)"`).FindStringSubmatch(payload); len(match) > 1 {
		return match[1]
	}
	return ""
}

func extractOfficialConversationIDs(payload string) (string, []string, []string, []string) {
	conversation := officialImageConversationID(payload)
	files, sediments, directURLs := officialImageRefsFromPayload(payload)
	return conversation, files, sediments, directURLs
}

func officialImageRefsFromPayload(payload string) ([]string, []string, []string) {
	fileIDs := regexp.MustCompile(`file-service://([A-Za-z0-9_-]+)`).FindAllStringSubmatch(payload, -1)
	files := make([]string, 0, len(fileIDs))
	for _, match := range fileIDs {
		if len(match) > 1 {
			files = appendUniqueString(files, match[1])
		}
	}
	sedimentIDs := regexp.MustCompile(`sediment://([A-Za-z0-9_-]+)`).FindAllStringSubmatch(payload, -1)
	sediments := make([]string, 0, len(sedimentIDs))
	for _, match := range sedimentIDs {
		if len(match) > 1 {
			sediments = appendUniqueString(sediments, match[1])
		}
	}
	return files, sediments, officialImageDirectURLs(payload)
}

func officialImageToolResultRefs(event map[string]any) ([]string, []string, []string) {
	message := officialImageEventMessage(event)
	if len(message) == 0 {
		return nil, nil, nil
	}
	raw, err := json.Marshal(message)
	if err != nil {
		return nil, nil, nil
	}
	return officialImageRefsFromPayload(string(raw))
}

func officialImageDirectURLs(payload string) []string {
	var out []string
	re := regexp.MustCompile(`https:\\?/\\?/(?:files\.oaiusercontent\.com|oaidalleapiprodscus\.blob\.core\.windows\.net)[^"\\]+`)
	for _, raw := range re.FindAllString(payload, -1) {
		url := strings.ReplaceAll(raw, `\/`, `/`)
		url = strings.ReplaceAll(url, `\u0026`, `&`)
		if strings.Contains(url, "openaiassets.blob.core.windows.net/$web/chatgpt/") {
			continue
		}
		out = appendUniqueString(out, url)
	}
	for _, match := range regexp.MustCompile(`data:image/[A-Za-z0-9.+-]+;base64,[A-Za-z0-9+/=\s]+`).FindAllString(payload, -1) {
		out = appendUniqueString(out, strings.TrimSpace(match))
	}
	return out
}

func officialImageDownloadURLs(values []string) []string {
	var out []string
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if strings.Contains(value, "openaiassets.blob.core.windows.net/$web/chatgpt/") {
			continue
		}
		out = appendUniqueString(out, value)
	}
	return out
}

func isOfficialImageToolEvent(event map[string]any) bool {
	message := officialImageEventMessage(event)
	metadata := util.StringMap(message["metadata"])
	author := util.StringMap(message["author"])
	return strings.EqualFold(util.Clean(author["role"]), "tool") && util.Clean(metadata["async_task_type"]) == "image_gen"
}

func officialImageEventMessage(event map[string]any) map[string]any {
	value := util.StringMap(event["v"])
	message := util.StringMap(event["message"])
	if len(message) == 0 {
		message = util.StringMap(value["message"])
	}
	return message
}

func officialImageAssistantText(event map[string]any) string {
	for _, candidate := range []any{event, event["v"]} {
		message := util.StringMap(util.StringMap(candidate)["message"])
		author := util.StringMap(message["author"])
		if !strings.EqualFold(util.Clean(author["role"]), "assistant") {
			continue
		}
		if text := officialImageMessageText(message); text != "" {
			return text
		}
	}
	return ""
}

func officialImageTextMessage(event map[string]any) string {
	for _, candidate := range []any{event, event["v"]} {
		message := util.StringMap(util.StringMap(candidate)["message"])
		if len(message) == 0 {
			continue
		}
		if text := officialImageMessageText(message); text != "" {
			return text
		}
	}
	return ""
}

func officialImageMessageText(message map[string]any) string {
	content := util.StringMap(message["content"])
	parts, _ := content["parts"].([]any)
	var out []string
	for _, part := range parts {
		if text := officialImageTextValue(part); text != "" {
			out = append(out, strings.TrimSpace(text))
		}
	}
	return strings.TrimSpace(strings.Join(out, "\n"))
}

func officialImageTextValue(value any) string {
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed)
	case []any:
		var parts []string
		for _, item := range typed {
			if text := officialImageTextValue(item); text != "" {
				parts = append(parts, text)
			}
		}
		return strings.TrimSpace(strings.Join(parts, "\n"))
	case map[string]any:
		for _, key := range []string{"text", "delta", "content", "value", "v"} {
			if text := officialImageTextValue(typed[key]); text != "" {
				return text
			}
		}
		if parts := anyList(typed["parts"]); len(parts) > 0 {
			return officialImageTextValue(parts)
		}
	}
	return ""
}

func (c *Client) resolveOfficialImageResults(ctx context.Context, request ResponsesImageRequest, event ResponsesImageEvent) ([]ResponsesImageEvent, error) {
	conversationID := strings.TrimSpace(event.ConversationID)
	fileIDs := filterOfficialImageIDs(event.FileIDs)
	sedimentIDs := filterOfficialImageIDs(event.SedimentIDs)
	directURLs := officialImageDownloadURLs(event.DirectURLs)
	limit := officialImageResultLimit(request.ResultLimit)
	if conversationID != "" && len(fileIDs) == 0 && len(sedimentIDs) == 0 && len(directURLs) == 0 {
		polledFiles, polledSediments, polledURLs, err := c.pollOfficialImageResults(ctx, conversationID, officialImagePollTimeout)
		if err != nil {
			return nil, err
		}
		fileIDs = appendUniqueString(fileIDs, polledFiles...)
		sedimentIDs = appendUniqueString(sedimentIDs, polledSediments...)
		directURLs = appendUniqueString(directURLs, polledURLs...)
	}
	imageFileIDs := officialImageFileIDs(fileIDs, sedimentIDs)
	if len(imageFileIDs) == 0 && len(directURLs) == 0 {
		return nil, nil
	}
	results := make([]ResponsesImageEvent, 0, len(directURLs)+len(imageFileIDs))
	for index, directURL := range directURLs {
		if len(results) >= limit {
			break
		}
		data, downloadErr := c.downloadOfficialImageResult(ctx, directURL)
		if downloadErr != nil {
			return nil, downloadErr
		}
		results = append(results, ResponsesImageEvent{
			Type:           "image_result",
			ItemID:         fmt.Sprintf("direct_image_%d", index+1),
			Result:         base64.StdEncoding.EncodeToString(data),
			RevisedPrompt:  strings.TrimSpace(request.Prompt),
			OutputFormat:   "png",
			Created:        time.Now().Unix(),
			ConversationID: conversationID,
			FileIDs:        append([]string(nil), fileIDs...),
			SedimentIDs:    append([]string(nil), sedimentIDs...),
			DirectURLs:     append([]string(nil), directURLs...),
		})
	}
	for index, fileID := range imageFileIDs {
		if len(results) >= limit {
			break
		}
		data, downloadErr := c.downloadOfficialImageFile(ctx, conversationID, fileID)
		if downloadErr != nil {
			return nil, downloadErr
		}
		results = append(results, ResponsesImageEvent{
			Type:           "image_result",
			ItemID:         fmt.Sprintf("image_%d", index+1),
			Result:         base64.StdEncoding.EncodeToString(data),
			RevisedPrompt:  strings.TrimSpace(request.Prompt),
			OutputFormat:   "png",
			Created:        time.Now().Unix(),
			ConversationID: conversationID,
			FileIDs:        append([]string(nil), fileIDs...),
			SedimentIDs:    append([]string(nil), sedimentIDs...),
			DirectURLs:     append([]string(nil), directURLs...),
		})
	}
	return results, nil
}

func officialImageResultLimit(value int) int {
	if value < 1 {
		return 1
	}
	if value > 4 {
		return 4
	}
	return value
}

func officialImageFileIDs(fileIDs, sedimentIDs []string) []string {
	out := appendUniqueString(nil, filterOfficialImageIDs(fileIDs)...)
	for _, sedimentID := range sedimentIDs {
		out = appendUniqueString(out, sedimentID)
	}
	return out
}

func filterOfficialImageIDs(values []string) []string {
	var out []string
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || value == "file_upload" {
			continue
		}
		out = appendUniqueString(out, value)
	}
	return out
}

func (c *Client) pollOfficialImageResults(ctx context.Context, conversationID string, timeout time.Duration) ([]string, []string, []string, error) {
	if strings.TrimSpace(conversationID) == "" {
		return nil, nil, nil, nil
	}
	deadline := time.Now().Add(timeout)
	pollCount := 0
	for time.Now().Before(deadline) {
		pollCount++
		fileIDs, sedimentIDs, directURLs, err := c.fetchOfficialConversationImageIDs(ctx, conversationID)
		if err != nil {
			return nil, nil, nil, err
		}
		if pollCount == 1 || pollCount%3 == 0 {
			libraryFileIDs, libraryErr := c.fetchOfficialLibraryImageIDs(ctx, conversationID)
			if libraryErr == nil {
				fileIDs = appendUniqueString(fileIDs, libraryFileIDs...)
			}
		}
		if len(fileIDs) > 0 || len(sedimentIDs) > 0 || len(directURLs) > 0 {
			return fileIDs, sedimentIDs, directURLs, nil
		}
		select {
		case <-ctx.Done():
			return nil, nil, nil, ctx.Err()
		case <-time.After(4 * time.Second):
		}
	}
	return nil, nil, nil, nil
}

func (c *Client) fetchOfficialConversationImageIDs(ctx context.Context, conversationID string) ([]string, []string, []string, error) {
	path := "/backend-api/conversation/" + conversationID
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+path, nil)
	for key, value := range c.headers(path, map[string]string{"Accept": "application/json"}) {
		req.Header.Set(key, value)
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, nil, nil, upstreamTransportError(path, err)
	}
	defer resp.Body.Close()
	if err := ensureOK(resp, path); err != nil {
		return nil, nil, nil, err
	}
	rawBody, readErr := io.ReadAll(resp.Body)
	if readErr != nil {
		return nil, nil, nil, readErr
	}
	var data map[string]any
	if err := json.Unmarshal(rawBody, &data); err != nil {
		return nil, nil, nil, err
	}
	mapping := util.StringMap(data["mapping"])
	var fileIDs []string
	var sedimentIDs []string
	var directURLs []string
	for _, raw := range mapping {
		node, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		message := util.StringMap(node["message"])
		author := util.StringMap(message["author"])
		metadata := util.StringMap(message["metadata"])
		content := util.StringMap(message["content"])
		if !strings.EqualFold(util.Clean(author["role"]), "tool") || util.Clean(metadata["async_task_type"]) != "image_gen" {
			continue
		}
		rawMessage, marshalErr := json.Marshal(message)
		if marshalErr == nil {
			messageFileIDs, messageSedimentIDs, messageDirectURLs := officialImageRefsFromPayload(string(rawMessage))
			fileIDs = appendUniqueString(fileIDs, messageFileIDs...)
			sedimentIDs = appendUniqueString(sedimentIDs, messageSedimentIDs...)
			directURLs = appendUniqueString(directURLs, messageDirectURLs...)
		}
		if util.Clean(content["content_type"]) != "multimodal_text" {
			continue
		}
		if parts, ok := content["parts"].([]any); ok {
			for _, rawPart := range parts {
				text := ""
				if item, ok := rawPart.(map[string]any); ok {
					text = util.Clean(item["asset_pointer"])
				} else if value, ok := rawPart.(string); ok {
					text = value
				}
				for _, match := range regexp.MustCompile(`file-service://([A-Za-z0-9_-]+)`).FindAllStringSubmatch(text, -1) {
					if len(match) > 1 {
						fileIDs = appendUniqueString(fileIDs, match[1])
					}
				}
				for _, match := range regexp.MustCompile(`sediment://([A-Za-z0-9_-]+)`).FindAllStringSubmatch(text, -1) {
					if len(match) > 1 {
						sedimentIDs = appendUniqueString(sedimentIDs, match[1])
					}
				}
			}
		}
	}
	return fileIDs, sedimentIDs, directURLs, nil
}

func (c *Client) fetchOfficialLibraryImageIDs(ctx context.Context, conversationID string) ([]string, error) {
	conversationID = strings.TrimSpace(conversationID)
	if conversationID == "" {
		return nil, nil
	}
	path := "/backend-api/files/library"
	payload, _ := json.Marshal(map[string]any{"limit": 20, "cursor": nil})
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+path, bytes.NewReader(payload))
	for key, value := range c.headers(path, map[string]string{"Content-Type": "application/json", "Accept": "application/json"}) {
		req.Header.Set(key, value)
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, upstreamTransportError(path, err)
	}
	defer resp.Body.Close()
	if err := ensureOK(resp, path); err != nil {
		return nil, err
	}
	var data struct {
		Items []struct {
			FileID              string `json:"file_id"`
			MimeType            string `json:"mime_type"`
			LibraryFileCategory string `json:"library_file_category"`
			State               string `json:"state"`
			OriginationThreadID string `json:"origination_thread_id"`
		} `json:"items"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return nil, err
	}
	var ids []string
	for _, item := range data.Items {
		if item.FileID == "" || item.OriginationThreadID != conversationID {
			continue
		}
		if item.State != "" && !strings.EqualFold(item.State, "ready") {
			continue
		}
		if item.LibraryFileCategory != "" && !strings.EqualFold(item.LibraryFileCategory, "image") {
			continue
		}
		if item.MimeType != "" && !strings.HasPrefix(strings.ToLower(item.MimeType), "image/") {
			continue
		}
		ids = appendUniqueString(ids, item.FileID)
	}
	return ids, nil
}

func (c *Client) getOfficialFileDownloadURL(ctx context.Context, conversationID, fileID string) (string, error) {
	conversationID = strings.TrimSpace(conversationID)
	if conversationID == "" {
		return "", fmt.Errorf("conversation_id is required for official image download")
	}
	query := urlpkg.Values{}
	query.Set("conversation_id", conversationID)
	query.Set("inline", "false")
	targetPath := "/backend-api/files/download/" + urlpkg.PathEscape(fileID)
	path := targetPath + "?" + query.Encode()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+path, nil)
	for key, value := range c.headers(targetPath, map[string]string{"Accept": "application/json"}) {
		req.Header.Set(key, value)
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", upstreamTransportError(path, err)
	}
	defer resp.Body.Close()
	if err := ensureOK(resp, path); err != nil {
		return "", err
	}
	var data map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return "", err
	}
	return firstNonEmpty(util.Clean(data["download_url"]), util.Clean(data["url"])), nil
}

func (c *Client) downloadOfficialImageResult(ctx context.Context, imageURL string) ([]byte, error) {
	imageURL = strings.TrimSpace(imageURL)
	if imageURL == "" {
		return nil, fmt.Errorf("official image result url is empty")
	}
	if strings.HasPrefix(imageURL, "data:") {
		_, data, ok := strings.Cut(imageURL, ",")
		if !ok {
			return nil, fmt.Errorf("invalid official image data url")
		}
		decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(data))
		if err != nil {
			return nil, err
		}
		return decoded, nil
	}
	return c.downloadOfficialImage(ctx, imageURL)
}

func (c *Client) downloadOfficialImageFile(ctx context.Context, conversationID, fileID string) ([]byte, error) {
	var lastErr error
	for attempt := 1; attempt <= officialImageDownloadAttempts; attempt++ {
		downloadURL, err := c.getOfficialFileDownloadURL(ctx, conversationID, fileID)
		if err == nil && strings.TrimSpace(downloadURL) == "" {
			err = fmt.Errorf("official image file %s returned empty download URL", fileID)
		}
		if err == nil {
			var data []byte
			data, err = c.downloadOfficialImage(ctx, downloadURL)
			if err == nil {
				return data, nil
			}
		}
		lastErr = err
		if attempt == officialImageDownloadAttempts {
			break
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(officialImageDownloadRetryDelay):
		}
	}
	return nil, lastErr
}

func (c *Client) downloadOfficialImage(ctx context.Context, url string) ([]byte, error) {
	target := strings.TrimSpace(url)
	parsed, err := urlpkg.Parse(target)
	if err != nil {
		return nil, err
	}
	if !parsed.IsAbs() {
		base, baseErr := urlpkg.Parse(c.BaseURL)
		if baseErr != nil {
			return nil, baseErr
		}
		parsed = base.ResolveReference(parsed)
		target = parsed.String()
	}
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if c.isChatGPTBackendURL(parsed) {
		path := parsed.EscapedPath()
		for key, value := range c.headers(path, map[string]string{"Accept": "image/avif,image/webp,image/apng,image/svg+xml,image/*,*/*;q=0.8"}) {
			req.Header.Set(key, value)
		}
	} else if c.userAgent != "" {
		req.Header.Set("User-Agent", c.userAgent)
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, upstreamTransportError("image_download", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		data, _ := io.ReadAll(resp.Body)
		return nil, upstreamHTTPError("image_download", resp.StatusCode, data)
	}
	return io.ReadAll(resp.Body)
}

func appendUniqueString(base []string, values ...string) []string {
	seen := map[string]struct{}{}
	for _, item := range base {
		if strings.TrimSpace(item) == "" {
			continue
		}
		seen[item] = struct{}{}
	}
	for _, value := range values {
		value = strings.TrimSpace(value)
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

func (c *Client) chatGPTAccountID() string {
	if c == nil || c.lookup == nil || strings.TrimSpace(c.AccessToken) == "" {
		return ""
	}
	account := c.lookup.GetAccount(c.AccessToken)
	return firstNonEmpty(
		util.Clean(account["chatgpt_account_id"]),
		util.Clean(account["account_id"]),
		util.Clean(account["user_id"]),
	)
}
