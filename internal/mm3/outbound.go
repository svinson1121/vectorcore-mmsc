package mm3

import (
	"bytes"
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"mime/multipart"
	"net"
	"net/smtp"
	"net/textproto"
	"strings"
	"time"

	"github.com/vectorcore/vectorcore-mmsc/internal/config"
	"github.com/vectorcore/vectorcore-mmsc/internal/db"
	"github.com/vectorcore/vectorcore-mmsc/internal/message"
)

type DialFunc func(ctx context.Context, network, address string) (net.Conn, error)

type Outbound struct {
	runtime     *config.RuntimeStore
	hostname    string
	dialFn      DialFunc
	tlsConfigFn func(string) *tls.Config
}

func NewOutbound(runtime *config.RuntimeStore, hostname string) *Outbound {
	return &Outbound{
		runtime:  runtime,
		hostname: hostname,
		tlsConfigFn: func(host string) *tls.Config {
			return &tls.Config{ServerName: host, MinVersion: tls.VersionTLS12}
		},
		dialFn: func(ctx context.Context, network, address string) (net.Conn, error) {
			dialer := &net.Dialer{Timeout: 10 * time.Second}
			return dialer.DialContext(ctx, network, address)
		},
	}
}

func (o *Outbound) Send(ctx context.Context, msg *message.Message) error {
	if msg == nil || len(msg.To) == 0 {
		return fmt.Errorf("mm3: missing recipients")
	}
	if o.runtime == nil {
		return fmt.Errorf("mm3: runtime store is not configured")
	}
	snapshot := o.runtime.Snapshot()
	if snapshot.MM3Relay == nil || !snapshot.MM3Relay.Enabled {
		return fmt.Errorf("mm3: relay is not configured")
	}
	relay := *snapshot.MM3Relay
	if relay.SMTPHost == "" {
		return fmt.Errorf("mm3: relay smtp_host is required")
	}
	envelope, mailFrom, err := EncodeEnvelope(msg, relay, o.hostname)
	if err != nil {
		return err
	}
	return o.sendOnce(ctx, relay, mailFrom, msg.To, envelope)
}

func EncodeEnvelope(msg *message.Message, relay db.MM3Relay, hostname string) ([]byte, string, error) {
	if msg == nil {
		return nil, "", fmt.Errorf("mm3: nil message")
	}
	mailFrom := normalizeMailbox(msg.From, relay, hostname)
	if mailFrom == "" {
		return nil, "", fmt.Errorf("mm3: sender address is required")
	}

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	for _, part := range msg.Parts {
		header := textproto.MIMEHeader{}
		header.Set("Content-Type", part.ContentType)
		if part.ContentID != "" {
			header.Set("Content-ID", part.ContentID)
		}
		if part.ContentLocation != "" {
			header.Set("Content-Location", part.ContentLocation)
		}
		pw, err := writer.CreatePart(header)
		if err != nil {
			return nil, "", err
		}
		if _, err := pw.Write(part.Data); err != nil {
			return nil, "", err
		}
	}
	if err := writer.Close(); err != nil {
		return nil, "", err
	}

	var out bytes.Buffer
	fmt.Fprintf(&out, "From: %s\r\n", mailFrom)
	fmt.Fprintf(&out, "To: %s\r\n", strings.Join(msg.To, ","))
	if msg.Subject != "" {
		fmt.Fprintf(&out, "Subject: %s\r\n", msg.Subject)
	}
	if msg.ID != "" {
		fmt.Fprintf(&out, "Message-ID: <%s>\r\n", msg.ID)
	}
	fmt.Fprintf(&out, "MIME-Version: 1.0\r\n")
	if len(msg.Parts) == 1 {
		fmt.Fprintf(&out, "Content-Type: %s\r\n", msg.Parts[0].ContentType)
	} else {
		fmt.Fprintf(&out, "Content-Type: multipart/related; boundary=%s\r\n", writer.Boundary())
	}
	fmt.Fprintf(&out, "X-VectorCore-MMS-Origin: %d\r\n", msg.Origin)
	fmt.Fprintf(&out, "\r\n")
	if len(msg.Parts) == 1 {
		out.Write(msg.Parts[0].Data)
	} else {
		out.Write(body.Bytes())
	}
	return out.Bytes(), mailFrom, nil
}

func normalizeMailbox(value string, relay db.MM3Relay, hostname string) string {
	value = stripTypeSuffix(strings.TrimSpace(value))
	if value == "" {
		if relay.DefaultFromAddress != "" {
			return strings.TrimSpace(relay.DefaultFromAddress)
		}
		return ""
	}
	if strings.Contains(value, "@") {
		return value
	}
	domain := strings.TrimSpace(relay.DefaultSenderDomain)
	if domain == "" {
		domain = strings.TrimSpace(hostname)
	}
	if domain == "" {
		return value
	}
	return value + "@" + domain
}

func stripTypeSuffix(value string) string {
	if idx := strings.Index(strings.ToUpper(value), "/TYPE="); idx > 0 {
		return value[:idx]
	}
	return value
}

func (o *Outbound) sendOnce(ctx context.Context, relay db.MM3Relay, from string, to []string, envelope []byte) error {
	addr := fmt.Sprintf("%s:%d", relay.SMTPHost, relay.SMTPPort)
	conn, err := o.dialFn(ctx, "tcp", addr)
	if err != nil {
		return err
	}
	defer conn.Close()

	client, err := smtp.NewClient(conn, relay.SMTPHost)
	if err != nil {
		return err
	}
	defer client.Close()

	if err := client.Hello(o.hostname); err != nil {
		return err
	}
	if relay.TLSEnabled {
		if ok, _ := client.Extension("STARTTLS"); !ok {
			return fmt.Errorf("mm3: relay %s does not support STARTTLS", relay.SMTPHost)
		}
		if err := client.StartTLS(o.tlsConfigFn(relay.SMTPHost)); err != nil {
			return err
		}
	}
	if relay.SMTPAuth {
		if ok, _ := client.Extension("AUTH"); !ok {
			return fmt.Errorf("mm3: relay %s does not support AUTH", relay.SMTPHost)
		}
		auth := smtp.PlainAuth("", relay.SMTPUser, relay.SMTPPass, relay.SMTPHost)
		if err := client.Auth(auth); err != nil {
			return err
		}
	}
	if err := client.Mail(from); err != nil {
		return err
	}
	for _, recipient := range to {
		if err := client.Rcpt(recipient); err != nil {
			return err
		}
	}
	wc, err := client.Data()
	if err != nil {
		return err
	}
	if _, err := wc.Write(envelope); err != nil {
		_ = wc.Close()
		return err
	}
	if err := wc.Close(); err != nil {
		return err
	}
	if err := client.Quit(); err != nil && err != io.EOF {
		return err
	}
	return nil
}
