package service

import (
	"encoding/json"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

type testImageConfig struct {
	root string
}

func (c testImageConfig) ImagesDir() string {
	path := filepath.Join(c.root, "images")
	_ = os.MkdirAll(path, 0o755)
	return path
}

func (c testImageConfig) ImageThumbnailsDir() string {
	path := filepath.Join(c.root, "image_thumbnails")
	_ = os.MkdirAll(path, 0o755)
	return path
}

func (c testImageConfig) ImageMetadataDir() string {
	path := filepath.Join(c.root, "image_metadata")
	_ = os.MkdirAll(path, 0o755)
	return path
}

func (c testImageConfig) CleanupOldImages() int {
	return 0
}

var allImages = ImageAccessScope{All: true}

func TestImageServiceListImagesReturnsEmptyArrays(t *testing.T) {
	service := NewImageService(testImageConfig{root: t.TempDir()})
	result := service.ListImages("http://127.0.0.1:8000", "", "", allImages)

	data, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	if string(data) != `{"groups":[],"items":[]}` {
		t.Fatalf("ListImages() JSON = %s", data)
	}
}

func TestImageServiceListImagesReturnsDimensionsWithoutGeneratingThumbnails(t *testing.T) {
	root := t.TempDir()
	config := testImageConfig{root: root}
	imagePath := filepath.Join(config.ImagesDir(), "2026", "04", "29", "sample.png")
	if err := os.MkdirAll(filepath.Dir(imagePath), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := writeTestPNG(imagePath); err != nil {
		t.Fatalf("writeTestPNG() error = %v", err)
	}

	service := NewImageService(config)
	result := service.ListImages("http://127.0.0.1:8000", "", "", allImages)
	items := result["items"].([]map[string]any)
	if len(items) != 1 {
		t.Fatalf("items = %#v", items)
	}
	if got := toString(items[0]["path"]); got != "2026/04/29/sample.png" {
		t.Fatalf("path = %q, want relative image path", got)
	}
	thumbnailURL := toString(items[0]["thumbnail_url"])
	thumbnailPath := requireThumbnailURLPath(t, thumbnailURL)
	if !strings.HasSuffix(thumbnailPath, thumbnailExtension) {
		t.Fatalf("thumbnail_url path = %q, want %s suffix", thumbnailPath, thumbnailExtension)
	}
	if numericMetaValue(items[0]["width"]) != 32 || numericMetaValue(items[0]["height"]) != 24 {
		t.Fatalf("ListImages() dimensions = %#v, want 32x24", items[0])
	}
	if toString(items[0]["resolution"]) != "32x24" {
		t.Fatalf("ListImages() resolution = %#v, want 32x24", items[0]["resolution"])
	}
	if toString(items[0]["aspect_ratio"]) != "4:3" {
		t.Fatalf("ListImages() aspect_ratio = %#v, want 4:3", items[0]["aspect_ratio"])
	}
	if toString(items[0]["orientation"]) != "landscape" {
		t.Fatalf("ListImages() orientation = %#v, want landscape", items[0]["orientation"])
	}
	thumbPath := filepath.Join(config.ImageThumbnailsDir(), "2026", "04", "29", "sample.png"+thumbnailExtension)
	if _, err := os.Stat(thumbPath); !os.IsNotExist(err) {
		t.Fatalf("ListImages() should not create thumbnail synchronously, stat error = %v", err)
	}
}

func TestImageServiceEnsureThumbnailCreatesJPEGThumbnails(t *testing.T) {
	root := t.TempDir()
	config := testImageConfig{root: root}
	imagePath := filepath.Join(config.ImagesDir(), "2026", "04", "29", "sample.png")
	if err := os.MkdirAll(filepath.Dir(imagePath), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := writeTestPNG(imagePath); err != nil {
		t.Fatalf("writeTestPNG() error = %v", err)
	}

	service := NewImageService(config)
	service.EnsureThumbnails([]string{"2026/04/29/sample.png"})
	thumbnailRel := "2026/04/29/sample.png" + thumbnailExtension
	if !strings.HasSuffix(thumbnailRel, thumbnailExtension) {
		t.Fatalf("thumbnail_rel = %q, want %s suffix", thumbnailRel, thumbnailExtension)
	}

	thumbPath := filepath.Join(config.ImageThumbnailsDir(), filepath.FromSlash(thumbnailRel))
	file, err := os.Open(thumbPath)
	if err != nil {
		t.Fatalf("open thumbnail: %v", err)
	}
	defer file.Close()
	decoded, err := jpeg.Decode(file)
	if err != nil {
		t.Fatalf("decode jpeg thumbnail: %v", err)
	}
	if decoded.Bounds().Dx() <= 0 || decoded.Bounds().Dy() <= 0 {
		t.Fatalf("decoded thumbnail has invalid bounds: %v", decoded.Bounds())
	}
	if decoded.Bounds().Dx() > ThumbnailSize || decoded.Bounds().Dy() > ThumbnailSize {
		t.Fatalf("decoded thumbnail bounds = %v, want max side <= %d", decoded.Bounds(), ThumbnailSize)
	}
	meta, err := os.ReadFile(thumbPath + ".json")
	if err != nil {
		t.Fatalf("read thumbnail metadata: %v", err)
	}
	var metadata map[string]any
	if err := json.Unmarshal(meta, &metadata); err != nil {
		t.Fatalf("unmarshal thumbnail metadata: %v", err)
	}
	if numericMetaValue(metadata["thumbnail_size"]) != ThumbnailSize {
		t.Fatalf("thumbnail_size metadata = %v, want %d", metadata["thumbnail_size"], ThumbnailSize)
	}
	if numericMetaValue(metadata["thumbnail_quality"]) != thumbnailQuality {
		t.Fatalf("thumbnail_quality metadata = %v, want %d", metadata["thumbnail_quality"], thumbnailQuality)
	}
}

func TestImageServiceEnsureThumbnailsKeepsLargeImageThumbnailSmall(t *testing.T) {
	root := t.TempDir()
	config := testImageConfig{root: root}
	imagePath := filepath.Join(config.ImagesDir(), "2026", "04", "29", "large.png")
	if err := os.MkdirAll(filepath.Dir(imagePath), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := writeLargeTestPNG(imagePath); err != nil {
		t.Fatalf("writeLargeTestPNG() error = %v", err)
	}

	service := NewImageService(config)
	service.EnsureThumbnails([]string{"2026/04/29/large.png"})

	thumbPath := filepath.Join(config.ImageThumbnailsDir(), "2026", "04", "29", "large.png"+thumbnailExtension)
	info, err := os.Stat(thumbPath)
	if err != nil {
		t.Fatalf("stat thumbnail: %v", err)
	}
	if info.Size() > 120*1024 {
		t.Fatalf("thumbnail size = %d bytes, want <= 120KiB", info.Size())
	}

	file, err := os.Open(thumbPath)
	if err != nil {
		t.Fatalf("open thumbnail: %v", err)
	}
	defer file.Close()
	decoded, err := jpeg.Decode(file)
	if err != nil {
		t.Fatalf("decode jpeg thumbnail: %v", err)
	}
	if decoded.Bounds().Dx() > ThumbnailSize || decoded.Bounds().Dy() > ThumbnailSize {
		t.Fatalf("decoded thumbnail bounds = %v, want max side <= %d", decoded.Bounds(), ThumbnailSize)
	}
}

func TestImageServiceEnsureThumbnailsCreatesCachedThumbnailFromImageURL(t *testing.T) {
	root := t.TempDir()
	config := testImageConfig{root: root}
	imagePath := filepath.Join(config.ImagesDir(), "2026", "04", "29", "sample.png")
	if err := os.MkdirAll(filepath.Dir(imagePath), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := writeTestPNG(imagePath); err != nil {
		t.Fatalf("writeTestPNG() error = %v", err)
	}

	service := NewImageService(config)
	service.EnsureThumbnails([]string{"http://127.0.0.1:8000/images/2026/04/29/sample.png"})

	thumbPath := filepath.Join(config.ImageThumbnailsDir(), "2026", "04", "29", "sample.png"+thumbnailExtension)
	if _, err := os.Stat(thumbPath); err != nil {
		t.Fatalf("thumbnail was not created: %v", err)
	}
	if _, err := os.Stat(thumbPath + ".json"); err != nil {
		t.Fatalf("thumbnail metadata was not created: %v", err)
	}

	result := service.ListImages("http://127.0.0.1:8000", "", "", allImages)
	items := result["items"].([]map[string]any)
	if len(items) != 1 {
		t.Fatalf("items = %#v", items)
	}
	if items[0]["width"] == nil || items[0]["height"] == nil {
		t.Fatalf("ListImages() did not read warmed thumbnail metadata: %#v", items[0])
	}
}

func TestImageServiceEnsureThumbnailsReusesFreshThumbnail(t *testing.T) {
	root := t.TempDir()
	config := testImageConfig{root: root}
	imagePath := filepath.Join(config.ImagesDir(), "2026", "04", "29", "sample.png")
	if err := os.MkdirAll(filepath.Dir(imagePath), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := writeTestPNG(imagePath); err != nil {
		t.Fatalf("writeTestPNG() error = %v", err)
	}

	service := NewImageService(config)
	service.EnsureThumbnails([]string{"2026/04/29/sample.png"})
	thumbPath := filepath.Join(config.ImageThumbnailsDir(), "2026", "04", "29", "sample.png"+thumbnailExtension)
	firstInfo, err := os.Stat(thumbPath)
	if err != nil {
		t.Fatalf("stat thumbnail: %v", err)
	}

	service.EnsureThumbnails([]string{"2026/04/29/sample.png"})
	secondInfo, err := os.Stat(thumbPath)
	if err != nil {
		t.Fatalf("stat thumbnail after reuse: %v", err)
	}
	if !secondInfo.ModTime().Equal(firstInfo.ModTime()) {
		t.Fatalf("fresh thumbnail was regenerated: first=%s second=%s", firstInfo.ModTime(), secondInfo.ModTime())
	}
}

func TestImageServiceEnsureThumbnailsRegeneratesStaleThumbnail(t *testing.T) {
	root := t.TempDir()
	config := testImageConfig{root: root}
	imagePath := filepath.Join(config.ImagesDir(), "2026", "04", "29", "sample.png")
	if err := os.MkdirAll(filepath.Dir(imagePath), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := writeTestPNG(imagePath); err != nil {
		t.Fatalf("writeTestPNG() error = %v", err)
	}

	service := NewImageService(config)
	service.EnsureThumbnails([]string{"2026/04/29/sample.png"})
	thumbPath := filepath.Join(config.ImageThumbnailsDir(), "2026", "04", "29", "sample.png"+thumbnailExtension)
	staleTime := time.Now().Add(-time.Hour).Truncate(time.Second)
	if err := os.Chtimes(thumbPath, staleTime, staleTime); err != nil {
		t.Fatalf("Chtimes() error = %v", err)
	}

	service.EnsureThumbnails([]string{"2026/04/29/sample.png"})
	info, err := os.Stat(thumbPath)
	if err != nil {
		t.Fatalf("stat regenerated thumbnail: %v", err)
	}
	if !info.ModTime().After(staleTime) {
		t.Fatalf("stale thumbnail was not regenerated: got %s, stale %s", info.ModTime(), staleTime)
	}
}

func TestImageServiceEnsureThumbnailsRefreshesInvalidMetadata(t *testing.T) {
	root := t.TempDir()
	config := testImageConfig{root: root}
	imagePath := filepath.Join(config.ImagesDir(), "2026", "04", "29", "sample.png")
	if err := os.MkdirAll(filepath.Dir(imagePath), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := writeTestPNG(imagePath); err != nil {
		t.Fatalf("writeTestPNG() error = %v", err)
	}

	service := NewImageService(config)
	service.EnsureThumbnails([]string{"2026/04/29/sample.png"})
	thumbPath := filepath.Join(config.ImageThumbnailsDir(), "2026", "04", "29", "sample.png"+thumbnailExtension)
	if err := os.WriteFile(thumbPath+".json", []byte(`{"width":1,"height":1,"thumbnail_size":1,"thumbnail_version":0}`), 0o644); err != nil {
		t.Fatalf("write stale metadata: %v", err)
	}

	service.EnsureThumbnails([]string{"2026/04/29/sample.png"})
	meta, err := os.ReadFile(thumbPath + ".json")
	if err != nil {
		t.Fatalf("read thumbnail metadata: %v", err)
	}
	var metadata map[string]any
	if err := json.Unmarshal(meta, &metadata); err != nil {
		t.Fatalf("unmarshal metadata: %v", err)
	}
	if numericMetaValue(metadata["thumbnail_size"]) != ThumbnailSize || numericMetaValue(metadata["thumbnail_version"]) != thumbnailCacheVersion || numericMetaValue(metadata["thumbnail_quality"]) != thumbnailQuality {
		t.Fatalf("metadata was not refreshed: %#v", metadata)
	}
}

func TestImageServiceEnsureThumbnailsHandlesConcurrentSameImage(t *testing.T) {
	root := t.TempDir()
	config := testImageConfig{root: root}
	imagePath := filepath.Join(config.ImagesDir(), "2026", "04", "29", "sample.png")
	if err := os.MkdirAll(filepath.Dir(imagePath), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := writeTestPNG(imagePath); err != nil {
		t.Fatalf("writeTestPNG() error = %v", err)
	}

	service := NewImageService(config)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			service.EnsureThumbnails([]string{"2026/04/29/sample.png"})
		}()
	}
	wg.Wait()

	thumbPath := filepath.Join(config.ImageThumbnailsDir(), "2026", "04", "29", "sample.png"+thumbnailExtension)
	file, err := os.Open(thumbPath)
	if err != nil {
		t.Fatalf("open thumbnail: %v", err)
	}
	defer file.Close()
	if _, err := jpeg.Decode(file); err != nil {
		t.Fatalf("decode concurrent thumbnail: %v", err)
	}
}

func TestImageServiceDeleteImagesRemovesOriginalAndThumbnail(t *testing.T) {
	root := t.TempDir()
	config := testImageConfig{root: root}
	imagePath := filepath.Join(config.ImagesDir(), "2026", "04", "29", "sample.png")
	if err := os.MkdirAll(filepath.Dir(imagePath), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := writeTestPNG(imagePath); err != nil {
		t.Fatalf("writeTestPNG() error = %v", err)
	}

	service := NewImageService(config)
	service.EnsureThumbnails([]string{"2026/04/29/sample.png"})
	thumbPath := filepath.Join(config.ImageThumbnailsDir(), "2026", "04", "29", "sample.png"+thumbnailExtension)
	if _, err := os.Stat(thumbPath); err != nil {
		t.Fatalf("thumbnail was not created: %v", err)
	}

	result, err := service.DeleteImages([]string{"2026/04/29/sample.png"}, allImages)
	if err != nil {
		t.Fatalf("DeleteImages() error = %v", err)
	}
	if result["deleted"] != 1 || result["missing"] != 0 {
		t.Fatalf("DeleteImages() = %#v", result)
	}
	if _, err := os.Stat(imagePath); !os.IsNotExist(err) {
		t.Fatalf("original still exists, stat error = %v", err)
	}
	if _, err := os.Stat(thumbPath); !os.IsNotExist(err) {
		t.Fatalf("thumbnail still exists, stat error = %v", err)
	}
	if _, err := os.Stat(thumbPath + ".json"); !os.IsNotExist(err) {
		t.Fatalf("thumbnail metadata still exists, stat error = %v", err)
	}
}

func TestImageServiceScopesImagesByOwner(t *testing.T) {
	root := t.TempDir()
	config := testImageConfig{root: root}
	alicePath := filepath.Join(config.ImagesDir(), "2026", "04", "29", "alice.png")
	bobPath := filepath.Join(config.ImagesDir(), "2026", "04", "29", "bob.png")
	legacyPath := filepath.Join(config.ImagesDir(), "2026", "04", "29", "legacy.png")
	for _, path := range []string{alicePath, bobPath, legacyPath} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("MkdirAll() error = %v", err)
		}
		if err := writeTestPNG(path); err != nil {
			t.Fatalf("writeTestPNG(%s) error = %v", path, err)
		}
	}

	service := NewImageService(config)
	service.RecordImageOwners([]string{"2026/04/29/alice.png"}, "linuxdo:123")
	service.RecordImageOwners([]string{"http://127.0.0.1:8000/images/2026/04/29/bob.png"}, "linuxdo:456")

	alice := service.ListImages("http://127.0.0.1:8000", "", "", ImageAccessScope{OwnerID: "linuxdo:123"})
	aliceItems := alice["items"].([]map[string]any)
	if len(aliceItems) != 1 || aliceItems[0]["path"] != "2026/04/29/alice.png" {
		t.Fatalf("alice ListImages() = %#v", alice)
	}
	admin := service.ListImages("http://127.0.0.1:8000", "", "", allImages)
	if items := admin["items"].([]map[string]any); len(items) != 3 {
		t.Fatalf("admin ListImages() = %#v", admin)
	}

	result, err := service.DeleteImages([]string{"2026/04/29/bob.png", "2026/04/29/alice.png"}, ImageAccessScope{OwnerID: "linuxdo:123"})
	if err != nil {
		t.Fatalf("DeleteImages(owner) error = %v", err)
	}
	if result["deleted"] != 1 || result["missing"] != 1 {
		t.Fatalf("DeleteImages(owner) = %#v", result)
	}
	if _, err := os.Stat(alicePath); !os.IsNotExist(err) {
		t.Fatalf("alice image still exists, stat error = %v", err)
	}
	if _, err := os.Stat(bobPath); err != nil {
		t.Fatalf("bob image should not be deleted, stat error = %v", err)
	}
}

func TestImageServicePublicVisibility(t *testing.T) {
	root := t.TempDir()
	config := testImageConfig{root: root}
	aliceRel := "2026/04/29/alice.png"
	bobRel := "2026/04/29/bob.png"
	for _, rel := range []string{aliceRel, bobRel} {
		path := filepath.Join(config.ImagesDir(), filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("MkdirAll() error = %v", err)
		}
		if err := writeTestPNG(path); err != nil {
			t.Fatalf("writeTestPNG(%s) error = %v", path, err)
		}
	}

	service := NewImageService(config)
	service.RecordGeneratedImages([]string{aliceRel}, "linuxdo:123", "alice", ImageVisibilityPublic)
	service.RecordGeneratedImages([]string{bobRel}, "linuxdo:456", "bob", ImageVisibilityPrivate)

	public := service.ListImages("http://127.0.0.1:8000", "", "", ImageAccessScope{Public: true})
	publicItems := public["items"].([]map[string]any)
	if len(publicItems) != 1 || publicItems[0]["path"] != aliceRel {
		t.Fatalf("public ListImages() = %#v", public)
	}
	if publicItems[0]["visibility"] != ImageVisibilityPublic || publicItems[0]["owner_name"] != "alice" || publicItems[0]["published_at"] == "" {
		t.Fatalf("public metadata = %#v", publicItems[0])
	}

	if _, err := service.UpdateImageVisibility(aliceRel, ImageVisibilityPrivate, ImageAccessScope{OwnerID: "linuxdo:456"}); err == nil {
		t.Fatal("UpdateImageVisibility(other owner) error = nil")
	}
	if _, err := service.UpdateImageVisibility("http://127.0.0.1:8000/images/"+aliceRel, ImageVisibilityPrivate, ImageAccessScope{OwnerID: "linuxdo:123"}); err != nil {
		t.Fatalf("UpdateImageVisibility(owner private) error = %v", err)
	}
	public = service.ListImages("http://127.0.0.1:8000", "", "", ImageAccessScope{Public: true})
	if items := public["items"].([]map[string]any); len(items) != 0 {
		t.Fatalf("private image should leave public gallery: %#v", public)
	}
}

func TestImageServiceListImagesReturnsRequestedResolutionPreset(t *testing.T) {
	root := t.TempDir()
	config := testImageConfig{root: root}
	rel := "2026/04/29/alice.png"
	path := filepath.Join(config.ImagesDir(), filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := writeTestPNG(path); err != nil {
		t.Fatalf("writeTestPNG() error = %v", err)
	}

	service := NewImageService(config)
	service.RecordGeneratedImages([]string{rel}, "linuxdo:123", "alice", ImageVisibilityPrivate, GeneratedImageMetadata{
		ResolutionPreset: "2k",
		RequestedSize:    "2048x2048",
		Prompt:           "画一张商业海报",
	})

	list := service.ListImages("http://127.0.0.1:8000", "", "", allImages)
	items := list["items"].([]map[string]any)
	if len(items) != 1 {
		t.Fatalf("ListImages() = %#v", list)
	}
	if items[0]["resolution_preset"] != "2k" || items[0]["requested_size"] != "2048x2048" {
		t.Fatalf("request metadata = %#v", items[0])
	}
	if items[0]["prompt"] != "画一张商业海报" {
		t.Fatalf("prompt metadata = %#v", items[0])
	}
	if items[0]["resolution"] != "32x24" {
		t.Fatalf("actual resolution = %#v, want 32x24", items[0]["resolution"])
	}
}

func TestImageServiceDeleteImagesRejectsTraversal(t *testing.T) {
	root := t.TempDir()
	outsidePath := filepath.Join(root, "outside.png")
	if err := writeTestPNG(outsidePath); err != nil {
		t.Fatalf("writeTestPNG() error = %v", err)
	}

	service := NewImageService(testImageConfig{root: root})
	if _, err := service.DeleteImages([]string{"../outside.png"}, allImages); err == nil {
		t.Fatal("DeleteImages() error = nil, want traversal rejection")
	}
	if _, err := os.Stat(outsidePath); err != nil {
		t.Fatalf("outside file was changed: %v", err)
	}
}

func requireThumbnailURLPath(t *testing.T, value string) string {
	t.Helper()
	parsed, err := url.Parse(value)
	if err != nil {
		t.Fatalf("parse thumbnail_url: %v", err)
	}
	if !strings.Contains(parsed.Path, "/image-thumbnails/") {
		t.Fatalf("thumbnail_url path = %q, want image thumbnail route", parsed.Path)
	}
	if parsed.Query().Get("v") == "" {
		t.Fatalf("thumbnail_url = %q, want cache-busting version query", value)
	}
	return parsed.Path
}

func writeTestPNG(path string) error {
	img := image.NewRGBA(image.Rect(0, 0, 32, 24))
	for y := 0; y < 24; y++ {
		for x := 0; x < 32; x++ {
			img.Set(x, y, color.RGBA{R: uint8(x * 8), G: uint8(y * 10), B: 120, A: 255})
		}
	}
	file, err := os.Create(path)
	if err != nil {
		return err
	}
	defer file.Close()
	return png.Encode(file, img)
}

func writeLargeTestPNG(path string) error {
	img := image.NewRGBA(image.Rect(0, 0, 1600, 1200))
	for y := 0; y < 1200; y++ {
		for x := 0; x < 1600; x++ {
			img.Set(x, y, color.RGBA{
				R: uint8((x*37 + y*17) & 0xff),
				G: uint8((x*13 ^ y*31) & 0xff),
				B: uint8((x*y + x*11 + y*7) & 0xff),
				A: 255,
			})
		}
	}
	file, err := os.Create(path)
	if err != nil {
		return err
	}
	defer file.Close()
	return png.Encode(file, img)
}
