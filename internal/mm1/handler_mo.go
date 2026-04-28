package mm1

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
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

var mm1MSISDNHeaders = []string{
	"X-WAP-Network-Client-MSISDN",
	"X-MSISDN",
	"X-Nokia-MSISDN",
}

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

type MOHandler struct {
	cfg    *config.Config
	repo   db.Repository
	store  store.Store
	router *routing.Engine
	smpp   PushSender
	mm4    MM4Sender
	mm3    MM3Sender
	adapt  *adapt.Pipeline
}

func NewMOHandler(cfg *config.Config, repo db.Repository, contentStore store.Store, router *routing.Engine, smpp PushSender, mm4 MM4Sender, mm3 MM3Sender, pipeline *adapt.Pipeline) *MOHandler {
	return &MOHandler{
		cfg:    cfg,
		repo:   repo,
		store:  contentStore,
		router: router,
		smpp:   smpp,
		mm4:    mm4,
		mm3:    mm3,
		adapt:  pipeline,
	}
}

func (h *MOHandler) Handle(w http.ResponseWriter, r *http.Request) {
	log := zap.L().With(zap.String("interface", "mm1"), zap.String("remote", r.RemoteAddr))
	data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, h.cfg.MM1.MaxBodySizeBytes))
	if err != nil {
		log.Debug("mm1 mo read failed", zap.Error(err))
		http.Error(w, "invalid body", http.StatusBadRequest)
		return
	}

	pdu, err := mmspdu.Decode(data)
	if err != nil {
		log.Debug("mm1 mo decode failed", zap.Error(err), zap.Int("payload_bytes", len(data)))
		http.Error(w, "invalid mms pdu", http.StatusBadRequest)
		return
	}
	if pdu.MessageType != mmspdu.MsgTypeSendReq && pdu.MessageType != mmspdu.MsgTypeForwardReq {
		log.Debug("mm1 mo unsupported message type", zap.Uint8("message_type", pdu.MessageType))
		http.Error(w, "unsupported message type", http.StatusBadRequest)
		return
	}

	if limit := h.cfg.Limits.MaxMessageSizeBytes; limit > 0 && int64(len(data)) > limit {
		log.Debug("mm1 mo message too large", zap.Int("payload_bytes", len(data)), zap.Int64("limit_bytes", limit))
		conf := mmspdu.NewSendConf(pdu.TransactionID, "", mmspdu.ResponseStatusErrorMessageSizeExceeded)
		if resp, err := mmspdu.Encode(conf); err == nil {
			w.Header().Set("Content-Type", "application/vnd.wap.mms-message")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(resp)
		} else {
			http.Error(w, "message too large", http.StatusRequestEntityTooLarge)
		}
		return
	}
	log = log.With(zap.String("transaction_id", pdu.TransactionID), zap.Uint8("message_type", pdu.MessageType))

	msg, routeResult, err := h.toMessage(r.Context(), r, pdu, data)
	if err != nil {
		log.Debug("mm1 mo message conversion failed", zap.Error(err))
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	log = log.With(zap.String("message_id", msg.ID))
	if routeResult != nil {
		log.Debug("mm1 mo route resolved", zap.String("destination", routeResult.Destination.String()), zap.Strings("recipients", msg.To))
	}
	if err := h.applyAdaptation(r.Context(), msg, routeResult); err != nil {
		log.Debug("mm1 mo adaptation failed", zap.Error(err))
		http.Error(w, err.Error(), http.StatusUnprocessableEntity)
		return
	}
	if err := h.persistLocalContent(r.Context(), msg, routeResult); err != nil {
		log.Warn("mm1 mo local content persistence failed", zap.Error(err))
		http.Error(w, "failed to store adapted content", http.StatusInternalServerError)
		return
	}
	if err := h.repo.CreateMessage(r.Context(), msg); err != nil {
		log.Warn("mm1 mo message persistence failed", zap.Error(err))
		http.Error(w, "failed to persist message", http.StatusInternalServerError)
		return
	}

	var response *mmspdu.PDU
	if pdu.MessageType == mmspdu.MsgTypeForwardReq {
		response = mmspdu.NewForwardConf(msg.TransactionID, msg.ID, mmspdu.ResponseStatusOk)
	} else {
		response = mmspdu.NewSendConf(msg.TransactionID, msg.ID, mmspdu.ResponseStatusOk)
	}

	resp, err := mmspdu.Encode(response)
	if err != nil {
		log.Warn("mm1 mo response encode failed", zap.Error(err))
		http.Error(w, "failed to encode response", http.StatusInternalServerError)
		return
	}

	log.Debug("mm1 mo accepted", zap.Int16("status", int16(msg.Status)), zap.Int("response_bytes", len(resp)))
	w.Header().Set("Content-Type", "application/vnd.wap.mms-message")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(resp)

	if routeResult != nil && routeResult.Destination == routing.DestinationReject {
		h.dispatchOriginDeliveryReportAsync(msg.Clone(), message.StatusRejected, log)
		return
	}
	if routeResult != nil && routeResult.Destination == routing.DestinationLocal {
		h.dispatchLocalNotificationAsync(msg.Clone(), cloneRoutingResult(routeResult), log)
		return
	}
	if err := h.dispatchMM4Relay(r.Context(), msg, routeResult); err != nil {
		_ = h.repo.UpdateMessageStatus(r.Context(), msg.ID, message.StatusUnreachable)
		log.Warn("mm1 mo mm4 relay failed", zap.Error(err))
		return
	}
	if err := h.dispatchMM3Relay(r.Context(), msg, routeResult); err != nil {
		_ = h.repo.UpdateMessageStatus(r.Context(), msg.ID, message.StatusUnreachable)
		log.Warn("mm1 mo mm3 relay failed", zap.Error(err))
	}
}

func (h *MOHandler) applyAdaptation(ctx context.Context, msg *message.Message, result *routing.Result) error {
	if h.adapt == nil || result == nil || result.Destination != routing.DestinationLocal {
		return nil
	}
	parts, err := h.adapt.Adapt(ctx, msg.Parts)
	if err != nil {
		return err
	}
	msg.Parts = parts
	if adaptedSize := partsSize(parts); adaptedSize > 0 {
		msg.MessageSize = adaptedSize
	}
	return nil
}

func (h *MOHandler) toMessage(ctx context.Context, r *http.Request, pdu *mmspdu.PDU, raw []byte) (*message.Message, *routing.Result, error) {
	from, err := h.senderAddressFromRequest(r, pdu)
	if err != nil {
		return nil, nil, fmt.Errorf("missing from: %w", err)
	}
	to, err := headerText(pdu, mmspdu.FieldTo)
	if err != nil {
		return nil, nil, fmt.Errorf("missing to: %w", err)
	}

	messageID := routing.NewMessageID()
	contentPath, err := h.store.Put(ctx, messageID, bytes.NewReader(raw), int64(len(raw)), "application/vnd.wap.mms-message")
	if err != nil {
		return nil, nil, fmt.Errorf("store payload: %w", err)
	}

	recipients := []string{stripTypeSuffix(to)}
	result, err := h.router.ResolveRecipients(ctx, recipients)
	if err != nil {
		return nil, nil, fmt.Errorf("route message: %w", err)
	}

	status := message.StatusQueued
	if result != nil && result.Destination == routing.DestinationReject {
		status = message.StatusRejected
	} else if result != nil && (result.Destination == routing.DestinationMM4 || result.Destination == routing.DestinationMM3) {
		status = message.StatusForwarded
	}

	msg := &message.Message{
		ID:            messageID,
		TransactionID: pdu.TransactionID,
		Status:        status,
		Direction:     message.DirectionMO,
		From:          stripTypeSuffix(from),
		To:            recipients,
		ContentType:   contentTypeOf(pdu),
		MMSVersion:    pdu.MMSVersion,
		MessageSize:   int64(len(raw)),
		ContentPath:   contentPath,
		StoreID:       contentPath,
		Origin:        message.InterfaceMM1,
		Parts:         toMessageParts(pdu),
	}
	if value, ok := pdu.Headers[mmspdu.FieldDeliveryReport]; ok {
		if token, err := value.Token(); err == nil {
			msg.DeliveryReport = token == mmspdu.BooleanYes
		}
	}
	if value, ok := pdu.Headers[mmspdu.FieldReadReply]; ok {
		if token, err := value.Token(); err == nil {
			msg.ReadReport = token == mmspdu.BooleanYes
		}
	}
	if subject, err := headerText(pdu, mmspdu.FieldSubject); err == nil {
		msg.Subject = subject
	}
	if value, ok := pdu.Headers[mmspdu.FieldExpiry]; ok {
		if t, err := value.Time(); err == nil && !t.IsZero() {
			msg.Expiry = &t
		}
	}
	message.ApplyDefaultExpiry(msg, h.cfg.Limits.DefaultMessageExpiry, h.cfg.Limits.MaxMessageRetention)
	if len(msg.Parts) == 0 {
		return nil, nil, fmt.Errorf("missing message content")
	}
	return msg, result, nil
}

func (h *MOHandler) senderAddressFromRequest(r *http.Request, pdu *mmspdu.PDU) (string, error) {
	if pdu != nil {
		if value, ok := pdu.Headers[mmspdu.FieldFrom]; ok {
			if text, err := value.Text(); err == nil {
				return stripTypeSuffix(text), nil
			}
			if value.IsInsertAddress() {
				if headerValue := msisdnFromHTTPHeaders(r); headerValue != "" {
					return headerValue, nil
				}
			}
		}
	}
	if headerValue := msisdnFromHTTPHeaders(r); headerValue != "" {
		return headerValue, nil
	}
	return "", errors.New("header missing")
}

func msisdnFromHTTPHeaders(r *http.Request) string {
	if r == nil {
		return ""
	}
	for _, name := range mm1MSISDNHeaders {
		if value := strings.TrimSpace(r.Header.Get(name)); value != "" {
			return stripTypeSuffix(value)
		}
	}
	return ""
}

func (h *MOHandler) dispatchLocalNotification(ctx context.Context, msg *message.Message, result *routing.Result) error {
	if result == nil || result.Destination != routing.DestinationLocal || h.smpp == nil || len(msg.To) == 0 {
		return nil
	}

	retrieveURL := h.cfg.MM1.RetrieveBaseURL
	if retrieveURL == "" {
		retrieveURL = "/mms/retrieve"
	}
	contentLocation := retrieveURL + "?id=" + url.QueryEscape(msg.ID)
	notification := mmspdu.NewNotificationInd(msg.TransactionID, contentLocation)
	notification.MMSVersion = "1.2"
	from := msg.From
	if from == "" {
		from = "#insert"
	}
	notification.Headers[mmspdu.FieldFrom] = mmspdu.NewFromValue(from)
	if msg.Subject != "" {
		notification.Headers[mmspdu.FieldSubject] = mmspdu.NewEncodedStringValue(mmspdu.FieldSubject, msg.Subject)
	}
	notification.Headers[mmspdu.FieldMessageClass] = mmspdu.NewTokenValue(mmspdu.FieldMessageClass, mmspduMessageClass(msg.MessageClass))
	notification.Headers[mmspdu.FieldMessageSize] = mmspdu.NewLongIntegerValue(mmspdu.FieldMessageSize, uint64(msg.MessageSize))
	notification.Headers[mmspdu.FieldExpiry] = mmspdu.NewRelativeDateValue(mmspdu.FieldExpiry, notificationExpirySeconds(msg.Expiry))

	encoded, err := mmspdu.Encode(notification)
	if err != nil {
		return err
	}
	push := mmspdu.WrapWAPPush(encoded)
	for _, recipient := range msg.To {
		if tracked, ok := h.smpp.(trackedPushSender); ok {
			if err := tracked.SubmitWAPPushForMessage(ctx, msg.ID, msg.From, recipient, push); err != nil {
				return err
			}
			continue
		}
		if err := h.smpp.SubmitWAPPush(ctx, msg.From, recipient, push); err != nil {
			return err
		}
	}
	if err := h.repo.UpdateMessageStatus(ctx, msg.ID, message.StatusDelivering); err != nil {
		return err
	}
	msg.Status = message.StatusDelivering
	_ = h.repo.AppendMessageEvent(ctx, db.MessageEvent{
		MessageID: msg.ID,
		Source:    "dispatch",
		Type:      "local-notification-submitted",
		Summary:   "Local notification submitted",
		Detail:    strings.Join(msg.To, ","),
	})
	return nil
}

func mmspduMessageClass(class message.MessageClass) byte {
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

func notificationExpirySeconds(expiry *time.Time) uint64 {
	if expiry != nil && !expiry.IsZero() {
		delta := time.Until(expiry.UTC())
		if delta <= 0 {
			return 1
		}
		return uint64(delta.Round(time.Second) / time.Second)
	}
	return uint64((7 * 24 * time.Hour) / time.Second)
}

func (h *MOHandler) dispatchLocalNotificationAsync(msg message.Message, result *routing.Result, log *zap.Logger) {
	go func() {
		ctx := context.Background()
		if err := h.dispatchLocalNotification(ctx, &msg, result); err != nil {
			log.Warn("mm1 mo local notification dispatch deferred", zap.Error(err))
			_ = h.repo.AppendMessageEvent(ctx, db.MessageEvent{
				MessageID: msg.ID,
				Source:    "dispatch",
				Type:      "local-notification-deferred",
				Summary:   "Local notification dispatch deferred",
				Detail:    err.Error(),
			})
		}
	}()
}

func (h *MOHandler) dispatchOriginDeliveryReportAsync(msg message.Message, status message.Status, log *zap.Logger) {
	go func() {
		ctx := context.Background()
		if err := h.dispatchOriginDeliveryReport(ctx, &msg, status); err != nil {
			log.Warn("mm1 mo origin delivery report failed", zap.Error(err))
			_ = h.repo.AppendMessageEvent(ctx, db.MessageEvent{
				MessageID: msg.ID,
				Source:    "dispatch",
				Type:      "origin-delivery-report-failed",
				Summary:   "Origin delivery report failed",
				Detail:    err.Error(),
			})
		}
	}()
}

func (h *MOHandler) dispatchOriginDeliveryReport(ctx context.Context, msg *message.Message, status message.Status) error {
	return SendOriginDeliveryReport(ctx, h.repo, h.smpp, msg, status)
}

func cloneRoutingResult(result *routing.Result) *routing.Result {
	if result == nil {
		return nil
	}
	cloned := *result
	if result.Peer != nil {
		peer := *result.Peer
		cloned.Peer = &peer
	}
	return &cloned
}

func (h *MOHandler) dispatchMM4Relay(ctx context.Context, msg *message.Message, result *routing.Result) error {
	if result == nil || result.Destination != routing.DestinationMM4 || h.mm4 == nil {
		return nil
	}
	return h.mm4.Send(ctx, msg)
}

func (h *MOHandler) dispatchMM3Relay(ctx context.Context, msg *message.Message, result *routing.Result) error {
	if result == nil || result.Destination != routing.DestinationMM3 || h.mm3 == nil {
		return nil
	}
	return h.mm3.Send(ctx, msg)
}

func (h *MOHandler) persistLocalContent(ctx context.Context, msg *message.Message, result *routing.Result) error {
	if result == nil || result.Destination != routing.DestinationLocal || len(msg.Parts) == 0 {
		return nil
	}
	raw, err := mmspdu.Encode(mmspdu.NewRetrieveConf(msg.TransactionID, partsToPDU(msg.Parts)))
	if err != nil {
		return err
	}
	contentPath, err := h.store.Put(ctx, msg.ID, bytes.NewReader(raw), int64(len(raw)), "application/vnd.wap.mms-message")
	if err != nil {
		return err
	}
	msg.ContentPath = contentPath
	msg.StoreID = contentPath
	msg.MessageSize = int64(len(raw))
	return nil
}

func headerText(pdu *mmspdu.PDU, field byte) (string, error) {
	value, ok := pdu.Headers[field]
	if !ok {
		return "", errors.New("header missing")
	}
	return value.Text()
}

func contentTypeOf(pdu *mmspdu.PDU) string {
	value, ok := pdu.Headers[mmspdu.FieldContentType]
	if !ok {
		return ""
	}
	ct, err := value.ContentType()
	if err != nil {
		return ""
	}
	return ct.MediaType
}

func toMessageParts(pdu *mmspdu.PDU) []message.Part {
	if pdu.Body == nil || len(pdu.Body.Parts) == 0 {
		return nil
	}
	parts := make([]message.Part, 0, len(pdu.Body.Parts))
	for _, part := range pdu.Body.Parts {
		item := message.Part{
			ContentType: part.ContentType,
			Data:        append([]byte(nil), part.Data...),
			Size:        int64(len(part.Data)),
		}
		if cid, ok := part.Headers["content-id"]; ok {
			item.ContentID = cid
		}
		if loc, ok := part.Headers["content-location"]; ok {
			item.ContentLocation = loc
		}
		parts = append(parts, item)
	}
	return parts
}

func stripTypeSuffix(value string) string {
	if idx := strings.Index(strings.ToUpper(value), "/TYPE="); idx > 0 {
		return value[:idx]
	}
	return value
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
