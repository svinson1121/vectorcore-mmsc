package mm4

import (
	"bufio"
	"context"
	"net"
	"strings"
	"testing"

	"github.com/vectorcore/vectorcore-mmsc/internal/config"
	"github.com/vectorcore/vectorcore-mmsc/internal/db"
	"github.com/vectorcore/vectorcore-mmsc/internal/message"
	"github.com/vectorcore/vectorcore-mmsc/internal/routing"
	"github.com/vectorcore/vectorcore-mmsc/internal/store"
	"github.com/vectorcore/vectorcore-mmsc/internal/testpg"
)

func TestInboundHandleStoresAndDispatchesLocalNotificationPostgres(t *testing.T) {
	repo := testpg.OpenRepository(t)

	contentStore, err := store.New(context.Background(), config.StoreConfig{
		Backend: "filesystem",
		Filesystem: config.FilesystemConfig{
			Root: t.TempDir(),
		},
	})
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	defer contentStore.Close()

	push := &fakePushSender{}
	inbound := NewInboundServer(&config.Config{Adapt: config.AdaptConfig{Enabled: false}}, repo, contentStore, routing.NewEngine(repo), push, "mmsc.example.net")

	msg := &message.Message{
		ID:            "mid-pg-mm4",
		TransactionID: "txn-pg-mm4",
		From:          "+12025550100",
		To:            []string{"+12025550101"},
		Subject:       "hello from postgres peer",
		Parts: []message.Part{{
			ContentType: "text/plain",
			Data:        []byte("hello"),
		}},
	}
	envelope, err := EncodeEnvelope(msg, "peer.example.net")
	if err != nil {
		t.Fatalf("encode envelope: %v", err)
	}

	serverConn, clientConn := net.Pipe()
	defer clientConn.Close()

	done := make(chan error, 1)
	go func() {
		done <- inbound.Handle(serverConn)
	}()

	reader := bufio.NewReader(clientConn)
	if line := mustReadSMTPLine(t, reader); !strings.HasPrefix(line, "220 ") {
		t.Fatalf("unexpected banner: %q", line)
	}
	mustWriteSMTP(t, clientConn, "EHLO peer.example.net\r\n")
	_ = mustReadSMTPLine(t, reader)
	_ = mustReadSMTPLine(t, reader)
	mustWriteSMTP(t, clientConn, "MAIL FROM:<+12025550100>\r\n")
	_ = mustReadSMTPLine(t, reader)
	mustWriteSMTP(t, clientConn, "RCPT TO:<+12025550101>\r\n")
	_ = mustReadSMTPLine(t, reader)
	mustWriteSMTP(t, clientConn, "DATA\r\n")
	_ = mustReadSMTPLine(t, reader)
	mustWriteSMTP(t, clientConn, string(envelope)+"\r\n.\r\n")
	if line := mustReadSMTPLine(t, reader); line != "250 OK" {
		t.Fatalf("unexpected data completion: %q", line)
	}
	mustWriteSMTP(t, clientConn, "QUIT\r\n")
	_ = mustReadSMTPLine(t, reader)

	if err := <-done; err != nil {
		t.Fatalf("handle inbound: %v", err)
	}

	stored, err := repo.GetMessage(context.Background(), "mid-pg-mm4")
	if err != nil {
		t.Fatalf("get stored message: %v", err)
	}
	if stored.Origin != message.InterfaceMM4 || stored.Status != message.StatusDelivering {
		t.Fatalf("unexpected stored message state: %#v", stored)
	}
	if push.calls != 1 || push.msisdn != "+12025550101" {
		t.Fatalf("unexpected push dispatch: %#v", push)
	}

	items, err := repo.ListMessages(context.Background(), db.MessageFilter{Limit: 10})
	if err != nil {
		t.Fatalf("list messages: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("unexpected message count: %#v", items)
	}
}
