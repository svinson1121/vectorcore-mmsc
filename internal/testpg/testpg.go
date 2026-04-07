package testpg

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/vectorcore/vectorcore-mmsc/internal/db"
)

const envDSN = "VECTORCORE_MMSC_TEST_POSTGRES_DSN"

func OpenRepository(t *testing.T) db.Repository {
	t.Helper()

	dsn := os.Getenv(envDSN)
	if dsn == "" {
		t.Skip(envDSN + " is not set")
	}

	ctx := context.Background()
	repo, err := db.Open(ctx, db.OpenOptions{
		Driver:       "postgres",
		DSN:          dsn,
		MaxOpenConns: 1,
		MaxIdleConns: 1,
	})
	if err != nil {
		t.Fatalf("open postgres repo: %v", err)
	}

	schema := fmt.Sprintf("vc_mmsc_proto_test_%d", time.Now().UnixNano())
	if _, err := repo.DB().ExecContext(ctx, `create schema "`+schema+`"`); err != nil {
		_ = repo.Close()
		t.Fatalf("create schema: %v", err)
	}
	if _, err := repo.DB().ExecContext(ctx, `set search_path to "`+schema+`"`); err != nil {
		_ = repo.Close()
		t.Fatalf("set search_path: %v", err)
	}
	if err := db.RunMigrations(ctx, repo, os.DirFS("../..")); err != nil {
		_ = repo.Close()
		t.Fatalf("run migrations: %v", err)
	}

	t.Cleanup(func() {
		_ = repo.Close()
		admin, err := sql.Open("pgx", dsn)
		if err != nil {
			t.Fatalf("open postgres admin: %v", err)
		}
		defer admin.Close()
		if _, err := admin.ExecContext(context.Background(), `drop schema if exists "`+schema+`" cascade`); err != nil {
			t.Fatalf("drop schema: %v", err)
		}
	})

	return repo
}
