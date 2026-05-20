package service

import (
	"path/filepath"
	"sort"
	"sync"

	"chatgpt2api/internal/storage"
	"chatgpt2api/internal/util"
)

const (
	notificationsDocumentName = "notifications.json"
	notificationListLimit     = 200
	notificationKeepLimit     = 1000
)

// NotificationCategory enumerates the well-known notification kinds the rest of the system
// emits. Callers may pass any string value; the consts exist for type-safe usage in Go code.
const (
	NotificationCategoryRegistrationBonus    = "registration_bonus"
	NotificationCategoryRegistrationBackfill = "registration_bonus_backfill"
	NotificationCategoryInviteReward         = "invite_reward"
	NotificationCategoryInviteeBonus         = "invitee_bonus"
	NotificationCategoryCheckinReward        = "checkin_reward"
	NotificationCategoryAdminQuotaAdjustment = "admin_quota_adjustment"
	NotificationCategorySystem               = "system"
	NotificationCategoryShareVisit           = "share_visit"
)

// NotificationService persists per-user in-app notifications backed by notifications.json.
// Broadcasts are fanned out to every owner ID known to the profile service so unread state
// is tracked individually. The store keeps the most recent notificationKeepLimit entries
// per owner to bound on-disk size.
type NotificationService struct {
	mu       sync.Mutex
	store    storage.JSONDocumentBackend
	fallback string
	items    map[string][]map[string]any
}

// NewNotificationService constructs a service that persists into notifications.json. The
// service tolerates legacy or empty data: malformed records are dropped silently.
func NewNotificationService(dataDir string, backend storage.Backend) *NotificationService {
	s := &NotificationService{
		store:    jsonDocumentStoreFromBackend(backend),
		fallback: filepath.Join(dataDir, notificationsDocumentName),
		items:    map[string][]map[string]any{},
	}
	s.items = normalizeNotifications(loadStoredJSON(s.store, notificationsDocumentName, s.fallback))
	return s
}

// Push records a notification for a single owner. Returns the persisted public payload so
// callers can echo it back when useful (e.g. emit via SSE later).
func (s *NotificationService) Push(ownerID, category, title, body string, meta map[string]any) map[string]any {
	ownerID = util.Clean(ownerID)
	if ownerID == "" {
		return nil
	}
	item := buildNotification(ownerID, category, title, body, meta)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.items[ownerID] = trimNotificationsLocked(append([]map[string]any{item}, s.items[ownerID]...))
	_ = s.saveLocked()
	return util.CopyMap(item)
}

// Broadcast fans a notification out to every owner id supplied. Pair with
// UserProfileService.ListAllOwnerIDs to reach every known user. The target list may be empty
// (no-op).
func (s *NotificationService) Broadcast(ownerIDs []string, category, title, body string, meta map[string]any) int {
	count := 0
	for _, ownerID := range ownerIDs {
		if util.Clean(ownerID) == "" {
			continue
		}
		if s.Push(ownerID, category, title, body, meta) != nil {
			count++
		}
	}
	return count
}

// ListForOwner returns the most recent notifications for the supplied owner sorted by
// created_at descending. limit defaults to notificationListLimit when out of range.
func (s *NotificationService) ListForOwner(ownerID string, limit int) []map[string]any {
	ownerID = util.Clean(ownerID)
	if ownerID == "" {
		return nil
	}
	if limit <= 0 || limit > notificationListLimit {
		limit = notificationListLimit
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	items := s.items[ownerID]
	if len(items) == 0 {
		return []map[string]any{}
	}
	out := make([]map[string]any, 0, minInt(limit, len(items)))
	for _, item := range items {
		if len(out) >= limit {
			break
		}
		out = append(out, util.CopyMap(item))
	}
	return out
}

// UnreadCountForOwner is a fast accessor used by writeLoginResponse to surface an unread
// badge without fetching the full list.
func (s *NotificationService) UnreadCountForOwner(ownerID string) int {
	ownerID = util.Clean(ownerID)
	if ownerID == "" {
		return 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	count := 0
	for _, item := range s.items[ownerID] {
		if util.Clean(item["read_at"]) == "" {
			count++
		}
	}
	return count
}

// MarkRead toggles the read_at timestamp on a single notification. Returns the resulting
// public payload, or nil if not found.
func (s *NotificationService) MarkRead(ownerID, notificationID string) map[string]any {
	ownerID = util.Clean(ownerID)
	notificationID = util.Clean(notificationID)
	if ownerID == "" || notificationID == "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	items := s.items[ownerID]
	for index, item := range items {
		if util.Clean(item["id"]) != notificationID {
			continue
		}
		if util.Clean(item["read_at"]) == "" {
			item["read_at"] = util.NowISO()
			items[index] = item
			s.items[ownerID] = items
			_ = s.saveLocked()
		}
		return util.CopyMap(item)
	}
	return nil
}

// MarkAllRead stamps every unread notification for the owner with the current timestamp.
// Returns the count of notifications that transitioned to read.
func (s *NotificationService) MarkAllRead(ownerID string) int {
	ownerID = util.Clean(ownerID)
	if ownerID == "" {
		return 0
	}
	now := util.NowISO()
	s.mu.Lock()
	defer s.mu.Unlock()
	items := s.items[ownerID]
	count := 0
	for index, item := range items {
		if util.Clean(item["read_at"]) != "" {
			continue
		}
		item["read_at"] = now
		items[index] = item
		count++
	}
	if count > 0 {
		s.items[ownerID] = items
		_ = s.saveLocked()
	}
	return count
}

// Delete removes a notification from a specific owner's feed. Useful for admin or future
// dismiss UI flows. Returns true if a record was removed.
func (s *NotificationService) Delete(ownerID, notificationID string) bool {
	ownerID = util.Clean(ownerID)
	notificationID = util.Clean(notificationID)
	if ownerID == "" || notificationID == "" {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	items := s.items[ownerID]
	next := items[:0]
	removed := false
	for _, item := range items {
		if util.Clean(item["id"]) == notificationID {
			removed = true
			continue
		}
		next = append(next, item)
	}
	if removed {
		s.items[ownerID] = next
		_ = s.saveLocked()
	}
	return removed
}

func (s *NotificationService) saveLocked() error {
	flat := make([]map[string]any, 0)
	for _, list := range s.items {
		flat = append(flat, list...)
	}
	sort.Slice(flat, func(i, j int) bool {
		return util.Clean(flat[i]["created_at"]) > util.Clean(flat[j]["created_at"])
	})
	return saveStoredJSON(s.store, notificationsDocumentName, s.fallback, map[string]any{"items": flat})
}

func buildNotification(ownerID, category, title, body string, meta map[string]any) map[string]any {
	now := util.NowISO()
	id := util.NewHex(12)
	if util.Clean(category) == "" {
		category = NotificationCategorySystem
	}
	clean := func(value string) string { return util.Clean(value) }
	item := map[string]any{
		"id":         id,
		"owner_id":   clean(ownerID),
		"category":   clean(category),
		"title":      clean(title),
		"body":       clean(body),
		"meta":       util.CopyMap(meta),
		"created_at": now,
		"read_at":    "",
	}
	return item
}

func trimNotificationsLocked(items []map[string]any) []map[string]any {
	if len(items) <= notificationKeepLimit {
		return items
	}
	return items[:notificationKeepLimit]
}

func normalizeNotifications(raw any) map[string][]map[string]any {
	out := map[string][]map[string]any{}
	if raw == nil {
		return out
	}
	pull := func(items []any) {
		for _, item := range items {
			m, ok := item.(map[string]any)
			if !ok {
				continue
			}
			ownerID := util.Clean(m["owner_id"])
			if ownerID == "" {
				continue
			}
			id := util.Clean(m["id"])
			if id == "" {
				id = util.NewHex(12)
				m["id"] = id
			}
			if _, ok := m["meta"].(map[string]any); !ok {
				m["meta"] = map[string]any{}
			}
			out[ownerID] = append(out[ownerID], m)
		}
	}
	switch v := raw.(type) {
	case map[string]any:
		if items, ok := v["items"].([]any); ok {
			pull(items)
		}
	case []any:
		pull(v)
	}
	for ownerID, items := range out {
		sort.Slice(items, func(i, j int) bool {
			return util.Clean(items[i]["created_at"]) > util.Clean(items[j]["created_at"])
		})
		out[ownerID] = trimNotificationsLocked(items)
	}
	return out
}
