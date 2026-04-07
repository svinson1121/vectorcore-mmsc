package mm4

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/pem"
	"io"
	"math/big"
	"net"
	"net/textproto"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/vectorcore/vectorcore-mmsc/internal/adapt"
	"github.com/vectorcore/vectorcore-mmsc/internal/config"
	"github.com/vectorcore/vectorcore-mmsc/internal/db"
	"github.com/vectorcore/vectorcore-mmsc/internal/message"
	"github.com/vectorcore/vectorcore-mmsc/internal/mmspdu"
	"github.com/vectorcore/vectorcore-mmsc/internal/routing"
	"github.com/vectorcore/vectorcore-mmsc/internal/store"
	"github.com/vectorcore/vectorcore-mmsc/internal/wappush"
)

func TestEncodeDecodeEnvelopeRoundTrip(t *testing.T) {
	t.Parallel()

	msg := &message.Message{
		ID:            "mid-1",
		TransactionID: "txn-1",
		From:          "+12025550100",
		To:            []string{"+12025550101"},
		Subject:       "hello",
		Parts: []message.Part{
			{
				ContentType:     "text/plain",
				ContentID:       "<text1>",
				ContentLocation: "text1.txt",
				Data:            []byte("hello"),
			},
		},
	}

	envelope, err := EncodeEnvelope(msg, "mmsc.example.net")
	if err != nil {
		t.Fatalf("encode envelope: %v", err)
	}
	decoded, err := DecodeEnvelope(envelope)
	if err != nil {
		t.Fatalf("decode envelope: %v", err)
	}
	if decoded.ID != "mid-1" || decoded.TransactionID != "txn-1" {
		t.Fatalf("unexpected decoded ids: %#v", decoded)
	}
	if len(decoded.Parts) != 1 || string(decoded.Parts[0].Data) != "hello" {
		t.Fatalf("unexpected decoded parts: %#v", decoded.Parts)
	}
}

func TestDecodeEnvelopeWithMetaDeliveryReport(t *testing.T) {
	t.Parallel()

	raw := []byte(strings.Join([]string{
		"From: system@mmsc.example.net",
		"To: system@peer.example.net",
		"MIME-Version: 1.0",
		"Content-Type: text/plain",
		"X-Mms-3GPP-MMS-Version: 6.3.0",
		"X-Mms-Message-Type: MM4_delivery_report.REQ",
		"X-Mms-Transaction-ID: txn-report",
		"X-Mms-Message-ID: mid-report",
		"X-Mms-Originator-System: system=peer.example.net;party=+12025550101",
		"X-Mms-Status: Retrieved",
		"",
		"OK",
	}, "\r\n"))

	msg, meta, err := DecodeEnvelopeWithMeta(raw)
	if err != nil {
		t.Fatalf("decode envelope meta: %v", err)
	}
	if msg.ID != "mid-report" || meta.MessageType != mm4DeliveryReportReq || meta.Status != "Retrieved" {
		t.Fatalf("unexpected decoded report: msg=%#v meta=%#v", msg, meta)
	}
}

func TestDecodeEnvelopeWithMetaRejectsMultipartWithoutParts(t *testing.T) {
	t.Parallel()

	raw := []byte(strings.Join([]string{
		"From: system@mmsc.example.net",
		"To: system@peer.example.net",
		"MIME-Version: 1.0",
		"Content-Type: multipart/related; boundary=fixture-boundary",
		"X-Mms-3GPP-MMS-Version: 6.3.0",
		"X-Mms-Message-Type: MM4_forward.REQ",
		"X-Mms-Transaction-ID: txn-empty",
		"X-Mms-Message-ID: mid-empty",
		"X-Mms-Originator-System: system=peer.example.net;party=+12025550101",
		"",
		"--fixture-boundary--",
	}, "\r\n"))

	if _, _, err := DecodeEnvelopeWithMeta(raw); err == nil || !strings.Contains(err.Error(), "missing message content") {
		t.Fatalf("expected missing message content error, got %v", err)
	}
}

func TestPeerRouterResolveConfiguredPeer(t *testing.T) {
	t.Parallel()

	repo := newMM4Repo(t)
	if err := repo.UpsertMM4Peer(context.Background(), db.MM4Peer{
		Domain:     "peer.example.net",
		SMTPHost:   "smtp.peer.example.net",
		SMTPPort:   2525,
		TLSEnabled: false,
		Active:     true,
	}); err != nil {
		t.Fatalf("upsert peer: %v", err)
	}

	router := NewPeerRouter(repo)
	host, port, err := router.Resolve(context.Background(), "user@peer.example.net")
	if err != nil {
		t.Fatalf("resolve configured peer: %v", err)
	}
	if host != "smtp.peer.example.net" || port != 2525 {
		t.Fatalf("unexpected route: %s:%d", host, port)
	}
}

func TestOutboundSendUsesResolvedPeer(t *testing.T) {
	t.Parallel()

	repo := newMM4Repo(t)
	if err := repo.UpsertMM4Peer(context.Background(), db.MM4Peer{
		Domain:     "peer.example.net",
		SMTPHost:   "smtp.peer.example.net",
		SMTPPort:   2525,
		TLSEnabled: false,
		Active:     true,
	}); err != nil {
		t.Fatalf("upsert peer: %v", err)
	}

	router := NewPeerRouter(repo)
	outbound := NewOutbound(router, "mmsc.example.net")

	var (
		gotAddr string
		gotFrom string
		gotTo   []string
		gotMsg  []byte
	)
	serverConn, clientConn := net.Pipe()
	defer clientConn.Close()
	outbound.dialFn = func(context.Context, string, string) (net.Conn, error) {
		gotAddr = "smtp.peer.example.net:2525"
		return clientConn, nil
	}
	outbound.sleepFn = func(time.Duration) {}
	done := make(chan struct{})
	go fakeMM4SMTPServer(t, serverConn, false, "", "", func(from string, to []string, msg []byte) {
		gotFrom = from
		gotTo = append([]string(nil), to...)
		gotMsg = append([]byte(nil), msg...)
		close(done)
	})

	msg := &message.Message{
		ID:            "mid-2",
		TransactionID: "txn-2",
		From:          "+12025550100",
		To:            []string{"user@peer.example.net"},
		Parts: []message.Part{{
			ContentType: "text/plain",
			Data:        []byte("body"),
		}},
	}
	if err := outbound.Send(context.Background(), msg); err != nil {
		t.Fatalf("send outbound: %v", err)
	}
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for outbound smtp send")
	}
	if gotAddr != "smtp.peer.example.net:2525" || gotFrom != "+12025550100" || len(gotTo) != 1 {
		t.Fatalf("unexpected send call: addr=%s from=%s to=%#v", gotAddr, gotFrom, gotTo)
	}
	if !strings.Contains(string(gotMsg), "X-Mms-Message-Id: mid-2") {
		t.Fatalf("unexpected encoded message: %s", string(gotMsg))
	}
}

func TestOutboundSendUsesStartTLSAndAuth(t *testing.T) {
	t.Parallel()

	repo := newMM4Repo(t)
	if err := repo.UpsertMM4Peer(context.Background(), db.MM4Peer{
		Domain:     "peer.example.net",
		SMTPHost:   "smtp.peer.example.net",
		SMTPPort:   2525,
		SMTPAuth:   true,
		SMTPUser:   "peer-user",
		SMTPPass:   "peer-pass",
		TLSEnabled: true,
		Active:     true,
	}); err != nil {
		t.Fatalf("upsert peer: %v", err)
	}

	router := NewPeerRouter(repo)
	outbound := NewOutbound(router, "mmsc.example.net")
	serverConn, clientConn := net.Pipe()
	defer clientConn.Close()
	outbound.dialFn = func(context.Context, string, string) (net.Conn, error) {
		return clientConn, nil
	}
	outbound.sleepFn = func(time.Duration) {}
	outbound.tlsConfigFn = func(string) *tls.Config {
		return &tls.Config{InsecureSkipVerify: true, MinVersion: tls.VersionTLS12}
	}

	done := make(chan struct{})
	go fakeMM4SMTPServer(t, serverConn, true, "peer-user", "peer-pass", func(_ string, _ []string, _ []byte) {
		close(done)
	})

	msg := &message.Message{
		ID:            "mid-2a",
		TransactionID: "txn-2a",
		From:          "+12025550100",
		To:            []string{"user@peer.example.net"},
		Parts: []message.Part{{
			ContentType: "text/plain",
			Data:        []byte("body"),
		}},
	}
	if err := outbound.Send(context.Background(), msg); err != nil {
		t.Fatalf("send outbound tls/auth: %v", err)
	}
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for tls/auth smtp delivery")
	}
}

func TestOutboundRetriesTransientFailure(t *testing.T) {
	t.Parallel()

	repo := newMM4Repo(t)
	if err := repo.UpsertMM4Peer(context.Background(), db.MM4Peer{
		Domain:     "peer.example.net",
		SMTPHost:   "smtp.peer.example.net",
		SMTPPort:   2525,
		TLSEnabled: false,
		Active:     true,
	}); err != nil {
		t.Fatalf("upsert peer: %v", err)
	}

	router := NewPeerRouter(repo)
	outbound := NewOutbound(router, "mmsc.example.net")
	outbound.sleepFn = func(time.Duration) {}
	var attempts int
	outbound.dialFn = func(context.Context, string, string) (net.Conn, error) {
		serverConn, clientConn := net.Pipe()
		attempts++
		go func(attempt int) {
			defer serverConn.Close()
			reader := bufio.NewReader(serverConn)
			writer := bufio.NewWriter(serverConn)
			writeLine := func(line string) {
				_, _ = writer.WriteString(line + "\r\n")
				_ = writer.Flush()
			}
			writeLine("220 smtp.peer.example.net ESMTP")
			for {
				line, err := reader.ReadString('\n')
				if err != nil {
					return
				}
				line = strings.TrimRight(line, "\r\n")
				switch {
				case strings.HasPrefix(line, "EHLO "):
					writeLine("250-smtp.peer.example.net")
					writeLine("250 OK")
				case strings.HasPrefix(line, "MAIL FROM:"):
					if attempt == 1 {
						writeLine("451 temporary failure")
						return
					}
					writeLine("250 OK")
				case strings.HasPrefix(line, "RCPT TO:"):
					writeLine("250 OK")
				case line == "DATA":
					writeLine("354 End data with <CR><LF>.<CR><LF>")
				case line == ".":
					writeLine("250 OK")
				case line == "QUIT":
					writeLine("221 Bye")
					return
				}
			}
		}(attempts)
		return clientConn, nil
	}

	msg := &message.Message{
		ID:            "mid-retry",
		TransactionID: "txn-retry",
		From:          "+12025550100",
		To:            []string{"user@peer.example.net"},
		Parts: []message.Part{{
			ContentType: "text/plain",
			Data:        []byte("body"),
		}},
	}
	if err := outbound.Send(context.Background(), msg); err != nil {
		t.Fatalf("send outbound retry: %v", err)
	}
	if attempts != 2 {
		t.Fatalf("expected 2 attempts, got %d", attempts)
	}
}

func TestOutboundSendDeliveryReport(t *testing.T) {
	t.Parallel()

	repo := newMM4Repo(t)
	if err := repo.UpsertMM4Peer(context.Background(), db.MM4Peer{
		Domain:     "peer.example.net",
		SMTPHost:   "smtp.peer.example.net",
		SMTPPort:   2525,
		TLSEnabled: false,
		Active:     true,
	}); err != nil {
		t.Fatalf("upsert peer: %v", err)
	}

	router := NewPeerRouter(repo)
	outbound := NewOutbound(router, "mmsc.example.net")
	serverConn, clientConn := net.Pipe()
	defer clientConn.Close()
	outbound.dialFn = func(context.Context, string, string) (net.Conn, error) {
		return clientConn, nil
	}
	outbound.sleepFn = func(time.Duration) {}

	var gotMsg []byte
	done := make(chan struct{})
	go fakeMM4SMTPServer(t, serverConn, false, "", "", func(_ string, _ []string, msg []byte) {
		gotMsg = append([]byte(nil), msg...)
		close(done)
	})

	msg := &message.Message{
		ID:            "mid-dr",
		TransactionID: "txn-dr",
		Origin:        message.InterfaceMM4,
		OriginHost:    "system=peer.example.net;party=+12025550101",
		From:          "+12025550100",
		To:            []string{"+12025550101"},
	}
	if err := outbound.SendDeliveryReport(context.Background(), msg, message.StatusDelivered); err != nil {
		t.Fatalf("send delivery report: %v", err)
	}
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for delivery report smtp send")
	}
	payload := string(gotMsg)
	if !strings.Contains(payload, "X-Mms-Message-Type: MM4_delivery_report.REQ") || !strings.Contains(payload, "X-Mms-Status: Retrieved") {
		t.Fatalf("unexpected delivery report envelope: %s", payload)
	}
}

func TestOutboundSendReadReply(t *testing.T) {
	t.Parallel()

	repo := newMM4Repo(t)
	if err := repo.UpsertMM4Peer(context.Background(), db.MM4Peer{
		Domain:     "peer.example.net",
		SMTPHost:   "smtp.peer.example.net",
		SMTPPort:   2525,
		TLSEnabled: false,
		Active:     true,
	}); err != nil {
		t.Fatalf("upsert peer: %v", err)
	}

	router := NewPeerRouter(repo)
	outbound := NewOutbound(router, "mmsc.example.net")
	serverConn, clientConn := net.Pipe()
	defer clientConn.Close()
	outbound.dialFn = func(context.Context, string, string) (net.Conn, error) {
		return clientConn, nil
	}
	outbound.sleepFn = func(time.Duration) {}

	var gotMsg []byte
	done := make(chan struct{})
	go fakeMM4SMTPServer(t, serverConn, false, "", "", func(_ string, _ []string, msg []byte) {
		gotMsg = append([]byte(nil), msg...)
		close(done)
	})

	msg := &message.Message{
		ID:            "mid-rr",
		TransactionID: "txn-rr",
		Origin:        message.InterfaceMM4,
		OriginHost:    "system=peer.example.net;party=+12025550101",
		From:          "+12025550100",
		To:            []string{"+12025550101"},
	}
	if err := outbound.SendReadReply(context.Background(), msg); err != nil {
		t.Fatalf("send read reply: %v", err)
	}
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for read reply smtp send")
	}
	payload := string(gotMsg)
	if !strings.Contains(payload, "X-Mms-Message-Type: MM4_read_reply_report.REQ") || !strings.Contains(payload, "X-Mms-Read-Status: Read") {
		t.Fatalf("unexpected read reply envelope: %s", payload)
	}
}

func TestInboundHandleStoresAndDispatchesLocalNotification(t *testing.T) {
	t.Parallel()

	repo := newMM4Repo(t)

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
		ID:            "mid-3",
		TransactionID: "txn-3",
		From:          "+12025550100",
		To:            []string{"+12025550101"},
		Subject:       "hello from peer",
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
	if line := mustReadSMTPLine(t, reader); !strings.HasPrefix(line, "250-") {
		t.Fatalf("unexpected ehlo line: %q", line)
	}
	if line := mustReadSMTPLine(t, reader); !strings.HasPrefix(line, "250 ") {
		t.Fatalf("unexpected ehlo completion: %q", line)
	}

	mustWriteSMTP(t, clientConn, "MAIL FROM:<+12025550100>\r\n")
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

	mustWriteSMTP(t, clientConn, string(envelope)+"\r\n.\r\n")
	if line := mustReadSMTPLine(t, reader); line != "250 OK" {
		t.Fatalf("unexpected data completion: %q", line)
	}

	mustWriteSMTP(t, clientConn, "QUIT\r\n")
	if line := mustReadSMTPLine(t, reader); line != "221 Bye" {
		t.Fatalf("unexpected quit response: %q", line)
	}

	if err := <-done; err != nil {
		t.Fatalf("handle inbound: %v", err)
	}

	stored, err := repo.GetMessage(context.Background(), "mid-3")
	if err != nil {
		t.Fatalf("get stored message: %v", err)
	}
	if stored.Origin != message.InterfaceMM4 || stored.Status != message.StatusDelivering {
		t.Fatalf("unexpected stored message state: %#v", stored)
	}
	if stored.ContentPath == "" || stored.StoreID == "" {
		t.Fatalf("expected stored content refs, got %#v", stored)
	}
	if push.calls != 1 || push.msisdn != "+12025550101" {
		t.Fatalf("unexpected push dispatch: %#v", push)
	}
}

func TestInboundHandleRejectsMessageWhenAdaptationRemovesAllParts(t *testing.T) {
	t.Parallel()

	repo := newMM4Repo(t)

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
	inbound := NewInboundServer(&config.Config{Adapt: config.AdaptConfig{Enabled: true}}, repo, contentStore, routing.NewEngine(repo), push, "mmsc.example.net")
	inbound.adapt.SetAdapter(emptyAdapter{})

	msg := &message.Message{
		ID:            "mid-empty-adapt",
		TransactionID: "txn-empty-adapt",
		From:          "+12025550100",
		To:            []string{"+12025550101"},
		Subject:       "hello from peer",
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
	if line := mustReadSMTPLine(t, reader); !strings.HasPrefix(line, "250-") {
		t.Fatalf("unexpected ehlo line: %q", line)
	}
	if line := mustReadSMTPLine(t, reader); !strings.HasPrefix(line, "250 ") {
		t.Fatalf("unexpected ehlo completion: %q", line)
	}

	mustWriteSMTP(t, clientConn, "MAIL FROM:<+12025550100>\r\n")
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

	mustWriteSMTP(t, clientConn, string(envelope)+"\r\n.\r\n")
	if line := mustReadSMTPLine(t, reader); line != "550 Message rejected" {
		t.Fatalf("unexpected data completion: %q", line)
	}

	mustWriteSMTP(t, clientConn, "QUIT\r\n")
	if line := mustReadSMTPLine(t, reader); line != "221 Bye" {
		t.Fatalf("unexpected quit response: %q", line)
	}

	if err := <-done; err != nil {
		t.Fatalf("expected smtp session to close cleanly after reject, got %v", err)
	}

	messages, err := repo.ListMessages(context.Background(), db.MessageFilter{Limit: 10})
	if err != nil {
		t.Fatalf("list messages: %v", err)
	}
	if len(messages) != 0 {
		t.Fatalf("expected no stored messages, got %d", len(messages))
	}
	if push.calls != 0 {
		t.Fatalf("expected no wap push attempts, got %d", push.calls)
	}
}

func TestInboundHandleDATARejectsUnauthorizedRemote(t *testing.T) {
	t.Parallel()

	repo := newMM4Repo(t)
	if err := repo.UpsertMM4Peer(context.Background(), db.MM4Peer{
		Domain:     "peer.example.net",
		SMTPHost:   "smtp.peer.example.net",
		SMTPPort:   25,
		TLSEnabled: true,
		AllowedIPs: []string{"203.0.113.10"},
		Active:     true,
	}); err != nil {
		t.Fatalf("upsert peer: %v", err)
	}

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

	inbound := NewInboundServer(&config.Config{Adapt: config.AdaptConfig{Enabled: false}}, repo, contentStore, routing.NewEngine(repo), nil, "mmsc.example.net")
	msg := &message.Message{
		ID:            "mid-4",
		TransactionID: "txn-4",
		From:          "+12025550100",
		To:            []string{"+12025550101"},
		Parts: []message.Part{{
			ContentType: "text/plain",
			Data:        []byte("blocked"),
		}},
	}
	envelope, err := EncodeEnvelope(msg, "peer.example.net")
	if err != nil {
		t.Fatalf("encode envelope: %v", err)
	}

	serverConn, clientConn := net.Pipe()
	defer serverConn.Close()
	defer clientConn.Close()

	err = inbound.handleDATA(context.Background(), remoteAddrConn{
		Conn: serverConn,
		addr: &net.TCPAddr{IP: net.ParseIP("198.51.100.20"), Port: 25},
	}, msg.From, msg.To, envelope, nil)
	if err == nil {
		t.Fatal("expected unauthorized remote to be rejected")
	}

	messages, err := repo.ListMessages(context.Background(), db.MessageFilter{Limit: 10})
	if err != nil {
		t.Fatalf("list messages: %v", err)
	}
	if len(messages) != 0 {
		t.Fatalf("expected no stored messages, got %#v", messages)
	}
}

func TestInboundHandleAppliesAdaptationClassToStoredContent(t *testing.T) {
	t.Parallel()

	repo := newMM4Repo(t)
	if err := repo.UpsertAdaptationClass(context.Background(), db.AdaptationClass{
		Name:              "default",
		MaxMsgSizeBytes:   307200,
		MaxImageWidth:     640,
		MaxImageHeight:    480,
		AllowedImageTypes: []string{"image/jpeg", "image/gif", "image/png"},
		AllowedAudioTypes: []string{"audio/amr", "audio/mpeg", "audio/mp4"},
		AllowedVideoTypes: []string{"video/mp4"},
	}); err != nil {
		t.Fatalf("upsert adaptation class: %v", err)
	}
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
	inbound := NewInboundServer(&config.Config{Adapt: config.AdaptConfig{Enabled: true}}, repo, contentStore, routing.NewEngine(repo), push, "mmsc.example.net")
	inbound.adapt.SetAdapter(classAwareAdapter{
		transform: func(part message.Part, constraints adapt.Constraints) (message.Part, error) {
			if part.ContentType != "video/quicktime" {
				return part, nil
			}
			if len(constraints.AllowedVideoTypes) != 1 || constraints.AllowedVideoTypes[0] != "video/mp4" {
				t.Fatalf("unexpected video constraints: %#v", constraints)
			}
			part.ContentType = "video/mp4"
			part.Data = []byte("mp4 payload")
			part.Size = int64(len(part.Data))
			return part, nil
		},
	})

	msg := &message.Message{
		ID:            "mid-5",
		TransactionID: "txn-5",
		From:          "+12025550100",
		To:            []string{"+12025550101"},
		Subject:       "video from peer",
		Parts: []message.Part{{
			ContentType: "video/quicktime",
			Data:        []byte("mov payload"),
			Size:        int64(len("mov payload")),
		}},
	}
	envelope, err := EncodeEnvelope(msg, "peer.example.net")
	if err != nil {
		t.Fatalf("encode envelope: %v", err)
	}

	serverConn, clientConn := net.Pipe()
	defer serverConn.Close()
	defer clientConn.Close()

	err = inbound.handleDATA(context.Background(), remoteAddrConn{
		Conn: serverConn,
		addr: &net.TCPAddr{IP: net.ParseIP("198.51.100.20"), Port: 25},
	}, msg.From, msg.To, envelope, nil)
	if err != nil {
		t.Fatalf("handle data: %v", err)
	}

	stored, err := repo.GetMessage(context.Background(), "mid-5")
	if err != nil {
		t.Fatalf("get stored message: %v", err)
	}
	reader, _, err := contentStore.Get(context.Background(), stored.ContentPath)
	if err != nil {
		t.Fatalf("get stored content: %v", err)
	}
	defer reader.Close()
	raw, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("read stored content: %v", err)
	}
	pdu, err := mmspdu.Decode(raw)
	if err != nil {
		t.Fatalf("decode stored pdu: %v", err)
	}
	if pdu.Body == nil || len(pdu.Body.Parts) != 1 {
		t.Fatalf("unexpected stored pdu: %#v", pdu)
	}
	if pdu.Body.Parts[0].ContentType != "video/mp4" || string(pdu.Body.Parts[0].Data) != "mp4 payload" {
		t.Fatalf("unexpected adapted part: %#v", pdu.Body.Parts[0])
	}
}

func TestInboundHandleDeliveryReportUpdatesMessageStatus(t *testing.T) {
	t.Parallel()

	repo := newMM4Repo(t)
	if err := repo.CreateMessage(context.Background(), &message.Message{
		ID:            "mid-report",
		TransactionID: "txn-orig",
		Status:        message.StatusDelivering,
		Direction:     message.DirectionMT,
		Origin:        message.InterfaceMM4,
		From:          "+12025550100",
		To:            []string{"+12025550101"},
		ContentType:   "application/vnd.wap.mms-message",
	}); err != nil {
		t.Fatalf("create message: %v", err)
	}

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

	inbound := NewInboundServer(&config.Config{Adapt: config.AdaptConfig{Enabled: false}}, repo, contentStore, routing.NewEngine(repo), nil, "mmsc.example.net")

	raw := []byte(strings.Join([]string{
		"From: system@mmsc.example.net",
		"To: system@peer.example.net",
		"MIME-Version: 1.0",
		"Content-Type: text/plain",
		"X-Mms-3GPP-MMS-Version: 6.3.0",
		"X-Mms-Message-Type: MM4_delivery_report.REQ",
		"X-Mms-Transaction-ID: txn-report",
		"X-Mms-Message-ID: mid-report",
		"X-Mms-Originator-System: system=peer.example.net;party=+12025550101",
		"X-Mms-Status: Retrieved",
		"",
		"OK",
	}, "\r\n"))

	serverConn, clientConn := net.Pipe()
	defer serverConn.Close()
	defer clientConn.Close()

	err = inbound.handleDATA(context.Background(), remoteAddrConn{
		Conn: serverConn,
		addr: &net.TCPAddr{IP: net.ParseIP("198.51.100.20"), Port: 25},
	}, "", nil, raw, nil)
	if err != nil {
		t.Fatalf("handle delivery report: %v", err)
	}

	updated, err := repo.GetMessage(context.Background(), "mid-report")
	if err != nil {
		t.Fatalf("get updated message: %v", err)
	}
	if updated.Status != message.StatusDelivered {
		t.Fatalf("expected delivered status, got %#v", updated)
	}
}

func TestInboundHandleReadReplyUpdatesMessageStatus(t *testing.T) {
	t.Parallel()

	repo := newMM4Repo(t)
	if err := repo.CreateMessage(context.Background(), &message.Message{
		ID:            "mid-read",
		TransactionID: "txn-read",
		Status:        message.StatusDelivering,
		Direction:     message.DirectionMT,
		Origin:        message.InterfaceMM4,
		From:          "+12025550100",
		To:            []string{"+12025550101"},
		ContentType:   "application/vnd.wap.mms-message",
	}); err != nil {
		t.Fatalf("create message: %v", err)
	}

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

	inbound := NewInboundServer(&config.Config{Adapt: config.AdaptConfig{Enabled: false}}, repo, contentStore, routing.NewEngine(repo), nil, "mmsc.example.net")

	raw := []byte(strings.Join([]string{
		"From: system@mmsc.example.net",
		"To: system@peer.example.net",
		"MIME-Version: 1.0",
		"Content-Type: text/plain",
		"X-Mms-3GPP-MMS-Version: 6.3.0",
		"X-Mms-Message-Type: MM4_read_reply_report.REQ",
		"X-Mms-Transaction-ID: txn-read-report",
		"X-Mms-Message-ID: mid-read",
		"X-Mms-Originator-System: system=peer.example.net;party=+12025550101",
		"X-Mms-Read-Status: Read",
		"",
		"OK",
	}, "\r\n"))

	serverConn, clientConn := net.Pipe()
	defer serverConn.Close()
	defer clientConn.Close()

	err = inbound.handleDATA(context.Background(), remoteAddrConn{
		Conn: serverConn,
		addr: &net.TCPAddr{IP: net.ParseIP("198.51.100.20"), Port: 25},
	}, "", nil, raw, nil)
	if err != nil {
		t.Fatalf("handle read reply: %v", err)
	}

	updated, err := repo.GetMessage(context.Background(), "mid-read")
	if err != nil {
		t.Fatalf("get updated message: %v", err)
	}
	if updated.Status != message.StatusDelivered {
		t.Fatalf("expected delivered status, got %#v", updated)
	}
}

type classAwareAdapter struct {
	transform func(message.Part, adapt.Constraints) (message.Part, error)
}

type emptyAdapter struct{}

func (emptyAdapter) Adapt(_ context.Context, _ []message.Part, _ adapt.Constraints) ([]message.Part, error) {
	return nil, nil
}

func (a classAwareAdapter) Adapt(_ context.Context, parts []message.Part, constraints adapt.Constraints) ([]message.Part, error) {
	out := make([]message.Part, 0, len(parts))
	for _, part := range parts {
		next := part
		if a.transform != nil {
			var err error
			next, err = a.transform(part, constraints)
			if err != nil {
				return nil, err
			}
		}
		out = append(out, next)
	}
	return out, nil
}

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

type remoteAddrConn struct {
	net.Conn
	addr net.Addr
}

func (c remoteAddrConn) RemoteAddr() net.Addr {
	return c.addr
}

func mustReadSMTPLine(t *testing.T, reader *bufio.Reader) string {
	t.Helper()

	line, err := reader.ReadString('\n')
	if err != nil {
		t.Fatalf("read smtp line: %v", err)
	}
	return strings.TrimRight(line, "\r\n")
}

func mustWriteSMTP(t *testing.T, conn net.Conn, data string) {
	t.Helper()

	if _, err := conn.Write([]byte(data)); err != nil {
		t.Fatalf("write smtp data: %v", err)
	}
}

func fakeMM4SMTPServer(t *testing.T, conn net.Conn, requireTLS bool, wantUser string, wantPass string, onData func(from string, to []string, msg []byte)) {
	t.Helper()
	defer conn.Close()

	reader := textproto.NewReader(bufio.NewReader(conn))
	writer := bufio.NewWriter(conn)
	writeLine := func(line string) {
		_, _ = writer.WriteString(line + "\r\n")
		_ = writer.Flush()
	}

	writeLine("220 smtp.peer.example.net ESMTP")
	var (
		from string
		to   []string
		data []byte
	)
	for {
		line, err := reader.ReadLine()
		if err != nil {
			return
		}
		switch {
		case strings.HasPrefix(line, "EHLO "):
			if requireTLS {
				writeLine("250-smtp.peer.example.net")
				writeLine("250-STARTTLS")
				writeLine("250-AUTH PLAIN")
				writeLine("250 OK")
			} else {
				writeLine("250-smtp.peer.example.net")
				writeLine("250 OK")
			}
		case line == "STARTTLS":
			writeLine("220 Ready to start TLS")
			tlsConn := tls.Server(conn, &tls.Config{Certificates: []tls.Certificate{mustSelfSignedCert(t)}})
			if err := tlsConn.Handshake(); err != nil {
				t.Fatalf("tls handshake: %v", err)
			}
			reader = textproto.NewReader(bufio.NewReader(tlsConn))
			writer = bufio.NewWriter(tlsConn)
			conn = tlsConn
		case strings.HasPrefix(line, "AUTH PLAIN "):
			payload := strings.TrimPrefix(line, "AUTH PLAIN ")
			decoded, err := io.ReadAll(base64.NewDecoder(base64.StdEncoding, strings.NewReader(payload)))
			if err != nil {
				t.Fatalf("decode auth payload: %v", err)
			}
			parts := strings.Split(string(decoded), "\x00")
			if len(parts) < 3 || parts[1] != wantUser || parts[2] != wantPass {
				writeLine("535 authentication failed")
				return
			}
			writeLine("235 Authentication succeeded")
		case strings.HasPrefix(line, "MAIL FROM:"):
			from = strings.Trim(strings.TrimPrefix(line, "MAIL FROM:"), "<>")
			writeLine("250 OK")
		case strings.HasPrefix(line, "RCPT TO:"):
			to = append(to, strings.Trim(strings.TrimPrefix(line, "RCPT TO:"), "<>"))
			writeLine("250 OK")
		case line == "DATA":
			writeLine("354 End data with <CR><LF>.<CR><LF>")
			block, err := reader.ReadDotBytes()
			if err != nil {
				t.Fatalf("read data block: %v", err)
			}
			data = append([]byte(nil), block...)
			writeLine("250 OK")
		case line == "QUIT":
			writeLine("221 Bye")
			if onData != nil {
				onData(from, to, data)
			}
			return
		default:
			writeLine("250 OK")
		}
	}
}


func mustSelfSignedCert(t *testing.T) tls.Certificate {
	t.Helper()

	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate rsa key: %v", err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject: pkix.Name{
			CommonName: "smtp.peer.example.net",
		},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		DNSNames:              []string{"smtp.peer.example.net"},
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &priv.PublicKey, priv)
	if err != nil {
		t.Fatalf("create certificate: %v", err)
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(priv)})
	cert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		t.Fatalf("load x509 key pair: %v", err)
	}
	return cert
}

func newMM4Repo(t *testing.T) db.Repository {
	t.Helper()

	path := t.TempDir() + "/mm4.db"
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

func TestDispatchLocalNotificationUsesConfiguredRetrieveURL(t *testing.T) {
	t.Parallel()

	push := &fakePushSender{}
	s := &InboundServer{
		retrieveBaseURL: "http://mmsc.example.net:9090/mms/retrieve",
		push:            push,
		repo:            newMM4Repo(t),
	}
	msg := &message.Message{
		ID:            "test-id-1",
		TransactionID: "txn-url",
		From:          "+12025550100",
		To:            []string{"+12025550101"},
	}
	result := &routing.Result{Destination: routing.DestinationLocal}
	if err := s.dispatchLocalNotification(context.Background(), msg, result); err != nil {
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
		repo: newMM4Repo(t),
	}
	msg := &message.Message{
		ID:            "test-id-2",
		TransactionID: "txn-insert",
		From:          "", // empty — should fall back to insert-address-token
		To:            []string{"+12025550101"},
	}
	result := &routing.Result{Destination: routing.DestinationLocal}
	if err := s.dispatchLocalNotification(context.Background(), msg, result); err != nil {
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
