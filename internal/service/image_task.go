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

	imageOutputCallbackPayloadKey     = "image_output_callback"
	imageOutputSlotAcquirerPayloadKey = "image_output_slot_acquirer"
)

type ImageTaskHandler func(context.Context, Identity, map[string]any) (map[string]any, error)

type ImageOutputOptions struct {
	Format      string
	Compression *int
}

type ImageToolOptions struct {
	Background     string
	Moderation     string
	Style          string
	PartialImages  *int
	InputImageMask string
}

type ImageTaskService struct {
	mu                  sync.RWMutex
	path                string
	store               storage.JSONDocumentBackend
	docName             string
	generation          ImageTaskHandler
	edit                ImageTaskHandler
	chat                ImageTaskHandler
	retentionGetter     func() int
	taskTimeoutGetter   func() time.Duration
	userConcurrentLimit func() int
	userRPMLimit        func() int
	tasks               map[string]map[string]any
	cancels             map[string]context.CancelFunc
	ownerSubmitTimes    map[string][]time.Time
	ownerRunningUnits   map[string]int
	creationUnitCond    *sync.Cond
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
	s := &ImageTaskService{path: path, store: store, docName: "image_tasks.json", generation: generation, edit: edit, chat: chat, retentionGetter: retentionGetter, tasks: map[string]map[string]any{}, cancels: map[string]context.CancelFunc{}, ownerSubmitTimes: map[string][]time.Time{}, ownerRunningUnits: map[string]int{}}
	s.creationUnitCond = sync.NewCond(&s.mu)
	if len(limitGetters) > 0 {
		s.userConcurrentLimit = limitGetters[0]
	}
	if len(limitGetters) > 1 {
		s.userRPMLimit = limitGetters[1]
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

func (s *ImageTaskService) SubmitGenerationWithOptions(ctx context.Context, identity Identity, clientTaskID, prompt, model, size, quality, baseURL string, n int, messages any, metadata map[string]any, options ImageOutputOptions, toolOptions ImageToolOptions, visibilityValues ...string) (map[string]any, error) {
	return s.submitImageWithMetadataAndOptions(ctx, identity, clientTaskID, prompt, model, size, quality, baseURL, n, messages, metadata, "generate", nil, options, toolOptions, visibilityValues...)
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

func (s *ImageTaskService) SubmitEditWithOptions(ctx context.Context, identity Identity, clientTaskID, prompt, model, size, quality, baseURL string, images any, n int, messages any, metadata map[string]any, options ImageOutputOptions, toolOptions ImageToolOptions, visibilityValues ...string) (map[string]any, error) {
	return s.submitImageWithMetadataAndOptions(ctx, identity, clientTaskID, prompt, model, size, quality, baseURL, n, messages, metadata, "edit", images, options, toolOptions, visibilityValues...)
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
	return s.submitImageWithMetadataAndOptions(ctx, identity, clientTaskID, prompt, model, size, quality, baseURL, n, messages, metadata, mode, images, ImageOutputOptions{}, ImageToolOptions{}, visibilityValues...)
}

func (s *ImageTaskService) submitImageWithMetadataAndOptions(ctx context.Context, identity Identity, clientTaskID, prompt, model, size, quality, baseURL string, n int, messages any, metadata map[string]any, mode string, images any, options ImageOutputOptions, toolOptions ImageToolOptions, visibilityValues ...string) (map[string]any, error) {
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
	mergeImageToolOptions(payload, toolOptions)
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
	if s.cleanupLocked() {
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
	outputFormat := NormalizeImageOutputFormat(util.Clean(payload["output_format"]))
	task := map[string]any{"id": taskID, "owner_id": owner, "status": TaskStatusQueued, "mode": mode, "model": firstNonEmpty(util.Clean(payload["model"]), util.ImageModelAuto), "size": util.Clean(payload["size"]), "quality": util.Clean(payload["quality"]), "output_format": outputFormat, "visibility": util.Clean(payload["visibility"]), "count": count, "created_at": now, "updated_at": now}
	if mode == "generate" || mode == "edit" {
		task["output_statuses"] = initialImageOutputStatuses(count)
	}
	if SupportsImageOutputCompression(outputFormat) {
		if compression, ok := normalizedImageOutputCompressionValue(payload["output_compression"]); ok {
			task["output_compression"] = compression
		}
	}
	mergePublicImageToolTaskFields(task, payload)
	s.tasks[key] = task
	s.cancels[key] = cancel
	_ = s.saveLocked()
	result := publicTask(task)
	s.mu.Unlock()
	go s.runTask(taskCtx, key, mode, identity, payload)
	return result, nil
}

func (s *ImageTaskService) runTask(ctx context.Context, key, mode string, identity Identity, payload map[string]any) {
	defer s.removeTaskCancel(key)
	runCtx, cancel := context.WithTimeout(ctx, s.taskTimeout())
	defer cancel()

	handler := s.generation
	if mode == "edit" {
		handler = s.edit
	} else if mode == "chat" {
		handler = s.chat
	}
	if mode == "generate" || mode == "edit" {
		payload[imageOutputCallbackPayloadKey] = func(data []map[string]any) {
			if len(data) == 0 {
				return
			}
			s.updateImageTaskPartialData(key, data)
		}
		payload[imageOutputSlotAcquirerPayloadKey] = func(ctx context.Context, index int) (func(), error) {
			release, err := s.AcquireCreationUnit(ctx, identity)
			if err != nil {
				return nil, err
			}
			if !s.ensureTaskRunning(key) {
				release()
				return nil, context.Canceled
			}
			if !s.markImageOutputStatus(key, index, "running") {
				release()
				return nil, context.Canceled
			}
			return release, nil
		}
	} else if mode == "chat" {
		release, err := s.AcquireCreationUnit(runCtx, identity)
		if err != nil {
			status := TaskStatusError
			message := err.Error()
			if ctx.Err() != nil {
				status = TaskStatusCancelled
				message = "任务已终止"
			} else if runCtx.Err() == context.DeadlineExceeded {
				message = "图片生成超时，请稍后重试或降低分辨率"
			}
			s.updateActiveTask(key, map[string]any{"status": status, "error": message, "data": []any{}})
			return
		}
		if !s.ensureTaskRunning(key) {
			release()
			return
		}
		defer release()
	}
	result, err := handler(runCtx, identity, payload)
	if err != nil {
		status := TaskStatusError
		message := err.Error()
		if ctx.Err() != nil {
			status = TaskStatusCancelled
			message = "任务已终止"
		} else if runCtx.Err() == context.DeadlineExceeded {
			message = "图片生成超时，请稍后重试或降低分辨率"
		}
		updates := map[string]any{"status": status, "error": message, "data": taskResultData(result)}
		if mode == "generate" || mode == "edit" {
			updates["output_statuses"] = failedImageOutputStatuses(taskCount(mode, payload), result)
		}
		if outputType := util.Clean(result["output_type"]); outputType != "" {
			updates["output_type"] = outputType
		}
		s.updateActiveTask(key, updates)
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
		s.updateActiveTask(key, updates)
		return
	}
	updates := map[string]any{"status": TaskStatusSuccess, "data": data, "error": ""}
	if mode == "generate" || mode == "edit" {
		statuses := initialImageOutputStatuses(taskCount(mode, payload))
		for index, item := range data {
			if index >= len(statuses) {
				break
			}
			if hasImageTaskOutputData(item) {
				statuses[index] = "success"
			}
		}
		updates["output_statuses"] = statuses
	}
	if outputType != "" {
		updates["output_type"] = outputType
	}
	s.updateActiveTask(key, updates)
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

func (s *ImageTaskService) checkUserTaskLimitsLocked(identity Identity, owner string, _ int, now time.Time) error {
	if identity.Role != AuthRoleUser {
		return nil
	}
	if limit := s.userRPMLimitValue(); limit > 0 {
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

func (s *ImageTaskService) userConcurrentLimitValue() int {
	if s.userConcurrentLimit == nil {
		return 0
	}
	limit := s.userConcurrentLimit()
	if limit < 1 {
		return 0
	}
	return limit
}

func (s *ImageTaskService) AcquireCreationUnit(ctx context.Context, identity Identity) (func(), error) {
	if identity.Role != AuthRoleUser {
		return noopCreationUnitRelease, nil
	}
	owner := ownerID(identity)
	s.mu.Lock()
	defer s.mu.Unlock()
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		limit := s.userConcurrentLimitValue()
		if limit <= 0 || s.ownerRunningUnits[owner] < limit {
			s.ownerRunningUnits[owner]++
			released := false
			return func() {
				s.mu.Lock()
				defer s.mu.Unlock()
				if released {
					return
				}
				released = true
				if s.ownerRunningUnits[owner] <= 1 {
					delete(s.ownerRunningUnits, owner)
				} else {
					s.ownerRunningUnits[owner]--
				}
				s.creationUnitCond.Broadcast()
			}, nil
		}
		timer := time.AfterFunc(100*time.Millisecond, func() {
			s.mu.Lock()
			s.creationUnitCond.Broadcast()
			s.mu.Unlock()
		})
		s.creationUnitCond.Wait()
		timer.Stop()
	}
}

func noopCreationUnitRelease() {}

func (s *ImageTaskService) ensureTaskRunning(key string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	task := s.tasks[key]
	if task == nil {
		return false
	}
	status := util.Clean(task["status"])
	if status == TaskStatusRunning {
		return true
	}
	if status != TaskStatusQueued {
		return false
	}
	task["status"] = TaskStatusRunning
	task["error"] = ""
	task["updated_at"] = util.NowLocal()
	_ = s.saveLocked()
	return true
}

func (s *ImageTaskService) markImageOutputStatus(key string, index int, status string) bool {
	if index < 1 {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	task := s.tasks[key]
	if task == nil || !isActiveTaskStatus(util.Clean(task["status"])) {
		return false
	}
	count := storedImageOutputCount(task)
	if index > count {
		return false
	}
	statuses := normalizedImageOutputStatuses(util.Clean(task["mode"]), count, task["output_statuses"])
	if len(statuses) == 0 {
		return true
	}
	if statuses[index-1] == "success" {
		return true
	}
	statuses[index-1] = status
	task["output_statuses"] = statuses
	task["updated_at"] = util.NowLocal()
	_ = s.saveLocked()
	return true
}

func (s *ImageTaskService) userRPMLimitValue() int {
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
	s.mu.Lock()
	defer s.mu.Unlock()
	task := s.tasks[key]
	if task == nil {
		return false
	}
	if !isActiveTaskStatus(util.Clean(task["status"])) {
		return false
	}
	for k, v := range updates {
		task[k] = v
	}
	task["updated_at"] = util.NowLocal()
	_ = s.saveLocked()
	return true
}

func (s *ImageTaskService) updateImageTaskPartialData(key string, data []map[string]any) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	task := s.tasks[key]
	if task == nil || !isActiveTaskStatus(util.Clean(task["status"])) {
		return false
	}
	count := storedImageOutputCount(task)
	statuses := normalizedImageOutputStatuses(util.Clean(task["mode"]), count, task["output_statuses"])
	for index, item := range data {
		if index >= len(statuses) {
			break
		}
		if hasImageTaskOutputData(item) {
			statuses[index] = "success"
		}
	}
	task["data"] = cloneTaskData(data)
	if len(statuses) > 0 {
		task["output_statuses"] = statuses
	}
	task["updated_at"] = util.NowLocal()
	_ = s.saveLocked()
	return true
}

func (s *ImageTaskService) removeTaskCancel(key string) {
	s.mu.Lock()
	defer s.mu.Unlock()
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
		} else if task["mode"] == "chat" {
			mode = "chat"
		}
		count := taskCount(mode, task)
		visibility, _ := NormalizeImageVisibility(util.Clean(task["visibility"]))
		outputFormat := NormalizeImageOutputFormat(util.Clean(task["output_format"]))
		normalized := map[string]any{"id": id, "owner_id": owner, "status": status, "mode": mode, "model": firstNonEmpty(util.Clean(task["model"]), util.ImageModelAuto), "size": util.Clean(task["size"]), "quality": util.Clean(task["quality"]), "output_format": outputFormat, "visibility": visibility, "count": count, "created_at": firstNonEmpty(util.Clean(task["created_at"]), util.NowLocal()), "updated_at": firstNonEmpty(util.Clean(task["updated_at"]), util.Clean(task["created_at"]), util.NowLocal())}
		if SupportsImageOutputCompression(outputFormat) {
			if compression, ok := normalizedImageOutputCompressionValue(task["output_compression"]); ok {
				normalized["output_compression"] = compression
			}
		}
	if data := util.AsMapSlice(task["data"]); data != nil {
		normalized["data"] = data
	}
	if statuses := normalizedImageOutputStatuses(mode, count, task["output_statuses"]); len(statuses) > 0 {
		normalized["output_statuses"] = statuses
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
	if SupportsImageOutputCompression(util.Clean(item["output_format"])) {
		if compression, ok := normalizedImageOutputCompressionValue(task["output_compression"]); ok {
			item["output_compression"] = compression
		}
	}
	mergePublicImageToolTaskFields(item, task)
	if statuses := util.AsStringSlice(task["output_statuses"]); len(statuses) > 0 {
		item["output_statuses"] = append([]string(nil), statuses...)
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

func storedImageOutputCount(task map[string]any) int {
	return imageTaskCount(task)
}

func initialImageOutputStatuses(count int) []string {
	if count < 1 {
		count = 1
	}
	statuses := make([]string, count)
	for index := range statuses {
		statuses[index] = "queued"
	}
	return statuses
}

func failedImageOutputStatuses(count int, result map[string]any) []string {
	statuses := initialImageOutputStatuses(count)
	for index := range statuses {
		statuses[index] = "error"
	}
	for index, item := range util.AsMapSlice(result["data"]) {
		if index >= len(statuses) {
			break
		}
		if hasImageTaskOutputData(item) {
			statuses[index] = "success"
		}
	}
	return statuses
}

func normalizedImageOutputStatuses(mode string, count int, value any) []string {
	if mode != "generate" && mode != "edit" {
		return nil
	}
	if count < 1 {
		count = 1
	}
	source := util.AsStringSlice(value)
	statuses := make([]string, count)
	for index := range statuses {
		status := "queued"
		if index < len(source) {
			switch source[index] {
			case "queued", "running", "success", "error":
				status = source[index]
			}
		}
		statuses[index] = status
	}
	return statuses
}

func hasImageTaskOutputData(item map[string]any) bool {
	if item == nil {
		return false
	}
	return util.Clean(item["b64_json"]) != "" || util.Clean(item["url"]) != ""
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
	if !SupportsImageOutputCompression(format) || options.Compression == nil {
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

func mergeImageToolOptions(payload map[string]any, options ImageToolOptions) {
	for key, value := range map[string]string{
		"background":       options.Background,
		"moderation":       options.Moderation,
		"style":            options.Style,
		"input_image_mask": options.InputImageMask,
	} {
		if strings.TrimSpace(value) != "" {
			payload[key] = strings.TrimSpace(value)
		}
	}
	if options.PartialImages != nil && *options.PartialImages > 0 {
		payload["partial_images"] = *options.PartialImages
	}
}

func mergePublicImageToolTaskFields(target, source map[string]any) {
	for _, key := range []string{"background", "moderation", "style", "input_image_mask"} {
		if value := util.Clean(source[key]); value != "" {
			target[key] = value
		}
	}
	if value := util.ToInt(source["partial_images"], 0); value > 0 {
		target["partial_images"] = value
	}
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

func SupportsImageOutputCompression(format string) bool {
	return NormalizeImageOutputFormat(format) == "jpeg"
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

func taskResultData(result map[string]any) []map[string]any {
	if result == nil {
		return []map[string]any{}
	}
	data := util.AsMapSlice(result["data"])
	if data == nil {
		return []map[string]any{}
	}
	return cloneTaskData(data)
}

func cloneTaskData(items []map[string]any) []map[string]any {
	if len(items) == 0 {
		return []map[string]any{}
	}
	out := make([]map[string]any, 0, len(items))
	for _, item := range items {
		if item == nil {
			out = append(out, map[string]any{})
			continue
		}
		out = append(out, util.CopyMap(item))
	}
	return out
}

func isActiveTaskStatus(status string) bool {
	return status == TaskStatusQueued || status == TaskStatusRunning
}

func parseTaskTime(value any) time.Time {
	text := util.Clean(value)
	for _, layout := range []string{"2006-01-02 15:04:05", "2006-01-02T15:04:05.999999", "2006-01-02T15:04:05", time.RFC3339Nano} {
		if t, err := time.Parse(layout, text); err == nil {
			return t
		}
	}
	return time.Unix(0, 0)
}
