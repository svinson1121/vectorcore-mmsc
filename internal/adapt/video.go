package adapt

import (
	"context"
	"fmt"
	"strings"

	"github.com/vectorcore/vectorcore-mmsc/internal/message"
)

type VideoAdapter struct {
	bin    string
	runner commandRunner
}

func NewVideoAdapter(bin string) VideoAdapter {
	if strings.TrimSpace(bin) == "" {
		bin = "ffmpeg"
	}
	return VideoAdapter{
		bin:    bin,
		runner: execRunner{},
	}
}

func (v VideoAdapter) Adapt(ctx context.Context, parts []message.Part, constraints Constraints) ([]message.Part, error) {
	out := cloneParts(parts)
	for idx, part := range out {
		if !strings.HasPrefix(strings.ToLower(part.ContentType), "video/") {
			continue
		}
		if isAllowedType(part.ContentType, constraints.AllowedVideoTypes, "video") {
			if part.Size == 0 {
				part.Size = int64(len(part.Data))
				out[idx] = part
			}
			continue
		}
		targetType := preferredVideoType(constraints.AllowedVideoTypes)
		if targetType == "" {
			return nil, fmt.Errorf("adapt: unsupported video type %q", part.ContentType)
		}
		encoded, err := v.transcode(ctx, part.Data, targetType)
		if err != nil {
			return nil, err
		}
		part.ContentType = targetType
		part.Data = encoded
		part.Size = int64(len(encoded))
		out[idx] = part
	}
	if err := v.shrinkToFit(ctx, &out, constraints); err != nil {
		return nil, err
	}
	if err := validateParts(out, constraints); err != nil {
		return nil, err
	}
	return out, nil
}

func (v VideoAdapter) transcode(ctx context.Context, input []byte, targetType string) ([]byte, error) {
	if v.runner == nil {
		return nil, fmt.Errorf("adapt: missing video runner")
	}
	args, err := ffmpegArgsForVideo(targetType, 0)
	if err != nil {
		return nil, err
	}
	return v.runner.Run(ctx, v.bin, args, input)
}

func (v VideoAdapter) shrinkToFit(ctx context.Context, parts *[]message.Part, constraints Constraints) error {
	if constraints.MaxMessageSizeBytes <= 0 || totalSize(*parts) <= constraints.MaxMessageSizeBytes {
		return nil
	}
	for idx, part := range *parts {
		if !strings.HasPrefix(strings.ToLower(part.ContentType), "video/") {
			continue
		}
		targetType := normalizeVideoType(part.ContentType)
		if !isAllowedType(targetType, constraints.AllowedVideoTypes, "video") {
			targetType = preferredVideoType(constraints.AllowedVideoTypes)
		}
		if targetType == "" {
			continue
		}
		for profile := 1; profile < 4 && totalSize(*parts) > constraints.MaxMessageSizeBytes; profile++ {
			args, err := ffmpegArgsForVideo(targetType, profile)
			if err != nil {
				return err
			}
			encoded, err := v.runner.Run(ctx, v.bin, args, part.Data)
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

func ffmpegArgsForVideo(targetType string, profile int) ([]string, error) {
	switch normalizeVideoType(targetType) {
	case "video/mp4":
		args := []string{"-v", "error", "-i", "pipe:0", "-movflags", "frag_keyframe+empty_moov"}
		switch profile {
		case 1:
			args = append(args, "-vf", "scale=426:320", "-b:v", "320k", "-crf", "34")
		case 2:
			args = append(args, "-vf", "scale=320:240", "-b:v", "192k", "-crf", "38")
		case 3:
			args = append(args, "-vf", "scale=240:180", "-b:v", "128k", "-crf", "40")
		default:
			args = append(args, "-vf", "scale=640:480", "-b:v", "512k", "-crf", "30")
		}
		args = append(args, "-f", "mp4", "pipe:1")
		return args, nil
	case "video/3gpp":
		args := []string{"-v", "error", "-i", "pipe:0"}
		switch profile {
		case 1:
			args = append(args, "-vf", "scale=320:240", "-b:v", "192k")
		case 2:
			args = append(args, "-vf", "scale=240:180", "-b:v", "128k")
		case 3:
			args = append(args, "-vf", "scale=176:144", "-b:v", "96k")
		default:
			args = append(args, "-vf", "scale=352:288", "-b:v", "256k")
		}
		args = append(args, "-f", "3gp", "pipe:1")
		return args, nil
	default:
		return nil, fmt.Errorf("adapt: unsupported video target %q", targetType)
	}
}

func normalizeVideoType(contentType string) string {
	contentType = strings.ToLower(strings.TrimSpace(contentType))
	switch contentType {
	case "video/mp4":
		return "video/mp4"
	case "video/3gpp", "video/3gp":
		return "video/3gpp"
	default:
		return contentType
	}
}

func preferredVideoType(allowed []string) string {
	for _, item := range allowed {
		switch normalizeVideoType(item) {
		case "video/3gpp", "video/mp4":
			return normalizeVideoType(item)
		}
	}
	return ""
}
