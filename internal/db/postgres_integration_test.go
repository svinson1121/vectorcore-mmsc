package db

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
)

func TestRunMigrationsPostgres(t *testing.T) {
	repo := newPostgresRepoForTest(t)

	assertPostgresTableExists(t, repo.DB(), "messages")
	assertPostgresTableExists(t, repo.DB(), "subscribers")
	assertPostgresTableExists(t, repo.DB(), "mm4_peers")
	assertPostgresTableExists(t, repo.DB(), "mm3_relay")
	assertPostgresTableExists(t, repo.DB(), "mm7_vasps")
	assertPostgresTableExists(t, repo.DB(), "smpp_upstream")
	assertPostgresTableExists(t, repo.DB(), "smpp_submissions")
	assertPostgresTableExists(t, repo.DB(), "adaptation_classes")
	assertPostgresTableExists(t, repo.DB(), "message_events")

	var migrationCount int
	if err := repo.DB().QueryRow(`select count(*) from schema_migrations`).Scan(&migrationCount); err != nil {
		t.Fatalf("count migrations: %v", err)
	}
	if migrationCount != 10 {
		t.Fatalf("unexpected migration count: got %d want 10", migrationCount)
	}
}

func TestRepositoryRuntimeConfigLifecyclePostgres(t *testing.T) {
	repo := newPostgresRepoForTest(t)

	ctx := context.Background()
	if err := repo.UpsertMM4Peer(ctx, MM4Peer{
		Domain:     "peer.example.net",
		SMTPHost:   "smtp.peer.example.net",
		SMTPPort:   2525,
		TLSEnabled: true,
		Active:     true,
	}); err != nil {
		t.Fatalf("upsert peer: %v", err)
	}
	if err := repo.UpsertMM7VASP(ctx, MM7VASP{
		VASPID:     "vasp-1",
		Protocol:   "eaif",
		Version:    "3.0",
		DeliverURL: "https://vasp.example.net/deliver",
		ReportURL:  "https://vasp.example.net/report",
		Active:     true,
	}); err != nil {
		t.Fatalf("upsert vasp: %v", err)
	}
	if err := repo.UpsertMM3Relay(ctx, MM3Relay{
		Enabled:             true,
		SMTPHost:            "smtp.example.net",
		SMTPPort:            2525,
		DefaultSenderDomain: "mmsc.example.net",
	}); err != nil {
		t.Fatalf("upsert mm3 relay: %v", err)
	}

	peers, err := repo.ListMM4Peers(ctx)
	if err != nil {
		t.Fatalf("list peers: %v", err)
	}
	if len(peers) != 1 || peers[0].Domain != "peer.example.net" {
		t.Fatalf("unexpected peers: %#v", peers)
	}
	vasps, err := repo.ListMM7VASPs(ctx)
	if err != nil {
		t.Fatalf("list vasps: %v", err)
	}
	if len(vasps) != 1 || vasps[0].Protocol != "eaif" || vasps[0].Version != "3.0" {
		t.Fatalf("unexpected vasps: %#v", vasps)
	}
	relay, err := repo.GetMM3Relay(ctx)
	if err != nil {
		t.Fatalf("get mm3 relay: %v", err)
	}
	if relay == nil || relay.SMTPHost != "smtp.example.net" {
		t.Fatalf("unexpected relay: %#v", relay)
	}
}

func newPostgresRepoForTest(t *testing.T) Repository {
	t.Helper()

	dsn := os.Getenv("VECTORCORE_MMSC_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("VECTORCORE_MMSC_TEST_POSTGRES_DSN is not set")
	}

	ctx := context.Background()
	repo, err := Open(ctx, OpenOptions{
		Driver:       "postgres",
		DSN:          dsn,
		MaxOpenConns: 1,
		MaxIdleConns: 1,
	})
	if err != nil {
		t.Fatalf("open postgres repo: %v", err)
	}

	schema := fmt.Sprintf("vc_mmsc_test_%d", time.Now().UnixNano())
	if _, err := repo.DB().ExecContext(ctx, `create schema "`+schema+`"`); err != nil {
		_ = repo.Close()
		t.Fatalf("create schema: %v", err)
	}
	if _, err := repo.DB().ExecContext(ctx, `set search_path to "`+schema+`"`); err != nil {
		_ = repo.Close()
		t.Fatalf("set search_path: %v", err)
	}
	if err := RunMigrations(ctx, repo, os.DirFS("../..")); err != nil {
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

func assertPostgresTableExists(t *testing.T, db *sql.DB, name string) {
	t.Helper()

	var found string
	if err := db.QueryRow(`select tablename from pg_tables where schemaname = current_schema() and tablename = $1`, name).Scan(&found); err != nil {
		t.Fatalf("lookup table %s: %v", name, err)
	}
}
