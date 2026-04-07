package config

import (
	"context"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/vectorcore/vectorcore-mmsc/internal/db"
)

func TestWatcherPollReloadsSQLiteRuntimeConfig(t *testing.T) {
	t.Parallel()

	repo := newRuntimeTestRepo(t)
	store := NewRuntimeStore()
	watcher := NewWatcher(DatabaseConfig{
		Driver:                "sqlite",
		DSN:                   "ignored",
		RuntimeReloadInterval: 10 * time.Millisecond,
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
	if err := repo.UpsertMM3Relay(context.Background(), db.MM3Relay{
		Enabled:             true,
		SMTPHost:            "smtp.example.net",
		SMTPPort:            25,
		DefaultSenderDomain: "mmsc.example.net",
	}); err != nil {
		t.Fatalf("upsert mm3 relay: %v", err)
	}

	waitForSnapshot(t, store, func(snapshot RuntimeSnapshot) bool {
		return len(snapshot.Peers) == 1 &&
			snapshot.Peers[0].Domain == "peer.example.net" &&
			snapshot.MM3Relay != nil &&
			snapshot.MM3Relay.SMTPHost == "smtp.example.net"
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

func waitForSnapshot(t *testing.T, store *RuntimeStore, predicate func(RuntimeSnapshot) bool) {
	t.Helper()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if predicate(store.Snapshot()) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for runtime snapshot: %#v", store.Snapshot())
}
