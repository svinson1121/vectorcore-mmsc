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
	"math/big"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vectorcore/vectorcore-mmsc/internal/config"
	"github.com/vectorcore/vectorcore-mmsc/internal/db"
	"github.com/vectorcore/vectorcore-mmsc/internal/message"
	"github.com/vectorcore/vectorcore-mmsc/internal/routing"
	"github.com/vectorcore/vectorcore-mmsc/internal/store"
)

func TestInboundSMTPAuthAllowsDATAWithoutAllowlistedIP(t *testing.T) {
	t.Parallel()

	repo := newMM4Repo(t)
	if err := repo.UpsertMM4Peer(context.Background(), db.MM4Peer{
		Domain:     "peer.example.net",
		SMTPHost:   "smtp.peer.example.net",
		SMTPPort:   25,
		SMTPAuth:   true,
		SMTPUser:   "peer-user",
		SMTPPass:   "peer-pass",
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
		ID:            "mid-auth",
		TransactionID: "txn-auth",
		From:          "+12025550100",
		To:            []string{"+12025550101"},
		Parts: []message.Part{{
			ContentType: "text/plain",
			Data:        []byte("auth"),
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
		done <- inbound.Handle(remoteAddrConn{
			Conn: serverConn,
			addr: &net.TCPAddr{IP: net.ParseIP("198.51.100.20"), Port: 25},
		})
	}()

	reader := bufio.NewReader(clientConn)
	if line := mustReadSMTPLine(t, reader); !strings.HasPrefix(line, "220 ") {
		t.Fatalf("unexpected banner: %q", line)
	}
	mustWriteSMTP(t, clientConn, "EHLO peer.example.net\r\n")
	if line := mustReadSMTPLine(t, reader); !strings.HasPrefix(line, "250-") {
		t.Fatalf("unexpected ehlo line: %q", line)
	}
	if line := mustReadSMTPLine(t, reader); !strings.Contains(line, "AUTH PLAIN") {
		t.Fatalf("expected auth advertisement, got %q", line)
	}
	if line := mustReadSMTPLine(t, reader); !strings.HasPrefix(line, "250 ") {
		t.Fatalf("unexpected ehlo completion: %q", line)
	}

	payload := base64.StdEncoding.EncodeToString([]byte("\x00peer-user\x00peer-pass"))
	mustWriteSMTP(t, clientConn, "AUTH PLAIN "+payload+"\r\n")
	if line := mustReadSMTPLine(t, reader); !strings.HasPrefix(line, "235 ") {
		t.Fatalf("unexpected auth response: %q", line)
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

	stored, err := repo.GetMessage(context.Background(), "mid-auth")
	if err != nil {
		t.Fatalf("get stored message: %v", err)
	}
	if stored.ID != "mid-auth" {
		t.Fatalf("unexpected stored message: %#v", stored)
	}
}

func TestInboundSMTPAuthRejectsInvalidCredentials(t *testing.T) {
	t.Parallel()

	repo := newMM4Repo(t)
	if err := repo.UpsertMM4Peer(context.Background(), db.MM4Peer{
		Domain:   "peer.example.net",
		SMTPHost: "smtp.peer.example.net",
		SMTPPort: 25,
		SMTPAuth: true,
		SMTPUser: "peer-user",
		SMTPPass: "peer-pass",
		Active:   true,
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
	serverConn, clientConn := net.Pipe()
	defer clientConn.Close()

	done := make(chan error, 1)
	go func() {
		done <- inbound.Handle(serverConn)
	}()

	reader := bufio.NewReader(clientConn)
	_ = mustReadSMTPLine(t, reader)
	mustWriteSMTP(t, clientConn, "EHLO peer.example.net\r\n")
	_ = mustReadSMTPLine(t, reader)
	_ = mustReadSMTPLine(t, reader)
	_ = mustReadSMTPLine(t, reader)

	payload := base64.StdEncoding.EncodeToString([]byte("\x00peer-user\x00wrong-pass"))
	mustWriteSMTP(t, clientConn, "AUTH PLAIN "+payload+"\r\n")
	if line := mustReadSMTPLine(t, reader); !strings.HasPrefix(line, "535 ") {
		t.Fatalf("unexpected auth failure response: %q", line)
	}
	mustWriteSMTP(t, clientConn, "QUIT\r\n")
	_ = mustReadSMTPLine(t, reader)
	if err := <-done; err != nil {
		t.Fatalf("handle inbound: %v", err)
	}
}

func TestInboundSTARTTLSAcceptsMessage(t *testing.T) {
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

	dir := t.TempDir()
	certOut := filepath.Join(dir, "mm4.crt")
	keyOut := filepath.Join(dir, "mm4.key")
	keyPEM, certPEM := mustSelfSignedPEM(t)
	if err := os.WriteFile(certOut, certPEM, 0o644); err != nil {
		t.Fatalf("write cert pem: %v", err)
	}
	if err := os.WriteFile(keyOut, keyPEM, 0o600); err != nil {
		t.Fatalf("write key pem: %v", err)
	}

	cfg := &config.Config{
		Adapt: config.AdaptConfig{Enabled: false},
		MM4: config.MM4Config{
			TLSCertFile: certOut,
			TLSKeyFile:  keyOut,
		},
	}
	inbound := NewInboundServer(cfg, repo, contentStore, routing.NewEngine(repo), nil, "mmsc.example.net")
	msg := &message.Message{
		ID:            "mid-tls",
		TransactionID: "txn-tls",
		From:          "+12025550100",
		To:            []string{"+12025550101"},
		Parts: []message.Part{{
			ContentType: "text/plain",
			Data:        []byte("tls"),
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
	if line := mustReadSMTPLine(t, reader); !strings.Contains(line, "STARTTLS") {
		t.Fatalf("expected starttls advertisement, got %q", line)
	}
	_ = mustReadSMTPLine(t, reader)
	mustWriteSMTP(t, clientConn, "STARTTLS\r\n")
	if line := mustReadSMTPLine(t, reader); !strings.HasPrefix(line, "220 ") {
		t.Fatalf("unexpected starttls response: %q", line)
	}

	tlsClient := tls.Client(clientConn, &tls.Config{InsecureSkipVerify: true, MinVersion: tls.VersionTLS12})
	if err := tlsClient.Handshake(); err != nil {
		t.Fatalf("tls handshake: %v", err)
	}
	tlsReader := bufio.NewReader(tlsClient)
	mustWriteSMTP(t, tlsClient, "EHLO peer.example.net\r\n")
	_ = mustReadSMTPLine(t, tlsReader)
	_ = mustReadSMTPLine(t, tlsReader)
	mustWriteSMTP(t, tlsClient, "MAIL FROM:<+12025550100>\r\n")
	_ = mustReadSMTPLine(t, tlsReader)
	mustWriteSMTP(t, tlsClient, "RCPT TO:<+12025550101>\r\n")
	_ = mustReadSMTPLine(t, tlsReader)
	mustWriteSMTP(t, tlsClient, "DATA\r\n")
	_ = mustReadSMTPLine(t, tlsReader)
	mustWriteSMTP(t, tlsClient, string(envelope)+"\r\n.\r\n")
	_ = mustReadSMTPLine(t, tlsReader)
	mustWriteSMTP(t, tlsClient, "QUIT\r\n")
	_ = mustReadSMTPLine(t, tlsReader)

	if err := <-done; err != nil {
		t.Fatalf("handle inbound: %v", err)
	}
	if _, err := repo.GetMessage(context.Background(), "mid-tls"); err != nil {
		t.Fatalf("get stored tls message: %v", err)
	}
}

func mustSelfSignedPEM(t *testing.T) ([]byte, []byte) {
	t.Helper()

	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate rsa key: %v", err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject: pkix.Name{
			CommonName: "mmsc.example.net",
		},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		DNSNames:              []string{"mmsc.example.net"},
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
	return keyPEM, certPEM
}
