package media

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"image"
	"image/color"
	"image/jpeg"
	"os/exec"
	"testing"
	"time"

	"review-studio.local/core/internal/storage"
)

func TestFFmpegImageProcessorPreservesAspectRatio(t *testing.T) {
	requireMediaTools(t)

	source := encodeJPEG(t, 800, 400)
	output, err := NewFFmpegImageProcessor("ffmpeg").Process(
		context.Background(),
		bytes.NewReader(source),
		ImageProfile{
			MaxWidth: 320, MaxHeight: 320, Quality: 80,
		},
	)
	if err != nil {
		t.Fatalf("process image: %v", err)
	}
	result, err := NewFFProber("ffprobe").Probe(
		context.Background(),
		bytes.NewReader(output),
		ProbeHint{ObjectKey: "result.webp", MIMEType: "image/webp"},
	)
	if err != nil {
		t.Fatalf("probe output: %v", err)
	}
	if result.Width == nil || result.Height == nil ||
		*result.Width != 320 || *result.Height != 160 {
		t.Fatalf("unexpected output dimensions: %v x %v", result.Width, result.Height)
	}
}

func TestFFmpegImageProcessorAppliesEXIFOrientation(t *testing.T) {
	requireMediaTools(t)

	source := withEXIFOrientation(encodeJPEG(t, 80, 40), 6)
	output, err := NewFFmpegImageProcessor("ffmpeg").Process(
		context.Background(),
		bytes.NewReader(source),
		ImageProfile{
			MaxWidth: 320, MaxHeight: 320, Quality: 80,
		},
	)
	if err != nil {
		t.Fatalf("process oriented image: %v", err)
	}
	result, err := NewFFProber("ffprobe").Probe(
		context.Background(),
		bytes.NewReader(output),
		ProbeHint{ObjectKey: "result.webp", MIMEType: "image/webp"},
	)
	if err != nil {
		t.Fatalf("probe output: %v", err)
	}
	if result.Width == nil || result.Height == nil ||
		*result.Width != 40 || *result.Height != 80 {
		t.Fatalf("orientation was not applied: %v x %v", result.Width, result.Height)
	}
}

func TestValidateImageSourceRejectsPixelBomb(t *testing.T) {
	width := 20_000
	height := 10_000
	err := validateImageSource(
		storage.StoredObject{
			Status:           "available",
			SizeBytes:        1024,
			QuickFingerprint: "fingerprint",
			ModifiedAt:       time.Now(),
		},
		Metadata{
			Status:            "succeeded",
			MediaType:         "image",
			SourceFingerprint: "fingerprint",
			Width:             &width,
			Height:            &height,
		},
	)
	var coded interface{ Code() string }
	if !errors.As(err, &coded) || coded.Code() != "rendition.pixel_limit" {
		t.Fatalf("expected pixel limit error, got %v", err)
	}
}

func requireMediaTools(t *testing.T) {
	t.Helper()
	for _, name := range []string{"ffmpeg", "ffprobe"} {
		if _, err := exec.LookPath(name); err != nil {
			t.Skipf("%s is unavailable", name)
		}
	}
}

func encodeJPEG(t *testing.T, width, height int) []byte {
	t.Helper()
	value := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := range height {
		for x := range width {
			value.SetRGBA(x, y, color.RGBA{
				R: uint8(x % 255), G: uint8(y % 255), B: 120, A: 255,
			})
		}
	}
	var buffer bytes.Buffer
	if err := jpeg.Encode(&buffer, value, &jpeg.Options{Quality: 90}); err != nil {
		t.Fatalf("encode jpeg: %v", err)
	}
	return buffer.Bytes()
}

func withEXIFOrientation(jpegBytes []byte, orientation uint16) []byte {
	tiff := make([]byte, 26)
	copy(tiff[0:2], []byte{'I', 'I'})
	binary.LittleEndian.PutUint16(tiff[2:4], 42)
	binary.LittleEndian.PutUint32(tiff[4:8], 8)
	binary.LittleEndian.PutUint16(tiff[8:10], 1)
	binary.LittleEndian.PutUint16(tiff[10:12], 0x0112)
	binary.LittleEndian.PutUint16(tiff[12:14], 3)
	binary.LittleEndian.PutUint32(tiff[14:18], 1)
	binary.LittleEndian.PutUint16(tiff[18:20], orientation)

	payload := append([]byte("Exif\x00\x00"), tiff...)
	segment := []byte{0xff, 0xe1, 0, 0}
	binary.BigEndian.PutUint16(segment[2:4], uint16(len(payload)+2))
	segment = append(segment, payload...)

	result := make([]byte, 0, len(jpegBytes)+len(segment))
	result = append(result, jpegBytes[:2]...)
	result = append(result, segment...)
	result = append(result, jpegBytes[2:]...)
	return result
}
