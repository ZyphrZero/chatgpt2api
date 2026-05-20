package service

import (
	"bytes"
	"encoding/base64"
	"image"
	"image/color"
	"image/png"
	"strings"
	"testing"
)

func TestReadShareImageDataAcceptsValidDataURL(t *testing.T) {
	var buf bytes.Buffer
	img := image.NewRGBA(image.Rect(0, 0, 8, 8))
	for y := 0; y < 8; y++ {
		for x := 0; x < 8; x++ {
			img.Set(x, y, color.RGBA{R: uint8(x * 24), G: uint8(y * 24), B: 200, A: 255})
		}
	}
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("png encode: %v", err)
	}

	dataURL := "data:image/png;base64," + base64.StdEncoding.EncodeToString(buf.Bytes())
	data, err := readShareImageData(dataURL, t.TempDir())
	if err != nil {
		t.Fatalf("readShareImageData(valid data url) error = %v", err)
	}
	if !bytes.Equal(data, buf.Bytes()) {
		t.Fatalf("readShareImageData(valid data url) returned unexpected bytes")
	}
}

func TestReadShareImageDataRejectsInvalidDataURL(t *testing.T) {
	dataURL := "data:image/png;base64," + base64.StdEncoding.EncodeToString([]byte("not an image"))
	if _, err := readShareImageData(dataURL, t.TempDir()); err == nil || !strings.Contains(err.Error(), "invalid image data") {
		t.Fatalf("readShareImageData(invalid data url) error = %v, want invalid image data", err)
	}
}

func TestReadShareImageDataRejectsOversizedDataURL(t *testing.T) {
	encoded := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{0xff}, maxShareImageBytes+1))
	dataURL := "data:image/png;base64," + encoded
	if _, err := readShareImageData(dataURL, t.TempDir()); err == nil || !strings.Contains(err.Error(), "image is too large") {
		t.Fatalf("readShareImageData(oversized data url) error = %v, want image is too large", err)
	}
}
