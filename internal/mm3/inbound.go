package mm3

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net"
	"net/mail"
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
	cfg             config.MM3Config
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
		server.cfg = cfg.MM3
		server.retrieveBaseURL = cfg.MM1.RetrieveBaseURL
		server.defaultExpiry = cfg.Limits.DefaultMessageExpiry
		server.maxRetention = cfg.Limits.MaxMessageRetention
		if cfg.MM3.TLSCertFile != "" && cfg.MM3.TLSKeyFile != "" {
			if cert, err := tls.LoadX509KeyPair(cfg.MM3.TLSCertFile, cfg.MM3.TLSKeyFile); err == nil {
				server.tlsCfg = &tls.Config{
					Certificates: []tls.Certificate{cert},
					MinVersion:   tls.VersionTLS12,
				}
			}
		}
	}
	return server
}

func (s *InboundServer) Handle(conn net.Conn) error {
	defer conn.Close()
	log := zap.L().With(zap.String("interface", "mm3"), zap.String("remote", conn.RemoteAddr().String()), zap.String("local", conn.LocalAddr().String()))
	log.Debug("mm3 session started")

	reader := bufio.NewReader(conn)
	writer := bufio.NewWriter(conn)
	writeLine := func(line string) error {
		if _, err := writer.WriteString(line + "\r\n"); err != nil {
			return err
		}
		return writer.Flush()
	}

	if err := writeLine("220 VectorCore MMSC MM3"); err != nil {
		return err
	}

	var (
		mailFrom string
		rcpts    []string
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
			log.Debug("mm3 smtp greeting", zap.String("command", cmd))
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
			if err := writeLine("250 OK"); err != nil {
				return err
			}
		case upper == "STARTTLS":
			log.Debug("mm3 starttls requested")
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
		case strings.HasPrefix(upper, "MAIL FROM:"):
			mailFrom = extractPath(cmd)
			rcpts = nil
			log.Debug("mm3 mail from", zap.String("mail_from", mailFrom))
			if err := writeLine("250 OK"); err != nil {
				return err
			}
		case strings.HasPrefix(upper, "RCPT TO:"):
			rcpt := extractPath(cmd)
			rcpts = append(rcpts, rcpt)
			log.Debug("mm3 rcpt to", zap.String("recipient", rcpt), zap.Int("recipient_count", len(rcpts)))
			if err := writeLine("250 OK"); err != nil {
				return err
			}
		case upper == "RSET":
			mailFrom = ""
			rcpts = nil
			if err := writeLine("250 OK"); err != nil {
				return err
			}
		case upper == "NOOP":
			if err := writeLine("250 OK"); err != nil {
				return err
			}
		case upper == "DATA":
			log.Debug("mm3 data command", zap.String("mail_from", mailFrom), zap.Int("recipient_count", len(rcpts)))
			if err := writeLine("354 End data with <CR><LF>.<CR><LF>"); err != nil {
				return err
			}
			data, err := readSMTPData(reader)
			if err != nil {
				return err
			}
			if err := s.handleDATA(context.Background(), mailFrom, rcpts, data); err != nil {
				log.Warn("mm3 message rejected", zap.Error(err), zap.Int("payload_bytes", len(data)))
				if err := writeLine("550 Message rejected"); err != nil {
					return err
				}
				continue
			}
			log.Debug("mm3 message accepted", zap.Int("payload_bytes", len(data)))
			if err := writeLine("250 OK"); err != nil {
				return err
			}
		case upper == "QUIT":
			log.Debug("mm3 session ended by peer")
			return writeLine("221 Bye")
		default:
			if err := writeLine("502 Command not implemented"); err != nil {
				return err
			}
		}
	}
}

func (s *InboundServer) handleDATA(ctx context.Context, mailFrom string, rcpts []string, data []byte) error {
	log := zap.L().With(
		zap.String("interface", "mm3"),
		zap.String("mail_from", mailFrom),
		zap.Strings("recipients", rcpts),
		zap.Int("payload_bytes", len(data)),
	)
	if s.cfg.MaxMessageSizeBytes > 0 && int64(len(data)) > s.cfg.MaxMessageSizeBytes {
		log.Debug("mm3 message exceeds max size", zap.Int64("max_bytes", s.cfg.MaxMessageSizeBytes))
		return fmt.Errorf("mm3: message exceeds maximum size")
	}

	msg, err := decodeEmailMessage(data, mailFrom, rcpts, s.hostname)
	if err != nil {
		log.Debug("mm3 message decode failed", zap.Error(err))
		return err
	}
	routeResult, err := s.resolveRoute(ctx, msg)
	if err != nil {
		log.Warn("mm3 route resolution failed", zap.Error(err), zap.Strings("normalized_recipients", msg.To))
		return err
	}
	if routeResult != nil {
		log.Debug("mm3 route resolved", zap.String("destination", routeResult.Destination.String()), zap.String("message_id", msg.ID))
	}
	if routeResult == nil || routeResult.Destination != routing.DestinationLocal {
		destination := routing.DestinationUnknown.String()
		if routeResult != nil {
			destination = routeResult.Destination.String()
		}
		log.Debug("mm3 unsupported destination", zap.String("destination", destination))
		return fmt.Errorf("mm3: unsupported recipient destination")
	}
	if err := s.applyAdaptation(ctx, msg, routeResult); err != nil {
		log.Warn("mm3 adaptation failed", zap.Error(err), zap.String("message_id", msg.ID))
		return err
	}

	retrieve, err := mmspdu.Encode(mmspdu.NewRetrieveConf(msg.TransactionID, partsToPDUParts(msg.Parts)))
	if err != nil {
		log.Warn("mm3 content store failed", zap.Error(err), zap.String("message_id", msg.ID))
		return err
	}
	contentPath, err := s.store.Put(ctx, msg.ID, bytes.NewReader(retrieve), int64(len(retrieve)), "application/vnd.wap.mms-message")
	if err != nil {
		return err
	}
	msg.ContentPath = contentPath
	msg.StoreID = contentPath
	msg.MessageSize = int64(len(retrieve))

	message.ApplyDefaultExpiry(msg, s.defaultExpiry, s.maxRetention)
	if err := s.repo.CreateMessage(ctx, msg); err != nil {
		log.Warn("mm3 message persistence failed", zap.Error(err), zap.String("message_id", msg.ID))
		return err
	}
	log.Debug("mm3 inbound message persisted", zap.String("message_id", msg.ID), zap.Strings("recipients", msg.To))
	return s.dispatchLocalNotification(ctx, msg)
}

func decodeEmailMessage(data []byte, envelopeFrom string, envelopeRecipients []string, hostname string) (*message.Message, error) {
	parsed, err := mail.ReadMessage(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	recipients := normalizeRecipients(envelopeRecipients)
	if len(recipients) == 0 {
		recipients = parseAddressList(parsed.Header.Get("To"))
	}
	if len(recipients) == 0 {
		return nil, fmt.Errorf("mm3: missing recipients")
	}

	parts, topLevel, err := parseMIMEParts(parsed.Header, parsed.Body)
	if err != nil {
		return nil, err
	}
	if len(parts) == 0 {
		return nil, fmt.Errorf("mm3: missing message content")
	}

	from := strings.TrimSpace(envelopeFrom)
	if from == "" {
		from = firstAddress(parsed.Header.Get("From"))
	}
	if from == "" {
		from = "system-user@" + hostname
	}

	subject := parsed.Header.Get("Subject")
	msgID := parsed.Header.Get("Message-ID")
	msgID = strings.Trim(strings.TrimSpace(msgID), "<>")
	if msgID == "" {
		msgID = routing.NewMessageID()
	}
	transactionID := routing.NewMessageID()

	return &message.Message{
		ID:            msgID,
		TransactionID: transactionID,
		Status:        message.StatusQueued,
		Direction:     message.DirectionMT,
		From:          from,
		To:            recipients,
		Subject:       subject,
		Parts:         parts,
		ContentType:   topLevel,
		MMSVersion:    "1.3",
		Origin:        message.InterfaceMM3,
		OriginHost:    hostname,
	}, nil
}

func parseMIMEParts(header mail.Header, body io.Reader) ([]message.Part, string, error) {
	mediaType, params, err := mime.ParseMediaType(header.Get("Content-Type"))
	if err != nil || mediaType == "" {
		payload, readErr := io.ReadAll(body)
		if readErr != nil {
			return nil, "", readErr
		}
		return []message.Part{{
			ContentType: "text/plain",
			Data:        payload,
			Size:        int64(len(payload)),
		}}, "text/plain", nil
	}
	if !strings.HasPrefix(strings.ToLower(mediaType), "multipart/") {
		payload, err := io.ReadAll(body)
		if err != nil {
			return nil, "", err
		}
		return []message.Part{{
			ContentType: mediaType,
			Data:        payload,
			Size:        int64(len(payload)),
		}}, mediaType, nil
	}
	reader := multipart.NewReader(body, params["boundary"])
	parts, err := readMultipart(reader)
	if err != nil {
		return nil, "", err
	}
	return parts, mediaType, nil
}

func readMultipart(reader *multipart.Reader) ([]message.Part, error) {
	var parts []message.Part
	for {
		part, err := reader.NextPart()
		if err != nil {
			if err == io.EOF {
				return parts, nil
			}
			return nil, err
		}
		mediaType, params, parseErr := mime.ParseMediaType(part.Header.Get("Content-Type"))
		if parseErr == nil && strings.HasPrefix(strings.ToLower(mediaType), "multipart/") {
			nested, err := readMultipart(multipart.NewReader(part, params["boundary"]))
			if err != nil {
				return nil, err
			}
			parts = append(parts, nested...)
			continue
		}
		payload, err := io.ReadAll(part)
		if err != nil {
			return nil, err
		}
		if mediaType == "" {
			mediaType = "application/octet-stream"
		}
		item := message.Part{
			ContentType: mediaType,
			Data:        payload,
			Size:        int64(len(payload)),
		}
		if cid := strings.TrimSpace(part.Header.Get("Content-ID")); cid != "" {
			item.ContentID = strings.Trim(cid, "<>")
		}
		if loc := strings.TrimSpace(part.Header.Get("Content-Location")); loc != "" {
			item.ContentLocation = loc
		}
		parts = append(parts, item)
	}
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
	return nil
}

func (s *InboundServer) dispatchLocalNotification(ctx context.Context, msg *message.Message) error {
	if s.push == nil || len(msg.To) == 0 {
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
	notification.Headers[mmspdu.FieldMessageClass] = mmspdu.NewTokenValue(mmspdu.FieldMessageClass, mm3MessageClass(msg.MessageClass))
	notification.Headers[mmspdu.FieldExpiry] = mmspdu.NewRelativeDateValue(mmspdu.FieldExpiry, mm3NotificationExpirySeconds(msg.Expiry))
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

func mm3MessageClass(class message.MessageClass) byte {
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

func mm3NotificationExpirySeconds(expiry *time.Time) uint64 {
	if expiry != nil && !expiry.IsZero() {
		delta := time.Until(expiry.UTC())
		if delta <= 0 {
			return 1
		}
		return uint64(delta.Round(time.Second) / time.Second)
	}
	return uint64((7 * 24 * time.Hour) / time.Second)
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
		value = strings.TrimSpace(value)
		if value != "" {
			out = append(out, value)
		}
	}
	return out
}

func parseAddressList(value string) []string {
	if value == "" {
		return nil
	}
	addrs, err := mail.ParseAddressList(value)
	if err != nil {
		return nil
	}
	out := make([]string, 0, len(addrs))
	for _, addr := range addrs {
		if addr.Address != "" {
			out = append(out, addr.Address)
		}
	}
	return out
}

func firstAddress(value string) string {
	items := parseAddressList(value)
	if len(items) == 0 {
		return ""
	}
	return items[0]
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
