package config

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/vectorcore/vectorcore-mmsc/internal/db"
)

func TestLoadRuntimeSnapshot(t *testing.T) {
	t.Parallel()

	repo := newRuntimeTestRepo(t)

	if err := repo.UpsertMM4Peer(context.Background(), db.MM4Peer{
		Domain:     "peer.example.net",
		SMTPHost:   "smtp.peer.example.net",
		SMTPPort:   25,
		TLSEnabled: true,
		AllowedIPs: []string{"192.0.2.10", "192.0.2.11"},
		Active:     true,
	}); err != nil {
		t.Fatalf("upsert mm4 peer: %v", err)
	}

	if err := repo.UpsertMM7VASP(context.Background(), db.MM7VASP{
		VASPID:     "vasp-1",
		VASID:      "service-1",
		Protocol:   "eaif",
		Version:    "3.0",
		DeliverURL: "https://vasp.example.net/deliver",
		ReportURL:  "https://vasp.example.net/report",
		Active:     true,
	}); err != nil {
		t.Fatalf("upsert mm7 vasp: %v", err)
	}
	if err := repo.UpsertMM3Relay(context.Background(), db.MM3Relay{
		Enabled:             true,
		SMTPHost:            "smtp.example.net",
		SMTPPort:            25,
		DefaultSenderDomain: "mmsc.example.net",
	}); err != nil {
		t.Fatalf("upsert mm3 relay: %v", err)
	}

	if err := repo.UpsertSMPPUpstream(context.Background(), db.SMPPUpstream{
		Name:     "primary",
		Host:     "smsc.example.net",
		Port:     2775,
		SystemID: "mmsc",
		Password: "secret",
		BindMode: "transceiver",
		Active:   true,
	}); err != nil {
		t.Fatalf("upsert smpp upstream: %v", err)
	}
	if err := repo.UpsertAdaptationClass(context.Background(), db.AdaptationClass{
		Name:              "low-end",
		MaxMsgSizeBytes:   65536,
		MaxImageWidth:     320,
		MaxImageHeight:    240,
		AllowedImageTypes: []string{"image/jpeg"},
		AllowedAudioTypes: []string{"audio/amr"},
		AllowedVideoTypes: []string{"video/3gpp"},
	}); err != nil {
		t.Fatalf("upsert adaptation class: %v", err)
	}

	snapshot, err := LoadRuntimeSnapshot(context.Background(), repo)
	if err != nil {
		t.Fatalf("load runtime snapshot: %v", err)
	}
	if len(snapshot.Peers) != 1 || snapshot.Peers[0].Domain != "peer.example.net" {
		t.Fatalf("unexpected peers snapshot: %#v", snapshot.Peers)
	}
	if snapshot.MM3Relay == nil || snapshot.MM3Relay.SMTPHost != "smtp.example.net" {
		t.Fatalf("unexpected mm3 snapshot: %#v", snapshot.MM3Relay)
	}
	if len(snapshot.VASPs) != 1 || snapshot.VASPs[0].VASPID != "vasp-1" || snapshot.VASPs[0].Protocol != "eaif" || snapshot.VASPs[0].Version != "3.0" {
		t.Fatalf("unexpected vasps snapshot: %#v", snapshot.VASPs)
	}
	if len(snapshot.SMPPUpstreams) != 1 || snapshot.SMPPUpstreams[0].Name != "primary" {
		t.Fatalf("unexpected smpp snapshot: %#v", snapshot.SMPPUpstreams)
	}
	if len(snapshot.Adaptation) == 0 {
		t.Fatal("expected adaptation classes in runtime snapshot")
	}
}

func TestRuntimeStoreSnapshotIsolation(t *testing.T) {
	t.Parallel()

	store := NewRuntimeStore()
	store.Replace(RuntimeSnapshot{
		Peers:      []db.MM4Peer{{Domain: "peer.example.net"}},
		MM3Relay:   &db.MM3Relay{SMTPHost: "smtp.example.net"},
		Adaptation: []db.AdaptationClass{{Name: "default"}},
	})

	snap := store.Snapshot()
	snap.Peers[0].Domain = "mutated.example.net"
	snap.MM3Relay.SMTPHost = "mutated.example.net"

	next := store.Snapshot()
	if next.Peers[0].Domain != "peer.example.net" {
		t.Fatalf("expected snapshot copy isolation, got %#v", next.Peers)
	}
	if next.MM3Relay == nil || next.MM3Relay.SMTPHost != "smtp.example.net" {
		t.Fatalf("expected mm3 snapshot isolation, got %#v", next.MM3Relay)
	}
	if next.Adaptation[0].Name != "default" {
		t.Fatalf("expected adaptation snapshot isolation, got %#v", next.Adaptation)
	}
}

func TestRuntimeStoreSubscribe(t *testing.T) {
	t.Parallel()

	store := NewRuntimeStore()
	ch := store.Subscribe(1)

	store.Replace(RuntimeSnapshot{
		SMPPUpstreams: []db.SMPPUpstream{{Name: "primary"}},
	})

	select {
	case snapshot := <-ch:
		if len(snapshot.SMPPUpstreams) != 1 || snapshot.SMPPUpstreams[0].Name != "primary" {
			t.Fatalf("unexpected snapshot: %#v", snapshot)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for runtime snapshot")
	}
}

func newRuntimeTestRepo(t *testing.T) db.Repository {
	t.Helper()

	path := t.TempDir() + "/runtime.db"
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
