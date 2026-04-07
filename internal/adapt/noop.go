package adapt

import (
	"context"
	"fmt"
	"strings"

	"github.com/vectorcore/vectorcore-mmsc/internal/message"
)

type Noop struct{}

func (Noop) Adapt(_ context.Context, parts []message.Part, constraints Constraints) ([]message.Part, error) {
	cloned := cloneParts(parts)
	if err := validateParts(cloned, constraints); err != nil {
		return nil, err
	}
	return cloned, nil
}

func validateParts(parts []message.Part, constraints Constraints) error {
	var total int64
	for _, part := range parts {
		size := part.Size
		if size == 0 {
			size = int64(len(part.Data))
		}
		total += size
		if err := validateContentType(part.ContentType, constraints); err != nil {
			return err
		}
	}
	if constraints.MaxMessageSizeBytes > 0 && total > constraints.MaxMessageSizeBytes {
		return fmt.Errorf("adapt: message size %d exceeds limit %d", total, constraints.MaxMessageSizeBytes)
	}
	return nil
}

func validateContentType(contentType string, constraints Constraints) error {
	contentType = strings.ToLower(strings.TrimSpace(contentType))
	switch {
	case strings.HasPrefix(contentType, "image/"):
		return validateAllowed(contentType, constraints.AllowedImageTypes, "image")
	case strings.HasPrefix(contentType, "audio/"):
		return validateAllowed(contentType, constraints.AllowedAudioTypes, "audio")
	case strings.HasPrefix(contentType, "video/"):
		return validateAllowed(contentType, constraints.AllowedVideoTypes, "video")
	default:
		return nil
	}
}

func validateAllowed(contentType string, allowed []string, family string) error {
	if len(allowed) == 0 {
		return nil
	}
	normalizedContentType := normalizeAllowedType(contentType, family)
	for _, item := range allowed {
		if strings.EqualFold(normalizeAllowedType(item, family), normalizedContentType) {
			return nil
		}
	}
	return fmt.Errorf("adapt: unsupported %s type %q", family, contentType)
}

func normalizeAllowedType(contentType string, family string) string {
	switch family {
	case "image":
		return normalizeImageType(contentType)
	case "audio":
		return normalizeAudioType(contentType)
	case "video":
		return normalizeVideoType(contentType)
	default:
		return strings.ToLower(strings.TrimSpace(contentType))
	}
}
