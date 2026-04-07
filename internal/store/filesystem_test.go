package store

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/vectorcore/vectorcore-mmsc/internal/config"
)

func TestFilesystemStoreRoundTrip(t *testing.T) {
	t.Parallel()

	s, err := NewFilesystem(config.FilesystemConfig{Root: t.TempDir()})
	if err != nil {
		t.Fatalf("new filesystem store: %v", err)
	}

	key, err := s.Put(context.Background(), "msg-1", bytes.NewReader([]byte("payload")), 7, "application/vnd.wap.mms-message")
	if err != nil {
		t.Fatalf("put: %v", err)
	}

	exists, err := s.Exists(context.Background(), key)
	if err != nil {
		t.Fatalf("exists: %v", err)
	}
	if !exists {
		t.Fatal("expected stored object to exist")
	}

	rc, size, err := s.Get(context.Background(), key)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer rc.Close()

	body, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if string(body) != "payload" {
		t.Fatalf("unexpected body: %q", string(body))
	}
	if size != int64(len(body)) {
		t.Fatalf("unexpected size: got %d want %d", size, len(body))
	}

	if err := s.Delete(context.Background(), key); err != nil {
		t.Fatalf("delete: %v", err)
	}
	exists, err = s.Exists(context.Background(), key)
	if err != nil {
		t.Fatalf("exists after delete: %v", err)
	}
	if exists {
		t.Fatal("expected object to be deleted")
	}

	messageDir := filepath.Dir(s.(*filesystemStore).fullPath(key))
	if _, err := os.Stat(messageDir); !os.IsNotExist(err) {
		t.Fatalf("expected message directory to be pruned, stat err=%v", err)
	}
}
