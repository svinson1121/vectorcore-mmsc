package adapt

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/vectorcore/vectorcore-mmsc/internal/config"
)

type ValidationResult struct {
	Warnings []string
}

func ValidateEnvironment(cfg config.AdaptConfig) (ValidationResult, error) {
	if !cfg.Enabled {
		return ValidationResult{}, nil
	}

	var result ValidationResult

	if err := validateCommand(cfg.FFmpegPath, "ffmpeg"); err != nil {
		return result, fmt.Errorf("adaptation requires ffmpeg: %w", err)
	}

	if strings.TrimSpace(cfg.LibvipsPath) != "" {
		if err := validateCommand(cfg.LibvipsPath, "vips"); err != nil {
			return result, fmt.Errorf("adaptation libvips_path is configured but invalid: %w", err)
		}
		result.Warnings = append(result.Warnings, "libvips is configured but image adaptation currently uses the in-process Go image path")
	}

	return result, nil
}

func validateCommand(value string, fallback string) error {
	name := strings.TrimSpace(value)
	if name == "" {
		name = fallback
	}
	if strings.ContainsRune(name, filepath.Separator) {
		info, err := os.Stat(name)
		if err != nil {
			return err
		}
		if info.IsDir() {
			return errors.New("path points to a directory")
		}
		if info.Mode()&0o111 == 0 {
			return errors.New("path is not executable")
		}
		return nil
	}
	_, err := exec.LookPath(name)
	return err
}
