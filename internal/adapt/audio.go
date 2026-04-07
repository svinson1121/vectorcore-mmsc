package adapt

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"

	"github.com/vectorcore/vectorcore-mmsc/internal/message"
)

type commandRunner interface {
	Run(context.Context, string, []string, []byte) ([]byte, error)
}

type execRunner struct{}

func (execRunner) Run(ctx context.Context, bin string, args []string, input []byte) ([]byte, error) {
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Stdin = bytes.NewReader(input)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if stderr.Len() > 0 {
			return nil, fmt.Errorf("adapt: ffmpeg failed: %s", strings.TrimSpace(stderr.String()))
		}
		return nil, err
	}
	return stdout.Bytes(), nil
}

type AudioAdapter struct {
	bin    string
	runner commandRunner
}

func NewAudioAdapter(bin string) AudioAdapter {
	if strings.TrimSpace(bin) == "" {
		bin = "ffmpeg"
	}
	return AudioAdapter{
		bin:    bin,
		runner: execRunner{},
	}
}

func (a AudioAdapter) Adapt(ctx context.Context, parts []message.Part, constraints Constraints) ([]message.Part, error) {
	out := cloneParts(parts)
	for idx, part := range out {
		if !strings.HasPrefix(strings.ToLower(part.ContentType), "audio/") {
			continue
		}
		if isAllowedType(part.ContentType, constraints.AllowedAudioTypes, "audio") {
			if part.Size == 0 {
				part.Size = int64(len(part.Data))
				out[idx] = part
			}
			continue
		}
		targetType := preferredAudioType(constraints.AllowedAudioTypes)
		if targetType == "" {
			return nil, fmt.Errorf("adapt: unsupported audio type %q", part.ContentType)
		}
		encoded, err := a.transcode(ctx, part.Data, targetType)
		if err != nil {
			return nil, err
		}
		part.ContentType = targetType
		part.Data = encoded
		part.Size = int64(len(encoded))
		out[idx] = part
	}
	if err := a.shrinkToFit(ctx, &out, constraints); err != nil {
		return nil, err
	}
	if err := validateParts(out, constraints); err != nil {
		return nil, err
	}
	return out, nil
}

func (a AudioAdapter) transcode(ctx context.Context, input []byte, targetType string) ([]byte, error) {
	if a.runner == nil {
		return nil, fmt.Errorf("adapt: missing audio runner")
	}
	args, err := ffmpegArgsForAudio(targetType, 0)
	if err != nil {
		return nil, err
	}
	return a.runner.Run(ctx, a.bin, args, input)
}

func (a AudioAdapter) shrinkToFit(ctx context.Context, parts *[]message.Part, constraints Constraints) error {
	if constraints.MaxMessageSizeBytes <= 0 || totalSize(*parts) <= constraints.MaxMessageSizeBytes {
		return nil
	}
	for idx, part := range *parts {
		if !strings.HasPrefix(strings.ToLower(part.ContentType), "audio/") {
			continue
		}
		targetType := normalizeAudioType(part.ContentType)
		if !isAllowedType(targetType, constraints.AllowedAudioTypes, "audio") {
			targetType = preferredAudioType(constraints.AllowedAudioTypes)
		}
		if targetType == "" {
			continue
		}
		for profile := 1; profile < 4 && totalSize(*parts) > constraints.MaxMessageSizeBytes; profile++ {
			args, err := ffmpegArgsForAudio(targetType, profile)
			if err != nil {
				return err
			}
			encoded, err := a.runner.Run(ctx, a.bin, args, part.Data)
			if err != nil {
				return err
			}
			part.ContentType = targetType
			part.Data = encoded
			part.Size = int64(len(encoded))
			(*parts)[idx] = part
		}
	}
	return nil
}

func ffmpegArgsForAudio(targetType string, profile int) ([]string, error) {
	switch normalizeAudioType(targetType) {
	case "audio/mpeg":
		bitrate := "96k"
		switch profile {
		case 1:
			bitrate = "64k"
		case 2:
			bitrate = "48k"
		case 3:
			bitrate = "32k"
		}
		return []string{"-v", "error", "-i", "pipe:0", "-b:a", bitrate, "-f", "mp3", "pipe:1"}, nil
	case "audio/mp4":
		bitrate := "96k"
		switch profile {
		case 1:
			bitrate = "64k"
		case 2:
			bitrate = "48k"
		case 3:
			bitrate = "32k"
		}
		return []string{"-v", "error", "-i", "pipe:0", "-c:a", "aac", "-b:a", bitrate, "-f", "mp4", "pipe:1"}, nil
	case "audio/amr":
		bitrate := "12.2k"
		switch profile {
		case 1:
			bitrate = "7.95k"
		case 2:
			bitrate = "6.7k"
		case 3:
			bitrate = "5.9k"
		}
		return []string{"-v", "error", "-i", "pipe:0", "-ar", "8000", "-ac", "1", "-b:a", bitrate, "-f", "amr", "pipe:1"}, nil
	default:
		return nil, fmt.Errorf("adapt: unsupported audio target %q", targetType)
	}
}

func normalizeAudioType(contentType string) string {
	contentType = strings.ToLower(strings.TrimSpace(contentType))
	switch contentType {
	case "audio/mp3", "audio/mpeg":
		return "audio/mpeg"
	case "audio/mp4", "audio/aac", "audio/x-m4a":
		return "audio/mp4"
	case "audio/amr":
		return "audio/amr"
	default:
		return contentType
	}
}

func preferredAudioType(allowed []string) string {
	for _, item := range allowed {
		switch normalizeAudioType(item) {
		case "audio/amr", "audio/mpeg", "audio/mp4":
			return normalizeAudioType(item)
		}
	}
	return ""
}
