package routing

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/vectorcore/vectorcore-mmsc/internal/db"
)

func TestNewMessageID(t *testing.T) {
	t.Parallel()

	id := NewMessageID()
	if _, err := uuid.Parse(id); err != nil {
		t.Fatalf("message id is not a valid UUID: %q", err)
	}
	if strings.Contains(id, "@") {
		t.Fatalf("message id must not contain '@': %q", id)
	}
}

func TestEngineResolve(t *testing.T) {
	t.Parallel()

	repo := newRoutingRepo(t)
	if err := repo.UpsertMM4Peer(context.Background(), db.MM4Peer{
		Domain:     "peer.example.net",
		SMTPHost:   "smtp.peer.example.net",
		SMTPPort:   25,
		TLSEnabled: true,
		Active:     true,
	}); err != nil {
		t.Fatalf("upsert peer: %v", err)
	}

	engine := NewEngine(repo)

	local, err := engine.Resolve(context.Background(), "+12025550100/TYPE=PLMN")
	if err != nil {
		t.Fatalf("resolve local: %v", err)
	}
	if local.Destination != DestinationLocal || local.Peer != nil {
		t.Fatalf("unexpected local result: %#v", local)
	}

	peer, err := engine.Resolve(context.Background(), "user@peer.example.net")
	if err != nil {
		t.Fatalf("resolve peer: %v", err)
	}
	if peer.Destination != DestinationMM4 || peer.Peer == nil {
		t.Fatalf("unexpected peer result: %#v", peer)
	}

	email, err := engine.Resolve(context.Background(), "user@example.org")
	if err != nil {
		t.Fatalf("resolve email: %v", err)
	}
	if email.Destination != DestinationMM3 || email.Peer != nil {
		t.Fatalf("unexpected email result: %#v", email)
	}

	sameLocal, err := engine.ResolveRecipients(context.Background(), []string{"+12025550100", "+12025550101"})
	if err != nil {
		t.Fatalf("resolve local recipients: %v", err)
	}
	if sameLocal.Destination != DestinationLocal {
		t.Fatalf("unexpected local recipients result: %#v", sameLocal)
	}

	samePeer, err := engine.ResolveRecipients(context.Background(), []string{"user1@peer.example.net", "user2@peer.example.net"})
	if err != nil {
		t.Fatalf("resolve peer recipients: %v", err)
	}
	if samePeer.Destination != DestinationMM4 || samePeer.Peer == nil {
		t.Fatalf("unexpected peer recipients result: %#v", samePeer)
	}

	if _, err := engine.ResolveRecipients(context.Background(), []string{"+12025550100", "user@example.org"}); err == nil {
		t.Fatal("expected mixed recipient destination error")
	}
}

func newRoutingRepo(t *testing.T) db.Repository {
	t.Helper()

	path := t.TempDir() + "/routing.db"
	repo, err := db.Open(context.Background(), db.OpenOptions{
		Driver: "sqlite",
		DSN:    path,
	})
	if err != nil {
		t.Fatalf("open repo: %v", err)
	}
	t.Cleanup(func() { _ = repo.Close() })
	if err := db.RunMigrations(context.Background(), repo, os.DirFS("../..")); err != nil {
		t.Fatalf("run migrations: %v", err)
	}
	return repo
}
