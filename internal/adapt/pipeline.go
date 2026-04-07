package adapt

import (
	"context"

	"github.com/vectorcore/vectorcore-mmsc/internal/config"
	"github.com/vectorcore/vectorcore-mmsc/internal/db"
	"github.com/vectorcore/vectorcore-mmsc/internal/message"
)

type Pipeline struct {
	enabled bool
	adapter Adapter
	repo    db.Repository
}

func NewPipeline(cfg config.AdaptConfig, repo db.Repository) *Pipeline {
	return &Pipeline{
		enabled: cfg.Enabled,
		adapter: multiAdapter{
			adapters: []Adapter{
				ImageAdapter{},
				NewAudioAdapter(cfg.FFmpegPath),
				NewVideoAdapter(cfg.FFmpegPath),
			},
		},
		repo: repo,
	}
}

func (p *Pipeline) SetAdapter(adapter Adapter) {
	if p == nil || adapter == nil {
		return
	}
	p.adapter = adapter
}

type multiAdapter struct {
	adapters []Adapter
}

func (m multiAdapter) Adapt(ctx context.Context, parts []message.Part, constraints Constraints) ([]message.Part, error) {
	out := cloneParts(parts)
	var err error
	for _, adapter := range m.adapters {
		out, err = adapter.Adapt(ctx, out, constraints)
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}

func (p *Pipeline) Adapt(ctx context.Context, parts []message.Part) ([]message.Part, error) {
	return p.AdaptForClass(ctx, parts, "default", 0)
}

func (p *Pipeline) AdaptForClass(ctx context.Context, parts []message.Part, className string, maxMessageSize int64) ([]message.Part, error) {
	cloned := cloneParts(parts)
	if !p.enabled {
		return cloned, nil
	}
	constraints := defaultConstraints()
	if p.repo != nil {
		name := className
		if name == "" {
			name = "default"
		}
		class, err := p.repo.GetAdaptationClass(ctx, name)
		if err == nil && class != nil {
			constraints = constraintsFromClass(*class)
		}
	}
	if maxMessageSize > 0 {
		constraints.MaxMessageSizeBytes = maxMessageSize
	}
	return p.adapter.Adapt(ctx, cloned, constraints)
}

func defaultConstraints() Constraints {
	return Constraints{
		MaxMessageSizeBytes: 307200,
		MaxImageWidth:       640,
		MaxImageHeight:      480,
		AllowedImageTypes:   []string{"image/jpeg", "image/gif", "image/png"},
		AllowedAudioTypes:   []string{"audio/amr", "audio/mpeg", "audio/mp4"},
		AllowedVideoTypes:   []string{"video/3gpp", "video/mp4"},
	}
}

func constraintsFromClass(class db.AdaptationClass) Constraints {
	return Constraints{
		MaxMessageSizeBytes: class.MaxMsgSizeBytes,
		MaxImageWidth:       class.MaxImageWidth,
		MaxImageHeight:      class.MaxImageHeight,
		AllowedImageTypes:   append([]string(nil), class.AllowedImageTypes...),
		AllowedAudioTypes:   append([]string(nil), class.AllowedAudioTypes...),
		AllowedVideoTypes:   append([]string(nil), class.AllowedVideoTypes...),
	}
}

func (p *Pipeline) AdaptForSubscriber(ctx context.Context, parts []message.Part, subscriber *db.Subscriber) ([]message.Part, error) {
	if subscriber == nil {
		return cloneParts(parts), nil
	}
	className := subscriber.AdaptationClass
	if className == "" {
		className = "default"
	}
	return p.AdaptForClass(ctx, parts, className, subscriber.MaxMessageSize)
}

func cloneParts(parts []message.Part) []message.Part {
	out := make([]message.Part, 0, len(parts))
	for _, part := range parts {
		cloned := part
		cloned.Data = append([]byte(nil), part.Data...)
		out = append(out, cloned)
	}
	return out
}
