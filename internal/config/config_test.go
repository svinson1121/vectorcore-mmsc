package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLoadConfig(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "vectorcore-mmsc.yaml")
	body := []byte(`
database:
  driver: sqlite
  dsn: ":memory:"
api:
  listen: ":18080"
store:
  backend: filesystem
  filesystem:
    root: "/tmp/vectorcore-mmsc-test"
  tiered:
    offload_after: "5m"
    local_cache: true
`)
	if err := os.WriteFile(path, body, 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}

	if cfg.Database.Driver != "sqlite" {
		t.Fatalf("unexpected database driver: %q", cfg.Database.Driver)
	}
	if cfg.Database.DSN != ":memory:" {
		t.Fatalf("unexpected database dsn: %q", cfg.Database.DSN)
	}
	if cfg.API.Listen != ":18080" {
		t.Fatalf("unexpected api.listen: %q", cfg.API.Listen)
	}
	if cfg.Store.Tiered.OffloadAfter != 5*time.Minute {
		t.Fatalf("unexpected offload_after: %s", cfg.Store.Tiered.OffloadAfter)
	}
	if cfg.MM1.Listen == "" {
		t.Fatal("expected default MM1 listen to be preserved")
	}
	if cfg.Database.RuntimeReloadInterval != 5*time.Second {
		t.Fatalf("unexpected runtime_reload_interval: %s", cfg.Database.RuntimeReloadInterval)
	}
	if cfg.MM7.Version != "5.3.0" {
		t.Fatalf("unexpected default mm7 version: %q", cfg.MM7.Version)
	}
}

func TestValidateRequiresFilesystemRoot(t *testing.T) {
	t.Parallel()

	cfg := Default()
	cfg.Database.DSN = ":memory:"
	cfg.Store.Backend = "filesystem"
	cfg.Store.Filesystem.Root = ""

	if err := cfg.Validate(); err == nil {
		t.Fatal("expected validation error for missing filesystem root")
	}
}

func TestLoadResolvesRelativePathsFromConfigDir(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "vectorcore-mmsc.yaml")
	body := []byte(`
database:
  driver: sqlite
  dsn: "./runtime/mmsc.db"
api:
  listen: ":18080"
store:
  backend: filesystem
  filesystem:
    root: "./data/store"
log:
  file: "./log/mmsc.log"
`)
	if err := os.WriteFile(path, body, 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}

	if want := filepath.Join(dir, "runtime", "mmsc.db"); cfg.Database.DSN != want {
		t.Fatalf("unexpected database dsn: got %q want %q", cfg.Database.DSN, want)
	}
	if want := filepath.Join(dir, "data", "store"); cfg.Store.Filesystem.Root != want {
		t.Fatalf("unexpected filesystem root: got %q want %q", cfg.Store.Filesystem.Root, want)
	}
	if want := filepath.Join(dir, "log", "mmsc.log"); cfg.Log.File != want {
		t.Fatalf("unexpected log file: got %q want %q", cfg.Log.File, want)
	}
}

func TestLoadResolvesSQLiteFileDSNFromConfigDir(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "vectorcore-mmsc.yaml")
	body := []byte(`
database:
  driver: sqlite
  dsn: "file:runtime/mmsc.db?cache=shared&_busy_timeout=5000"
api:
  listen: ":18080"
store:
  backend: filesystem
  filesystem:
    root: "./data/store"
log:
  file: "./log/mmsc.log"
`)
	if err := os.WriteFile(path, body, 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}

	want := "file:" + filepath.Join(dir, "runtime", "mmsc.db") + "?cache=shared&_busy_timeout=5000"
	if cfg.Database.DSN != want {
		t.Fatalf("unexpected database dsn: got %q want %q", cfg.Database.DSN, want)
	}
}
