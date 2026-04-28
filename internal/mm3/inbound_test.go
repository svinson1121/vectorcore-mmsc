package mm3

import (
	"bufio"
	"bytes"
	"context"
	"io"
	"net"
	"os"
	"strings"
	"testing"

	"github.com/vectorcore/vectorcore-mmsc/internal/config"
	"github.com/vectorcore/vectorcore-mmsc/internal/db"
	"github.com/vectorcore/vectorcore-mmsc/internal/message"
	"github.com/vectorcore/vectorcore-mmsc/internal/mmspdu"
	"github.com/vectorcore/vectorcore-mmsc/internal/routing"
	"github.com/vectorcore/vectorcore-mmsc/internal/store"
	"github.com/vectorcore/vectorcore-mmsc/internal/wappush"
)

type fakePushSender struct {
	sourceAddr string
	msisdn     string
	push       []byte
	calls      int
}

func (f *fakePushSender) SubmitWAPPush(_ context.Context, sourceAddr string, msisdn string, push []byte) error {
	f.sourceAddr = sourceAddr
	f.msisdn = msisdn
	f.push = append([]byte(nil), push...)
	f.calls++
	return nil
}

func TestInboundAcceptsLocalEmailToMMS(t *testing.T) {
	t.Parallel()

	repo := newMM3Repo(t)
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
	if line := mustReadSMTPLine(t, reader); !strings.HasPrefix(line, "220 ") {
		t.Fatalf("unexpected banner: %q", line)
	}
	mustWriteSMTP(t, clientConn, "EHLO mail.example.net\r\n")
	_ = mustReadSMTPLine(t, reader)
	_ = mustReadSMTPLine(t, reader)
	mustWriteSMTP(t, clientConn, "MAIL FROM:<sender@example.org>\r\n")
	if line := mustReadSMTPLine(t, reader); line != "250 OK" {
		t.Fatalf("unexpected mail from response: %q", line)
	}
	mustWriteSMTP(t, clientConn, "RCPT TO:<+12025550101>\r\n")
	if line := mustReadSMTPLine(t, reader); line != "250 OK" {
		t.Fatalf("unexpected rcpt response: %q", line)
	}
	mustWriteSMTP(t, clientConn, "DATA\r\n")
	if line := mustReadSMTPLine(t, reader); !strings.HasPrefix(line, "354 ") {
		t.Fatalf("unexpected data response: %q", line)
	}

	var body bytes.Buffer
	body.WriteString("From: Sender <sender@example.org>\r\n")
	body.WriteString("To: +12025550101\r\n")
	body.WriteString("Subject: Hello from mail\r\n")
	body.WriteString("MIME-Version: 1.0\r\n")
	body.WriteString("Content-Type: multipart/mixed; boundary=abc123\r\n")
	body.WriteString("\r\n")
	body.WriteString("--abc123\r\n")
	body.WriteString("Content-Type: text/plain\r\n")
	body.WriteString("\r\n")
	body.WriteString("hello world\r\n")
	body.WriteString("--abc123\r\n")
	body.WriteString("Content-Type: image/jpeg\r\n")
	body.WriteString("Content-ID: <img1>\r\n")
	body.WriteString("\r\n")
	body.WriteString("jpeg-data\r\n")
	body.WriteString("--abc123--\r\n")
	mustWriteSMTP(t, clientConn, body.String()+"\r\n.\r\n")
	if line := mustReadSMTPLine(t, reader); line != "250 OK" {
		t.Fatalf("unexpected data completion: %q", line)
	}
	mustWriteSMTP(t, clientConn, "QUIT\r\n")
	_ = mustReadSMTPLine(t, reader)
	if err := <-done; err != nil {
		t.Fatalf("handle inbound: %v", err)
	}

	items, err := repo.ListMessages(context.Background(), db.MessageFilter{Limit: 10})
	if err != nil {
		t.Fatalf("list messages: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected one stored message, got %#v", items)
	}
	msg := items[0]
	if msg.Origin != message.InterfaceMM3 || msg.From != "sender@example.org" || msg.To[0] != "+12025550101" {
		t.Fatalf("unexpected stored message: %#v", msg)
	}
	if push.calls != 1 || push.msisdn != "+12025550101" {
		t.Fatalf("unexpected push state: %#v", push)
	}
	unwrapped, err := wappush.UnwrapMMSPDU(push.push)
	if err != nil {
		t.Fatalf("unwrap push: %v", err)
	}
	pdu, err := mmspdu.Decode(unwrapped)
	if err != nil {
		t.Fatalf("decode push pdu: %v", err)
	}
	if pdu.MessageType != mmspdu.MsgTypeNotificationInd {
		t.Fatalf("unexpected push type: %x", pdu.MessageType)
	}
}

func TestInboundRejectsUnsupportedEmailRecipient(t *testing.T) {
	t.Parallel()

	repo := newMM3Repo(t)
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

	cfg := &config.Config{Adapt: config.AdaptConfig{Enabled: false}}
	cfg.MM3.MaxMessageSizeBytes = 1024 * 1024
	inbound := NewInboundServer(cfg, repo, contentStore, routing.NewEngine(repo), nil, "mmsc.example.net")

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
	mustWriteSMTP(t, clientConn, "RCPT TO:<user@example.org>\r\n")
	_ = mustReadSMTPLine(t, reader)
	mustWriteSMTP(t, clientConn, "DATA\r\n")
	_ = mustReadSMTPLine(t, reader)
	mustWriteSMTP(t, clientConn, "From: sender@example.org\r\nTo: user@example.org\r\nSubject: Nope\r\n\r\nhello\r\n.\r\n")
	if line := mustReadSMTPLine(t, reader); line != "550 Message rejected" {
		t.Fatalf("unexpected rejection response: %q", line)
	}
	mustWriteSMTP(t, clientConn, "QUIT\r\n")
	_ = mustReadSMTPLine(t, reader)
	if err := <-done; err != nil {
		t.Fatalf("handle inbound: %v", err)
	}
}

func newMM3Repo(t *testing.T) db.Repository {
	t.Helper()

	path := t.TempDir() + "/mm3.db"
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
	addMM3TestRoute(t, repo, db.MM4Route{
		Name:       "Local test prefix",
		MatchType:  "msisdn_prefix",
		MatchValue: "+1202555",
		EgressType: "local",
		Priority:   100,
		Active:     true,
	})
	return repo
}

func addMM3TestRoute(t *testing.T, repo db.Repository, route db.MM4Route) {
	t.Helper()
	if err := repo.UpsertMM4Route(context.Background(), route); err != nil {
		t.Fatalf("upsert route %s: %v", route.Name, err)
	}
}

func mustReadSMTPLine(t *testing.T, reader *bufio.Reader) string {
	t.Helper()
	line, err := reader.ReadString('\n')
	if err != nil {
		t.Fatalf("read smtp line: %v", err)
	}
	return strings.TrimRight(line, "\r\n")
}

func mustWriteSMTP(t *testing.T, conn net.Conn, payload string) {
	t.Helper()
	if _, err := io.WriteString(conn, payload); err != nil {
		t.Fatalf("write smtp data: %v", err)
	}
}

func TestDispatchLocalNotificationUsesConfiguredRetrieveURL(t *testing.T) {
	t.Parallel()

	push := &fakePushSender{}
	s := &InboundServer{
		retrieveBaseURL: "http://mmsc.example.net:9090/mms/retrieve",
		push:            push,
		repo:            newMM3Repo(t),
	}
	msg := &message.Message{
		ID:            "test-id-1",
		TransactionID: "txn-url",
		From:          "+12025550100",
		To:            []string{"+12025550101"},
	}
	if err := s.dispatchLocalNotification(context.Background(), msg); err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if push.calls != 1 {
		t.Fatalf("expected 1 push call, got %d", push.calls)
	}
	unwrapped, err := wappush.UnwrapMMSPDU(push.push)
	if err != nil {
		t.Fatalf("unwrap: %v", err)
	}
	pdu, err := mmspdu.Decode(unwrapped)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	loc, err := pdu.Headers[mmspdu.FieldContentLocation].Text()
	if err != nil {
		t.Fatalf("content-location: %v", err)
	}
	if !strings.HasPrefix(loc, "http://mmsc.example.net:9090/mms/retrieve") {
		t.Fatalf("unexpected content-location: %q", loc)
	}
}

func TestDispatchLocalNotificationFromFallbackInsertAddress(t *testing.T) {
	t.Parallel()

	push := &fakePushSender{}
	s := &InboundServer{
		push: push,
		repo: newMM3Repo(t),
	}
	msg := &message.Message{
		ID:            "test-id-2",
		TransactionID: "txn-insert",
		From:          "", // empty — should fall back to insert-address-token
		To:            []string{"+12025550101"},
	}
	if err := s.dispatchLocalNotification(context.Background(), msg); err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if push.calls != 1 {
		t.Fatalf("expected 1 push call, got %d", push.calls)
	}
	unwrapped, err := wappush.UnwrapMMSPDU(push.push)
	if err != nil {
		t.Fatalf("unwrap: %v", err)
	}
	pdu, err := mmspdu.Decode(unwrapped)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	fromVal, ok := pdu.Headers[mmspdu.FieldFrom]
	if !ok {
		t.Fatal("From header missing from notification PDU")
	}
	// Insert-address-token is encoded as {0x01, 0x81} per OMA MMS spec.
	if len(fromVal.Raw) != 2 || fromVal.Raw[0] != 0x01 || fromVal.Raw[1] != 0x81 {
		t.Fatalf("expected insert-address-token {0x01,0x81}, got %x", fromVal.Raw)
	}
}
