package store

import (
	"bytes"
	"context"
	"io"
	"testing"

	"github.com/vectorcore/vectorcore-mmsc/internal/config"
)

type memoryStore struct {
	objects map[string][]byte
}

func newMemoryStore() *memoryStore {
	return &memoryStore{objects: make(map[string][]byte)}
}

func (s *memoryStore) Put(_ context.Context, id string, r io.Reader, _ int64, _ string) (string, error) {
	body, err := io.ReadAll(r)
	if err != nil {
		return "", err
	}
	key := objectKey(id)
	s.objects[key] = body
	return key, nil
}

func (s *memoryStore) Get(_ context.Context, key string) (io.ReadCloser, int64, error) {
	body := s.objects[key]
	return io.NopCloser(bytes.NewReader(body)), int64(len(body)), nil
}

func (s *memoryStore) Delete(_ context.Context, key string) error {
	delete(s.objects, key)
	return nil
}

func (s *memoryStore) Exists(_ context.Context, key string) (bool, error) {
	_, ok := s.objects[key]
	return ok, nil
}

func (s *memoryStore) Close() error {
	return nil
}

func TestTieredStoreCachesRemoteReads(t *testing.T) {
	t.Parallel()

	local, err := NewFilesystem(config.FilesystemConfig{Root: t.TempDir()})
	if err != nil {
		t.Fatalf("new local filesystem store: %v", err)
	}
	remote := newMemoryStore()
	key, err := remote.Put(context.Background(), "msg-remote", bytes.NewReader([]byte("remote-body")), 11, "application/vnd.wap.mms-message")
	if err != nil {
		t.Fatalf("seed remote object: %v", err)
	}

	s := &tieredStore{
		local:      local,
		remote:     remote,
		localCache: true,
	}

	rc, size, err := s.Get(context.Background(), key)
	if err != nil {
		t.Fatalf("tiered get: %v", err)
	}
	defer rc.Close()

	body, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("read tiered body: %v", err)
	}
	if string(body) != "remote-body" {
		t.Fatalf("unexpected body: %q", string(body))
	}
	if size != int64(len(body)) {
		t.Fatalf("unexpected size: got %d want %d", size, len(body))
	}

	exists, err := local.Exists(context.Background(), key)
	if err != nil {
		t.Fatalf("local exists after cache fill: %v", err)
	}
	if !exists {
		t.Fatal("expected remote read to populate local cache")
	}
}
