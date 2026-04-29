package queue

import (
	"context"
	"strings"
	"time"

	"go.uber.org/zap"

	"github.com/vectorcore/vectorcore-mmsc/internal/config"
	"github.com/vectorcore/vectorcore-mmsc/internal/db"
	"github.com/vectorcore/vectorcore-mmsc/internal/message"
	"github.com/vectorcore/vectorcore-mmsc/internal/mmspdu"
	"github.com/vectorcore/vectorcore-mmsc/internal/smpp"
)

const retryInterval = time.Minute

type pushSender interface {
	SubmitWAPPush(ctx context.Context, sourceAddr string, msisdn string, pushPDU []byte) error
}

type trackedPushSender interface {
	SubmitWAPPushForMessage(ctx context.Context, internalMessageID string, sourceAddr string, msisdn string, pushPDU []byte) error
}

type Dispatcher struct {
	repo db.Repository
	push pushSender
	cfg  config.MM1Config
	log  *zap.Logger
}

func New(cfg config.MM1Config, repo db.Repository, push pushSender) *Dispatcher {
	return &Dispatcher{
		repo: repo,
		push: push,
		cfg:  cfg,
		log:  zap.L().With(zap.String("component", "queue-dispatcher")),
	}
}

func (d *Dispatcher) Run(ctx context.Context) {
	d.log.Info("message queue dispatcher started", zap.Duration("interval", retryInterval))
	d.dispatchQueued(ctx)
	ticker := time.NewTicker(retryInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			d.log.Info("message queue dispatcher stopped")
			return
		case <-ticker.C:
			d.dispatchQueued(ctx)
		}
	}
}

func (d *Dispatcher) dispatchQueued(ctx context.Context) {
	if d.repo == nil || d.push == nil {
		return
	}
	status := message.StatusQueued
	items, err := d.repo.ListMessages(ctx, db.MessageFilter{Status: &status, Limit: 100})
	if err != nil {
		d.log.Warn("queue dispatch: list queued messages failed", zap.Error(err))
		return
	}
	for i := range items {
		msg := items[i]
		if msg.Expiry != nil && time.Now().UTC().After(msg.Expiry.UTC()) {
			continue
		}
		if err := d.dispatchLocalNotification(ctx, &msg); err != nil {
			d.log.Warn("queue dispatch: local notification failed", zap.String("message_id", msg.ID), zap.Error(err))
			_ = d.repo.AppendMessageEvent(ctx, db.MessageEvent{
				MessageID: msg.ID,
				Source:    "queue",
				Type:      "local-notification-retry-failed",
				Summary:   "Local notification retry failed",
				Detail:    err.Error(),
			})
			continue
		}
		_ = d.repo.AppendMessageEvent(ctx, db.MessageEvent{
			MessageID: msg.ID,
			Source:    "queue",
			Type:      "local-notification-retry-submitted",
			Summary:   "Local notification retry submitted",
			Detail:    strings.Join(msg.To, ","),
		})
	}
}

func (d *Dispatcher) dispatchLocalNotification(ctx context.Context, msg *message.Message) error {
	if msg == nil || len(msg.To) == 0 {
		return nil
	}
	notification := mmspdu.NewNotificationInd(msg.TransactionID, d.contentLocation(msg.ID))
	notification.MMSVersion = "1.2"
	from := msg.From
	if from == "" {
		from = "#insert"
	}
	notification.Headers[mmspdu.FieldFrom] = mmspdu.NewFromValue(from)
	if msg.Subject != "" {
		notification.Headers[mmspdu.FieldSubject] = mmspdu.NewEncodedStringValue(mmspdu.FieldSubject, msg.Subject)
	}
	notification.Headers[mmspdu.FieldMessageClass] = mmspdu.NewTokenValue(mmspdu.FieldMessageClass, messageClass(msg.MessageClass))
	notification.Headers[mmspdu.FieldMessageSize] = mmspdu.NewLongIntegerValue(mmspdu.FieldMessageSize, uint64(msg.MessageSize))
	notification.Headers[mmspdu.FieldExpiry] = mmspdu.NewRelativeDateValue(mmspdu.FieldExpiry, notificationExpirySeconds(msg.Expiry))

	encoded, err := mmspdu.Encode(notification)
	if err != nil {
		return err
	}
	pushPDU := mmspdu.WrapWAPPush(encoded)
	for _, recipient := range msg.To {
		if tracked, ok := d.push.(trackedPushSender); ok {
			if err := tracked.SubmitWAPPushForMessage(ctx, msg.ID, msg.From, recipient, pushPDU); err != nil {
				return err
			}
			continue
		}
		if err := d.push.SubmitWAPPush(ctx, msg.From, recipient, pushPDU); err != nil {
			return err
		}
	}
	if err := d.repo.UpdateMessageStatus(ctx, msg.ID, message.StatusDelivering); err != nil {
		return err
	}
	msg.Status = message.StatusDelivering
	return nil
}

func (d *Dispatcher) contentLocation(id string) string {
	base := d.cfg.RetrieveBaseURL
	if base == "" {
		base = "/mms/retrieve"
	}
	return base + "?id=" + id
}

func messageClass(class message.MessageClass) byte {
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

var _ pushSender = (*smpp.Manager)(nil)
