package mm1

import (
	"context"
	"fmt"
	"strings"

	"github.com/vectorcore/vectorcore-mmsc/internal/db"
	"github.com/vectorcore/vectorcore-mmsc/internal/message"
	"github.com/vectorcore/vectorcore-mmsc/internal/mmspdu"
)

const (
	DeliveryReportPolicyRequestedOnly   = "requested_only"
	DeliveryReportPolicyAlwaysOnFailure = "always_on_failure"
	DeliveryReportPolicyDisabled        = "disabled"
)

func SendOriginDeliveryReport(ctx context.Context, repo db.Repository, push PushSender, msg *message.Message, status message.Status, policy ...string) error {
	if msg == nil || msg.Origin != message.InterfaceMM1 || push == nil || msg.From == "" {
		return nil
	}
	resolvedPolicy := DeliveryReportPolicyRequestedOnly
	if len(policy) > 0 && strings.TrimSpace(policy[0]) != "" {
		resolvedPolicy = strings.TrimSpace(policy[0])
	}
	if !shouldSendOriginDeliveryReport(msg, status, resolvedPolicy) {
		appendOriginReportSkipped(ctx, repo, msg.ID, resolvedPolicy, msg.DeliveryReport)
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

func shouldSendOriginDeliveryReport(msg *message.Message, status message.Status, policy string) bool {
	switch policy {
	case DeliveryReportPolicyDisabled:
		return false
	case DeliveryReportPolicyAlwaysOnFailure:
		return msg.DeliveryReport || isFailureStatus(status)
	default:
		return msg.DeliveryReport
	}
}

func isFailureStatus(status message.Status) bool {
	return status == message.StatusRejected || status == message.StatusExpired || status == message.StatusUnreachable
}

func appendOriginReportSkipped(ctx context.Context, repo db.Repository, messageID string, policy string, requested bool) {
	if repo == nil || messageID == "" {
		return
	}
	_ = repo.AppendMessageEvent(ctx, db.MessageEvent{
		MessageID: messageID,
		Source:    "dispatch",
		Type:      "origin-delivery-report-skipped",
		Summary:   "Origin delivery report skipped",
		Detail:    fmt.Sprintf("policy=%s requested=%t", policy, requested),
	})
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
