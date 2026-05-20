package service

import (
	"path/filepath"
	"sync"

	"chatgpt2api/internal/storage"
	"chatgpt2api/internal/util"
)

type AnnouncementService struct {
	mu      sync.Mutex
	path    string
	store   storage.JSONDocumentBackend
	items   []map[string]any
	docName string
}

func NewAnnouncementService(dataDir string, backend ...storage.Backend) *AnnouncementService {
	s := &AnnouncementService{path: filepath.Join(dataDir, "announcements.json"), store: firstJSONDocumentStore(backend), docName: "announcements.json"}
	s.items = s.load()
	if migrated := migrateAnnouncementsToMarkdown(s.items); migrated {
		_ = s.saveLocked()
	}
	return s
}

// migrateAnnouncementsToMarkdown rewrites any items whose content still uses the
// retired HTML markup into the Markdown subset rendered by AnnouncementMarkdown.
// It mutates the supplied slice in place and reports whether any item changed
// so the caller can persist the result. Plain Markdown items are skipped, which
// makes the migration idempotent across restarts.
func migrateAnnouncementsToMarkdown(items []map[string]any) bool {
	changed := false
	for _, item := range items {
		raw := util.Clean(item["content"])
		if !htmlInAnnouncement(raw) {
			continue
		}
		converted := announcementHTMLToMarkdown(raw)
		if converted == raw {
			continue
		}
		item["content"] = converted
		changed = true
	}
	return changed
}

func (s *AnnouncementService) ListAll() []map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	return copyMaps(s.items)
}

func (s *AnnouncementService) ListVisible(target string) []map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]map[string]any, 0, len(s.items))
	for _, item := range s.items {
		if !isAnnouncementVisible(item, target) {
			continue
		}
		out = append(out, publicAnnouncement(item))
	}
	return out
}

func (s *AnnouncementService) Create(updates map[string]any) map[string]any {
	now := util.NowISO()
	id := util.NewHex(12)
	item := normalizeAnnouncement(mergeMaps(map[string]any{
		"id":         id,
		"enabled":    true,
		"created_at": now,
		"updated_at": now,
	}, updates, map[string]any{
		"id":         id,
		"created_at": now,
		"updated_at": now,
	}))
	s.mu.Lock()
	defer s.mu.Unlock()
	s.items = append(s.items, item)
	_ = s.saveLocked()
	return util.CopyMap(item)
}

func (s *AnnouncementService) Update(id string, updates map[string]any) map[string]any {
	id = util.Clean(id)
	if id == "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for index, item := range s.items {
		if item["id"] != id {
			continue
		}
		next := normalizeAnnouncement(mergeMaps(item, updates, map[string]any{
			"id":         id,
			"created_at": item["created_at"],
			"updated_at": util.NowISO(),
		}))
		s.items[index] = next
		_ = s.saveLocked()
		return util.CopyMap(next)
	}
	return nil
}

func (s *AnnouncementService) Delete(id string) bool {
	id = util.Clean(id)
	if id == "" {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	next := s.items[:0]
	removed := false
	for _, item := range s.items {
		if item["id"] == id {
			removed = true
			continue
		}
		next = append(next, item)
	}
	if removed {
		s.items = next
		_ = s.saveLocked()
	}
	return removed
}

func (s *AnnouncementService) load() []map[string]any {
	raw := loadStoredJSON(s.store, s.docName, s.path)
	items := make([]map[string]any, 0)
	for _, item := range anyList(raw) {
		if itemMap, ok := item.(map[string]any); ok {
			normalized := normalizeAnnouncement(itemMap)
			if util.Clean(normalized["content"]) != "" {
				items = append(items, normalized)
			}
		}
	}
	return items
}

func (s *AnnouncementService) saveLocked() error {
	return saveStoredJSON(s.store, s.docName, s.path, s.items)
}

func normalizeAnnouncement(raw map[string]any) map[string]any {
	now := util.NowISO()
	id := util.Clean(raw["id"])
	if id == "" {
		id = util.NewHex(12)
	}
	createdAt := util.Clean(raw["created_at"])
	if createdAt == "" {
		createdAt = now
	}
	updatedAt := util.Clean(raw["updated_at"])
	if updatedAt == "" {
		updatedAt = createdAt
	}
	title := util.Clean(raw["title"])
	if title == "" {
		title = "公告"
	}
	return map[string]any{
		"id":         id,
		"title":      title,
		"content":    util.Clean(raw["content"]),
		"enabled":    util.ToBool(util.ValueOr(raw["enabled"], true)),
		"show_login": util.ToBool(raw["show_login"]),
		"show_image": util.ToBool(raw["show_image"]),
		"created_at": createdAt,
		"updated_at": updatedAt,
	}
}

func isAnnouncementVisible(item map[string]any, target string) bool {
	if !util.ToBool(util.ValueOr(item["enabled"], true)) || util.Clean(item["content"]) == "" {
		return false
	}
	switch target {
	case "login":
		return util.ToBool(item["show_login"])
	case "image":
		return util.ToBool(item["show_image"])
	default:
		return util.ToBool(item["show_login"]) || util.ToBool(item["show_image"])
	}
}

func publicAnnouncement(item map[string]any) map[string]any {
	return map[string]any{
		"id":         item["id"],
		"title":      item["title"],
		"content":    item["content"],
		"show_login": util.ToBool(item["show_login"]),
		"show_image": util.ToBool(item["show_image"]),
	}
}
