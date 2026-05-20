package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestImageTaskServiceIdempotencyOwnerIsolationAndCompletion(t *testing.T) {
	path := filepath.Join(t.TempDir(), "image_tasks.json")
	handlerCalls := make(chan map[string]any, 4)
	handler := func(ctx context.Context, identity Identity, payload map[string]any) (map[string]any, error) {
		handlerCalls <- payload
		return map[string]any{"data": []map[string]any{{"url": "https://example.test/image.png"}}}, nil
	}
	svc := NewImageTaskService(path, handler, handler, handler, func() int { return 30 })

	alice := Identity{ID: "alice", Name: "Alice", Role: "user"}
	bob := Identity{ID: "bob", Name: "Bob", Role: "user"}

	first, err := svc.SubmitGeneration(context.Background(), alice, "task-1", "draw", "gpt-image-2", "1024x1024", "high", "https://base.test", 1, nil)
	if err != nil {
		t.Fatalf("SubmitGeneration() error = %v", err)
	}
	second, err := svc.SubmitGeneration(context.Background(), alice, "task-1", "different", "gpt-image-2", "1024x1024", "high", "https://base.test", 1, nil)
	if err != nil {
		t.Fatalf("second SubmitGeneration() error = %v", err)
	}
	if first["id"] != second["id"] {
		t.Fatalf("idempotent task id mismatch: %#v %#v", first, second)
	}
	waitForTaskStatus(t, svc, alice, "task-1", TaskStatusSuccess)
	select {
	case <-handlerCalls:
	default:
		t.Fatal("handler was not called")
	}
	if len(handlerCalls) != 0 {
		t.Fatalf("handler calls after duplicate = %d extra, want 0", len(handlerCalls))
	}
	if got := svc.ListTasks(bob, []string{"task-1"}); len(got["items"].([]map[string]any)) != 0 {
		t.Fatalf("bob can see alice task: %#v", got)
	}
	if got := svc.ListTasks(bob, []string{"task-1"}); len(got["missing_ids"].([]string)) != 1 {
		t.Fatalf("bob missing ids = %#v", got)
	}
}

func TestImageTaskServiceUsesOwnerIDAroundCredentialRotation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "image_tasks.json")
	handlerCalls := make(chan map[string]any, 4)
	handler := func(ctx context.Context, identity Identity, payload map[string]any) (map[string]any, error) {
		handlerCalls <- payload
		return map[string]any{"data": []map[string]any{{"url": "https://example.test/image.png"}}}, nil
	}
	svc := NewImageTaskService(path, handler, handler, handler, func() int { return 30 })
	ownerID := "linuxdo:123"
	oldKey := Identity{ID: ownerID, OwnerID: ownerID, CredentialID: "key-old", Name: "Alice", Role: "user"}
	newKey := Identity{ID: ownerID, OwnerID: ownerID, CredentialID: "key-new", Name: "Alice", Role: "user"}
	otherOwner := Identity{ID: "linuxdo:456", OwnerID: "linuxdo:456", CredentialID: "key-other", Name: "Bob", Role: "user"}

	if _, err := svc.SubmitGeneration(context.Background(), oldKey, "task-1", "draw", "gpt-image-2", "1024x1024", "high", "https://base.test", 1, nil); err != nil {
		t.Fatalf("SubmitGeneration() error = %v", err)
	}
	waitForTaskStatus(t, svc, newKey, "task-1", TaskStatusSuccess)
	if got := svc.ListTasks(newKey, []string{"task-1"}); len(got["items"].([]map[string]any)) != 1 {
		t.Fatalf("rotated credential cannot see owner task: %#v", got)
	}
	if got := svc.ListTasks(otherOwner, []string{"task-1"}); len(got["items"].([]map[string]any)) != 0 || len(got["missing_ids"].([]string)) != 1 {
		t.Fatalf("other owner should not see task: %#v", got)
	}
	if _, err := svc.SubmitGeneration(context.Background(), newKey, "task-1", "different", "gpt-image-2", "1024x1024", "high", "https://base.test", 1, nil); err != nil {
		t.Fatalf("second SubmitGeneration() error = %v", err)
	}
	if len(handlerCalls) != 1 {
		t.Fatalf("credential rotation should not create a duplicate task, handler calls = %d", len(handlerCalls))
	}
}

func TestImageTaskServiceListTasksReturnsEmptyArrays(t *testing.T) {
	path := filepath.Join(t.TempDir(), "image_tasks.json")
	svc := NewImageTaskService(path, failingImageTaskHandler, failingImageTaskHandler, failingImageTaskHandler, func() int { return 30 })
	identity := Identity{ID: "alice", Name: "Alice", Role: "user"}

	for name, got := range map[string]map[string]any{
		"empty list":   svc.ListTasks(identity, nil),
		"missing task": svc.ListTasks(identity, []string{"missing"}),
	} {
		items, ok := got["items"].([]map[string]any)
		if !ok {
			t.Fatalf("%s items type = %T", name, got["items"])
		}
		if items == nil {
			t.Fatalf("%s items is nil", name)
		}
		missing, ok := got["missing_ids"].([]string)
		if !ok {
			t.Fatalf("%s missing_ids type = %T", name, got["missing_ids"])
		}
		if missing == nil {
			t.Fatalf("%s missing_ids is nil", name)
		}

		data, err := json.Marshal(got)
		if err != nil {
			t.Fatalf("%s Marshal() error = %v", name, err)
		}
		text := string(data)
		if strings.Contains(text, `"items":null`) || strings.Contains(text, `"missing_ids":null`) {
			t.Fatalf("%s encoded nil arrays: %s", name, text)
		}
	}
}

func TestImageTaskServiceRejectsBlankPromptBeforeQueueing(t *testing.T) {
	path := filepath.Join(t.TempDir(), "image_tasks.json")
	svc := NewImageTaskService(path, failingImageTaskHandler, failingImageTaskHandler, failingImageTaskHandler, func() int { return 30 })
	identity := Identity{ID: "alice", Name: "Alice", Role: "user"}

	for name, submit := range map[string]func() (map[string]any, error){
		"generation": func() (map[string]any, error) {
			return svc.SubmitGeneration(context.Background(), identity, "task-1", "  ", "gpt-image-2", "1024x1024", "high", "https://base.test", 1, nil)
		},
		"edit": func() (map[string]any, error) {
			return svc.SubmitEdit(context.Background(), identity, "task-2", "\t", "gpt-image-2", "1024x1024", "high", "https://base.test", []any{"image"}, 1, nil)
		},
		"chat": func() (map[string]any, error) {
			return svc.SubmitChat(context.Background(), identity, "task-3", " ", "auto", []map[string]any{{"role": "user", "content": "hello"}})
		},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := submit(); err == nil || err.Error() != "prompt is required" {
				t.Fatalf("Submit() error = %v, want prompt is required", err)
			}
		})
	}

	got := svc.ListTasks(identity, nil)
	if len(got["items"].([]map[string]any)) != 0 {
		t.Fatalf("blank prompt should not queue tasks: %#v", got)
	}
}

func TestImageTaskServicePassesMessagesToHandler(t *testing.T) {
	path := filepath.Join(t.TempDir(), "image_tasks.json")
	handlerCalls := make(chan map[string]any, 1)
	handler := func(ctx context.Context, identity Identity, payload map[string]any) (map[string]any, error) {
		handlerCalls <- payload
		return map[string]any{"data": []map[string]any{{"url": "https://example.test/image.png"}}}, nil
	}
	svc := NewImageTaskService(path, handler, handler, handler, func() int { return 30 })
	identity := Identity{ID: "alice", Name: "Alice", Role: "user"}
	messages := []any{
		map[string]any{"role": "user", "content": "你好，你是什么模型？"},
		map[string]any{"role": "assistant", "content": "我是 GPT-5 Mini。"},
		map[string]any{"role": "user", "content": "我之前说了什么？"},
	}

	if _, err := svc.SubmitGeneration(context.Background(), identity, "task-1", "我之前说了什么？", "auto", "", "high", "https://base.test", 1, messages); err != nil {
		t.Fatalf("SubmitGeneration() error = %v", err)
	}

	var payload map[string]any
	select {
	case payload = <-handlerCalls:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for handler payload")
	}
	if got := payload["messages"]; got == nil {
		t.Fatalf("payload messages missing: %#v", payload)
	}
	if got := payload["prompt"]; got != "我之前说了什么？" {
		t.Fatalf("payload prompt = %#v, want current prompt", got)
	}
	if got := payload["quality"]; got != "high" {
		t.Fatalf("payload quality = %#v, want high", got)
	}
	waitForTaskTerminalStatus(t, svc, identity, "task-1")
}

func TestImageTaskServicePassesImageRequestMetadataToHandler(t *testing.T) {
	path := filepath.Join(t.TempDir(), "image_tasks.json")
	handlerCalls := make(chan map[string]any, 1)
	handler := func(ctx context.Context, identity Identity, payload map[string]any) (map[string]any, error) {
		handlerCalls <- payload
		return map[string]any{"data": []map[string]any{{"url": "https://example.test/image.png"}}}, nil
	}
	svc := NewImageTaskService(path, handler, handler, handler, func() int { return 30 })
	identity := Identity{ID: "alice", Name: "Alice", Role: "user"}

	if _, err := svc.SubmitGenerationWithMetadata(context.Background(), identity, "task-1", "draw", "gpt-image-2", "2048x2048", "high", "https://base.test", 1, nil, map[string]any{"image_resolution": "2k", "requested_size": "2048x2048"}); err != nil {
		t.Fatalf("SubmitGenerationWithMetadata() error = %v", err)
	}

	select {
	case payload := <-handlerCalls:
		if got := payload["image_resolution"]; got != "2k" {
			t.Fatalf("payload image_resolution = %#v, want 2k in %#v", got, payload)
		}
		if got := payload["requested_size"]; got != "2048x2048" {
			t.Fatalf("payload requested_size = %#v, want 2048x2048 in %#v", got, payload)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for handler payload")
	}
	waitForTaskTerminalStatus(t, svc, identity, "task-1")
}

func TestImageTaskServiceSubmitsChatTasks(t *testing.T) {
	path := filepath.Join(t.TempDir(), "image_tasks.json")
	handlerCalls := make(chan map[string]any, 1)
	imageHandler := func(ctx context.Context, identity Identity, payload map[string]any) (map[string]any, error) {
		return map[string]any{"data": []map[string]any{{"url": "https://example.test/image.png"}}}, nil
	}
	chatHandler := func(ctx context.Context, identity Identity, payload map[string]any) (map[string]any, error) {
		handlerCalls <- payload
		return map[string]any{"output_type": "text", "data": []map[string]any{{"text_response": "chat response"}}}, nil
	}
	svc := NewImageTaskService(path, imageHandler, imageHandler, chatHandler, func() int { return 30 }, func() int { return 0 })
	identity := Identity{ID: "alice", Name: "Alice", Role: "user"}
	messages := []map[string]any{{"role": "user", "content": "hello"}}

	if _, err := svc.SubmitChat(context.Background(), identity, "chat-1", "hello", "auto", messages); err != nil {
		t.Fatalf("SubmitChat() error = %v", err)
	}
	waitForTaskStatus(t, svc, identity, "chat-1", TaskStatusSuccess)
	got := svc.ListTasks(identity, []string{"chat-1"})
	item := got["items"].([]map[string]any)[0]
	if item["mode"] != "chat" {
		t.Fatalf("mode = %#v, want chat in %#v", item["mode"], item)
	}
	if item["output_type"] != "text" {
		t.Fatalf("output_type = %#v, want text in %#v", item["output_type"], item)
	}
	data := item["data"].([]map[string]any)
	if len(data) != 1 || data[0]["text_response"] != "chat response" {
		t.Fatalf("text response data = %#v", data)
	}
	select {
	case payload := <-handlerCalls:
		if got := payload["messages"]; got == nil {
			t.Fatalf("chat payload messages missing: %#v", payload)
		}
	default:
		t.Fatal("chat handler was not called")
	}
}

func TestImageTaskServiceSubmitsResponseImageTasks(t *testing.T) {
	path := filepath.Join(t.TempDir(), "image_tasks.json")
	handlerCalls := make(chan map[string]any, 1)
	imageHandler := func(ctx context.Context, identity Identity, payload map[string]any) (map[string]any, error) {
		return map[string]any{"data": []map[string]any{{"url": "https://example.test/image.png"}}}, nil
	}
	responseImageHandler := func(ctx context.Context, identity Identity, payload map[string]any) (map[string]any, error) {
		handlerCalls <- payload
		return map[string]any{"data": []map[string]any{{"url": "https://example.test/response.png"}}}, nil
	}
	svc := NewImageTaskService(path, imageHandler, imageHandler, imageHandler, func() int { return 30 }, func() int { return 0 })
	svc.SetResponseImageHandler(responseImageHandler)
	identity := Identity{ID: "alice", Name: "Alice", Role: "user"}
	messages := []map[string]any{{"role": "user", "content": "生成封面"}}
	images := []any{"data:image/png;base64,cG5n"}

	if _, err := svc.SubmitResponseImageGeneration(context.Background(), identity, "response-1", "生成封面", "gpt-5.5", "16:9", "high", "https://base.test", images, 1, messages); err != nil {
		t.Fatalf("SubmitResponseImageGeneration() error = %v", err)
	}
	waitForTaskStatus(t, svc, identity, "response-1", TaskStatusSuccess)
	got := svc.ListTasks(identity, []string{"response-1"})
	item := got["items"].([]map[string]any)[0]
	if item["mode"] != "response-image" {
		t.Fatalf("mode = %#v, want response-image in %#v", item["mode"], item)
	}
	if item["model"] != "gpt-5.5" || item["quality"] != "high" {
		t.Fatalf("model/quality = %#v/%#v in %#v", item["model"], item["quality"], item)
	}
	select {
	case payload := <-handlerCalls:
		if got := payload["images"]; got == nil {
			t.Fatalf("response image payload missing images: %#v", payload)
		}
		if got := payload["messages"]; got == nil {
			t.Fatalf("response image payload missing messages: %#v", payload)
		}
	default:
		t.Fatal("response image handler was not called")
	}
}

func TestImageTaskServiceLimitsConcurrentImageSlots(t *testing.T) {
	path := filepath.Join(t.TempDir(), "image_tasks.json")
	started := make(chan string, 2)
	release := make(chan struct{})
	handler := func(ctx context.Context, identity Identity, payload map[string]any) (map[string]any, error) {
		started <- payload["prompt"].(string)
		<-release
		return map[string]any{"data": []map[string]any{{"url": "https://example.test/image.png"}}}, nil
	}
	svc := NewImageTaskService(path, handler, handler, handler, func() int { return 30 }, func() int { return 2 })
	identity := Identity{ID: "alice", Name: "Alice", Role: "user"}

	if _, err := svc.SubmitGeneration(context.Background(), identity, "task-1", "first", "gpt-image-2", "1024x1024", "high", "https://base.test", 2, nil); err != nil {
		t.Fatalf("SubmitGeneration(first) error = %v", err)
	}
	if got := waitForStartedTask(t, started); got != "first" {
		t.Fatalf("started task = %q, want first", got)
	}
	if _, err := svc.SubmitGeneration(context.Background(), identity, "task-2", "second", "gpt-image-2", "1024x1024", "high", "https://base.test", 1, nil); err != nil {
		t.Fatalf("SubmitGeneration(second) error = %v", err)
	}

	time.Sleep(350 * time.Millisecond)
	if got := svc.ListTasks(identity, []string{"task-2"}); got["items"].([]map[string]any)[0]["status"] != TaskStatusQueued {
		t.Fatalf("second task should stay queued while first holds image slots: %#v", got)
	}
	select {
	case prompt := <-started:
		t.Fatalf("second task started before slots were released: %s", prompt)
	default:
	}

	close(release)
	if got := waitForStartedTask(t, started); got != "second" {
		t.Fatalf("started task after release = %q, want second", got)
	}
	waitForTaskStatus(t, svc, identity, "task-1", TaskStatusSuccess)
	waitForTaskStatus(t, svc, identity, "task-2", TaskStatusSuccess)
}

func TestImageTaskServiceDoesNotInjectSingleTaskWorkerRacingHint(t *testing.T) {
	path := filepath.Join(t.TempDir(), "image_tasks.json")
	started := make(chan struct {
		prompt     string
		maxWorkers int
	}, 3)
	release := make(chan struct{})
	handler := func(ctx context.Context, identity Identity, payload map[string]any) (map[string]any, error) {
		started <- struct {
			prompt     string
			maxWorkers int
		}{prompt: payload["prompt"].(string), maxWorkers: intValue(payload["max_image_workers"])}
		<-release
		return map[string]any{"data": []map[string]any{{"url": "https://example.test/image.png"}}}, nil
	}
	svc := NewImageTaskService(path, handler, handler, handler, func() int { return 30 }, func() int { return 4 })
	identity := Identity{ID: "alice", Name: "Alice", Role: "user"}

	for _, id := range []string{"task-1", "task-2", "task-3"} {
		if _, err := svc.SubmitGeneration(context.Background(), identity, id, id, "gpt-image-2", "", "medium", "https://base.test", 1, nil); err != nil {
			t.Fatalf("SubmitGeneration(%s) error = %v", id, err)
		}
	}

	first := waitForStartedImageTask(t, started)
	second := waitForStartedImageTask(t, started)
	third := waitForStartedImageTask(t, started)
	got := map[string]int{first.prompt: first.maxWorkers, second.prompt: second.maxWorkers, third.prompt: third.maxWorkers}
	counts := map[int]int{}
	for _, workers := range got {
		counts[workers]++
	}
	if counts[0] != 3 {
		t.Fatalf("worker budgets = %#v, want no max_image_workers racing hints", got)
	}
	close(release)
	waitForTaskStatus(t, svc, identity, "task-1", TaskStatusSuccess)
	waitForTaskStatus(t, svc, identity, "task-2", TaskStatusSuccess)
	waitForTaskStatus(t, svc, identity, "task-3", TaskStatusSuccess)
}

func TestImageTaskServiceLimitsUserDefaultConcurrentImages(t *testing.T) {
	path := filepath.Join(t.TempDir(), "image_tasks.json")
	started := make(chan string, 2)
	release := make(chan struct{})
	handler := func(ctx context.Context, identity Identity, payload map[string]any) (map[string]any, error) {
		started <- payload["prompt"].(string)
		<-release
		return map[string]any{"data": []map[string]any{{"url": "https://example.test/image.png"}}}, nil
	}
	svc := NewImageTaskService(path, handler, handler, handler, func() int { return 30 }, func() int { return 8 }, func() int { return 2 }, nil)
	alice := Identity{ID: "alice", Name: "Alice", Role: AuthRoleUser}
	bob := Identity{ID: "bob", Name: "Bob", Role: AuthRoleUser}

	if _, err := svc.SubmitGeneration(context.Background(), alice, "task-1", "first", "gpt-image-2", "1024x1024", "high", "https://base.test", 2, nil); err != nil {
		t.Fatalf("SubmitGeneration(first) error = %v", err)
	}
	if got := waitForStartedTask(t, started); got != "first" {
		t.Fatalf("started task = %q, want first", got)
	}
	if _, err := svc.SubmitGeneration(context.Background(), alice, "task-2", "second", "gpt-image-2", "1024x1024", "high", "https://base.test", 1, nil); err == nil {
		t.Fatal("SubmitGeneration(second) error = nil, want user limit")
	} else {
		var limitErr ImageTaskLimitError
		if !errors.As(err, &limitErr) {
			t.Fatalf("SubmitGeneration(second) error = %T %v, want ImageTaskLimitError", err, err)
		}
	}
	if _, err := svc.SubmitGeneration(context.Background(), bob, "task-1", "bob", "gpt-image-2", "1024x1024", "high", "https://base.test", 1, nil); err != nil {
		t.Fatalf("SubmitGeneration(bob) should use an independent limit: %v", err)
	}
	close(release)
	waitForTaskStatus(t, svc, alice, "task-1", TaskStatusSuccess)
	waitForTaskStatus(t, svc, bob, "task-1", TaskStatusSuccess)
}

func TestImageTaskServiceLimitsUserDefaultRPM(t *testing.T) {
	path := filepath.Join(t.TempDir(), "image_tasks.json")
	handler := func(ctx context.Context, identity Identity, payload map[string]any) (map[string]any, error) {
		return map[string]any{"data": []map[string]any{{"url": "https://example.test/image.png"}}}, nil
	}
	svc := NewImageTaskService(path, handler, handler, handler, func() int { return 30 }, func() int { return 8 }, nil, func() int { return 1 })
	user := Identity{ID: "alice", Name: "Alice", Role: AuthRoleUser}
	admin := Identity{ID: "admin", Name: "Admin", Role: AuthRoleAdmin}

	if _, err := svc.SubmitGeneration(context.Background(), user, "task-1", "first", "gpt-image-2", "1024x1024", "high", "https://base.test", 1, nil); err != nil {
		t.Fatalf("SubmitGeneration(first) error = %v", err)
	}
	waitForTaskStatus(t, svc, user, "task-1", TaskStatusSuccess)
	if _, err := svc.SubmitGeneration(context.Background(), user, "task-2", "second", "gpt-image-2", "1024x1024", "high", "https://base.test", 1, nil); err == nil {
		t.Fatal("SubmitGeneration(second) error = nil, want RPM limit")
	} else {
		var limitErr ImageTaskLimitError
		if !errors.As(err, &limitErr) {
			t.Fatalf("SubmitGeneration(second) error = %T %v, want ImageTaskLimitError", err, err)
		}
	}
	if _, err := svc.SubmitGeneration(context.Background(), admin, "task-1", "admin first", "gpt-image-2", "1024x1024", "high", "https://base.test", 1, nil); err != nil {
		t.Fatalf("admin should bypass user RPM limit: %v", err)
	}
	if _, err := svc.SubmitGeneration(context.Background(), admin, "task-2", "admin second", "gpt-image-2", "1024x1024", "high", "https://base.test", 1, nil); err != nil {
		t.Fatalf("admin should bypass user RPM limit on second request: %v", err)
	}
	waitForTaskStatus(t, svc, admin, "task-1", TaskStatusSuccess)
	waitForTaskStatus(t, svc, admin, "task-2", TaskStatusSuccess)
}

func TestImageTaskServiceCancelsQueuedTask(t *testing.T) {
	path := filepath.Join(t.TempDir(), "image_tasks.json")
	started := make(chan string, 2)
	release := make(chan struct{})
	handler := func(ctx context.Context, identity Identity, payload map[string]any) (map[string]any, error) {
		started <- payload["prompt"].(string)
		<-release
		return map[string]any{"data": []map[string]any{{"url": "https://example.test/image.png"}}}, nil
	}
	svc := NewImageTaskService(path, handler, handler, handler, func() int { return 30 }, func() int { return 1 })
	identity := Identity{ID: "alice", Name: "Alice", Role: "user"}

	if _, err := svc.SubmitGeneration(context.Background(), identity, "task-1", "first", "gpt-image-2", "1024x1024", "high", "https://base.test", 1, nil); err != nil {
		t.Fatalf("SubmitGeneration(first) error = %v", err)
	}
	if got := waitForStartedTask(t, started); got != "first" {
		t.Fatalf("started task = %q, want first", got)
	}
	if _, err := svc.SubmitGeneration(context.Background(), identity, "task-2", "second", "gpt-image-2", "1024x1024", "high", "https://base.test", 1, nil); err != nil {
		t.Fatalf("SubmitGeneration(second) error = %v", err)
	}

	cancelled, err := svc.CancelTask(identity, "task-2")
	if err != nil {
		t.Fatalf("CancelTask() error = %v", err)
	}
	if cancelled["status"] != TaskStatusCancelled {
		t.Fatalf("cancelled task status = %#v", cancelled)
	}
	close(release)
	waitForTaskStatus(t, svc, identity, "task-1", TaskStatusSuccess)
	waitForTaskStatus(t, svc, identity, "task-2", TaskStatusCancelled)
	select {
	case prompt := <-started:
		t.Fatalf("cancelled queued task still started: %s", prompt)
	default:
	}
}

func TestImageTaskServiceCancelsRunningTask(t *testing.T) {
	path := filepath.Join(t.TempDir(), "image_tasks.json")
	started := make(chan struct{})
	handlerDone := make(chan error, 1)
	handler := func(ctx context.Context, identity Identity, payload map[string]any) (map[string]any, error) {
		close(started)
		<-ctx.Done()
		handlerDone <- ctx.Err()
		return nil, ctx.Err()
	}
	svc := NewImageTaskService(path, handler, handler, handler, func() int { return 30 })
	identity := Identity{ID: "alice", Name: "Alice", Role: "user"}

	if _, err := svc.SubmitGeneration(context.Background(), identity, "task-1", "draw", "gpt-image-2", "1024x1024", "high", "https://base.test", 1, nil); err != nil {
		t.Fatalf("SubmitGeneration() error = %v", err)
	}
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for task handler to start")
	}

	cancelled, err := svc.CancelTask(identity, "task-1")
	if err != nil {
		t.Fatalf("CancelTask() error = %v", err)
	}
	if cancelled["status"] != TaskStatusCancelled {
		t.Fatalf("cancelled task status = %#v", cancelled)
	}
	select {
	case err := <-handlerDone:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("handler ctx err = %v, want context.Canceled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("task handler did not observe cancellation")
	}
	waitForTaskStatus(t, svc, identity, "task-1", TaskStatusCancelled)
}

func TestImageTaskServicePreservesPartialDataOnFailure(t *testing.T) {
	path := filepath.Join(t.TempDir(), "image_tasks.json")
	handler := func(ctx context.Context, identity Identity, payload map[string]any) (map[string]any, error) {
		return map[string]any{"data": []map[string]any{{"url": "https://example.test/first.png"}}}, errors.New("second image failed")
	}
	svc := NewImageTaskService(path, handler, handler, handler, func() int { return 30 })
	identity := Identity{ID: "alice", Name: "Alice", Role: "user"}

	if _, err := svc.SubmitGeneration(context.Background(), identity, "task-1", "draw", "gpt-image-2", "1024x1024", "high", "https://base.test", 2, nil); err != nil {
		t.Fatalf("SubmitGeneration() error = %v", err)
	}
	waitForTaskStatus(t, svc, identity, "task-1", TaskStatusError)
	got := svc.ListTasks(identity, []string{"task-1"})
	item := got["items"].([]map[string]any)[0]
	data := item["data"].([]map[string]any)
	if len(data) != 1 || data[0]["url"] != "https://example.test/first.png" {
		t.Fatalf("partial data was not preserved: %#v", item)
	}
	if item["error"] != "second image failed" {
		t.Fatalf("partial failure error = %#v", item)
	}
}

func TestImageTaskServiceMarksTimedOutTaskAsError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "image_tasks.json")
	handler := func(ctx context.Context, identity Identity, payload map[string]any) (map[string]any, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	svc := NewImageTaskService(path, handler, handler, handler, func() int { return 30 })
	svc.SetTaskTimeoutGetter(func() time.Duration { return 20 * time.Millisecond })
	identity := Identity{ID: "alice", Name: "Alice", Role: "user"}

	if _, err := svc.SubmitGeneration(context.Background(), identity, "task-1", "draw", "gpt-image-2", "1024x1024", "high", "https://base.test", 1, nil); err != nil {
		t.Fatalf("SubmitGeneration() error = %v", err)
	}
	waitForTaskStatus(t, svc, identity, "task-1", TaskStatusError)
	got := svc.ListTasks(identity, []string{"task-1"})
	item := got["items"].([]map[string]any)[0]
	if item["error"] != "图片生成超过等待上限，请稍后重试或切换账号" {
		t.Fatalf("timeout error = %#v", item)
	}
}

func TestImageTaskServiceExpiresStaleRunningTaskOnList(t *testing.T) {
	path := filepath.Join(t.TempDir(), "image_tasks.json")
	svc := NewImageTaskService(path, failingImageTaskHandler, failingImageTaskHandler, failingImageTaskHandler, func() int { return 30 })
	svc.SetTaskTimeoutGetter(func() time.Duration { return 20 * time.Millisecond })
	identity := Identity{ID: "admin", Name: "Admin", Role: AuthRoleAdmin}
	key := taskKey(ownerID(identity), "task-1")
	staleTime := time.Now().Add(-time.Minute).Format("2006-01-02 15:04:05")
	cancelled := make(chan struct{})
	svc.mu.Lock()
	svc.tasks[key] = map[string]any{
		"id":         "task-1",
		"owner_id":   ownerID(identity),
		"status":     TaskStatusRunning,
		"mode":       "generate",
		"model":      "auto",
		"count":      1,
		"created_at": staleTime,
		"updated_at": staleTime,
	}
	svc.cancels[key] = func() { close(cancelled) }
	svc.mu.Unlock()

	got := svc.ListTasks(identity, []string{"task-1"})
	item := got["items"].([]map[string]any)[0]
	if item["status"] != TaskStatusError {
		t.Fatalf("stale running task status = %#v, want error", item)
	}
	if item["error"] != "图片生成超过等待上限，请稍后重试或切换账号" {
		t.Fatalf("stale running task error = %#v", item)
	}
	select {
	case <-cancelled:
	default:
		t.Fatal("stale running task cancel func was not called")
	}
}

func TestImageTaskServiceRestartsStaleQueuedTask(t *testing.T) {
	path := filepath.Join(t.TempDir(), "image_tasks.json")
	handlerCalls := make(chan string, 1)
	handler := func(ctx context.Context, identity Identity, payload map[string]any) (map[string]any, error) {
		handlerCalls <- fmt.Sprint(payload["prompt"])
		return map[string]any{"data": []map[string]any{{"url": "https://example.test/restarted.png"}}}, nil
	}
	svc := NewImageTaskService(path, handler, handler, handler, func() int { return 30 })
	identity := Identity{ID: "admin", Name: "Admin", Role: AuthRoleAdmin}
	staleTime := time.Now().Add(-time.Minute).Format("2006-01-02 15:04:05")
	key := taskKey(ownerID(identity), "task-1")
	svc.mu.Lock()
	svc.tasks[key] = map[string]any{
		"id":         "task-1",
		"owner_id":   ownerID(identity),
		"status":     TaskStatusQueued,
		"mode":       "generate",
		"model":      "auto",
		"count":      1,
		"created_at": staleTime,
		"updated_at": staleTime,
	}
	svc.mu.Unlock()

	task, err := svc.SubmitGeneration(context.Background(), identity, "task-1", "restart stale", "auto", "", "medium", "https://base.test", 1, nil)
	if err != nil {
		t.Fatalf("SubmitGeneration() error = %v", err)
	}
	if task["status"] != TaskStatusQueued {
		t.Fatalf("restarted task initial status = %#v, want queued", task)
	}
	select {
	case got := <-handlerCalls:
		if got != "restart stale" {
			t.Fatalf("handler prompt = %q, want restart stale", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for stale queued task restart")
	}
	waitForTaskStatus(t, svc, identity, "task-1", TaskStatusSuccess)
}

func TestImageTaskServicePreservesTextOutputType(t *testing.T) {
	path := filepath.Join(t.TempDir(), "image_tasks.json")
	handler := func(ctx context.Context, identity Identity, payload map[string]any) (map[string]any, error) {
		return map[string]any{"message": "text response", "output_type": "text"}, nil
	}
	svc := NewImageTaskService(path, handler, handler, handler, func() int { return 30 })
	identity := Identity{ID: "alice", Name: "Alice", Role: "user"}

	if _, err := svc.SubmitGeneration(context.Background(), identity, "task-1", "who are you", "gpt-image-2", "1024x1024", "high", "https://base.test", 1, nil); err != nil {
		t.Fatalf("SubmitGeneration() error = %v", err)
	}
	waitForTaskStatus(t, svc, identity, "task-1", TaskStatusSuccess)
	got := svc.ListTasks(identity, []string{"task-1"})
	item := got["items"].([]map[string]any)[0]
	if item["output_type"] != "text" {
		t.Fatalf("output_type = %#v, want text in %#v", item["output_type"], item)
	}
	data := item["data"].([]map[string]any)
	if len(data) != 1 || data[0]["text_response"] != "text response" {
		t.Fatalf("text response data = %#v", data)
	}
}

func TestImageTaskServiceTreatsTextOutputErrorAsMessage(t *testing.T) {
	path := filepath.Join(t.TempDir(), "image_tasks.json")
	handler := func(ctx context.Context, identity Identity, payload map[string]any) (map[string]any, error) {
		return map[string]any{"message": "text-only image task", "output_type": "text"}, fmt.Errorf("image_generation_text_response")
	}
	svc := NewImageTaskService(path, handler, handler, handler, func() int { return 30 })
	identity := Identity{ID: "alice", Name: "Alice", Role: "user"}

	if _, err := svc.SubmitGeneration(context.Background(), identity, "task-1", "who are you", "gpt-image-2", "1024x1024", "high", "https://base.test", 1, nil); err != nil {
		t.Fatalf("SubmitGeneration() error = %v", err)
	}
	waitForTaskStatus(t, svc, identity, "task-1", TaskStatusSuccess)
	got := svc.ListTasks(identity, []string{"task-1"})
	item := got["items"].([]map[string]any)[0]
	if item["output_type"] != "text" || item["error"] != nil && item["error"] != "" {
		t.Fatalf("task item = %#v, want successful text output", item)
	}
	data := item["data"].([]map[string]any)
	if len(data) != 1 || data[0]["text_response"] != "text-only image task" {
		t.Fatalf("text response data = %#v", data)
	}
}

func TestImageTaskServiceRestoresUnfinishedTasksAsErrors(t *testing.T) {
	path := filepath.Join(t.TempDir(), "image_tasks.json")
	raw := map[string]any{"tasks": []map[string]any{
		{"id": "queued", "owner_id": "alice", "status": TaskStatusQueued, "mode": "generate", "created_at": "2026-01-01 00:00:00", "updated_at": "2026-01-01 00:00:00"},
		{"id": "running", "owner_id": "alice", "status": TaskStatusRunning, "mode": "edit", "created_at": "2026-01-01 00:00:00", "updated_at": "2026-01-01 00:00:00"},
	}}
	data, err := json.Marshal(raw)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	svc := NewImageTaskService(path, failingImageTaskHandler, failingImageTaskHandler, failingImageTaskHandler, func() int { return 30 })
	got := svc.ListTasks(Identity{ID: "alice"}, []string{"queued", "running"})
	items := got["items"].([]map[string]any)
	if len(items) != 2 {
		t.Fatalf("items = %#v", items)
	}
	for _, item := range items {
		if item["status"] != TaskStatusError {
			t.Fatalf("unfinished task was not restored as error: %#v", item)
		}
		if item["error"] == nil {
			t.Fatalf("restored task missing error text: %#v", item)
		}
	}
}

func waitForStartedTask(t *testing.T, started <-chan string) string {
	t.Helper()
	select {
	case prompt := <-started:
		return prompt
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for task handler to start")
	}
	return ""
}

func waitForStartedImageTask(t *testing.T, started <-chan struct {
	prompt     string
	maxWorkers int
}) struct {
	prompt     string
	maxWorkers int
} {
	t.Helper()
	select {
	case item := <-started:
		return item
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for task handler to start")
	}
	return struct {
		prompt     string
		maxWorkers int
	}{}
}

func intValue(value any) int {
	switch v := value.(type) {
	case int:
		return v
	case int64:
		return int(v)
	case float64:
		return int(v)
	default:
		return 0
	}
}

func failingImageTaskHandler(context.Context, Identity, map[string]any) (map[string]any, error) {
	return nil, errors.New("unexpected handler call")
}

func waitForTaskStatus(t *testing.T, svc *ImageTaskService, identity Identity, taskID, want string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		got := svc.ListTasks(identity, []string{taskID})
		items := got["items"].([]map[string]any)
		if len(items) == 1 && items[0]["status"] == want {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("task %s did not reach status %s", taskID, want)
}

func waitForTaskTerminalStatus(t *testing.T, svc *ImageTaskService, identity Identity, taskID string) map[string]any {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		got := svc.ListTasks(identity, []string{taskID})
		items := got["items"].([]map[string]any)
		if len(items) == 1 {
			status := items[0]["status"]
			if status == TaskStatusSuccess || status == TaskStatusError || status == TaskStatusCancelled {
				return items[0]
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("task %s did not reach a terminal status", taskID)
	return nil
}
