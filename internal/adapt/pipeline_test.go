package adapt

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/png"
	"testing"

	"github.com/vectorcore/vectorcore-mmsc/internal/config"
	"github.com/vectorcore/vectorcore-mmsc/internal/message"
)

func TestPipelineDisabledPassthrough(t *testing.T) {
	t.Parallel()

	pipeline := NewPipeline(config.AdaptConfig{Enabled: false}, nil)
	parts := []message.Part{{ContentType: "image/webp", Data: []byte("abc"), Size: 3}}
	got, err := pipeline.AdaptForClass(context.Background(), parts, "default", 1)
	if err != nil {
		t.Fatalf("adapt disabled should pass through: %v", err)
	}
	if len(got) != 1 || got[0].ContentType != "image/webp" {
		t.Fatalf("unexpected parts: %#v", got)
	}
}

func TestPipelineRejectsOversizedMessage(t *testing.T) {
	t.Parallel()

	pipeline := NewPipeline(config.AdaptConfig{Enabled: true}, nil)
	_, err := pipeline.AdaptForClass(context.Background(), []message.Part{{ContentType: "text/plain", Data: []byte("hello"), Size: 5}}, "default", 4)
	if err == nil {
		t.Fatal("expected size validation failure")
	}
}

func TestPipelineRejectsUnsupportedMediaType(t *testing.T) {
	t.Parallel()

	pipeline := NewPipeline(config.AdaptConfig{Enabled: true}, nil)
	_, err := pipeline.AdaptForClass(context.Background(), []message.Part{{ContentType: "image/webp", Data: []byte("hello"), Size: 5}}, "default", 10)
	if err == nil {
		t.Fatal("expected media type validation failure")
	}
}

func TestPipelineResizesLargeImage(t *testing.T) {
	t.Parallel()

	var payload bytes.Buffer
	img := image.NewRGBA(image.Rect(0, 0, 1200, 900))
	for y := 0; y < 900; y++ {
		for x := 0; x < 1200; x++ {
			img.Set(x, y, color.RGBA{R: uint8(x % 255), G: uint8(y % 255), B: 120, A: 255})
		}
	}
	if err := png.Encode(&payload, img); err != nil {
		t.Fatalf("encode source image: %v", err)
	}

	pipeline := NewPipeline(config.AdaptConfig{Enabled: true}, nil)
	got, err := pipeline.AdaptForClass(context.Background(), []message.Part{{
		ContentType: "image/png",
		Data:        payload.Bytes(),
		Size:        int64(payload.Len()),
	}}, "default", 10*1024*1024)
	if err != nil {
		t.Fatalf("adapt image: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("unexpected adapted parts: %#v", got)
	}
	decoded, _, err := image.Decode(bytes.NewReader(got[0].Data))
	if err != nil {
		t.Fatalf("decode adapted image: %v", err)
	}
	if decoded.Bounds().Dx() > 640 || decoded.Bounds().Dy() > 480 {
		t.Fatalf("expected resized image within 640x480, got %dx%d", decoded.Bounds().Dx(), decoded.Bounds().Dy())
	}
}

func TestPipelineAcceptsNormalizedMediaAliases(t *testing.T) {
	t.Parallel()

	pipeline := NewPipeline(config.AdaptConfig{Enabled: true}, nil)
	parts := []message.Part{
		{ContentType: "image/jpg", Data: []byte("img"), Size: 3},
		{ContentType: "audio/mp3", Data: []byte("aud"), Size: 3},
		{ContentType: "video/3gp", Data: []byte("vid"), Size: 3},
	}
	got, err := pipeline.AdaptForClass(context.Background(), parts, "default", 32)
	if err != nil {
		t.Fatalf("adapt aliases: %v", err)
	}
	if len(got) != len(parts) {
		t.Fatalf("unexpected adapted parts: %#v", got)
	}
}
