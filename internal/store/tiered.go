package store

import (
	"bytes"
	"context"
	"io"
	"time"

	"github.com/vectorcore/vectorcore-mmsc/internal/config"
)

type tieredStore struct {
	local        Store
	remote       Store
	offloadAfter time.Duration
	localCache   bool
}

func NewTiered(ctx context.Context, cfg config.StoreConfig) (Store, error) {
	local, err := NewFilesystem(cfg.Filesystem)
	if err != nil {
		return nil, err
	}
	remote, err := NewS3(ctx, cfg.S3)
	if err != nil {
		local.Close()
		return nil, err
	}
	return &tieredStore{
		local:        local,
		remote:       remote,
		offloadAfter: cfg.Tiered.OffloadAfter,
		localCache:   cfg.Tiered.LocalCache,
	}, nil
}

func (s *tieredStore) Put(ctx context.Context, id string, r io.Reader, size int64, contentType string) (string, error) {
	body, err := io.ReadAll(newContextReader(ctx, r))
	if err != nil {
		return "", err
	}

	key, err := s.local.Put(ctx, id, bytes.NewReader(body), size, contentType)
	if err != nil {
		return "", err
	}

	go s.asyncOffload(id, body, contentType)
	return key, nil
}

func (s *tieredStore) Get(ctx context.Context, key string) (io.ReadCloser, int64, error) {
	if exists, err := s.local.Exists(ctx, key); err == nil && exists {
		return s.local.Get(ctx, key)
	}

	body, size, err := s.remote.Get(ctx, key)
	if err != nil {
		return nil, 0, err
	}
	if !s.localCache {
		return body, size, nil
	}

	payload, err := io.ReadAll(body)
	body.Close()
	if err != nil {
		return nil, 0, err
	}
	if err := writeLocalCachedObject(ctx, s.local, key, payload); err != nil {
		return nil, 0, err
	}
	return io.NopCloser(bytes.NewReader(payload)), int64(len(payload)), nil
}

func (s *tieredStore) Delete(ctx context.Context, key string) error {
	_ = s.local.Delete(ctx, key)
	return s.remote.Delete(ctx, key)
}

func (s *tieredStore) Exists(ctx context.Context, key string) (bool, error) {
	if exists, err := s.local.Exists(ctx, key); err == nil && exists {
		return true, nil
	}
	return s.remote.Exists(ctx, key)
}

func (s *tieredStore) Close() error {
	if err := s.local.Close(); err != nil {
		return err
	}
	return s.remote.Close()
}

func (s *tieredStore) asyncOffload(id string, body []byte, contentType string) {
	delay := s.offloadAfter
	if delay <= 0 {
		delay = time.Hour
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	<-timer.C
	_, _ = s.remote.Put(context.Background(), id, bytes.NewReader(body), int64(len(body)), contentType)
}
