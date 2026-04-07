package mm4

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/smtp"
	"strings"
	"time"

	"github.com/vectorcore/vectorcore-mmsc/internal/db"
	"github.com/vectorcore/vectorcore-mmsc/internal/message"
)

type DialFunc func(ctx context.Context, network, address string) (net.Conn, error)
type SleepFunc func(time.Duration)

type Outbound struct {
	router      *PeerRouter
	hostname    string
	dialFn      DialFunc
	sleepFn     SleepFunc
	tlsConfigFn func(string) *tls.Config
	maxRetries  int
}

func NewOutbound(router *PeerRouter, hostname string) *Outbound {
	return &Outbound{
		router:     router,
		hostname:   hostname,
		maxRetries: 3,
		sleepFn:    time.Sleep,
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
		return fmt.Errorf("mm4: missing recipients")
	}

	envelope, err := EncodeEnvelope(msg, o.hostname)
	if err != nil {
		return err
	}

	peer, configured, err := o.router.ResolvePeer(ctx, msg.To[0])
	if err != nil {
		return err
	}
	if !configured {
		host, port, err := o.router.Resolve(ctx, msg.To[0])
		if err != nil {
			return err
		}
		peer = db.MM4Peer{
			SMTPHost:   host,
			SMTPPort:   port,
			TLSEnabled: false,
		}
	}
	if o.router != nil && o.router.repo != nil && msg.ID != "" {
		_ = o.router.repo.AppendMessageEvent(ctx, db.MessageEvent{
			MessageID: msg.ID,
			Source:    "mm4",
			Type:      "relay-attempt",
			Summary:   "MM4 relay queued",
			Detail:    fmt.Sprintf("peer=%s:%d recipient=%s", peer.SMTPHost, peer.SMTPPort, msg.To[0]),
		})
	}

	return o.SendEnvelope(ctx, peer, envelope, msg.From, msg.To)
}

func (o *Outbound) SendDeliveryReport(ctx context.Context, msg *message.Message, status message.Status) error {
	if msg == nil || msg.Origin != message.InterfaceMM4 || msg.OriginHost == "" {
		return nil
	}
	peer, err := o.resolveOriginPeer(ctx, parseOriginatorSystemHost(msg.OriginHost))
	if err != nil {
		return err
	}
	envelope, err := EncodeDeliveryReportEnvelope(msg, o.hostname, mm4StatusForMessage(status), "Ok")
	if err != nil {
		return err
	}
	if o.router != nil && o.router.repo != nil && msg.ID != "" {
		_ = o.router.repo.AppendMessageEvent(ctx, db.MessageEvent{
			MessageID: msg.ID,
			Source:    "mm4",
			Type:      "delivery-report",
			Summary:   "MM4 delivery report sent",
			Detail:    fmt.Sprintf("origin_peer=%s status=%s", peer.SMTPHost, mm4StatusForMessage(status)),
		})
	}
	return o.SendEnvelope(ctx, peer, envelope, o.hostname, []string{reportRecipient(msg)})
}

func (o *Outbound) SendReadReply(ctx context.Context, msg *message.Message) error {
	if msg == nil || msg.Origin != message.InterfaceMM4 || msg.OriginHost == "" {
		return nil
	}
	peer, err := o.resolveOriginPeer(ctx, parseOriginatorSystemHost(msg.OriginHost))
	if err != nil {
		return err
	}
	envelope, err := EncodeReadReplyEnvelope(msg, o.hostname, "Read", "Ok")
	if err != nil {
		return err
	}
	if o.router != nil && o.router.repo != nil && msg.ID != "" {
		_ = o.router.repo.AppendMessageEvent(ctx, db.MessageEvent{
			MessageID: msg.ID,
			Source:    "mm4",
			Type:      "read-reply",
			Summary:   "MM4 read reply sent",
			Detail:    fmt.Sprintf("origin_peer=%s", peer.SMTPHost),
		})
	}
	return o.SendEnvelope(ctx, peer, envelope, o.hostname, []string{reportRecipient(msg)})
}

func (o *Outbound) SendEnvelope(ctx context.Context, peer db.MM4Peer, envelope []byte, from string, to []string) error {
	var lastErr error
	for attempt := 0; attempt < o.maxRetries; attempt++ {
		if attempt > 0 {
			o.sleepFn(time.Duration(1<<uint(attempt-1)) * time.Second)
		}
		lastErr = o.sendOnce(ctx, peer, from, to, envelope)
		if lastErr == nil {
			return nil
		}
		if !isTransientSMTPError(lastErr) {
			return lastErr
		}
	}
	return lastErr
}

func (o *Outbound) sendOnce(ctx context.Context, peer db.MM4Peer, from string, to []string, envelope []byte) error {
	addr := fmt.Sprintf("%s:%d", peer.SMTPHost, peer.SMTPPort)
	conn, err := o.dialFn(ctx, "tcp", addr)
	if err != nil {
		return err
	}
	defer conn.Close()

	host := peer.SMTPHost
	client, err := smtp.NewClient(conn, host)
	if err != nil {
		return err
	}
	defer client.Close()

	if err := client.Hello(o.hostname); err != nil {
		return err
	}
	if peer.TLSEnabled {
		if ok, _ := client.Extension("STARTTLS"); !ok {
			return fmt.Errorf("mm4: peer %s does not support STARTTLS", host)
		}
		if err := client.StartTLS(o.tlsConfigFn(host)); err != nil {
			return err
		}
	}
	if peer.SMTPAuth {
		auth := smtp.PlainAuth("", peer.SMTPUser, peer.SMTPPass, host)
		if ok, _ := client.Extension("AUTH"); !ok {
			return fmt.Errorf("mm4: peer %s does not support AUTH", host)
		}
		if err := client.Auth(auth); err != nil {
			return err
		}
	}
	if err := client.Mail(from); err != nil {
		return err
	}
	for _, rcpt := range to {
		if err := client.Rcpt(rcpt); err != nil {
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
	if err := client.Quit(); err != nil && err != io.EOF && !isIgnorableSMTPShutdownError(err) {
		return err
	}
	return nil
}

func isTransientSMTPError(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	if len(msg) > 0 && msg[0] == '4' {
		return true
	}
	return strings.Contains(msg, " 4") || strings.Contains(strings.ToLower(msg), "temporary")
}

func isIgnorableSMTPShutdownError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "closenotify") || strings.Contains(msg, "closed pipe")
}

func (o *Outbound) resolveOriginPeer(ctx context.Context, originHost string) (db.MM4Peer, error) {
	peers, err := o.router.repo.ListMM4Peers(ctx)
	if err != nil {
		return db.MM4Peer{}, err
	}
	for _, peer := range peers {
		if !peer.Active {
			continue
		}
		if strings.EqualFold(peer.Domain, originHost) || strings.EqualFold(peer.SMTPHost, originHost) {
			return peer, nil
		}
	}
	return db.MM4Peer{}, fmt.Errorf("mm4: no peer configured for origin host %s", originHost)
}

func mm4StatusForMessage(status message.Status) string {
	switch status {
	case message.StatusDelivered:
		return "Retrieved"
	case message.StatusRejected:
		return "Rejected"
	case message.StatusExpired:
		return "Expired"
	case message.StatusUnreachable:
		return "Unrecognised"
	default:
		return "Forwarded"
	}
}
