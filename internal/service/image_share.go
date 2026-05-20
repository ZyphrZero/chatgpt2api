package service

import (
	"bytes"
	"crypto/md5"
	"encoding/base64"
	"errors"
	"fmt"
	"image"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"chatgpt2api/internal/storage"
	"chatgpt2api/internal/util"

	_ "github.com/HugoSmits86/nativewebp"
)

const imageSharesDocumentName = "image_shares.json"

const maxShareImageBytes = 15 << 20

type ImageShareService struct {
	mu       sync.Mutex
	store    storage.JSONDocumentBackend
	fallback string
	imageDir string
	items    map[string]map[string]any
}

func NewImageShareService(dataDir string, backend storage.Backend) *ImageShareService {
	s := &ImageShareService{
		store:    jsonDocumentStoreFromBackend(backend),
		fallback: filepath.Join(dataDir, "image_shares.json"),
		imageDir: filepath.Join(dataDir, "share_images"),
		items:    map[string]map[string]any{},
	}
	_ = os.MkdirAll(s.imageDir, 0o755)
	s.items = normalizeImageShares(loadStoredJSON(s.store, imageSharesDocumentName, s.fallback))
	return s
}

func (s *ImageShareService) ImageDir() string {
	_ = os.MkdirAll(s.imageDir, 0o755)
	return s.imageDir
}

func (s *ImageShareService) Create(identity Identity, body map[string]any, baseURL, sourceImageRoot string) (map[string]any, error) {
	imageData, err := readShareImageData(util.Clean(body["image"]), sourceImageRoot)
	if err != nil {
		return nil, err
	}
	base := strings.TrimRight(util.Clean(baseURL), "/")
	if base == "" {
		return nil, errors.New("base url is required")
	}
	requestedID, err := normalizeRequestedShareID(body["share_id"])
	if err != nil {
		return nil, err
	}
	suffix, mimeType := detectShareImageSuffix(imageData)
	dayDir := time.Now().In(shanghaiTZ).Format("2006/01/02")
	digest := fmt.Sprintf("%x", md5.Sum(imageData))

	s.mu.Lock()
	defer s.mu.Unlock()
	shareID := requestedID
	if shareID == "" {
		shareID = s.newShareIDLocked()
	} else if _, ok := s.items[shareID]; ok {
		return nil, errors.New("share id already exists")
	}
	rel := filepath.ToSlash(filepath.Join(dayDir, shareID+"_"+digest+suffix))
	full := filepath.Join(s.imageDir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		return nil, err
	}
	if err := os.WriteFile(full, imageData, 0o644); err != nil {
		return nil, err
	}
	item := map[string]any{
		"id":             shareID,
		"owner_id":       identityScopeFromIdentity(identity),
		"created_at":     statsNow(),
		"image_path":     rel,
		"image_url":      base + "/share-images/" + rel,
		"share_url":      base + "/share?id=" + url.QueryEscape(shareID),
		"mime_type":      mimeType,
		"prompt":         util.Clean(body["prompt"]),
		"revised_prompt": util.Clean(body["revised_prompt"]),
		"model":          util.Clean(body["model"]),
		"size":           util.Clean(body["size"]),
		"quality":        util.Clean(body["quality"]),
		"result_index":   maxInt(1, util.ToInt(body["result_index"], 1)),
	}
	s.items[shareID] = item
	if err := s.saveLocked(); err != nil {
		return nil, err
	}
	return publicImageShare(item), nil
}

func (s *ImageShareService) Get(shareID string) map[string]any {
	shareID = util.Clean(shareID)
	if shareID == "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	item := s.items[shareID]
	if item == nil {
		return nil
	}
	return publicImageShare(item)
}

// OwnerIDOf returns the owner_id stored when the share was created. Used by the HTTP layer
// to resolve the inviter's invite code without forcing this service to depend on the user
// profile service.
func (s *ImageShareService) OwnerIDOf(shareID string) string {
	shareID = util.Clean(shareID)
	if shareID == "" {
		return ""
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	item := s.items[shareID]
	if item == nil {
		return ""
	}
	return util.Clean(item["owner_id"])
}

func (s *ImageShareService) saveLocked() error {
	return saveStoredJSON(s.store, imageSharesDocumentName, s.fallback, map[string]any{"items": s.items})
}

func (s *ImageShareService) newShareIDLocked() string {
	for i := 0; i < 50; i++ {
		id := strings.ReplaceAll(strings.ReplaceAll(util.RandomTokenURL(6), "-", ""), "_", "")
		if len(id) > 8 {
			id = id[:8]
		}
		if id != "" {
			if _, ok := s.items[id]; !ok {
				return id
			}
		}
	}
	return util.NewHex(10)
}

func normalizeImageShares(raw any) map[string]map[string]any {
	out := map[string]map[string]any{}
	if obj, ok := raw.(map[string]any); ok {
		if items, ok := obj["items"].(map[string]any); ok {
			for key, value := range items {
				item := util.StringMap(value)
				if util.Clean(item["id"]) == "" {
					item["id"] = key
				}
				if id := util.Clean(item["id"]); id != "" {
					out[id] = item
				}
			}
			return out
		}
		for _, item := range util.AsMapSlice(obj["items"]) {
			if id := util.Clean(item["id"]); id != "" {
				out[id] = item
			}
		}
	}
	return out
}

func publicImageShare(item map[string]any) map[string]any {
	return map[string]any{
		"id":             item["id"],
		"share_url":      item["share_url"],
		"image_url":      item["image_url"],
		"mime_type":      util.Clean(item["mime_type"]),
		"prompt":         util.Clean(item["prompt"]),
		"revised_prompt": util.Clean(item["revised_prompt"]),
		"model":          util.Clean(item["model"]),
		"size":           util.Clean(item["size"]),
		"quality":        util.Clean(item["quality"]),
		"result_index":   maxInt(1, util.ToInt(item["result_index"], 1)),
		"created_at":     item["created_at"],
	}
}

func normalizeRequestedShareID(value any) (string, error) {
	text := util.Clean(value)
	if text == "" {
		return "", nil
	}
	if len(text) < 6 || len(text) > 32 {
		return "", errors.New("invalid share id")
	}
	for _, r := range text {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			continue
		}
		return "", errors.New("invalid share id")
	}
	return text, nil
}

func readShareImageData(value, sourceImageRoot string) ([]byte, error) {
	value = util.Clean(value)
	if value == "" {
		return nil, errors.New("image is required")
	}
	if strings.HasPrefix(value, "data:image/") {
		_, data, ok := strings.Cut(value, ",")
		if !ok {
			return nil, errors.New("invalid image data")
		}
		if decodedLen := base64.StdEncoding.DecodedLen(len(data)); decodedLen > maxShareImageBytes {
			return nil, errors.New("image is too large")
		}
		decoded, err := base64.StdEncoding.DecodeString(data)
		if err != nil {
			return nil, errors.New("invalid image data")
		}
		if err := validateShareImageData(decoded); err != nil {
			return nil, err
		}
		return decoded, nil
	}
	rel, err := imageRelativePathFromValueForShare(value)
	if err != nil {
		return nil, err
	}
	root, err := filepath.Abs(sourceImageRoot)
	if err != nil {
		return nil, err
	}
	full := filepath.Join(root, filepath.FromSlash(rel))
	if !pathInsideRootForShare(root, full) {
		return nil, errors.New("invalid image path")
	}
	info, err := os.Stat(full)
	if err != nil {
		return nil, err
	}
	if info.IsDir() {
		return nil, errors.New("invalid image path")
	}
	if info.Size() > maxShareImageBytes {
		return nil, errors.New("image is too large")
	}
	data, err := os.ReadFile(full)
	if err != nil {
		return nil, err
	}
	if err := validateShareImageData(data); err != nil {
		return nil, err
	}
	return data, nil
}

func imageRelativePathFromValueForShare(value string) (string, error) {
	text := strings.TrimSpace(value)
	if text == "" {
		return "", errors.New("invalid image path")
	}
	if parsed, err := url.Parse(text); err == nil {
		pathValue := parsed.EscapedPath()
		if pathValue == "" {
			pathValue = parsed.Path
		}
		if parsed.Scheme != "" || strings.HasPrefix(pathValue, "/") {
			const prefix = "/images/"
			index := strings.Index(pathValue, prefix)
			if index < 0 {
				return "", errors.New("invalid image path")
			}
			rel, err := url.PathUnescape(pathValue[index+len(prefix):])
			if err != nil {
				return "", errors.New("invalid image path")
			}
			return cleanShareRelativePath(rel)
		}
	}
	return cleanShareRelativePath(text)
}

func cleanShareRelativePath(rel string) (string, error) {
	rel = filepath.ToSlash(strings.TrimSpace(rel))
	if rel == "" {
		return "", errors.New("invalid image path")
	}
	for _, part := range strings.Split(rel, "/") {
		if part == "" || part == "." || part == ".." || strings.Contains(part, ":") {
			return "", errors.New("invalid image path")
		}
	}
	return rel, nil
}

func pathInsideRootForShare(root, target string) bool {
	rel, err := filepath.Rel(root, target)
	if err != nil {
		return false
	}
	return rel == "." || (!strings.HasPrefix(rel, "..") && !filepath.IsAbs(rel))
}

func detectShareImageSuffix(data []byte) (string, string) {
	if len(data) >= 8 && string(data[:8]) == "\x89PNG\r\n\x1a\n" {
		return ".png", "image/png"
	}
	if len(data) >= 3 && data[0] == 0xff && data[1] == 0xd8 && data[2] == 0xff {
		return ".jpg", "image/jpeg"
	}
	if len(data) >= 12 && string(data[:4]) == "RIFF" && string(data[8:12]) == "WEBP" {
		return ".webp", "image/webp"
	}
	return ".png", "image/png"
}

func validateShareImageData(data []byte) error {
	if len(data) == 0 {
		return errors.New("image is required")
	}
	if len(data) > maxShareImageBytes {
		return errors.New("image is too large")
	}
	if _, _, err := image.DecodeConfig(bytes.NewReader(data)); err != nil {
		return errors.New("invalid image data")
	}
	return nil
}
