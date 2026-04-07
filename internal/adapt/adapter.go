package adapt

import (
	"context"

	"github.com/vectorcore/vectorcore-mmsc/internal/message"
)

type Adapter interface {
	Adapt(context.Context, []message.Part, Constraints) ([]message.Part, error)
}

type Constraints struct {
	MaxMessageSizeBytes int64
	MaxImageWidth       int
	MaxImageHeight      int
	AllowedImageTypes   []string
	AllowedAudioTypes   []string
	AllowedVideoTypes   []string
}
