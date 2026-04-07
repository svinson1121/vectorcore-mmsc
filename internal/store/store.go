package store

import (
	"context"
	"fmt"
	"io"

	"github.com/vectorcore/vectorcore-mmsc/internal/config"
)

type Store interface {
	Put(ctx context.Context, id string, r io.Reader, size int64, contentType string) (string, error)
	Get(ctx context.Context, key string) (io.ReadCloser, int64, error)
	Delete(ctx context.Context, key string) error
	Exists(ctx context.Context, key string) (bool, error)
	Close() error
}

func New(ctx context.Context, cfg config.StoreConfig) (Store, error) {
	switch cfg.Backend {
	case "filesystem":
		return NewFilesystem(cfg.Filesystem)
	case "s3":
		return NewS3(ctx, cfg.S3)
	case "tiered":
		return NewTiered(ctx, cfg)
	default:
		return nil, fmt.Errorf("unsupported store backend %q", cfg.Backend)
	}
}
