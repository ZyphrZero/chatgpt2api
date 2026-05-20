package service

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"chatgpt2api/internal/storage"
	"chatgpt2api/internal/util"
)

const (
	TaskStatusQueued    = "queued"
	TaskStatusRunning   = "running"
	TaskStatusSuccess   = "success"
	TaskStatusError     = "error"
	TaskStatusCancelled = "cancelled"

	defaultImageTaskTimeout = 5 * time.Minute
	staleQueuedTaskAge      = 10 * time.Second
)

type ImageTaskHandler func(context.Context, Identity, map[string]any) (map[string]any, error)

type ImageOutputOptions struct {
	Format      string
	Compression *int
}

type ImageTaskService struct {
	mu                  sync.RWMutex
	path                string
	store               storage.JSONDocumentBackend
	docName             string
	generation          ImageTaskHandler
	edit                ImageTaskHandler
	chat                ImageTaskHandler
	responseImage       ImageTaskHandler
	retentionGetter     func() int
	concurrentLimit     func() int
	taskTimeoutGetter   func() time.Duration
	userConcurrentLimit func() int
	userRPMLimit        func() int
	runningImages       int
	tasks               map[string]map[string]any
	cancels             map[string]context.CancelFunc
	ownerSubmitTimes    map[string][]time.Time
}

type ImageTaskLimitError struct {
	Message string
}

func (e ImageTaskLimitError) Error() string {
	return e.Message
}

func NewImageTaskService(path string, generation ImageTaskHandler, edit ImageTaskHandler, chat ImageTaskHandler, retentionGetter func() int, limitGetters ...func() int) *ImageTaskService {
	return newImageTaskService(path, nil, generation, edit, chat, retentionGetter, limitGetters...)
}

func NewStoredImageTaskService(path string, backend storage.Backend, generation ImageTaskHandler, edit ImageTaskHandler, chat ImageTaskHandler, retentionGetter func() int, limitGetters ...func() int) *ImageTaskService {
	return newImageTaskService(path, jsonDocumentStoreFromBackend(backend), generation, edit, chat, retentionGetter, limitGetters...)
}

func newImageTaskService(path string, store storage.JSONDocumentBackend, generation ImageTaskHandler, edit ImageTaskHandler, chat ImageTaskHandler, retentionGetter func() int, limitGetters ...func() int) *ImageTaskService {
	s := &ImageTaskService{path: path, store: store, docName: "image_tasks.json", generation: generation, edit: edit, chat: chat, responseImage: generation, retentionGetter: retentionGetter, tasks: map[string]map[string]any{}, cancels: map[string]context.CancelFunc{}, ownerSubmitTimes: map[string][]time.Time{}}
	if len(limitGetters) > 0 {
		s.concurrentLimit = limitGetters[0]
	}
	if len(limitGetters) > 1 {
		s.userConcurrentLimit = limitGetters[1]
	}
	if len(limitGetters) > 2 {
		s.userRPMLimit = limitGetters[2]
	}
	_ = os.MkdirAll(filepath.Dir(path), 0o755)
	s.mu.Lock()
	s.tasks = s.loadLocked()
	changed := s.recoverUnfinishedLocked()
	if s.cleanupLocked() || changed {
		_ = s.saveLocked()
	}
	s.mu.Unlock()
	return s
}

func (s *ImageTaskService) SetResponseImageHandler(handler ImageTaskHandler) {
	if handler == nil {
		return
	}
	s.responseImage = handler
}

func (s *ImageTaskService) SetTaskTimeoutGetter(getter func() time.Duration) {
	if getter == nil {
		return
	}
	s.taskTimeoutGetter = getter
}

func (s *ImageTaskService) SubmitGeneration(ctx context.Context, identity Identity, clientTaskID, prompt, model, size, quality, baseURL string, n int, messages any, visibilityValues ...string) (map[string]any, error) {
	prompt = strings.TrimSpace(prompt)
	if prompt == "" {
		return nil, fmt.Errorf("prompt is required")
	}
	visibility, err := imageTaskVisibility(visibilityValues...)
	if err != nil {
		return nil, err
	}
	payload := map[string]any{"prompt": prompt, "model": model, "n": normalizedImageTaskCount(n), "size": size, "quality": quality, "response_format": "url", "base_url": baseURL, "visibility": visibility}
	if messages != nil {
		payload["messages"] = messages
	}
	return s.submit(ctx, identity, clientTaskID, "generate", payload)
}

func (s *ImageTaskService) SubmitGenerationWithMetadata(ctx context.Context, identity Identity, clientTaskID, prompt, model, size, quality, baseURL string, n int, messages any, metadata map[string]any, visibilityValues ...string) (map[string]any, error) {
	return s.submitImageWithMetadata(ctx, identity, clientTaskID, prompt, model, size, quality, baseURL, n, messages, metadata, "generate", nil, visibilityValues...)
}

func (s *ImageTaskService) SubmitGenerationWithOptions(ctx context.Context, identity Identity, clientTaskID, prompt, model, size, quality, baseURL string, n int, messages any, metadata map[string]any, options ImageOutputOptions, visibilityValues ...string) (map[string]any, error) {
	return s.submitImageWithMetadataAndOptions(ctx, identity, clientTaskID, prompt, model, size, quality, baseURL, n, messages, metadata, "generate", nil, options, visibilityValues...)
}

func (s *ImageTaskService) SubmitResponseImageGeneration(ctx context.Context, identity Identity, clientTaskID, prompt, model, size, quality, baseURL string, images any, n int, messages any, visibilityValues ...string) (map[string]any, error) {
	prompt = strings.TrimSpace(prompt)
	if prompt == "" {
		return nil, fmt.Errorf("prompt is required")
	}
	visibility, err := imageTaskVisibility(visibilityValues...)
	if err != nil {
		return nil, err
	}
	payload := map[string]any{"prompt": prompt, "images": images, "model": model, "n": normalizedImageTaskCount(n), "size": size, "quality": quality, "response_format": "url", "base_url": baseURL, "visibility": visibility}
	if messages != nil {
		payload["messages"] = messages
	}
	return s.submit(ctx, identity, clientTaskID, "response-image", payload)
}

func (s *ImageTaskService) SubmitResponseImageGenerationWithMetadata(ctx context.Context, identity Identity, clientTaskID, prompt, model, size, quality, baseURL string, images any, n int, messages any, metadata map[string]any, visibilityValues ...string) (map[string]any, error) {
	return s.submitImageWithMetadata(ctx, identity, clientTaskID, prompt, model, size, quality, baseURL, n, messages, metadata, "response-image", images, visibilityValues...)
}

func (s *ImageTaskService) SubmitResponseImageGenerationWithOptions(ctx context.Context, identity Identity, clientTaskID, prompt, model, size, quality, baseURL string, images any, n int, messages any, metadata map[string]any, options ImageOutputOptions, visibilityValues ...string) (map[string]any, error) {
	return s.submitImageWithMetadataAndOptions(ctx, identity, clientTaskID, prompt, model, size, quality, baseURL, n, messages, metadata, "response-image", images, options, visibilityValues...)
}

func (s *ImageTaskService) SubmitEdit(ctx context.Context, identity Identity, clientTaskID, prompt, model, size, quality, baseURL string, images any, n int, messages any, visibilityValues ...string) (map[string]any, error) {
	prompt = strings.TrimSpace(prompt)
	if prompt == "" {
		return nil, fmt.Errorf("prompt is required")
	}
	visibility, err := imageTaskVisibility(visibilityValues...)
	if err != nil {
		return nil, err
	}
	payload := map[string]any{"prompt": prompt, "images": images, "model": model, "n": normalizedImageTaskCount(n), "size": size, "quality": quality, "response_format": "url", "base_url": baseURL, "visibility": visibility}
	if messages != nil {
		payload["messages"] = messages
	}
	return s.submit(ctx, identity, clientTaskID, "edit", payload)
}

func (s *ImageTaskService) SubmitEditWithMetadata(ctx context.Context, identity Identity, clientTaskID, prompt, model, size, quality, baseURL string, images any, n int, messages any, metadata map[string]any, visibilityValues ...string) (map[string]any, error) {
	return s.submitImageWithMetadata(ctx, identity, clientTaskID, prompt, model, size, quality, baseURL, n, messages, metadata, "edit", images, visibilityValues...)
}

func (s *ImageTaskService) SubmitEditWithOptions(ctx context.Context, identity Identity, clientTaskID, prompt, model, size, quality, baseURL string, images any, n int, messages any, metadata map[string]any, options ImageOutputOptions, visibilityValues ...string) (map[string]any, error) {
	return s.submitImageWithMetadataAndOptions(ctx, identity, clientTaskID, prompt, model, size, quality, baseURL, n, messages, metadata, "edit", images, options, visibilityValues...)
}

func (s *ImageTaskService) SubmitChat(ctx context.Context, identity Identity, clientTaskID, prompt, model string, messages any) (map[string]any, error) {
	prompt = strings.TrimSpace(prompt)
	if prompt == "" {
		return nil, fmt.Errorf("prompt is required")
	}
	if len(util.AsMapSlice(messages)) == 0 {
		return nil, fmt.Errorf("messages are required")
	}
	payload := map[string]any{"prompt": prompt, "model": model, "messages": messages, "n": 1, "visibility": ImageVisibilityPrivate}
	return s.submit(ctx, identity, clientTaskID, "chat", payload)
}

func (s *ImageTaskService) submitImageWithMetadata(ctx context.Context, identity Identity, clientTaskID, prompt, model, size, quality, baseURL string, n int, messages any, metadata map[string]any, mode string, images any, visibilityValues ...string) (map[string]any, error) {
	return s.submitImageWithMetadataAndOptions(ctx, identity, clientTaskID, prompt, model, size, quality, baseURL, n, messages, metadata, mode, images, ImageOutputOptions{}, visibilityValues...)
}

func (s *ImageTaskService) submitImageWithMetadataAndOptions(ctx context.Context, identity Identity, clientTaskID, prompt, model, size, quality, baseURL string, n int, messages any, metadata map[string]any, mode string, images any, options ImageOutputOptions, visibilityValues ...string) (map[string]any, error) {
	prompt = strings.TrimSpace(prompt)
	if prompt == "" {
		return nil, fmt.Errorf("prompt is required")
	}
	visibility, err := imageTaskVisibility(visibilityValues...)
	if err != nil {
		return nil, err
	}
	payload := map[string]any{"prompt": prompt, "model": model, "n": normalizedImageTaskCount(n), "size": size, "quality": quality, "response_format": "url", "base_url": baseURL, "visibility": visibility}
	if images != nil {
		payload["images"] = images
	}
	if messages != nil {
		payload["messages"] = messages
	}
	mergeImageTaskMetadata(payload, metadata)
	mergeImageOutputOptions(payload, options)
	return s.submit(ctx, identity, clientTaskID, mode, payload)
}

func (s *ImageTaskService) ListTasks(identity Identity, taskIDs []string) map[string]any {
	owner := ownerID(identity)
	requested := make([]string, 0, len(taskIDs))
	for _, id := range taskIDs {
		if id = strings.TrimSpace(id); id != "" {
			requested = append(requested, id)
		}
	}
	s.mu.Lock()
	expired, cancels := s.expireTimedOutRunningTasksLocked(time.Now())
	changed := expired
	if s.cleanupLocked() {
		changed = true
	}
	if changed {
		_ = s.saveLocked()
	}
	items := make([]map[string]any, 0)
	missing := make([]string, 0)
	if len(requested) == 0 {
		for _, task := range s.tasks {
			if task["owner_id"] == owner {
				items = append(items, publicTask(task))
			}
		}
		sort.Slice(items, func(i, j int) bool { return util.Clean(items[i]["updated_at"]) > util.Clean(items[j]["updated_at"]) })
	} else {
		for _, id := range requested {
			task := s.tasks[taskKey(owner, id)]
			if task == nil {
				missing = append(missing, id)
			} else {
				items = append(items, publicTask(task))
			}
		}
	}
	s.mu.Unlock()
	for _, cancel := range cancels {
		cancel()
	}
	return map[string]any{"items": items, "missing_ids": missing}
}

func (s *ImageTaskService) CancelTask(identity Identity, clientTaskID string) (map[string]any, error) {
	taskID := strings.TrimSpace(clientTaskID)
	if taskID == "" {
		return nil, fmt.Errorf("client_task_id is required")
	}
	key := taskKey(ownerID(identity), taskID)
	now := util.NowLocal()
	var cancel context.CancelFunc
	s.mu.Lock()
	task := s.tasks[key]
	if task == nil {
		s.mu.Unlock()
		return nil, fmt.Errorf("creation task not found")
	}
	if isActiveTaskStatus(util.Clean(task["status"])) {
		task["status"] = TaskStatusCancelled
		task["error"] = "任务已终止"
		if task["data"] == nil {
			task["data"] = []any{}
		}
		task["updated_at"] = now
		cancel = s.cancels[key]
		delete(s.cancels, key)
		_ = s.saveLocked()
	}
	result := publicTask(task)
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	return result, nil
}

func (s *ImageTaskService) submit(ctx context.Context, identity Identity, clientTaskID, mode string, payload map[string]any) (map[string]any, error) {
	taskID := strings.TrimSpace(clientTaskID)
	if taskID == "" {
		return nil, fmt.Errorf("client_task_id is required")
	}
	owner := ownerID(identity)
	key := taskKey(owner, taskID)
	now := util.NowLocal()
	s.mu.Lock()
	cleaned := s.cleanupLocked()
	if existing := s.tasks[key]; existing != nil {
		if util.Clean(existing["status"]) == TaskStatusQueued && taskAge(existing, now) >= staleQueuedTaskAge {
			oldCancel := s.cancels[key]
			taskCtx, cancel := context.WithCancel(context.Background())
			runID := newTaskRunID()
			existing["run_id"] = runID
			existing["error"] = ""
			existing["updated_at"] = now
			s.cancels[key] = cancel
			_ = s.saveLocked()
			result := publicTask(existing)
			s.mu.Unlock()
			if oldCancel != nil {
				oldCancel()
			}
			go s.runTask(taskCtx, key, runID, mode, identity, payload)
			return result, nil
		}
		if cleaned {
			_ = s.saveLocked()
		}
		result := publicTask(existing)
		s.mu.Unlock()
		return result, nil
	}
	count := taskCount(mode, payload)
	if err := s.checkUserTaskLimitsLocked(identity, owner, count, time.Now()); err != nil {
		if cleaned {
			_ = s.saveLocked()
		}
		s.mu.Unlock()
		return nil, err
	}
	taskCtx, cancel := context.WithCancel(context.Background())
	runID := newTaskRunID()
	task := map[string]any{"id": taskID, "owner_id": owner, "status": TaskStatusQueued, "mode": mode, "model": firstNonEmpty(util.Clean(payload["model"]), util.ImageModelAuto), "size": util.Clean(payload["size"]), "quality": util.Clean(payload["quality"]), "output_format": NormalizeImageOutputFormat(util.Clean(payload["output_format"])), "visibility": util.Clean(payload["visibility"]), "count": count, "run_id": runID, "created_at": now, "updated_at": now}
	if compression, ok := normalizedImageOutputCompressionValue(payload["output_compression"]); ok {
		task["output_compression"] = compression
	}
	s.tasks[key] = task
	s.cancels[key] = cancel
	_ = s.saveLocked()
	result := publicTask(task)
	s.mu.Unlock()
	go s.runTask(taskCtx, key, runID, mode, identity, payload)
	return result, nil
}

func (s *ImageTaskService) runTask(ctx context.Context, key, runID, mode string, identity Identity, payload map[string]any) {
	defer s.removeTaskCancel(key, runID)
	if isImageTaskMode(mode) {
		slots, ok := s.acquireImageSlots(ctx, imageTaskCount(payload))
		if !ok {
			s.updateActiveTaskForRun(key, runID, map[string]any{"status": TaskStatusCancelled, "error": "任务已终止", "data": []any{}}, true)
			return
		}
		defer s.releaseImageSlots(slots)
	}
	if !s.updateActiveTaskForRun(key, runID, map[string]any{"status": TaskStatusRunning, "error": ""}, false) {
		return
	}
	runCtx, cancel := context.WithTimeout(ctx, s.taskTimeout())
	defer cancel()

	handler := s.generation
	if mode == "edit" {
		handler = s.edit
	} else if mode == "chat" {
		handler = s.chat
	} else if mode == "response-image" {
		handler = s.responseImage
	}
	runPayload := payload
	result, err := handler(runCtx, identity, runPayload)
	if err != nil {
		if util.Clean(result["output_type"]) == "text" {
			if data := textTaskResultData(result); len(data) > 0 {
				s.updateActiveTaskForRun(key, runID, map[string]any{"status": TaskStatusSuccess, "data": data, "error": "", "output_type": "text"}, true)
				return
			}
		}
		status := TaskStatusError
		message := err.Error()
		if ctx.Err() != nil {
			status = TaskStatusCancelled
			message = "任务已终止"
		} else if runCtx.Err() == context.DeadlineExceeded {
			message = "图片生成超过等待上限，请稍后重试或切换账号"
		}
		updates := map[string]any{"status": status, "error": message, "data": taskResultData(result)}
		if outputType := util.Clean(result["output_type"]); outputType != "" {
			updates["output_type"] = outputType
		}
		s.updateActiveTaskForRun(key, runID, updates, true)
		return
	}
	data := util.AsMapSlice(result["data"])
	outputType := util.Clean(result["output_type"])
	if outputType == "text" && len(data) == 0 {
		if text := util.Clean(result["message"]); text != "" {
			data = []map[string]any{{"text_response": text}}
		}
	}
	if len(data) == 0 {
		message := firstNonEmpty(util.Clean(result["message"]), "task returned no output data")
		updates := map[string]any{"status": TaskStatusError, "error": message, "data": []any{}}
		if outputType != "" {
			updates["output_type"] = outputType
		}
		s.updateActiveTaskForRun(key, runID, updates, true)
		return
	}
	updates := map[string]any{"status": TaskStatusSuccess, "data": data, "error": ""}
	if outputType != "" {
		updates["output_type"] = outputType
	}
	s.updateActiveTaskForRun(key, runID, updates, true)
}

func textTaskResultData(result map[string]any) []map[string]any {
	data := util.AsMapSlice(result["data"])
	if len(data) > 0 {
		return data
	}
	if text := util.Clean(result["message"]); text != "" {
		return []map[string]any{{"text_response": text}}
	}
	return nil
}

func (s *ImageTaskService) acquireImageSlots(ctx context.Context, requested int) (int, bool) {
	if requested < 1 {
		requested = 1
	}
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		limit := s.imageConcurrentLimit()
		slots := requested
		if slots > limit {
			slots = limit
		}
		s.mu.Lock()
		if s.runningImages+slots <= limit {
			s.runningImages += slots
			s.mu.Unlock()
			return slots, true
		}
		s.mu.Unlock()
		select {
		case <-ctx.Done():
			return 0, false
		case <-ticker.C:
		}
	}
}

func (s *ImageTaskService) releaseImageSlots(slots int) {
	if slots < 1 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.runningImages -= slots
	if s.runningImages < 0 {
		s.runningImages = 0
	}
}

func (s *ImageTaskService) currentRunningImages() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.runningImages
}

func (s *ImageTaskService) imageConcurrentLimit() int {
	limit := 4
	if s.concurrentLimit != nil {
		limit = s.concurrentLimit()
	}
	if limit < 1 {
		return 1
	}
	return limit
}

func imageTaskMaxWorkersForLoad(active, limit int) int {
	if active < 1 {
		active = 1
	}
	if limit < 1 {
		limit = 1
	}
	light := limit / 4
	if light < 1 {
		light = 1
	}
	medium := limit / 2
	if medium <= light {
		medium = light + 1
	}
	if active <= light {
		return 4
	}
	if active <= medium {
		return 2
	}
	return 2
}

func (s *ImageTaskService) taskTimeout() time.Duration {
	if s.taskTimeoutGetter == nil {
		return defaultImageTaskTimeout
	}
	timeout := s.taskTimeoutGetter()
	if timeout <= 0 {
		return defaultImageTaskTimeout
	}
	return timeout
}

func (s *ImageTaskService) checkUserTaskLimitsLocked(identity Identity, owner string, requested int, now time.Time) error {
	if identity.Role != AuthRoleUser {
		return nil
	}
	if requested < 1 {
		requested = 1
	}
	if limit := s.userImageConcurrentLimit(); limit > 0 && s.activeOwnerTaskCountLocked(owner)+requested > limit {
		return ImageTaskLimitError{Message: fmt.Sprintf("用户并发限制已达到（最多 %d 个任务处理中）", limit)}
	}
	if limit := s.userImageRPMLimit(); limit > 0 {
		cutoff := now.Add(-time.Minute)
		times := s.ownerSubmitTimes[owner]
		kept := times[:0]
		for _, item := range times {
			if item.After(cutoff) {
				kept = append(kept, item)
			}
		}
		if len(kept) >= limit {
			s.ownerSubmitTimes[owner] = kept
			return ImageTaskLimitError{Message: fmt.Sprintf("用户 RPM 速率限制已达到（每分钟最多 %d 次）", limit)}
		}
		s.ownerSubmitTimes[owner] = append(kept, now)
	}
	return nil
}

func (s *ImageTaskService) activeOwnerTaskCountLocked(owner string) int {
	count := 0
	for _, task := range s.tasks {
		if task["owner_id"] != owner || !isActiveTaskStatus(util.Clean(task["status"])) {
			continue
		}
		count += storedTaskCount(task)
	}
	return count
}

func (s *ImageTaskService) userImageConcurrentLimit() int {
	if s.userConcurrentLimit == nil {
		return 0
	}
	limit := s.userConcurrentLimit()
	if limit < 1 {
		return 0
	}
	return limit
}

func (s *ImageTaskService) userImageRPMLimit() int {
	if s.userRPMLimit == nil {
		return 0
	}
	limit := s.userRPMLimit()
	if limit < 1 {
		return 0
	}
	return limit
}

func (s *ImageTaskService) updateActiveTask(key string, updates map[string]any) bool {
	return s.updateActiveTaskForRun(key, "", updates, true)
}

func (s *ImageTaskService) updateActiveTaskTransient(key string, updates map[string]any) bool {
	return s.updateActiveTaskForRun(key, "", updates, false)
}

func (s *ImageTaskService) updateActiveTaskForRun(key, runID string, updates map[string]any, save bool) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	task := s.tasks[key]
	if task == nil {
		return false
	}
	if runID != "" && util.Clean(task["run_id"]) != runID {
		return false
	}
	if !isActiveTaskStatus(util.Clean(task["status"])) {
		return false
	}
	for k, v := range updates {
		task[k] = v
	}
	task["updated_at"] = util.NowLocal()
	if save {
		_ = s.saveLocked()
	}
	return true
}

func (s *ImageTaskService) removeTaskCancel(key, runID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if runID != "" {
		task := s.tasks[key]
		if task != nil && util.Clean(task["run_id"]) != runID {
			return
		}
	}
	delete(s.cancels, key)
}

func (s *ImageTaskService) loadLocked() map[string]map[string]any {
	raw := loadStoredJSON(s.store, s.docName, s.path)
	if obj, ok := raw.(map[string]any); ok {
		raw = obj["tasks"]
	}
	tasks := map[string]map[string]any{}
	for _, item := range anyList(raw) {
		task, ok := item.(map[string]any)
		if !ok {
			continue
		}
		id := util.Clean(task["id"])
		owner := util.Clean(task["owner_id"])
		if id == "" || owner == "" {
			continue
		}
		status := util.Clean(task["status"])
		if status != TaskStatusQueued && status != TaskStatusRunning && status != TaskStatusSuccess && status != TaskStatusError && status != TaskStatusCancelled {
			status = TaskStatusError
		}
		mode := "generate"
		if task["mode"] == "edit" {
			mode = "edit"
		} else if task["mode"] == "response-image" {
			mode = "response-image"
		} else if task["mode"] == "chat" {
			mode = "chat"
		}
		count := taskCount(mode, task)
		visibility, _ := NormalizeImageVisibility(util.Clean(task["visibility"]))
		normalized := map[string]any{"id": id, "owner_id": owner, "status": status, "mode": mode, "model": firstNonEmpty(util.Clean(task["model"]), util.ImageModelAuto), "size": util.Clean(task["size"]), "quality": util.Clean(task["quality"]), "output_format": NormalizeImageOutputFormat(util.Clean(task["output_format"])), "visibility": visibility, "count": count, "created_at": firstNonEmpty(util.Clean(task["created_at"]), util.NowLocal()), "updated_at": firstNonEmpty(util.Clean(task["updated_at"]), util.Clean(task["created_at"]), util.NowLocal())}
		if compression, ok := normalizedImageOutputCompressionValue(task["output_compression"]); ok {
			normalized["output_compression"] = compression
		}
		if data := util.AsMapSlice(task["data"]); data != nil {
			normalized["data"] = data
		}
		if errText := util.Clean(task["error"]); errText != "" {
			normalized["error"] = errText
		}
		if outputType := util.Clean(task["output_type"]); outputType != "" {
			normalized["output_type"] = outputType
		}
		tasks[taskKey(owner, id)] = normalized
	}
	return tasks
}

func (s *ImageTaskService) saveLocked() error {
	items := make([]map[string]any, 0, len(s.tasks))
	for _, task := range s.tasks {
		items = append(items, task)
	}
	sort.Slice(items, func(i, j int) bool { return util.Clean(items[i]["updated_at"]) > util.Clean(items[j]["updated_at"]) })
	value := map[string]any{"tasks": items}
	if s.store != nil {
		return s.store.SaveJSONDocument(s.docName, value)
	}
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, append(data, '\n'), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

func (s *ImageTaskService) recoverUnfinishedLocked() bool {
	changed := false
	for _, task := range s.tasks {
		if task["status"] == TaskStatusQueued || task["status"] == TaskStatusRunning {
			task["status"] = TaskStatusError
			task["error"] = "服务已重启，未完成的任务已中断"
			task["updated_at"] = util.NowLocal()
			changed = true
		}
	}
	return changed
}

func (s *ImageTaskService) expireTimedOutRunningTasksLocked(now time.Time) (bool, []context.CancelFunc) {
	timeout := s.taskTimeout()
	grace := 30 * time.Second
	changed := false
	cancels := make([]context.CancelFunc, 0)
	nowText := util.NowLocal()
	for key, task := range s.tasks {
		if task["status"] != TaskStatusRunning {
			continue
		}
		updatedAt := parseTaskTime(task["updated_at"])
		if updatedAt.IsZero() || now.Sub(updatedAt) < timeout+grace {
			continue
		}
		task["status"] = TaskStatusError
		task["error"] = "图片生成超过等待上限，请稍后重试或切换账号"
		if task["data"] == nil {
			task["data"] = []any{}
		}
		task["updated_at"] = nowText
		if cancel := s.cancels[key]; cancel != nil {
			cancels = append(cancels, cancel)
			delete(s.cancels, key)
		}
		changed = true
	}
	return changed, cancels
}

func (s *ImageTaskService) cleanupLocked() bool {
	days := 30
	if s.retentionGetter != nil {
		days = s.retentionGetter()
	}
	if days < 1 {
		days = 1
	}
	cutoff := time.Now().Add(-time.Duration(days) * 24 * time.Hour)
	removed := false
	for key, task := range s.tasks {
		status := task["status"]
		if status != TaskStatusSuccess && status != TaskStatusError && status != TaskStatusCancelled {
			continue
		}
		if parseTaskTime(task["updated_at"]).Before(cutoff) {
			delete(s.tasks, key)
			removed = true
		}
	}
	return removed
}

func publicTask(task map[string]any) map[string]any {
	item := map[string]any{"id": task["id"], "status": task["status"], "mode": task["mode"], "model": task["model"], "size": task["size"], "created_at": task["created_at"], "updated_at": task["updated_at"]}
	if quality := util.Clean(task["quality"]); quality != "" {
		item["quality"] = quality
	}
	if format := NormalizeImageOutputFormat(util.Clean(task["output_format"])); format != "" {
		item["output_format"] = format
	}
	if compression, ok := normalizedImageOutputCompressionValue(task["output_compression"]); ok {
		item["output_compression"] = compression
	}
	if task["data"] != nil {
		item["data"] = task["data"]
	}
	if util.Clean(task["error"]) != "" {
		item["error"] = task["error"]
	}
	if util.Clean(task["output_type"]) != "" {
		item["output_type"] = task["output_type"]
	}
	if visibility := util.Clean(task["visibility"]); visibility != "" {
		item["visibility"] = visibility
	}
	return item
}

func imageTaskVisibility(values ...string) (string, error) {
	if len(values) == 0 {
		return ImageVisibilityPrivate, nil
	}
	return NormalizeImageVisibility(values[0])
}

func ownerID(identity Identity) string {
	if owner := util.Clean(identity.OwnerID); owner != "" {
		return owner
	}
	if id := util.Clean(identity.ID); id != "" {
		return id
	}
	return "anonymous"
}

func taskKey(owner, id string) string {
	return owner + ":" + id
}

func newTaskRunID() string {
	return util.NewHex(16)
}

func taskAge(task map[string]any, now string) time.Duration {
	updatedAt := parseTaskTime(task["updated_at"])
	nowAt := parseTaskTime(now)
	if updatedAt.IsZero() || nowAt.IsZero() {
		return 0
	}
	age := nowAt.Sub(updatedAt)
	if age < 0 {
		return 0
	}
	return age
}

func normalizedImageTaskCount(n int) int {
	if n < 1 {
		return 1
	}
	if n > 4 {
		return 4
	}
	return n
}

func imageTaskCount(payload map[string]any) int {
	if payload["n"] == nil {
		return normalizedImageTaskCount(util.ToInt(payload["count"], 1))
	}
	return normalizedImageTaskCount(util.ToInt(payload["n"], 1))
}

func taskCount(mode string, payload map[string]any) int {
	if mode == "chat" {
		return 1
	}
	return imageTaskCount(payload)
}

func mergeImageTaskMetadata(payload map[string]any, metadata map[string]any) {
	if len(metadata) == 0 {
		return
	}
	if preset := NormalizeImageResolutionPreset(util.Clean(metadata["image_resolution"])); preset != "" {
		payload["image_resolution"] = preset
	}
	if requestedSize := strings.TrimSpace(util.Clean(metadata["requested_size"])); requestedSize != "" {
		payload["requested_size"] = requestedSize
	}
}

func mergeImageOutputOptions(payload map[string]any, options ImageOutputOptions) {
	format := NormalizeImageOutputFormat(options.Format)
	if format == "" {
		return
	}
	payload["output_format"] = format
	if format == "png" || options.Compression == nil {
		delete(payload, "output_compression")
		return
	}
	compression := *options.Compression
	if compression < 0 {
		compression = 0
	} else if compression > 100 {
		compression = 100
	}
	payload["output_compression"] = compression
}

func NormalizeImageOutputFormat(format string) string {
	switch strings.ToLower(strings.TrimSpace(format)) {
	case "", "png":
		return "png"
	case "jpg", "jpeg":
		return "jpeg"
	case "webp":
		return "webp"
	default:
		return "png"
	}
}

func normalizedImageOutputCompressionValue(value any) (int, bool) {
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

func storedTaskCount(task map[string]any) int {
	return taskCount(util.Clean(task["mode"]), task)
}

func isImageTaskMode(mode string) bool {
	return mode == "generate" || mode == "edit" || mode == "response-image"
}

func taskResultData(result map[string]any) []map[string]any {
	if result == nil {
		return []map[string]any{}
	}
	data := util.AsMapSlice(result["data"])
	if data == nil {
		return []map[string]any{}
	}
	return data
}

func isActiveTaskStatus(status string) bool {
	return status == TaskStatusQueued || status == TaskStatusRunning
}

func parseTaskTime(value any) time.Time {
	text := util.Clean(value)
	for _, layout := range []string{"2006-01-02 15:04:05", "2006-01-02T15:04:05.999999", "2006-01-02T15:04:05"} {
		if t, err := time.ParseInLocation(layout, text, time.Local); err == nil {
			return t
		}
	}
	if t, err := time.Parse(time.RFC3339Nano, text); err == nil {
		return t
	}
	return time.Unix(0, 0)
}
