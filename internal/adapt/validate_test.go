package adapt

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/vectorcore/vectorcore-mmsc/internal/config"
)

func TestValidateEnvironmentDisabledSkipsToolChecks(t *testing.T) {
	t.Parallel()

	_, err := ValidateEnvironment(config.AdaptConfig{
		Enabled:     false,
		FFmpegPath:  "/definitely/missing/ffmpeg",
		LibvipsPath: "/definitely/missing/vips",
	})
	if err != nil {
		t.Fatalf("expected disabled adaptation to skip validation, got %v", err)
	}
}

func TestValidateEnvironmentRequiresFFmpegWhenEnabled(t *testing.T) {
	t.Parallel()

	_, err := ValidateEnvironment(config.AdaptConfig{
		Enabled:    true,
		FFmpegPath: "/definitely/missing/ffmpeg",
	})
	if err == nil {
		t.Fatal("expected ffmpeg validation error")
	}
}

func TestValidateEnvironmentWarnsForConfiguredLibvips(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	ffmpegPath := filepath.Join(dir, "ffmpeg")
	vipsPath := filepath.Join(dir, "vips")
	for _, path := range []string{ffmpegPath, vipsPath} {
		if err := os.WriteFile(path, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
			t.Fatalf("write tool %s: %v", path, err)
		}
	}

	result, err := ValidateEnvironment(config.AdaptConfig{
		Enabled:     true,
		FFmpegPath:  ffmpegPath,
		LibvipsPath: vipsPath,
	})
	if err != nil {
		t.Fatalf("validate environment: %v", err)
	}
	if len(result.Warnings) != 1 {
		t.Fatalf("expected one warning, got %#v", result.Warnings)
	}
}
