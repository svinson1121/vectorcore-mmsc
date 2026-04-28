package db

import (
	"context"
	"database/sql"
	"os"
	"testing"

	"github.com/vectorcore/vectorcore-mmsc/migrations"
)

func TestRunMigrationsSQLite(t *testing.T) {
	t.Parallel()

	path := t.TempDir() + "/mmsc.db"
	repo, err := Open(context.Background(), testSQLiteConfig(path))
	if err != nil {
		t.Fatalf("open sqlite repo: %v", err)
	}
	defer repo.Close()

	if err := RunMigrations(context.Background(), repo, os.DirFS("../..")); err != nil {
		t.Fatalf("run migrations first pass: %v", err)
	}
	if err := RunMigrations(context.Background(), repo, os.DirFS("../..")); err != nil {
		t.Fatalf("run migrations second pass: %v", err)
	}

	assertTableExists(t, repo.DB(), "messages")
	assertTableExists(t, repo.DB(), "subscribers")
	assertTableExists(t, repo.DB(), "mm4_peers")
	assertTableExists(t, repo.DB(), "mm3_relay")
	assertTableExists(t, repo.DB(), "mm7_vasps")
	assertTableExists(t, repo.DB(), "smpp_upstream")
	assertTableExists(t, repo.DB(), "smpp_submissions")
	assertTableExists(t, repo.DB(), "adaptation_classes")
	assertTableExists(t, repo.DB(), "message_events")
	assertTableExists(t, repo.DB(), "mm4_routes")
	assertTableExists(t, repo.DB(), "routing_rules")

	var migrationCount int
	if err := repo.DB().QueryRow(`select count(*) from schema_migrations`).Scan(&migrationCount); err != nil {
		t.Fatalf("count migrations: %v", err)
	}
	if migrationCount != 12 {
		t.Fatalf("unexpected migration count: got %d want 12", migrationCount)
	}
}

func TestRunMigrationsSQLiteEmbeddedFS(t *testing.T) {
	t.Parallel()

	path := t.TempDir() + "/mmsc.db"
	repo, err := Open(context.Background(), testSQLiteConfig(path))
	if err != nil {
		t.Fatalf("open sqlite repo: %v", err)
	}
	defer repo.Close()

	if err := RunMigrations(context.Background(), repo, migrations.FS); err != nil {
		t.Fatalf("run migrations from embedded fs: %v", err)
	}

	assertTableExists(t, repo.DB(), "messages")
	assertTableExists(t, repo.DB(), "message_events")
}

func assertTableExists(t *testing.T, db *sql.DB, name string) {
	t.Helper()

	var found string
	if err := db.QueryRow(`select name from sqlite_master where type = 'table' and name = ?`, name).Scan(&found); err != nil {
		t.Fatalf("lookup table %s: %v", name, err)
	}
}
