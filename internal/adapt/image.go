package adapt

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/gif"
	"image/jpeg"
	"image/png"
	"math"
	"strings"

	"github.com/vectorcore/vectorcore-mmsc/internal/message"
)

type ImageAdapter struct{}

func (ImageAdapter) Adapt(_ context.Context, parts []message.Part, constraints Constraints) ([]message.Part, error) {
	out := cloneParts(parts)
	for idx, part := range out {
		if !strings.HasPrefix(strings.ToLower(part.ContentType), "image/") {
			continue
		}
		adapted, changed, err := adaptImagePart(part, constraints)
		if err != nil {
			return nil, err
		}
		if changed {
			out[idx] = adapted
		}
	}
	if err := shrinkImagesToFit(&out, constraints); err != nil {
		return nil, err
	}
	if err := validateParts(out, constraints); err != nil {
		return nil, err
	}
	return out, nil
}

func adaptImagePart(part message.Part, constraints Constraints) (message.Part, bool, error) {
	img, format, err := image.Decode(bytes.NewReader(part.Data))
	if err != nil {
		return part, false, nil
	}
	targetType := normalizeImageType(part.ContentType)
	if !isAllowedType(targetType, constraints.AllowedImageTypes, "image") {
		targetType = preferredImageType(constraints.AllowedImageTypes)
	}
	if targetType == "" {
		return part, false, fmt.Errorf("adapt: unsupported image type %q", part.ContentType)
	}

	bounds := img.Bounds()
	targetW, targetH := boundedDimensions(bounds.Dx(), bounds.Dy(), constraints.MaxImageWidth, constraints.MaxImageHeight)
	changed := targetW != bounds.Dx() || targetH != bounds.Dy() || targetType != normalizeImageType(part.ContentType)
	if !changed {
		part.Size = int64(len(part.Data))
		return part, false, nil
	}

	resized := img
	if targetW != bounds.Dx() || targetH != bounds.Dy() {
		resized = resizeNearest(img, targetW, targetH)
	}
	encoded, actualType, err := encodeImage(resized, targetType, format)
	if err != nil {
		return part, false, err
	}
	part.ContentType = actualType
	part.Data = encoded
	part.Size = int64(len(encoded))
	return part, true, nil
}

func shrinkImagesToFit(parts *[]message.Part, constraints Constraints) error {
	if constraints.MaxMessageSizeBytes <= 0 || totalSize(*parts) <= constraints.MaxMessageSizeBytes {
		return nil
	}
	for idx, part := range *parts {
		if !strings.HasPrefix(strings.ToLower(part.ContentType), "image/") {
			continue
		}
		img, _, err := image.Decode(bytes.NewReader(part.Data))
		if err != nil {
			continue
		}
		for step := 0; step < 6 && totalSize(*parts) > constraints.MaxMessageSizeBytes; step++ {
			bounds := img.Bounds()
			nextW := maxInt(1, int(math.Round(float64(bounds.Dx())*0.8)))
			nextH := maxInt(1, int(math.Round(float64(bounds.Dy())*0.8)))
			if nextW == bounds.Dx() && nextH == bounds.Dy() {
				break
			}
			img = resizeNearest(img, nextW, nextH)
			encoded, actualType, err := encodeImage(img, normalizeImageType(part.ContentType), "")
			if err != nil {
				return err
			}
			part.Data = encoded
			part.Size = int64(len(encoded))
			part.ContentType = actualType
			(*parts)[idx] = part
		}
	}
	return nil
}

func encodeImage(img image.Image, targetType, sourceFormat string) ([]byte, string, error) {
	if targetType == "" {
		targetType = normalizeImageType(sourceFormat)
	}
	if targetType == "" {
		targetType = "image/jpeg"
	}
	var buf bytes.Buffer
	switch targetType {
	case "image/png":
		if err := png.Encode(&buf, img); err != nil {
			return nil, "", err
		}
	case "image/gif":
		if err := gif.Encode(&buf, img, nil); err != nil {
			return nil, "", err
		}
	default:
		targetType = "image/jpeg"
		if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 75}); err != nil {
			return nil, "", err
		}
	}
	return buf.Bytes(), targetType, nil
}

func resizeNearest(src image.Image, width, height int) image.Image {
	dst := image.NewRGBA(image.Rect(0, 0, width, height))
	srcBounds := src.Bounds()
	for y := 0; y < height; y++ {
		srcY := srcBounds.Min.Y + y*srcBounds.Dy()/height
		for x := 0; x < width; x++ {
			srcX := srcBounds.Min.X + x*srcBounds.Dx()/width
			dst.Set(x, y, src.At(srcX, srcY))
		}
	}
	return dst
}

func boundedDimensions(width, height, maxWidth, maxHeight int) (int, int) {
	if width <= 0 || height <= 0 {
		return width, height
	}
	scale := 1.0
	if maxWidth > 0 && width > maxWidth {
		scale = math.Min(scale, float64(maxWidth)/float64(width))
	}
	if maxHeight > 0 && height > maxHeight {
		scale = math.Min(scale, float64(maxHeight)/float64(height))
	}
	if scale >= 1.0 {
		return width, height
	}
	return maxInt(1, int(math.Round(float64(width)*scale))), maxInt(1, int(math.Round(float64(height)*scale)))
}

func normalizeImageType(contentType string) string {
	contentType = strings.ToLower(strings.TrimSpace(contentType))
	switch contentType {
	case "png", "image/png":
		return "image/png"
	case "gif", "image/gif":
		return "image/gif"
	case "jpeg", "jpg", "image/jpg", "image/jpeg":
		return "image/jpeg"
	default:
		return contentType
	}
}

func preferredImageType(allowed []string) string {
	for _, item := range allowed {
		normalized := normalizeImageType(item)
		switch normalized {
		case "image/jpeg", "image/png", "image/gif":
			return normalized
		}
	}
	return ""
}

func isAllowedType(contentType string, allowed []string, family string) bool {
	if len(allowed) == 0 {
		return true
	}
	normalizedContentType := normalizeAllowedType(contentType, family)
	for _, item := range allowed {
		if normalizeAllowedType(item, family) == normalizedContentType {
			return true
		}
	}
	return false
}

func totalSize(parts []message.Part) int64 {
	var total int64
	for _, part := range parts {
		if part.Size > 0 {
			total += part.Size
			continue
		}
		total += int64(len(part.Data))
	}
	return total
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
