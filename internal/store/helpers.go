package store

import (
	"context"
	"errors"
	"io"
	"os"
	"path"
	"strings"
	"time"
)

type contextReader struct {
	ctx context.Context
	r   io.Reader
}

func newContextReader(ctx context.Context, r io.Reader) io.Reader {
	return &contextReader{ctx: ctx, r: r}
}

func (r *contextReader) Read(p []byte) (int, error) {
	select {
	case <-r.ctx.Done():
		return 0, r.ctx.Err()
	default:
	}
	return r.r.Read(p)
}

func errorAs(err error, target any) bool {
	return errors.As(err, target)
}

func objectKey(id string) string {
	return objectKeyWithExt(id, ".mms")
}

func objectKeyWithExt(id, ext string) string {
	now := time.Now().UTC()
	return path.Join("mmsc", now.Format("2006"), now.Format("01"), now.Format("02"), id, "assembled"+normalizedExt(ext))
}

func normalizedExt(ext string) string {
	if ext == "" {
		return ".bin"
	}
	if strings.HasPrefix(ext, ".") {
		return ext
	}
	return "." + ext
}

func writeLocalCachedObject(ctx context.Context, local Store, key string, payload []byte) error {
	fsStore, ok := local.(*filesystemStore)
	if !ok {
		return nil
	}

	fullPath := fsStore.fullPath(key)
	if err := os.MkdirAll(path.Dir(fullPath), 0o755); err != nil {
		return err
	}
	return os.WriteFile(fullPath, payload, 0o644)
}
