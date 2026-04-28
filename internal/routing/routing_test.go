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
	for _, route := range []db.MM4Route{
		{Name: "Local prefix", MatchType: "msisdn_prefix", MatchValue: "+1202555", EgressType: "local", Priority: 100, Active: true},
		{Name: "Peer domain", MatchType: "recipient_domain", MatchValue: "peer.example.net", EgressType: "mm4", EgressTarget: "peer.example.net", Priority: 100, Active: true},
		{Name: "Email domain", MatchType: "recipient_domain", MatchValue: "example.org", EgressType: "mm3", Priority: 100, Active: true},
	} {
		if err := repo.UpsertMM4Route(context.Background(), route); err != nil {
			t.Fatalf("upsert route %s: %v", route.Name, err)
		}
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

	unmatched, err := engine.ResolveRecipients(context.Background(), []string{"+19995550100"})
	if err != nil {
		t.Fatalf("resolve unmatched recipient: %v", err)
	}
	if unmatched.Destination != DestinationLocal {
		t.Fatalf("expected unmatched recipient to fall back local, got %#v", unmatched)
	}
}

func TestEngineResolveMSISDNRoute(t *testing.T) {
	t.Parallel()

	repo := newRoutingRepo(t)
	if err := repo.UpsertMM4Peer(context.Background(), db.MM4Peer{
		Name:       "Carrier A",
		Domain:     "carrier-a.example.net",
		SMTPHost:   "smtp.carrier-a.example.net",
		SMTPPort:   25,
		TLSEnabled: true,
		Active:     true,
	}); err != nil {
		t.Fatalf("upsert peer: %v", err)
	}
	if err := repo.UpsertMM4Route(context.Background(), db.MM4Route{
		Name:         "Carrier A prefix",
		MatchType:    "msisdn_prefix",
		MatchValue:   "+1202555",
		EgressType:   "mm4",
		EgressTarget: "carrier-a.example.net",
		Priority:     100,
		Active:       true,
	}); err != nil {
		t.Fatalf("upsert route: %v", err)
	}

	result, err := NewEngine(repo).Resolve(context.Background(), "+12025550100/TYPE=PLMN")
	if err != nil {
		t.Fatalf("resolve routed msisdn: %v", err)
	}
	if result.Destination != DestinationMM4 || result.Peer == nil || result.Peer.Domain != "carrier-a.example.net" {
		t.Fatalf("unexpected routed msisdn result: %#v", result)
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
