package service

import (
	"fmt"
	"path/filepath"
	"sort"
	"sync"

	"chatgpt2api/internal/storage"
	"chatgpt2api/internal/util"
)

const promptFavoritesDocumentDir = "prompt_favorites"

type PromptFavoriteService struct {
	mu      sync.Mutex
	dataDir string
	store   storage.JSONDocumentBackend
}

func NewPromptFavoriteService(dataDir string, backend ...storage.Backend) *PromptFavoriteService {
	return &PromptFavoriteService{dataDir: dataDir, store: firstJSONDocumentStore(backend)}
}

func (s *PromptFavoriteService) List(ownerID string) []map[string]any {
	ownerID = util.Clean(ownerID)
	if ownerID == "" {
		return []map[string]any{}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return copyMaps(s.loadLocked(ownerID))
}

func (s *PromptFavoriteService) Upsert(ownerID string, body map[string]any) (map[string]any, error) {
	ownerID = util.Clean(ownerID)
	if ownerID == "" {
		return nil, fmt.Errorf("owner_id is required")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	items := s.loadLocked(ownerID)
	now := util.NowISO()
	existingIndex := -1
	existingFavoritedAt := ""
	for index, item := range items {
		if util.Clean(item["source"]) != util.Clean(body["source"]) || util.Clean(item["prompt_id"]) != util.Clean(body["prompt_id"]) {
			continue
		}
		existingIndex = index
		existingFavoritedAt = util.Clean(item["favorited_at"])
		break
	}

	item, err := normalizePromptFavoriteInput(body, now, existingFavoritedAt)
	if err != nil {
		return nil, err
	}
	if existingIndex >= 0 {
		items[existingIndex] = item
	} else {
		items = append(items, item)
	}
	sortPromptFavorites(items)
	if err := s.saveLocked(ownerID, items); err != nil {
		return nil, err
	}
	return util.CopyMap(item), nil
}

func (s *PromptFavoriteService) Delete(ownerID, id string) bool {
	ownerID = util.Clean(ownerID)
	id = util.Clean(id)
	if ownerID == "" || id == "" {
		return false
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	items := s.loadLocked(ownerID)
	next := items[:0]
	removed := false
	for _, item := range items {
		if util.Clean(item["id"]) == id {
			removed = true
			continue
		}
		next = append(next, item)
	}
	if !removed {
		return false
	}
	_ = s.saveLocked(ownerID, next)
	return true
}

func (s *PromptFavoriteService) loadLocked(ownerID string) []map[string]any {
	name := promptFavoriteDocumentName(ownerID)
	path := filepath.Join(s.dataDir, promptFavoritesDocumentDir, util.SHA256Hex(ownerID)+".json")
	raw := loadStoredJSON(s.store, name, path)
	items := make([]map[string]any, 0)
	for _, item := range util.AsMapSlice(util.StringMap(raw)["items"]) {
		if normalized := normalizeStoredPromptFavorite(item); normalized != nil {
			items = append(items, normalized)
		}
	}
	sortPromptFavorites(items)
	return items
}

func (s *PromptFavoriteService) saveLocked(ownerID string, items []map[string]any) error {
	name := promptFavoriteDocumentName(ownerID)
	path := filepath.Join(s.dataDir, promptFavoritesDocumentDir, util.SHA256Hex(ownerID)+".json")
	return saveStoredJSON(s.store, name, path, map[string]any{"items": items})
}

func promptFavoriteDocumentName(ownerID string) string {
	return promptFavoritesDocumentDir + "/" + util.SHA256Hex(ownerID) + ".json"
}

func normalizePromptFavoriteInput(body map[string]any, now, existingFavoritedAt string) (map[string]any, error) {
	promptID := util.Clean(body["prompt_id"])
	if promptID == "" {
		return nil, fmt.Errorf("prompt_id is required")
	}
	source := normalizePromptFavoriteSource(util.Clean(body["source"]))
	if source == "" {
		return nil, fmt.Errorf("source is required")
	}
	title := util.Clean(body["title"])
	if title == "" {
		return nil, fmt.Errorf("title is required")
	}
	prompt := util.Clean(body["prompt"])
	if prompt == "" {
		return nil, fmt.Errorf("prompt is required")
	}
	preview := util.Clean(body["preview"])
	if preview == "" {
		return nil, fmt.Errorf("preview is required")
	}
	author := util.Clean(body["author"])
	if author == "" {
		return nil, fmt.Errorf("author is required")
	}
	category := util.Clean(body["category"])
	if category == "" {
		category = "未分类"
	}
	mode := normalizePromptFavoriteMode(util.Clean(body["mode"]))
	sourceLabel := util.Clean(body["source_label"])
	if sourceLabel == "" {
		sourceLabel = source
	}
	favoritedAt := existingFavoritedAt
	if favoritedAt == "" {
		favoritedAt = now
	}

	item := map[string]any{
		"id":                   promptFavoriteID(source, promptID),
		"prompt_id":            promptID,
		"source":               source,
		"title":                title,
		"preview":              preview,
		"reference_image_urls": normalizePromptFavoriteStringList(body["reference_image_urls"]),
		"prompt":               prompt,
		"author":               author,
		"mode":                 mode,
		"category":             category,
		"source_label":         sourceLabel,
		"is_nsfw":              util.ToBool(body["is_nsfw"]),
		"favorited_at":         favoritedAt,
		"updated_at":           now,
	}
	if link := util.Clean(body["link"]); link != "" {
		item["link"] = link
	}
	if subCategory := util.Clean(body["sub_category"]); subCategory != "" {
		item["sub_category"] = subCategory
	}
	if created := util.Clean(body["created"]); created != "" {
		item["created"] = created
	}
	if localizations := normalizePromptFavoriteLocalizations(body["localizations"]); len(localizations) > 0 {
		item["localizations"] = localizations
	}
	return item, nil
}

func normalizeStoredPromptFavorite(raw map[string]any) map[string]any {
	item, err := normalizePromptFavoriteInput(raw, firstNonEmpty(util.Clean(raw["updated_at"]), util.NowISO()), util.Clean(raw["favorited_at"]))
	if err != nil {
		return nil
	}
	item["id"] = firstNonEmpty(util.Clean(raw["id"]), util.Clean(item["id"]))
	item["favorited_at"] = firstNonEmpty(util.Clean(raw["favorited_at"]), util.Clean(item["favorited_at"]))
	item["updated_at"] = firstNonEmpty(util.Clean(raw["updated_at"]), util.Clean(item["updated_at"]))
	return item
}

func promptFavoriteID(source, promptID string) string {
	return "pf_" + util.SHA256Hex(source + "\n" + promptID)[:24]
}

func normalizePromptFavoriteSource(source string) string {
	switch source {
	case "public-image-gallery", "banana-prompt-quicker", "awesome-gpt-image-2-prompts":
		return source
	default:
		return ""
	}
}

func normalizePromptFavoriteMode(mode string) string {
	if mode == "edit" {
		return "edit"
	}
	return "generate"
}

func normalizePromptFavoriteStringList(value any) []string {
	items := util.AsStringSlice(value)
	out := make([]string, 0, len(items))
	seen := map[string]struct{}{}
	for _, item := range items {
		cleaned := util.Clean(item)
		if cleaned == "" {
			continue
		}
		if _, ok := seen[cleaned]; ok {
			continue
		}
		seen[cleaned] = struct{}{}
		out = append(out, cleaned)
	}
	return out
}

func normalizePromptFavoriteLocalizations(value any) map[string]any {
	raw, ok := value.(map[string]any)
	if !ok {
		return nil
	}
	out := map[string]any{}
	for _, language := range []string{"zh-CN", "en"} {
		item, ok := raw[language].(map[string]any)
		if !ok {
			continue
		}
		title := util.Clean(item["title"])
		prompt := util.Clean(item["prompt"])
		category := util.Clean(item["category"])
		if title == "" || prompt == "" || category == "" {
			continue
		}
		normalized := map[string]any{
			"title":    title,
			"prompt":   prompt,
			"category": category,
		}
		if subCategory := util.Clean(item["sub_category"]); subCategory != "" {
			normalized["sub_category"] = subCategory
		}
		out[language] = normalized
	}
	return out
}

func sortPromptFavorites(items []map[string]any) {
	sort.SliceStable(items, func(i, j int) bool {
		return util.Clean(items[i]["favorited_at"]) > util.Clean(items[j]["favorited_at"])
	})
}
