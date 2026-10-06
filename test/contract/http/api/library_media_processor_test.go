package api

import (
	"bytes"
	"context"
	"image"
	"image/jpeg"
	"os/exec"
	"testing"

	. "github.com/kannachi323/misty/server/internal/platform/httpapi"

)


func TestFFmpegLibraryMediaProcessorCreatesMetadataStrippedPreview(t *testing.T) {
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg is not installed")
	}
	source := image.NewRGBA(image.Rect(0, 0, 3200, 1200))
	var encoded bytes.Buffer
	if err := jpeg.Encode(&encoded, source, &jpeg.Options{Quality: 85}); err != nil {
		t.Fatal(err)
	}
	processor, err := NewFFmpegLibraryMediaProcessor(ffmpeg)
	if err != nil {
		t.Fatal(err)
	}
	preview, err := processor.Preview(context.Background(), bytes.NewReader(encoded.Bytes()), int64(encoded.Len()), 1024)
	if err != nil {
		t.Fatal(err)
	}
	defer preview.Cleanup()
	reader, err := preview.Open()
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := jpeg.Decode(reader)
	_ = reader.Close()
	if err != nil {
		t.Fatal(err)
	}
	if decoded.Bounds().Dx() != 1024 || decoded.Bounds().Dy() != 384 || preview.MIMEType != "image/jpeg" || preview.ByteSize > 25_000_000 {
		t.Fatalf("preview = %dx%d %#v", decoded.Bounds().Dx(), decoded.Bounds().Dy(), preview)
	}
}

