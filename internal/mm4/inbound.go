package mm4

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"time"

	"go.uber.org/zap"

	"github.com/vectorcore/vectorcore-mmsc/internal/adapt"
	"github.com/vectorcore/vectorcore-mmsc/internal/config"
	"github.com/vectorcore/vectorcore-mmsc/internal/db"
	"github.com/vectorcore/vectorcore-mmsc/internal/message"
	"github.com/vectorcore/vectorcore-mmsc/internal/mmspdu"
	"github.com/vectorcore/vectorcore-mmsc/internal/routing"
	"github.com/vectorcore/vectorcore-mmsc/internal/store"
)

type PushSender interface {
	SubmitWAPPush(context.Context, string, string, []byte) error
}

type trackedPushSender interface {
	SubmitWAPPushForMessage(context.Context, string, string, string, []byte) error
}

type InboundServer struct {
	repo            db.Repository
	store           store.Store
	router          *routing.Engine
	push            PushSender
	hostname        string
	adapt           *adapt.Pipeline
	tlsCfg          *tls.Config
	retrieveBaseURL string
	defaultExpiry   time.Duration
	maxRetention    time.Duration
}

func NewInboundServer(cfg *config.Config, repo db.Repository, contentStore store.Store, router *routing.Engine, push PushSender, hostname string) *InboundServer {
	server := &InboundServer{
		repo:     repo,
		store:    contentStore,
		router:   router,
		push:     push,
		hostname: hostname,
		adapt:    adapt.NewPipeline(cfg.Adapt, repo),
	}
	if cfg != nil {
		server.retrieveBaseURL = cfg.MM1.RetrieveBaseURL
		server.defaultExpiry = cfg.Limits.DefaultMessageExpiry
		server.maxRetention = cfg.Limits.MaxMessageRetention
	}
	if cfg != nil && cfg.MM4.TLSCertFile != "" && cfg.MM4.TLSKeyFile != "" {
		if cert, err := tls.LoadX509KeyPair(cfg.MM4.TLSCertFile, cfg.MM4.TLSKeyFile); err == nil {
			server.tlsCfg = &tls.Config{
				Certificates: []tls.Certificate{cert},
				MinVersion:   tls.VersionTLS12,
			}
		}
	}
	return server
}

func (s *InboundServer) Handle(conn net.Conn) error {
	defer conn.Close()
	log := zap.L().With(zap.String("interface", "mm4"), zap.String("remote", conn.RemoteAddr().String()), zap.String("local", conn.LocalAddr().String()))
	log.Debug("mm4 session started")

	reader := bufio.NewReader(conn)
	writer := bufio.NewWriter(conn)
	writeLine := func(line string) error {
		if _, err := writer.WriteString(line + "\r\n"); err != nil {
			return err
		}
		return writer.Flush()
	}

	if err := writeLine("220 VectorCore MMSC MM4"); err != nil {
		return err
	}

	var (
		mailFrom string
		rcpts    []string
		authPeer *db.MM4Peer
	)

	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			if err == io.EOF {
				return nil
			}
			return err
		}
		cmd := strings.TrimSpace(line)
		upper := strings.ToUpper(cmd)

		switch {
		case strings.HasPrefix(upper, "EHLO"), strings.HasPrefix(upper, "HELO"):
			log.Debug("mm4 smtp greeting", zap.String("command", cmd))
			if err := writeLine("250-vectorcore-mmsc"); err != nil {
				return err
			}
			if s.tlsCfg != nil {
				if _, ok := conn.(*tls.Conn); !ok {
					if err := writeLine("250-STARTTLS"); err != nil {
						return err
					}
				}
			}
			if s.hasAuthPeers() {
				if err := writeLine("250-AUTH PLAIN"); err != nil {
					return err
				}
			}
			if err := writeLine("250 OK"); err != nil {
				return err
			}
		case upper == "STARTTLS":
			log.Debug("mm4 starttls requested")
			if s.tlsCfg == nil {
				if err := writeLine("454 TLS not available"); err != nil {
					return err
				}
				continue
			}
			if _, ok := conn.(*tls.Conn); ok {
				if err := writeLine("454 TLS already active"); err != nil {
					return err
				}
				continue
			}
			if err := writeLine("220 Ready to start TLS"); err != nil {
				return err
			}
			tlsConn := tls.Server(conn, s.tlsCfg)
			if err := tlsConn.Handshake(); err != nil {
				return err
			}
			conn = tlsConn
			reader = bufio.NewReader(conn)
			writer = bufio.NewWriter(conn)
			mailFrom = ""
			rcpts = nil
			authPeer = nil
		case strings.HasPrefix(upper, "AUTH PLAIN"):
			peer, err := s.authenticateSMTPPlain(cmd)
			if err != nil {
				log.Debug("mm4 authentication failed", zap.Error(err))
				if err := writeLine("535 Authentication failed"); err != nil {
					return err
				}
				continue
			}
			authPeer = peer
			log.Debug("mm4 authentication succeeded", zap.String("peer", peer.Domain))
			if err := writeLine("235 Authentication successful"); err != nil {
				return err
			}
		case strings.HasPrefix(upper, "MAIL FROM:"):
			mailFrom = extractPath(cmd)
			rcpts = nil
			log.Debug("mm4 mail from", zap.String("mail_from", mailFrom))
			if err := writeLine("250 OK"); err != nil {
				return err
			}
		case strings.HasPrefix(upper, "RCPT TO:"):
			rcpt := extractPath(cmd)
			rcpts = append(rcpts, rcpt)
			log.Debug("mm4 rcpt to", zap.String("recipient", rcpt), zap.Int("recipient_count", len(rcpts)))
			if err := writeLine("250 OK"); err != nil {
				return err
			}
		case upper == "RSET":
			mailFrom = ""
			rcpts = nil
			authPeer = nil
			if err := writeLine("250 OK"); err != nil {
				return err
			}
		case upper == "NOOP":
			if err := writeLine("250 OK"); err != nil {
				return err
			}
		case upper == "DATA":
			log.Debug("mm4 data command", zap.String("mail_from", mailFrom), zap.Int("recipient_count", len(rcpts)))
			if err := writeLine("354 End data with <CR><LF>.<CR><LF>"); err != nil {
				return err
			}
			data, err := readSMTPData(reader)
			if err != nil {
				return err
			}
			if err := s.handleDATA(context.Background(), conn, mailFrom, rcpts, data, authPeer); err != nil {
				log.Warn("mm4 message rejected", zap.Error(err), zap.Int("payload_bytes", len(data)))
				if err := writeLine("550 Message rejected"); err != nil {
					return err
				}
				continue
			}
			log.Debug("mm4 message accepted", zap.Int("payload_bytes", len(data)))
			if err := writeLine("250 OK"); err != nil {
				return err
			}
		case upper == "QUIT":
			log.Debug("mm4 session ended by peer")
			return writeLine("221 Bye")
		default:
			if err := writeLine("502 Command not implemented"); err != nil {
				return err
			}
		}
	}
}

func (s *InboundServer) handleDATA(ctx context.Context, conn net.Conn, mailFrom string, rcpts []string, data []byte, authPeer *db.MM4Peer) error {
	log := zap.L().With(
		zap.String("interface", "mm4"),
		zap.String("remote", conn.RemoteAddr().String()),
		zap.String("mail_from", mailFrom),
		zap.Strings("recipients", rcpts),
		zap.Int("payload_bytes", len(data)),
	)
	if !s.authorizedRemote(conn.RemoteAddr(), authPeer) {
		log.Warn("mm4 remote not authorized")
		return io.ErrUnexpectedEOF
	}

	msg, meta, err := DecodeEnvelopeWithMeta(data)
	if err != nil {
		log.Debug("mm4 envelope decode failed", zap.Error(err))
		return err
	}
	if msg.From == "" {
		msg.From = stripType(mailFrom)
	}
	if len(msg.To) == 0 {
		msg.To = normalizeRecipients(rcpts)
	}
	if msg.ID == "" {
		msg.ID = routing.NewMessageID()
	}
	msg.Direction = message.DirectionMT
	msg.Status = message.StatusQueued
	msg.Origin = message.InterfaceMM4
	msg.ContentType = "multipart/related"
	msg.MessageSize = int64(len(data))

	switch meta.MessageType {
	case mm4DeliveryReportReq:
		log.Debug("mm4 delivery report received", zap.String("message_id", msg.ID), zap.String("status", meta.Status), zap.String("request_status", meta.RequestStatusCode))
		return s.handleDeliveryReport(ctx, msg, meta)
	case mm4ReadReplyReportReq:
		log.Debug("mm4 read reply received", zap.String("message_id", msg.ID))
		return s.handleReadReply(ctx, msg, meta)
	case mm4ForwardRes, mm4DeliveryReportRes, mm4ReadReplyReportRes:
		log.Debug("mm4 response envelope ignored", zap.String("message_type", meta.MessageType))
		return nil
	case "", mm4ForwardReq:
	default:
		log.Debug("mm4 unsupported envelope type", zap.String("message_type", meta.MessageType))
		return io.ErrUnexpectedEOF
	}

	routeResult, err := s.resolveRoute(ctx, msg)
	if err != nil {
		log.Warn("mm4 route resolution failed", zap.Error(err), zap.Strings("normalized_recipients", msg.To))
		return err
	}
	if routeResult != nil {
		log.Debug("mm4 route resolved", zap.String("destination", routeResult.Destination.String()), zap.String("message_id", msg.ID))
	}
	if err := s.applyAdaptation(ctx, msg, routeResult); err != nil {
		log.Warn("mm4 adaptation failed", zap.Error(err), zap.String("message_id", msg.ID))
		return err
	}
	if len(msg.Parts) == 0 {
		log.Debug("mm4 inbound message missing content after adaptation", zap.String("message_id", msg.ID))
		return fmt.Errorf("mm4: missing message content")
	}

	retrieve, err := mmspdu.Encode(mmspdu.NewRetrieveConf(msg.TransactionID, partsToPDUParts(msg.Parts)))
	if err != nil {
		return err
	}
	contentPath, err := s.store.Put(ctx, msg.ID, bytes.NewReader(retrieve), int64(len(retrieve)), "application/vnd.wap.mms-message")
	if err != nil {
		return err
	}
	msg.ContentPath = contentPath
	msg.StoreID = contentPath

	message.ApplyDefaultExpiry(msg, s.defaultExpiry, s.maxRetention)
	if err := s.repo.CreateMessage(ctx, msg); err != nil {
		log.Warn("mm4 message persistence failed", zap.Error(err), zap.String("message_id", msg.ID))
		return err
	}
	log.Debug("mm4 inbound message persisted", zap.String("message_id", msg.ID), zap.Strings("recipients", msg.To))
	_ = s.repo.AppendMessageEvent(ctx, db.MessageEvent{
		MessageID: msg.ID,
		Source:    "mm4",
		Type:      "inbound",
		Summary:   "MM4 message received",
		Detail:    fmt.Sprintf("from=%s size=%d", msg.From, msg.MessageSize),
	})
	return s.dispatchLocalNotification(ctx, msg, routeResult)
}

func (s *InboundServer) handleDeliveryReport(ctx context.Context, msg *message.Message, meta *EnvelopeMeta) error {
	if msg == nil || msg.ID == "" {
		return io.ErrUnexpectedEOF
	}
	status := message.StatusDelivered
	if meta != nil && (strings.Contains(strings.ToLower(meta.Status), "reject") || strings.Contains(strings.ToLower(meta.Status), "unreach")) {
		status = message.StatusRejected
	}
	if meta != nil {
		_ = s.repo.AppendMessageEvent(ctx, db.MessageEvent{
			MessageID: msg.ID,
			Source:    "mm4",
			Type:      "delivery-report",
			Summary:   "MM4 delivery report received",
			Detail:    fmt.Sprintf("status=%s request_status=%s", meta.Status, meta.RequestStatusCode),
		})
	}
	return s.repo.UpdateMessageStatus(ctx, msg.ID, status)
}

func (s *InboundServer) handleReadReply(ctx context.Context, msg *message.Message, _ *EnvelopeMeta) error {
	if msg == nil || msg.ID == "" {
		return io.ErrUnexpectedEOF
	}
	_ = s.repo.AppendMessageEvent(ctx, db.MessageEvent{
		MessageID: msg.ID,
		Source:    "mm4",
		Type:      "read-reply",
		Summary:   "MM4 read reply received",
		Detail:    "read reply from peer MMSC",
	})
	return s.repo.UpdateMessageStatus(ctx, msg.ID, message.StatusDelivered)
}

func (s *InboundServer) authorizedRemote(addr net.Addr, authPeer *db.MM4Peer) bool {
	if authPeer != nil && authPeer.Active {
		return true
	}
	ipAddr, ok := addr.(*net.TCPAddr)
	if !ok || ipAddr.IP == nil {
		return true
	}
	peers, err := s.repo.ListMM4Peers(context.Background())
	if err != nil {
		return false
	}
	hasAllowlist := false
	for _, peer := range peers {
		if !peer.Active || len(peer.AllowedIPs) == 0 {
			continue
		}
		hasAllowlist = true
		for _, allowed := range peer.AllowedIPs {
			if allowed == ipAddr.IP.String() {
				return true
			}
		}
	}
	return !hasAllowlist
}

func (s *InboundServer) hasAuthPeers() bool {
	peers, err := s.repo.ListMM4Peers(context.Background())
	if err != nil {
		return false
	}
	for _, peer := range peers {
		if peer.Active && peer.SMTPAuth {
			return true
		}
	}
	return false
}

func (s *InboundServer) authenticateSMTPPlain(cmd string) (*db.MM4Peer, error) {
	parts := strings.SplitN(cmd, " ", 3)
	if len(parts) < 3 {
		return nil, errors.New("missing auth payload")
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(parts[2]))
	if err != nil {
		return nil, err
	}
	fields := strings.Split(string(raw), "\x00")
	if len(fields) < 3 {
		return nil, errors.New("invalid plain auth payload")
	}
	username := fields[1]
	password := fields[2]

	peers, err := s.repo.ListMM4Peers(context.Background())
	if err != nil {
		return nil, err
	}
	for _, peer := range peers {
		if peer.Active && peer.SMTPAuth && peer.SMTPUser == username && peer.SMTPPass == password {
			peerCopy := peer
			return &peerCopy, nil
		}
	}
	return nil, errors.New("invalid credentials")
}

func (s *InboundServer) dispatchLocalNotification(ctx context.Context, msg *message.Message, result *routing.Result) error {
	if s.push == nil || len(msg.To) == 0 {
		return nil
	}
	if result == nil || result.Destination != routing.DestinationLocal {
		return nil
	}

	retrieveURL := s.retrieveBaseURL
	if retrieveURL == "" {
		retrieveURL = "/mms/retrieve"
	}
	notification := mmspdu.NewNotificationInd(msg.TransactionID, retrieveURL+"?id="+msg.ID)
	notification.MMSVersion = "1.2"
	from := msg.From
	if from == "" {
		from = "#insert"
	}
	notification.Headers[mmspdu.FieldFrom] = mmspdu.NewFromValue(from)
	if msg.Subject != "" {
		notification.Headers[mmspdu.FieldSubject] = mmspdu.NewEncodedStringValue(mmspdu.FieldSubject, msg.Subject)
	}
	notification.Headers[mmspdu.FieldMessageClass] = mmspdu.NewTokenValue(mmspdu.FieldMessageClass, mm4MessageClass(msg.MessageClass))
	notification.Headers[mmspdu.FieldExpiry] = mmspdu.NewRelativeDateValue(mmspdu.FieldExpiry, mm4NotificationExpirySeconds(msg.Expiry))
	notification.Headers[mmspdu.FieldMessageSize] = mmspdu.NewLongIntegerValue(mmspdu.FieldMessageSize, uint64(msg.MessageSize))
	encoded, err := mmspdu.Encode(notification)
	if err != nil {
		return err
	}
	push := mmspdu.WrapWAPPush(encoded)
	for _, recipient := range msg.To {
		if tracked, ok := s.push.(trackedPushSender); ok {
			if err := tracked.SubmitWAPPushForMessage(ctx, msg.ID, msg.From, recipient, push); err != nil {
				return err
			}
			continue
		}
		if err := s.push.SubmitWAPPush(ctx, msg.From, recipient, push); err != nil {
			return err
		}
	}
	return s.repo.UpdateMessageStatus(ctx, msg.ID, message.StatusDelivering)
}

func mm4MessageClass(class message.MessageClass) byte {
	switch class {
	case message.ClassAdvertisement:
		return mmspdu.MessageClassAdvertisement
	case message.ClassInformational:
		return mmspdu.MessageClassInformational
	case message.ClassAuto:
		return mmspdu.MessageClassAuto
	default:
		return mmspdu.MessageClassPersonal
	}
}

func mm4NotificationExpirySeconds(expiry *time.Time) uint64 {
	if expiry != nil && !expiry.IsZero() {
		delta := time.Until(expiry.UTC())
		if delta <= 0 {
			return 1
		}
		return uint64(delta.Round(time.Second) / time.Second)
	}
	return uint64((7 * 24 * time.Hour) / time.Second)
}

func (s *InboundServer) resolveRoute(ctx context.Context, msg *message.Message) (*routing.Result, error) {
	if len(msg.To) == 0 {
		return nil, nil
	}
	return s.router.ResolveRecipients(ctx, msg.To)
}

func (s *InboundServer) applyAdaptation(ctx context.Context, msg *message.Message, result *routing.Result) error {
	if s.adapt == nil || result == nil || result.Destination != routing.DestinationLocal {
		return nil
	}
	parts, err := s.adapt.Adapt(ctx, msg.Parts)
	if err != nil {
		return err
	}
	msg.Parts = parts
	if adaptedSize := partsSize(parts); adaptedSize > 0 {
		msg.MessageSize = adaptedSize
	}
	return nil
}

func readSMTPData(reader *bufio.Reader) ([]byte, error) {
	var body bytes.Buffer
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return nil, err
		}
		if strings.TrimRight(line, "\r\n") == "." {
			return body.Bytes(), nil
		}
		if strings.HasPrefix(line, "..") {
			line = line[1:]
		}
		body.WriteString(line)
	}
}

func extractPath(line string) string {
	idx := strings.Index(line, ":")
	if idx < 0 {
		return ""
	}
	value := strings.TrimSpace(line[idx+1:])
	value = strings.TrimPrefix(value, "<")
	value = strings.TrimSuffix(value, ">")
	return value
}

func normalizeRecipients(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = stripType(strings.TrimSpace(value))
		if value != "" {
			out = append(out, value)
		}
	}
	return out
}

func partsToPDUParts(parts []message.Part) []mmspdu.Part {
	out := make([]mmspdu.Part, 0, len(parts))
	for _, part := range parts {
		headers := map[string]string{}
		if part.ContentID != "" {
			headers["content-id"] = part.ContentID
		}
		if part.ContentLocation != "" {
			headers["content-location"] = part.ContentLocation
		}
		out = append(out, mmspdu.Part{
			Headers:     headers,
			ContentType: part.ContentType,
			Data:        append([]byte(nil), part.Data...),
		})
	}
	return out
}

func partsSize(parts []message.Part) int64 {
	var total int64
	for _, part := range parts {
		if part.Size > 0 {
			total += part.Size
			continue
		}
		total += int64(len(part.Data))
	}
	return total
}
