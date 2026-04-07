package mm7

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"net"
	"net/http"
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

type MM4Sender interface {
	Send(context.Context, *message.Message) error
}

type MM3Sender interface {
	Send(context.Context, *message.Message) error
}

type Server struct {
	cfg    *config.Config
	repo   db.Repository
	store  store.Store
	router *routing.Engine
	push   PushSender
	mm4    MM4Sender
	mm3    MM3Sender
	adapt  *adapt.Pipeline
}

func NewServer(cfg *config.Config, repo db.Repository, contentStore store.Store, router *routing.Engine, push PushSender, mm4 MM4Sender, mm3 MM3Sender) *Server {
	return &Server{
		cfg:    cfg,
		repo:   repo,
		store:  contentStore,
		router: router,
		push:   push,
		mm4:    mm4,
		mm3:    mm3,
		adapt:  adapt.NewPipeline(cfg.Adapt, repo),
	}
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	log := zap.L().With(
		zap.String("interface", "mm7"),
		zap.String("remote", r.RemoteAddr),
		zap.String("method", r.Method),
		zap.String("path", r.URL.RequestURI()),
	)
	if r.Method != http.MethodPost {
		log.Debug("mm7 rejected method")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if s.cfg.MM7.EAIFPath != "" && r.URL.Path == s.cfg.MM7.EAIFPath {
		log.Debug("mm7 routing to eaif handler")
		s.handleEAIF(w, r)
		return
	}
	if s.cfg.MM7.Path != "" && r.URL.Path != s.cfg.MM7.Path {
		log.Debug("mm7 path not found")
		http.NotFound(w, r)
		return
	}

	parsed, err := ParseRequest(r)
	if err != nil {
		log.Debug("mm7 request parse failed", zap.Error(err))
		_ = WriteFaultWithVersion(w, http.StatusBadRequest, "", statusClientErr, "invalid MM7 request", "Client", s.cfg.MM7.Namespace)
		return
	}
	switch {
	case parsed.Envelope.Body.SubmitReq != nil:
		s.handleSubmit(w, r, parsed)
	case parsed.Envelope.Body.CancelReq != nil:
		s.handleCancel(w, r, parsed)
	case parsed.Envelope.Body.ReplaceReq != nil:
		s.handleReplace(w, r, parsed)
	default:
		log.Debug("mm7 unsupported operation", zap.String("transaction_id", parsed.Envelope.Header.TransactionID.Value))
		_ = WriteFaultWithVersion(w, http.StatusBadRequest, parsed.Envelope.Header.TransactionID.Value, statusClientErr, "unsupported MM7 operation", "Client", s.cfg.MM7.Namespace)
	}
}

func (s *Server) handleEAIF(w http.ResponseWriter, r *http.Request) {
	log := zap.L().With(zap.String("interface", "mm7"), zap.String("remote", r.RemoteAddr), zap.String("path", r.URL.RequestURI()))
	vasp, err := s.authenticate(r, "")
	if err != nil {
		log.Debug("mm7 eaif authentication failed", zap.Error(err))
		w.Header().Set("WWW-Authenticate", `Basic realm="VectorCore MMSC"`)
		WriteEAIFResponse(w, http.StatusUnauthorized, s.cfg.MM7.EAIFVersion, "")
		return
	}
	log = log.With(zap.String("vasp_id", vasp.VASPID))
	req, err := ParseEAIFRequest(r)
	if err != nil {
		log.Debug("mm7 eaif parse failed", zap.Error(err))
		WriteEAIFResponse(w, http.StatusBadRequest, s.cfg.MM7.EAIFVersion, "")
		return
	}

	msg, routeResult, err := s.buildEAIFMessage(req, vasp)
	if err != nil {
		log.Debug("mm7 eaif message build failed", zap.Error(err))
		WriteEAIFResponse(w, http.StatusBadRequest, s.cfg.MM7.EAIFVersion, "")
		return
	}
	log = log.With(zap.String("message_id", msg.ID), zap.String("transaction_id", msg.TransactionID))
	if routeResult != nil {
		log.Debug("mm7 eaif route resolved", zap.String("destination", routeResult.Destination.String()), zap.Strings("recipients", msg.To))
	}
	if err := s.applyAdaptation(r.Context(), msg, routeResult); err != nil {
		log.Debug("mm7 eaif adaptation failed", zap.Error(err))
		WriteEAIFResponse(w, http.StatusUnprocessableEntity, s.cfg.MM7.EAIFVersion, "")
		return
	}
	if err := s.persistLocalContent(r.Context(), msg, routeResult); err != nil {
		log.Warn("mm7 eaif content persistence failed", zap.Error(err))
		WriteEAIFResponse(w, http.StatusInternalServerError, s.cfg.MM7.EAIFVersion, "")
		return
	}
	message.ApplyDefaultExpiry(msg, s.cfg.Limits.DefaultMessageExpiry, s.cfg.Limits.MaxMessageRetention)
	if err := s.repo.CreateMessage(r.Context(), msg); err != nil {
		log.Warn("mm7 eaif message persistence failed", zap.Error(err))
		WriteEAIFResponse(w, http.StatusInternalServerError, s.cfg.MM7.EAIFVersion, "")
		return
	}
	_ = s.repo.AppendMessageEvent(r.Context(), db.MessageEvent{
		MessageID: msg.ID,
		Source:    "mm7",
		Type:      "submit",
		Summary:   "MM7 EAIF submit received",
		Detail:    fmt.Sprintf("vasp=%s route=%d", vasp.VASPID, routeResult.Destination),
	})
	if err := s.dispatchLocalNotification(r.Context(), msg, routeResult); err != nil {
		_ = s.repo.UpdateMessageStatus(r.Context(), msg.ID, message.StatusUnreachable)
		log.Warn("mm7 eaif local notification failed", zap.Error(err))
		WriteEAIFResponse(w, http.StatusBadGateway, s.cfg.MM7.EAIFVersion, msg.ID)
		return
	}
	if err := s.dispatchMM4Relay(r.Context(), msg, routeResult); err != nil {
		_ = s.repo.UpdateMessageStatus(r.Context(), msg.ID, message.StatusUnreachable)
		log.Warn("mm7 eaif mm4 relay failed", zap.Error(err))
		WriteEAIFResponse(w, http.StatusBadGateway, s.cfg.MM7.EAIFVersion, msg.ID)
		return
	}
	if err := s.dispatchMM3Relay(r.Context(), msg, routeResult); err != nil {
		_ = s.repo.UpdateMessageStatus(r.Context(), msg.ID, message.StatusUnreachable)
		log.Warn("mm7 eaif mm3 relay failed", zap.Error(err))
		WriteEAIFResponse(w, http.StatusBadGateway, s.cfg.MM7.EAIFVersion, msg.ID)
		return
	}
	log.Debug("mm7 eaif accepted")
	WriteEAIFResponse(w, http.StatusNoContent, s.cfg.MM7.EAIFVersion, msg.ID)
}

func (s *Server) handleSubmit(w http.ResponseWriter, r *http.Request, parsed *ParsedRequest) {
	req := parsed.Envelope.Body.SubmitReq
	log := zap.L().With(
		zap.String("interface", "mm7"),
		zap.String("remote", r.RemoteAddr),
		zap.String("transaction_id", parsed.Envelope.Header.TransactionID.Value),
	)
	vasp, err := s.authenticate(r, req.SenderIdentification.VASPID)
	if err != nil {
		log.Debug("mm7 submit authentication failed", zap.Error(err), zap.String("claimed_vasp_id", req.SenderIdentification.VASPID))
		_ = WriteFaultWithVersion(w, http.StatusUnauthorized, parsed.Envelope.Header.TransactionID.Value, statusAuthErr, "authentication failed", "Client.Auth", s.cfg.MM7.Namespace)
		return
	}
	log = log.With(zap.String("vasp_id", vasp.VASPID))

	msg, routeResult, err := s.buildMessage(parsed, vasp)
	if err != nil {
		log.Debug("mm7 submit message build failed", zap.Error(err))
		_ = WriteFaultWithVersion(w, http.StatusBadRequest, parsed.Envelope.Header.TransactionID.Value, statusClientErr, err.Error(), "Client", s.cfg.MM7.Namespace)
		return
	}
	log = log.With(zap.String("message_id", msg.ID))
	if routeResult != nil {
		log.Debug("mm7 submit route resolved", zap.String("destination", routeResult.Destination.String()), zap.Strings("recipients", msg.To))
	}
	if err := s.applyAdaptation(r.Context(), msg, routeResult); err != nil {
		log.Debug("mm7 submit adaptation failed", zap.Error(err))
		_ = WriteFaultWithVersion(w, http.StatusUnprocessableEntity, parsed.Envelope.Header.TransactionID.Value, statusClientErr, err.Error(), "Client", s.cfg.MM7.Namespace)
		return
	}
	if err := s.persistLocalContent(r.Context(), msg, routeResult); err != nil {
		log.Warn("mm7 submit content persistence failed", zap.Error(err))
		_ = WriteFaultWithVersion(w, http.StatusInternalServerError, parsed.Envelope.Header.TransactionID.Value, statusServerErr, "failed to store adapted content", "Server", s.cfg.MM7.Namespace)
		return
	}
	message.ApplyDefaultExpiry(msg, s.cfg.Limits.DefaultMessageExpiry, s.cfg.Limits.MaxMessageRetention)
	if err := s.repo.CreateMessage(r.Context(), msg); err != nil {
		log.Warn("mm7 submit message persistence failed", zap.Error(err))
		_ = WriteFaultWithVersion(w, http.StatusInternalServerError, parsed.Envelope.Header.TransactionID.Value, statusServerErr, "failed to persist message", "Server", s.cfg.MM7.Namespace)
		return
	}
	_ = s.repo.AppendMessageEvent(r.Context(), db.MessageEvent{
		MessageID: msg.ID,
		Source:    "mm7",
		Type:      "submit",
		Summary:   "MM7 SOAP submit received",
		Detail:    fmt.Sprintf("vasp=%s route=%d", vasp.VASPID, routeResult.Destination),
	})
	if err := s.dispatchLocalNotification(r.Context(), msg, routeResult); err != nil {
		_ = s.repo.UpdateMessageStatus(r.Context(), msg.ID, message.StatusUnreachable)
		log.Warn("mm7 submit local notification failed", zap.Error(err))
		_ = WriteFaultWithVersion(w, http.StatusBadGateway, parsed.Envelope.Header.TransactionID.Value, statusServerErr, "failed to dispatch notification", "Server", s.cfg.MM7.Namespace)
		return
	}
	if err := s.dispatchMM4Relay(r.Context(), msg, routeResult); err != nil {
		_ = s.repo.UpdateMessageStatus(r.Context(), msg.ID, message.StatusUnreachable)
		log.Warn("mm7 submit mm4 relay failed", zap.Error(err))
		_ = WriteFaultWithVersion(w, http.StatusBadGateway, parsed.Envelope.Header.TransactionID.Value, statusServerErr, "failed to relay message", "Server", s.cfg.MM7.Namespace)
		return
	}
	if err := s.dispatchMM3Relay(r.Context(), msg, routeResult); err != nil {
		_ = s.repo.UpdateMessageStatus(r.Context(), msg.ID, message.StatusUnreachable)
		log.Warn("mm7 submit mm3 relay failed", zap.Error(err))
		_ = WriteFaultWithVersion(w, http.StatusBadGateway, parsed.Envelope.Header.TransactionID.Value, statusServerErr, "failed to send email message", "Server", s.cfg.MM7.Namespace)
		return
	}
	log.Debug("mm7 submit accepted")
	_ = WriteOperationRsp(w, http.StatusOK, parsed.Envelope.Header.TransactionID.Value, "submit", msg.ID, s.cfg.MM7.Version, s.cfg.MM7.Namespace)
}

func (s *Server) handleCancel(w http.ResponseWriter, r *http.Request, parsed *ParsedRequest) {
	req := parsed.Envelope.Body.CancelReq
	vasp, err := s.authenticate(r, req.VASPID)
	if err != nil {
		_ = WriteFaultWithVersion(w, http.StatusUnauthorized, parsed.Envelope.Header.TransactionID.Value, statusAuthErr, "authentication failed", "Client.Auth", s.cfg.MM7.Namespace)
		return
	}
	msg, err := s.repo.GetMessage(r.Context(), req.MessageID)
	if err != nil {
		_ = WriteFaultWithVersion(w, http.StatusNotFound, parsed.Envelope.Header.TransactionID.Value, statusClientErr, "message not found", "Client", s.cfg.MM7.Namespace)
		return
	}
	if msg.Origin != message.InterfaceMM7 || msg.OriginHost != vasp.VASPID {
		_ = WriteFaultWithVersion(w, http.StatusForbidden, parsed.Envelope.Header.TransactionID.Value, statusAuthErr, "message does not belong to VASP", "Client.Auth", s.cfg.MM7.Namespace)
		return
	}
	if err := s.repo.UpdateMessageStatus(r.Context(), msg.ID, message.StatusRejected); err != nil {
		_ = WriteFaultWithVersion(w, http.StatusInternalServerError, parsed.Envelope.Header.TransactionID.Value, statusServerErr, "failed to cancel message", "Server", s.cfg.MM7.Namespace)
		return
	}
	_ = s.repo.AppendMessageEvent(r.Context(), db.MessageEvent{
		MessageID: msg.ID,
		Source:    "mm7",
		Type:      "cancel",
		Summary:   "MM7 cancel request applied",
		Detail:    fmt.Sprintf("vasp=%s", vasp.VASPID),
	})
	_ = WriteOperationRsp(w, http.StatusOK, parsed.Envelope.Header.TransactionID.Value, "cancel", msg.ID, s.cfg.MM7.Version, s.cfg.MM7.Namespace)
}

func (s *Server) handleReplace(w http.ResponseWriter, r *http.Request, parsed *ParsedRequest) {
	req := parsed.Envelope.Body.ReplaceReq
	vasp, err := s.authenticate(r, req.VASPID)
	if err != nil {
		_ = WriteFaultWithVersion(w, http.StatusUnauthorized, parsed.Envelope.Header.TransactionID.Value, statusAuthErr, "authentication failed", "Client.Auth", s.cfg.MM7.Namespace)
		return
	}
	msg, err := s.repo.GetMessage(r.Context(), req.MessageID)
	if err != nil {
		_ = WriteFaultWithVersion(w, http.StatusNotFound, parsed.Envelope.Header.TransactionID.Value, statusClientErr, "message not found", "Client", s.cfg.MM7.Namespace)
		return
	}
	if msg.Origin != message.InterfaceMM7 || msg.OriginHost != vasp.VASPID {
		_ = WriteFaultWithVersion(w, http.StatusForbidden, parsed.Envelope.Header.TransactionID.Value, statusAuthErr, "message does not belong to VASP", "Client.Auth", s.cfg.MM7.Namespace)
		return
	}
	parts, err := parsed.partsForContent(req.Content.Href)
	if err != nil {
		_ = WriteFaultWithVersion(w, http.StatusBadRequest, parsed.Envelope.Header.TransactionID.Value, statusClientErr, err.Error(), "Client", s.cfg.MM7.Namespace)
		return
	}
	rawPDU, err := mmspdu.Encode(mmspdu.NewRetrieveConf(msg.TransactionID, toPDUParts(parts)))
	if err != nil {
		_ = WriteFaultWithVersion(w, http.StatusInternalServerError, parsed.Envelope.Header.TransactionID.Value, statusServerErr, "failed to encode replacement content", "Server", s.cfg.MM7.Namespace)
		return
	}
	contentPath, err := s.store.Put(r.Context(), msg.ID, bytes.NewReader(rawPDU), int64(len(rawPDU)), "application/vnd.wap.mms-message")
	if err != nil {
		_ = WriteFaultWithVersion(w, http.StatusInternalServerError, parsed.Envelope.Header.TransactionID.Value, statusServerErr, "failed to store replacement content", "Server", s.cfg.MM7.Namespace)
		return
	}
	if err := s.repo.UpdateMessageContent(r.Context(), msg.ID, db.MessageContentUpdate{
		Subject:     req.Subject,
		ContentType: topLevelContentType(parts),
		MessageSize: int64(len(rawPDU)),
		ContentPath: contentPath,
		StoreID:     contentPath,
	}); err != nil {
		_ = WriteFaultWithVersion(w, http.StatusInternalServerError, parsed.Envelope.Header.TransactionID.Value, statusServerErr, "failed to replace message content", "Server", s.cfg.MM7.Namespace)
		return
	}
	_ = s.repo.AppendMessageEvent(r.Context(), db.MessageEvent{
		MessageID: msg.ID,
		Source:    "mm7",
		Type:      "replace",
		Summary:   "MM7 replace request applied",
		Detail:    fmt.Sprintf("vasp=%s subject=%s", vasp.VASPID, req.Subject),
	})
	_ = WriteOperationRsp(w, http.StatusOK, parsed.Envelope.Header.TransactionID.Value, "replace", msg.ID, s.cfg.MM7.Version, s.cfg.MM7.Namespace)
}

func (s *Server) authenticate(r *http.Request, claimedVASPID string) (*db.MM7VASP, error) {
	vasps, err := s.repo.ListMM7VASPs(r.Context())
	if err != nil {
		return nil, err
	}
	user, pass, hasAuth := basicAuth(r.Header.Get("Authorization"))
	for _, vasp := range vasps {
		if !vasp.Active {
			continue
		}
		if claimedVASPID != "" && vasp.VASPID != claimedVASPID {
			continue
		}
		if hasAuth {
			if user != vasp.VASPID || vasp.SharedSecret != pass {
				continue
			}
		} else if vasp.SharedSecret != "" {
			continue
		}
		if !allowedRemote(r.RemoteAddr, vasp.AllowedIPs) {
			continue
		}
		vaspCopy := vasp
		return &vaspCopy, nil
	}
	return nil, fmt.Errorf("vasp not authorized")
}

func (s *Server) buildMessage(parsed *ParsedRequest, vasp *db.MM7VASP) (*message.Message, *routing.Result, error) {
	req := parsed.Envelope.Body.SubmitReq
	if req == nil {
		return nil, nil, fmt.Errorf("missing submitReq")
	}
	recipients := make([]string, 0, len(req.Recipients.To))
	for _, to := range req.Recipients.To {
		switch {
		case to.Number != "":
			recipients = append(recipients, strings.TrimSpace(to.Number))
		case to.Email != "":
			recipients = append(recipients, strings.TrimSpace(to.Email))
		}
	}
	if len(recipients) == 0 {
		return nil, nil, fmt.Errorf("missing recipients")
	}

	parts, err := parsed.partsForContent(req.Content.Href)
	if err != nil {
		return nil, nil, err
	}
	transactionID := parsed.Envelope.Header.TransactionID.Value
	if transactionID == "" {
		transactionID = routing.NewMessageID()
	}
	messageID := routing.NewMessageID()
	routeResult, err := s.router.ResolveRecipients(context.Background(), recipients)
	if err != nil {
		return nil, nil, err
	}

	rawPDU, err := mmspdu.Encode(mmspdu.NewRetrieveConf(transactionID, toPDUParts(parts)))
	if err != nil {
		return nil, nil, fmt.Errorf("encode stored content: %w", err)
	}
	contentPath, err := s.store.Put(context.Background(), messageID, bytes.NewReader(rawPDU), int64(len(rawPDU)), "application/vnd.wap.mms-message")
	if err != nil {
		return nil, nil, fmt.Errorf("store content: %w", err)
	}

	status := message.StatusQueued
	if routeResult != nil && (routeResult.Destination == routing.DestinationMM4 || routeResult.Destination == routing.DestinationMM3) {
		status = message.StatusForwarded
	}
	return &message.Message{
		ID:            messageID,
		TransactionID: transactionID,
		Status:        status,
		Direction:     message.DirectionMT,
		From:          senderAddress(req, vasp),
		To:            recipients,
		Subject:       req.Subject,
		Parts:         parts,
		ContentType:   topLevelContentType(parts),
		MMSVersion:    "1.3",
		MessageSize:   int64(len(rawPDU)),
		ContentPath:   contentPath,
		StoreID:       contentPath,
		Origin:        message.InterfaceMM7,
		OriginHost:    vasp.VASPID,
	}, routeResult, nil
}

func (s *Server) buildEAIFMessage(req *EAIFRequest, vasp *db.MM7VASP) (*message.Message, *routing.Result, error) {
	if req == nil || req.PDU == nil {
		return nil, nil, fmt.Errorf("missing eaif request")
	}
	messageID := routing.NewMessageID()
	transactionID := req.PDU.TransactionID
	if transactionID == "" {
		transactionID = routing.NewMessageID()
	}
	routeResult, err := s.router.ResolveRecipients(context.Background(), req.Recipients)
	if err != nil {
		return nil, nil, err
	}
	status := message.StatusQueued
	if routeResult != nil && (routeResult.Destination == routing.DestinationMM4 || routeResult.Destination == routing.DestinationMM3) {
		status = message.StatusForwarded
	}
	msg := &message.Message{
		ID:            messageID,
		TransactionID: transactionID,
		Status:        status,
		Direction:     message.DirectionMT,
		From:          req.From,
		To:            append([]string(nil), req.Recipients...),
		Parts:         messagePartsFromPDU(req.PDU),
		ContentType:   pduContentType(req.PDU),
		MMSVersion:    req.PDU.MMSVersion,
		MessageSize:   int64(len(req.Raw)),
		ContentPath:   "",
		StoreID:       "",
		Origin:        message.InterfaceMM7,
		OriginHost:    vasp.VASPID,
	}
	if subject, err := pduHeaderText(req.PDU, mmspdu.FieldSubject); err == nil {
		msg.Subject = subject
	}
	return msg, routeResult, nil
}

func (s *Server) dispatchLocalNotification(ctx context.Context, msg *message.Message, result *routing.Result) error {
	if result == nil || result.Destination != routing.DestinationLocal || s.push == nil || len(msg.To) == 0 {
		return nil
	}
	retrieveURL := s.cfg.MM1.RetrieveBaseURL
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
	notification.Headers[mmspdu.FieldMessageClass] = mmspdu.NewTokenValue(mmspdu.FieldMessageClass, mm7MessageClass(msg.MessageClass))
	notification.Headers[mmspdu.FieldExpiry] = mmspdu.NewRelativeDateValue(mmspdu.FieldExpiry, mm7NotificationExpirySeconds(msg.Expiry))
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
	if err := s.repo.UpdateMessageStatus(ctx, msg.ID, message.StatusDelivering); err != nil {
		return err
	}
	msg.Status = message.StatusDelivering
	return nil
}

func mm7MessageClass(class message.MessageClass) byte {
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

func mm7NotificationExpirySeconds(expiry *time.Time) uint64 {
	if expiry != nil && !expiry.IsZero() {
		delta := time.Until(expiry.UTC())
		if delta <= 0 {
			return 1
		}
		return uint64(delta.Round(time.Second) / time.Second)
	}
	return uint64((7 * 24 * time.Hour) / time.Second)
}

func (s *Server) dispatchMM4Relay(ctx context.Context, msg *message.Message, result *routing.Result) error {
	if result == nil || result.Destination != routing.DestinationMM4 || s.mm4 == nil {
		return nil
	}
	return s.mm4.Send(ctx, msg)
}

func (s *Server) dispatchMM3Relay(ctx context.Context, msg *message.Message, result *routing.Result) error {
	if result == nil || result.Destination != routing.DestinationMM3 || s.mm3 == nil {
		return nil
	}
	return s.mm3.Send(ctx, msg)
}

func (s *Server) persistLocalContent(ctx context.Context, msg *message.Message, result *routing.Result) error {
	if result == nil || result.Destination != routing.DestinationLocal || len(msg.Parts) == 0 {
		return nil
	}
	rawPDU, err := mmspdu.Encode(mmspdu.NewRetrieveConf(msg.TransactionID, toPDUParts(msg.Parts)))
	if err != nil {
		return err
	}
	contentPath, err := s.store.Put(ctx, msg.ID, bytes.NewReader(rawPDU), int64(len(rawPDU)), "application/vnd.wap.mms-message")
	if err != nil {
		return err
	}
	msg.ContentPath = contentPath
	msg.StoreID = contentPath
	msg.MessageSize = int64(len(rawPDU))
	return nil
}

func (s *Server) applyAdaptation(ctx context.Context, msg *message.Message, result *routing.Result) error {
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

func (p *ParsedRequest) partsForContent(href string) ([]message.Part, error) {
	if len(p.Attachments) == 0 {
		return nil, fmt.Errorf("missing content attachment")
	}
	requested := normalizeCID(strings.TrimPrefix(href, "cid:"))
	parts := make([]message.Part, 0, len(p.Attachments))
	if requested != "" {
		attachment, ok := p.Attachments[requested]
		if !ok {
			return nil, fmt.Errorf("missing content attachment %q", requested)
		}
		parts = append(parts, attachmentToPart(attachment))
		for cid, attachment := range p.Attachments {
			if cid == requested {
				continue
			}
			parts = append(parts, attachmentToPart(attachment))
		}
		return parts, nil
	}
	for _, attachment := range p.Attachments {
		parts = append(parts, attachmentToPart(attachment))
	}
	return parts, nil
}

func attachmentToPart(attachment Attachment) message.Part {
	return message.Part{
		ContentType: attachment.ContentType,
		ContentID:   attachment.ContentID,
		Data:        append([]byte(nil), attachment.Data...),
		Size:        int64(len(attachment.Data)),
	}
}

func senderAddress(req *SubmitReq, vasp *db.MM7VASP) string {
	switch {
	case req.SenderIdentification.SenderAddress.Number != "":
		return req.SenderIdentification.SenderAddress.Number
	case req.SenderIdentification.SenderAddress.ShortCode != "":
		return req.SenderIdentification.SenderAddress.ShortCode
	case req.SenderIdentification.VASID != "":
		return req.SenderIdentification.VASID
	default:
		return vasp.VASPID
	}
}

func topLevelContentType(parts []message.Part) string {
	if len(parts) == 1 {
		return parts[0].ContentType
	}
	return "multipart/related"
}

func toPDUParts(parts []message.Part) []mmspdu.Part {
	out := make([]mmspdu.Part, 0, len(parts))
	for _, part := range parts {
		headers := map[string]string{}
		if part.ContentID != "" {
			headers["content-id"] = "<" + normalizeCID(part.ContentID) + ">"
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

func allowedRemote(remote string, allowed []string) bool {
	if len(allowed) == 0 {
		return true
	}
	host, _, err := net.SplitHostPort(remote)
	if err != nil {
		host = remote
	}
	for _, candidate := range allowed {
		if candidate == host {
			return true
		}
	}
	return false
}

func basicAuth(header string) (string, string, bool) {
	if !strings.HasPrefix(header, "Basic ") {
		return "", "", false
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(strings.TrimPrefix(header, "Basic ")))
	if err != nil {
		return "", "", false
	}
	parts := strings.SplitN(string(raw), ":", 2)
	if len(parts) != 2 {
		return "", "", false
	}
	return parts[0], parts[1], true
}
