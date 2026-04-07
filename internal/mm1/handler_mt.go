package mm1

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"net/http"

	"go.uber.org/zap"

	"github.com/vectorcore/vectorcore-mmsc/internal/config"
	"github.com/vectorcore/vectorcore-mmsc/internal/db"
	"github.com/vectorcore/vectorcore-mmsc/internal/message"
	"github.com/vectorcore/vectorcore-mmsc/internal/mmspdu"
	"github.com/vectorcore/vectorcore-mmsc/internal/store"
)

type MTHandler struct {
	cfg      *config.Config
	repo     db.Repository
	store    store.Store
	reporter MTReporter
}

type MTReporter interface {
	SendDeliveryReport(context.Context, *message.Message, message.Status) error
	SendReadReply(context.Context, *message.Message) error
}

func NewMTHandler(cfg *config.Config, repo db.Repository, contentStore store.Store, reporter MTReporter) *MTHandler {
	return &MTHandler{
		cfg:      cfg,
		repo:     repo,
		store:    contentStore,
		reporter: reporter,
	}
}

func (h *MTHandler) HandleRetrieve(w http.ResponseWriter, r *http.Request) {
	messageID := r.URL.Query().Get("id")
	log := zap.L().With(zap.String("interface", "mm1"), zap.String("message_id", messageID), zap.String("remote", r.RemoteAddr))
	if messageID == "" {
		log.Debug("mm1 retrieve missing id")
		http.Error(w, "missing id", http.StatusBadRequest)
		return
	}

	msg, err := h.repo.GetMessage(r.Context(), messageID)
	if err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, sql.ErrNoRows) {
			status = http.StatusNotFound
		}
		log.Debug("mm1 retrieve lookup failed", zap.Error(err), zap.Int("status", status))
		http.Error(w, "message not found", status)
		return
	}

	parts, err := h.loadParts(r.Context(), msg)
	if err != nil {
		log.Warn("mm1 retrieve load failed", zap.Error(err))
		http.Error(w, "failed to load message", http.StatusInternalServerError)
		return
	}
	if len(parts) == 0 {
		log.Warn("mm1 retrieve missing message parts")
		http.Error(w, "failed to load message", http.StatusInternalServerError)
		return
	}

	pdu := mmspdu.NewRetrieveConf(msg.TransactionID, parts)
	pdu.Headers[mmspdu.FieldResponseStatus] = mmspdu.NewTokenValue(mmspdu.FieldResponseStatus, mmspdu.ResponseStatusOk)
	if msg.ID != "" {
		pdu.Headers[mmspdu.FieldMessageID] = mmspdu.NewTextValue(mmspdu.FieldMessageID, msg.ID)
	}
	from := msg.From
	if from == "" {
		from = "#insert"
	}
	pdu.Headers[mmspdu.FieldFrom] = mmspdu.NewFromValue(from)
	if len(msg.To) > 0 {
		pdu.Headers[mmspdu.FieldTo] = mmspdu.NewAddressValue(mmspdu.FieldTo, msg.To[0])
	}
	if !msg.ReceivedAt.IsZero() {
		pdu.Headers[mmspdu.FieldDate] = mmspdu.NewDateValue(mmspdu.FieldDate, msg.ReceivedAt)
	}
	if msg.Subject != "" {
		pdu.Headers[mmspdu.FieldSubject] = mmspdu.NewEncodedStringValue(mmspdu.FieldSubject, msg.Subject)
	}

	resp, err := mmspdu.Encode(pdu)
	if err != nil {
		log.Warn("mm1 retrieve encode failed", zap.Error(err))
		http.Error(w, "failed to encode retrieve conf", http.StatusInternalServerError)
		return
	}

	if err := h.repo.UpdateMessageStatus(r.Context(), msg.ID, message.StatusDelivering); err != nil {
		log.Warn("mm1 retrieve status update failed", zap.Error(err))
		http.Error(w, "failed to update message", http.StatusInternalServerError)
		return
	}

	log.Debug("mm1 retrieve served", zap.String("transaction_id", msg.TransactionID), zap.Int("response_bytes", len(resp)))
	w.Header().Set("Content-Type", "application/vnd.wap.mms-message")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(resp)
}

func (h *MTHandler) HandleNotifyResp(w http.ResponseWriter, r *http.Request, pdu *mmspdu.PDU) {
	transactionID := pdu.TransactionID
	log := zap.L().With(zap.String("interface", "mm1"), zap.String("transaction_id", transactionID), zap.String("remote", r.RemoteAddr))
	msg, err := h.repo.GetMessageByTransactionID(r.Context(), transactionID)
	if err != nil {
		log.Debug("mm1 notify-resp message lookup failed", zap.Error(err))
		http.Error(w, "message not found", http.StatusNotFound)
		return
	}

	status := message.StatusDelivered
	if value, ok := pdu.Headers[mmspdu.FieldStatus]; ok {
		token, err := value.Token()
		if err == nil && token == mmspdu.StatusRejected {
			status = message.StatusRejected
		}
	}

	if err := h.repo.UpdateMessageStatus(r.Context(), msg.ID, status); err != nil {
		log.Warn("mm1 notify-resp status update failed", zap.Error(err), zap.String("message_id", msg.ID))
		http.Error(w, "failed to update message", http.StatusInternalServerError)
		return
	}
	if err := h.reportStatus(r.Context(), msg, status); err != nil {
		log.Warn("mm1 notify-resp report failed", zap.Error(err), zap.String("message_id", msg.ID))
		http.Error(w, "failed to report message status", http.StatusBadGateway)
		return
	}
	log.Debug("mm1 notify-resp applied", zap.String("message_id", msg.ID), zap.Int16("status", int16(status)))
	w.WriteHeader(http.StatusNoContent)
}

func (h *MTHandler) HandleAcknowledge(w http.ResponseWriter, r *http.Request, pdu *mmspdu.PDU) {
	transactionID := pdu.TransactionID
	log := zap.L().With(zap.String("interface", "mm1"), zap.String("transaction_id", transactionID), zap.String("remote", r.RemoteAddr))
	msg, err := h.repo.GetMessageByTransactionID(r.Context(), transactionID)
	if err != nil {
		log.Debug("mm1 acknowledge message lookup failed", zap.Error(err))
		http.Error(w, "message not found", http.StatusNotFound)
		return
	}

	if err := h.repo.UpdateMessageStatus(r.Context(), msg.ID, message.StatusDelivered); err != nil {
		log.Warn("mm1 acknowledge status update failed", zap.Error(err), zap.String("message_id", msg.ID))
		http.Error(w, "failed to update message", http.StatusInternalServerError)
		return
	}
	if err := h.reportStatus(r.Context(), msg, message.StatusDelivered); err != nil {
		log.Warn("mm1 acknowledge report failed", zap.Error(err), zap.String("message_id", msg.ID))
		http.Error(w, "failed to report message status", http.StatusBadGateway)
		return
	}
	log.Debug("mm1 acknowledge applied", zap.String("message_id", msg.ID))
	w.WriteHeader(http.StatusNoContent)
}

func (h *MTHandler) HandleDeliveryReport(w http.ResponseWriter, r *http.Request, pdu *mmspdu.PDU) {
	log := zap.L().With(zap.String("interface", "mm1"), zap.String("transaction_id", pdu.TransactionID), zap.String("remote", r.RemoteAddr))
	delivery, err := mmspdu.ParseDeliveryInd(pdu)
	if err != nil {
		log.Debug("mm1 delivery-report parse failed", zap.Error(err))
		http.Error(w, "invalid delivery indication", http.StatusBadRequest)
		return
	}
	msg, err := h.repo.GetMessage(r.Context(), delivery.MessageID)
	if err != nil {
		log.Debug("mm1 delivery-report message lookup failed", zap.Error(err), zap.String("message_id", delivery.MessageID))
		http.Error(w, "message not found", http.StatusNotFound)
		return
	}
	if err := h.repo.UpdateMessageStatus(r.Context(), msg.ID, message.StatusDelivered); err != nil {
		log.Warn("mm1 delivery-report status update failed", zap.Error(err), zap.String("message_id", msg.ID))
		http.Error(w, "failed to update message", http.StatusInternalServerError)
		return
	}
	if err := h.reportStatus(r.Context(), msg, message.StatusDelivered); err != nil {
		log.Warn("mm1 delivery-report report failed", zap.Error(err), zap.String("message_id", msg.ID))
		http.Error(w, "failed to report message status", http.StatusBadGateway)
		return
	}
	log.Debug("mm1 delivery-report applied", zap.String("message_id", msg.ID))
	w.WriteHeader(http.StatusNoContent)
}

func (h *MTHandler) HandleReadReport(w http.ResponseWriter, r *http.Request, pdu *mmspdu.PDU) {
	transactionID := pdu.TransactionID
	log := zap.L().With(zap.String("interface", "mm1"), zap.String("transaction_id", transactionID), zap.String("remote", r.RemoteAddr))
	msg, err := h.repo.GetMessageByTransactionID(r.Context(), transactionID)
	if err != nil {
		log.Debug("mm1 read-report message lookup failed", zap.Error(err))
		http.Error(w, "message not found", http.StatusNotFound)
		return
	}
	if err := h.repo.UpdateMessageStatus(r.Context(), msg.ID, message.StatusDelivered); err != nil {
		log.Warn("mm1 read-report status update failed", zap.Error(err), zap.String("message_id", msg.ID))
		http.Error(w, "failed to update message", http.StatusInternalServerError)
		return
	}
	if err := h.reportRead(r.Context(), msg); err != nil {
		log.Warn("mm1 read-report forwarding failed", zap.Error(err), zap.String("message_id", msg.ID))
		http.Error(w, "failed to report read reply", http.StatusBadGateway)
		return
	}
	log.Debug("mm1 read-report applied", zap.String("message_id", msg.ID))
	w.WriteHeader(http.StatusNoContent)
}

func (h *MTHandler) reportStatus(ctx context.Context, msg *message.Message, status message.Status) error {
	if h.reporter == nil {
		return nil
	}
	return h.reporter.SendDeliveryReport(ctx, msg, status)
}

func (h *MTHandler) reportRead(ctx context.Context, msg *message.Message) error {
	if h.reporter == nil {
		return nil
	}
	return h.reporter.SendReadReply(ctx, msg)
}

func (h *MTHandler) loadParts(ctx context.Context, msg *message.Message) ([]mmspdu.Part, error) {
	if len(msg.Parts) > 0 {
		return partsToPDU(msg.Parts), nil
	}
	if msg.ContentPath == "" {
		return nil, errors.New("message missing content path")
	}

	reader, _, err := h.store.Get(ctx, msg.ContentPath)
	if err != nil {
		return nil, err
	}
	defer reader.Close()

	raw, err := io.ReadAll(reader)
	if err != nil {
		return nil, err
	}
	pdu, err := mmspdu.Decode(raw)
	if err != nil {
		return nil, err
	}
	if pdu.Body == nil || len(pdu.Body.Parts) == 0 {
		return nil, errors.New("stored message missing parts")
	}
	return append([]mmspdu.Part(nil), pdu.Body.Parts...), nil
}

func partsToPDU(parts []message.Part) []mmspdu.Part {
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
