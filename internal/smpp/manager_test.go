package smpp

import (
	"context"
	"net"
	"os"
	"testing"
	"time"

	"github.com/vectorcore/vectorcore-mmsc/internal/config"
	"github.com/vectorcore/vectorcore-mmsc/internal/db"
	"github.com/vectorcore/vectorcore-mmsc/internal/message"
)

func TestManagerRefreshAndDefault(t *testing.T) {
	t.Parallel()

	manager := NewManager()
	defer manager.Close()

	err := manager.Refresh(config.RuntimeSnapshot{
		SMPPUpstreams: []db.SMPPUpstream{
			{
				Name:          "b-upstream",
				Host:          "b.example.net",
				Port:          2775,
				SystemID:      "b",
				Password:      "secret",
				BindMode:      "transceiver",
				EnquireLink:   30,
				ReconnectWait: 5,
				Active:        true,
			},
			{
				Name:          "a-upstream",
				Host:          "a.example.net",
				Port:          2775,
				SystemID:      "a",
				Password:      "secret",
				BindMode:      "transceiver",
				EnquireLink:   30,
				ReconnectWait: 5,
				Active:        true,
			},
		},
	})
	if err != nil {
		t.Fatalf("refresh manager: %v", err)
	}

	names := manager.Names()
	if len(names) != 2 || names[0] != "a-upstream" || names[1] != "b-upstream" {
		t.Fatalf("unexpected upstream order: %#v", names)
	}

	client, err := manager.Default()
	if err != nil {
		t.Fatalf("default client: %v", err)
	}
	if client.cfg.Host != "a.example.net" {
		t.Fatalf("unexpected default client host: %q", client.cfg.Host)
	}
}

func TestManagerRefreshRemovesInactiveUpstreams(t *testing.T) {
	t.Parallel()

	manager := NewManager()
	defer manager.Close()

	if err := manager.Refresh(config.RuntimeSnapshot{
		SMPPUpstreams: []db.SMPPUpstream{{
			Name:          "primary",
			Host:          "smsc.example.net",
			Port:          2775,
			SystemID:      "mmsc",
			Password:      "secret",
			BindMode:      "transceiver",
			EnquireLink:   30,
			ReconnectWait: 5,
			Active:        true,
		}},
	}); err != nil {
		t.Fatalf("initial refresh: %v", err)
	}

	if err := manager.Refresh(config.RuntimeSnapshot{}); err != nil {
		t.Fatalf("empty refresh: %v", err)
	}

	_, err := manager.Default()
	if err == nil {
		t.Fatal("expected missing upstream error")
	}
	if len(manager.Names()) != 0 {
		t.Fatalf("expected manager to be empty, got %#v", manager.Names())
	}
}

func TestManagerSubmitUsesDefaultClient(t *testing.T) {
	t.Parallel()

	manager := NewManager()
	defer manager.Close()

	push := []byte{0x01, 0x02}
	err := manager.Refresh(config.RuntimeSnapshot{
		SMPPUpstreams: []db.SMPPUpstream{{
			Name:          "primary",
			Host:          "ignored",
			Port:          2775,
			SystemID:      "mmsc",
			Password:      "secret",
			BindMode:      "transceiver",
			EnquireLink:   30,
			ReconnectWait: 5,
			Active:        true,
		}},
	})
	if err != nil {
		t.Fatalf("refresh manager: %v", err)
	}

	client, ok := manager.Get("primary")
	if !ok {
		t.Fatal("expected primary client")
	}
	serverConn, clientConn := net.Pipe()
	defer serverConn.Close()
	client.SetDialFunc(func(context.Context, string, string) (net.Conn, error) {
		return clientConn, nil
	})

	go fakeSMSCConn(t, serverConn, nil)

	if err := manager.SubmitWAPPush(context.Background(), "+12025550199", "+12025550100", push); err != nil {
		t.Fatalf("submit via manager: %v", err)
	}
}

func TestManagerSubmitForMessageStoresSMPPSubmissions(t *testing.T) {
	t.Parallel()

	repo := newSQLiteRepoForSMPPTest(t)
	if err := repo.CreateMessage(context.Background(), testMessage("msg-1")); err != nil {
		t.Fatalf("create message: %v", err)
	}

	manager := NewManager(repo)
	defer manager.Close()

	if err := manager.Refresh(config.RuntimeSnapshot{
		SMPPUpstreams: []db.SMPPUpstream{{
			Name:          "primary",
			Host:          "ignored",
			Port:          2775,
			SystemID:      "mmsc",
			Password:      "secret",
			BindMode:      "transceiver",
			EnquireLink:   30,
			ReconnectWait: 5,
			Active:        true,
		}},
	}); err != nil {
		t.Fatalf("refresh manager: %v", err)
	}

	client, ok := manager.Get("primary")
	if !ok {
		t.Fatal("expected primary client")
	}
	serverConn, clientConn := net.Pipe()
	defer serverConn.Close()
	client.SetDialFunc(func(context.Context, string, string) (net.Conn, error) {
		return clientConn, nil
	})
	go fakeSMSCConn(t, serverConn, nil)

	push := []byte{0x01, 0x02}
	if err := manager.SubmitWAPPushForMessage(context.Background(), "msg-1", "+12025550199", "+12025550100", push); err != nil {
		t.Fatalf("tracked submit via manager: %v", err)
	}

	submissions, err := repo.ListSMPPSubmissions(context.Background(), "msg-1")
	if err != nil {
		t.Fatalf("list smpp submissions: %v", err)
	}
	if len(submissions) != 1 {
		t.Fatalf("unexpected submission count: %d", len(submissions))
	}
	if submissions[0].UpstreamName != "primary" || submissions[0].Recipient != "+12025550100" || submissions[0].SMPPMessageID == "" {
		t.Fatalf("unexpected submission: %#v", submissions[0])
	}
}

func TestManagerDeliveryReceiptHandlerAppliedToClients(t *testing.T) {
	t.Parallel()

	manager := NewManager()
	defer manager.Close()

	var receiptID string
	done := make(chan struct{}, 1)
	manager.SetDeliveryReceiptHandler(func(upstream string, receipt *DeliveryReceipt) {
		if upstream == "primary" && receipt != nil {
			receiptID = receipt.ID
			done <- struct{}{}
		}
	})

	if err := manager.Refresh(config.RuntimeSnapshot{
		SMPPUpstreams: []db.SMPPUpstream{{
			Name:          "primary",
			Host:          "ignored",
			Port:          2775,
			SystemID:      "mmsc",
			Password:      "secret",
			BindMode:      "transceiver",
			EnquireLink:   30,
			ReconnectWait: 5,
			Active:        true,
		}},
	}); err != nil {
		t.Fatalf("refresh manager: %v", err)
	}

	client, ok := manager.Get("primary")
	if !ok {
		t.Fatal("expected primary client")
	}
	serverConn, clientConn := net.Pipe()
	defer serverConn.Close()
	client.SetDialFunc(func(context.Context, string, string) (net.Conn, error) {
		return clientConn, nil
	})

	go func() {
		defer serverConn.Close()
		wrapped := NewConn(serverConn)
		for {
			pdu, err := wrapped.ReadPDU()
			if err != nil {
				return
			}
			switch pdu.CommandID {
			case CmdBindTransceiver:
				_ = wrapped.WritePDU(&PDU{
					CommandID:      CmdBindTransceiverResp,
					CommandStatus:  ESMEROK,
					SequenceNumber: pdu.SequenceNumber,
					SystemID:       "smsc",
				})
				_ = wrapped.WritePDU(&PDU{
					CommandID:      CmdDeliverSM,
					CommandStatus:  ESMEROK,
					SequenceNumber: 77,
					ESMClass:       0x04,
					ShortMessage:   []byte("id:abc123 sub:001 dlvrd:001 submit:2404011200 done:2404011201 stat:DELIVRD err:000 text:hello"),
				})
				return
			case CmdDeliverSMResp:
				return
			}
		}
	}()

	if err := client.Connect(context.Background()); err != nil {
		t.Fatalf("connect client: %v", err)
	}

	select {
	case <-done:
		if receiptID != "abc123" {
			t.Fatalf("unexpected receipt id: %q", receiptID)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for manager receipt handler")
	}
}

func newSQLiteRepoForSMPPTest(t *testing.T) db.Repository {
	t.Helper()

	path := t.TempDir() + "/smpp.db"
	repo, err := db.Open(context.Background(), db.OpenOptions{
		Driver: "sqlite",
		DSN:    path,
	})
	if err != nil {
		t.Fatalf("open sqlite repo: %v", err)
	}
	t.Cleanup(func() { _ = repo.Close() })
	if err := db.RunMigrations(context.Background(), repo, os.DirFS("../..")); err != nil {
		t.Fatalf("run migrations: %v", err)
	}
	return repo
}

func testMessage(id string) *message.Message {
	return &message.Message{
		ID:            id,
		TransactionID: "txn-" + id,
		Status:        message.StatusDelivering,
		Direction:     message.DirectionMT,
		Origin:        message.InterfaceMM1,
		From:          "+12025550100",
		To:            []string{"+12025550101"},
		ContentType:   "application/vnd.wap.mms-message",
		MMSVersion:    "1.3",
	}
}
