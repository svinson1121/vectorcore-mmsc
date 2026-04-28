package mm3

import (
	"bufio"
	"bytes"
	"context"
	"net"
	"testing"

	"github.com/vectorcore/vectorcore-mmsc/internal/config"
	"github.com/vectorcore/vectorcore-mmsc/internal/db"
	"github.com/vectorcore/vectorcore-mmsc/internal/message"
	"github.com/vectorcore/vectorcore-mmsc/internal/routing"
	"github.com/vectorcore/vectorcore-mmsc/internal/store"
	"github.com/vectorcore/vectorcore-mmsc/internal/testpg"
)

func TestInboundAcceptsLocalEmailToMMSPostgres(t *testing.T) {
	repo := testpg.OpenRepository(t)
	addMM3TestRoute(t, repo, db.MM4Route{
		Name:       "Local test prefix",
		MatchType:  "msisdn_prefix",
		MatchValue: "+1202555",
		EgressType: "local",
		Priority:   100,
		Active:     true,
	})

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
	cfg := &config.Config{Adapt: config.AdaptConfig{Enabled: false}}
	cfg.MM3.InboundListen = ":2026"
	cfg.MM3.MaxMessageSizeBytes = 1024 * 1024
	inbound := NewInboundServer(cfg, repo, contentStore, routing.NewEngine(repo), push, "mmsc.example.net")

	serverConn, clientConn := net.Pipe()
	defer clientConn.Close()

	done := make(chan error, 1)
	go func() { done <- inbound.Handle(serverConn) }()

	reader := bufio.NewReader(clientConn)
	_ = mustReadSMTPLine(t, reader)
	mustWriteSMTP(t, clientConn, "EHLO mail.example.net\r\n")
	_ = mustReadSMTPLine(t, reader)
	_ = mustReadSMTPLine(t, reader)
	mustWriteSMTP(t, clientConn, "MAIL FROM:<sender@example.org>\r\n")
	_ = mustReadSMTPLine(t, reader)
	mustWriteSMTP(t, clientConn, "RCPT TO:<+12025550101>\r\n")
	_ = mustReadSMTPLine(t, reader)
	mustWriteSMTP(t, clientConn, "DATA\r\n")
	_ = mustReadSMTPLine(t, reader)

	var body bytes.Buffer
	body.WriteString("From: Sender <sender@example.org>\r\n")
	body.WriteString("To: +12025550101\r\n")
	body.WriteString("Subject: Hello from postgres mail\r\n")
	body.WriteString("Content-Type: text/plain\r\n")
	body.WriteString("\r\n")
	body.WriteString("hello world from postgres\r\n")
	mustWriteSMTP(t, clientConn, body.String()+"\r\n.\r\n")
	_ = mustReadSMTPLine(t, reader)
	mustWriteSMTP(t, clientConn, "QUIT\r\n")
	_ = mustReadSMTPLine(t, reader)
	if err := <-done; err != nil {
		t.Fatalf("handle inbound: %v", err)
	}

	items, err := repo.ListMessages(context.Background(), db.MessageFilter{Limit: 10})
	if err != nil {
		t.Fatalf("list messages: %v", err)
	}
	if len(items) != 1 || items[0].Origin != message.InterfaceMM3 {
		t.Fatalf("unexpected stored messages: %#v", items)
	}
	if push.calls != 1 || push.msisdn != "+12025550101" {
		t.Fatalf("unexpected push state: %#v", push)
	}
}
