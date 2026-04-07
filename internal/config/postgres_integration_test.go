package config

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	_ "github.com/jackc/pgx/v5/stdlib"
	"go.uber.org/zap"

	"github.com/vectorcore/vectorcore-mmsc/internal/db"
)

func TestWatcherListenReloadsPostgresRuntimeConfig(t *testing.T) {
	repo, dsn := newPostgresRuntimeRepoForTest(t)
	store := NewRuntimeStore()
	watcher := NewWatcher(DatabaseConfig{
		Driver: "postgres",
		DSN:    dsn,
	}, repo, store, zap.NewNop())

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() {
		done <- watcher.Run(ctx)
	}()

	waitForSnapshot(t, store, func(snapshot RuntimeSnapshot) bool {
		return len(snapshot.Adaptation) > 0
	})

	if err := repo.UpsertMM4Peer(context.Background(), db.MM4Peer{
		Domain:     "peer.example.net",
		SMTPHost:   "smtp.peer.example.net",
		SMTPPort:   25,
		TLSEnabled: true,
		Active:     true,
	}); err != nil {
		t.Fatalf("upsert mm4 peer: %v", err)
	}

	waitForSnapshot(t, store, func(snapshot RuntimeSnapshot) bool {
		return len(snapshot.Peers) == 1 && snapshot.Peers[0].Domain == "peer.example.net"
	})

	cancel()
	select {
	case err := <-done:
		if err != nil && err != context.Canceled {
			t.Fatalf("watcher exited with error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for watcher shutdown")
	}
}

func newPostgresRuntimeRepoForTest(t *testing.T) (db.Repository, string) {
	t.Helper()

	rawDSN := os.Getenv("VECTORCORE_MMSC_TEST_POSTGRES_DSN")
	if rawDSN == "" {
		t.Skip("VECTORCORE_MMSC_TEST_POSTGRES_DSN is not set")
	}

	ctx := context.Background()
	repo, err := db.Open(ctx, db.OpenOptions{
		Driver:       "postgres",
		DSN:          rawDSN,
		MaxOpenConns: 1,
		MaxIdleConns: 1,
	})
	if err != nil {
		t.Fatalf("open postgres repo: %v", err)
	}

	schema := fmt.Sprintf("vc_mmsc_cfg_test_%d", time.Now().UnixNano())
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

	cfg, err := pgx.ParseConfig(rawDSN)
	if err != nil {
		_ = repo.Close()
		t.Fatalf("parse postgres dsn: %v", err)
	}
	cfg.RuntimeParams["search_path"] = schema
	watcherDSN := cfg.ConnString()
	t.Cleanup(func() {
		_ = repo.Close()
		admin, err := sql.Open("pgx", rawDSN)
		if err != nil {
			t.Fatalf("open postgres admin: %v", err)
		}
		defer admin.Close()
		if _, err := admin.ExecContext(context.Background(), `drop schema if exists "`+schema+`" cascade`); err != nil {
			t.Fatalf("drop schema: %v", err)
		}
	})

	return repo, watcherDSN
}
