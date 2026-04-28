package mm1

import (
	"context"
	"fmt"

	"github.com/vectorcore/vectorcore-mmsc/internal/db"
	"github.com/vectorcore/vectorcore-mmsc/internal/message"
	"github.com/vectorcore/vectorcore-mmsc/internal/mmspdu"
)

func SendOriginDeliveryReport(ctx context.Context, repo db.Repository, push PushSender, msg *message.Message, status message.Status) error {
	if msg == nil || msg.Origin != message.InterfaceMM1 || !msg.DeliveryReport || push == nil || msg.From == "" {
		return nil
	}
	pdu := mmspdu.NewDeliveryInd(msg.ID, msg.From, deliveryStatusToken(status))
	encoded, err := mmspdu.Encode(pdu)
	if err != nil {
		return err
	}
	sourceAddr := ""
	if len(msg.To) > 0 {
		sourceAddr = msg.To[0]
	}
	if err := push.SubmitWAPPush(ctx, sourceAddr, msg.From, mmspdu.WrapWAPPush(encoded)); err != nil {
		return err
	}
	if repo != nil {
		_ = repo.AppendMessageEvent(ctx, db.MessageEvent{
			MessageID: msg.ID,
			Source:    "dispatch",
			Type:      "origin-delivery-report",
			Summary:   "Origin delivery report submitted",
			Detail:    fmt.Sprintf("status=%d", status),
		})
	}
	return nil
}

func deliveryStatusToken(status message.Status) byte {
	switch status {
	case message.StatusExpired:
		return mmspdu.StatusExpired
	case message.StatusRejected:
		return mmspdu.StatusRejected
	case message.StatusUnreachable:
		return mmspdu.StatusUnreachable
	case message.StatusForwarded:
		return mmspdu.StatusForwarded
	case message.StatusDelivered:
		return mmspdu.StatusRetrieved
	default:
		return mmspdu.StatusIndeterminate
	}
}
