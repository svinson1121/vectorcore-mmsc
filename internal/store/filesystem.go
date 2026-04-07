package store

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/vectorcore/vectorcore-mmsc/internal/config"
)

type filesystemStore struct {
	root string
}

func NewFilesystem(cfg config.FilesystemConfig) (Store, error) {
	if err := os.MkdirAll(cfg.Root, 0o755); err != nil {
		return nil, fmt.Errorf("create filesystem root: %w", err)
	}
	return &filesystemStore{root: cfg.Root}, nil
}

func (s *filesystemStore) Put(ctx context.Context, id string, r io.Reader, _ int64, contentType string) (string, error) {
	ext := extensionFromContentType(contentType)
	key := objectKeyWithExt(id, ext)
	path := s.fullPath(key)
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("create content dir: %w", err)
	}

	file, err := os.Create(path)
	if err != nil {
		return "", fmt.Errorf("create content file: %w", err)
	}
	defer file.Close()

	if _, err := io.Copy(file, newContextReader(ctx, r)); err != nil {
		return "", fmt.Errorf("write content file: %w", err)
	}
	return key, nil
}

func (s *filesystemStore) Get(_ context.Context, key string) (io.ReadCloser, int64, error) {
	file, err := os.Open(s.fullPath(key))
	if err != nil {
		return nil, 0, err
	}
	info, err := file.Stat()
	if err != nil {
		file.Close()
		return nil, 0, err
	}
	return file, info.Size(), nil
}

func (s *filesystemStore) Delete(_ context.Context, key string) error {
	fullPath := s.fullPath(key)
	if err := os.Remove(fullPath); err != nil {
		return err
	}
	return s.pruneEmptyParents(filepath.Dir(fullPath))
}

func (s *filesystemStore) Exists(_ context.Context, key string) (bool, error) {
	_, err := os.Stat(s.fullPath(key))
	if err == nil {
		return true, nil
	}
	if os.IsNotExist(err) {
		return false, nil
	}
	return false, err
}

func (s *filesystemStore) Close() error {
	return nil
}

func (s *filesystemStore) fullPath(key string) string {
	return filepath.Join(s.root, filepath.FromSlash(key))
}

func (s *filesystemStore) pruneEmptyParents(dir string) error {
	root := filepath.Clean(s.root)
	current := filepath.Clean(dir)
	for current != root && current != "." && current != string(filepath.Separator) {
		err := os.Remove(current)
		if err == nil {
			current = filepath.Dir(current)
			continue
		}
		if errors.Is(err, os.ErrNotExist) {
			current = filepath.Dir(current)
			continue
		}
		return nil
	}
	return nil
}

func extensionFromContentType(contentType string) string {
	switch strings.ToLower(contentType) {
	case "application/vnd.wap.mms-message":
		return ".mms"
	case "image/jpeg":
		return ".jpg"
	case "image/png":
		return ".png"
	case "image/gif":
		return ".gif"
	case "text/plain":
		return ".txt"
	default:
		return ".bin"
	}
}
