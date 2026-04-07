package smpp

import (
	"context"
	"crypto/rand"
	"fmt"
	"strings"
	"unicode"

	"go.uber.org/zap"

	"github.com/vectorcore/vectorcore-mmsc/internal/wappush"
)

type SegmentTracker func(segmentIndex int, segmentCount int, remoteMessageID string) error

func (c *Client) SubmitWAPPush(ctx context.Context, sourceAddr string, msisdn string, pushPDU []byte) error {
	return c.SubmitWAPPushTracked(ctx, sourceAddr, msisdn, pushPDU, nil)
}

func (c *Client) SubmitWAPPushTracked(ctx context.Context, sourceAddr string, msisdn string, pushPDU []byte, tracker SegmentTracker) error {
	var ref [1]byte
	if _, err := rand.Read(ref[:]); err != nil {
		return fmt.Errorf("smpp: failed to generate segment reference: %w", err)
	}
	segments, err := wappush.SegmentBinarySMS(pushPDU, ref[0])
	if err != nil {
		return err
	}
	log := zap.L().With(
		zap.String("interface", "smpp"),
		zap.String("host", c.cfg.Host),
		zap.Int("port", c.cfg.Port),
		zap.String("system_id", c.cfg.SystemID),
		zap.String("source_addr", sourceAddr),
		zap.String("recipient", msisdn),
		zap.Int("payload_bytes", len(pushPDU)),
		zap.Int("segment_count", len(segments)),
	)
	log.Debug("smpp segmented wap push")

	for index, segment := range segments {
		remoteID, err := c.submitSegment(ctx, sourceAddr, msisdn, segment)
		if err != nil {
			log.Warn("smpp segment submit failed", zap.Error(err), zap.Int("segment_index", index))
			return err
		}
		log.Debug("smpp segment submitted", zap.Int("segment_index", index), zap.Int("segment_bytes", len(segment)), zap.String("remote_message_id", remoteID))
		if tracker != nil {
			if err := tracker(index, len(segments), remoteID); err != nil {
				return err
			}
		}
	}
	return nil
}

func (c *Client) submitSegment(ctx context.Context, sourceAddr string, msisdn string, payload []byte) (string, error) {
	var remoteMessageID string
	log := zap.L().With(
		zap.String("interface", "smpp"),
		zap.String("host", c.cfg.Host),
		zap.Int("port", c.cfg.Port),
		zap.String("system_id", c.cfg.SystemID),
		zap.String("source_addr", sourceAddr),
		zap.String("recipient", msisdn),
		zap.Int("segment_bytes", len(payload)),
	)
	err := c.session.WithConn(ctx, func(conn *Conn) error {
		sourceTON, sourceNPI := classifySourceAddress(sourceAddr)
		req := &PDU{
			CommandID:          CmdSubmitSM,
			CommandStatus:      ESMEROK,
			SequenceNumber:     conn.NextSeq(),
			ServiceType:        "WAP",
			SourceAddrTON:      sourceTON,
			SourceAddrNPI:      sourceNPI,
			SourceAddr:         sourceAddr,
			DestAddrTON:        0x01,
			DestAddrNPI:        0x01,
			DestinationAddr:    msisdn,
			ESMClass:           ESMClassUDHI,
			// 0xF5: 8-bit data, Class 1 (ME-specific) — required for WAP Push.
			DataCoding:         0xF5,
			// No SMSC delivery receipt; WAP Push delivery is tracked at the
			// application layer, not via SMPP receipts.
			RegisteredDelivery: 0x00,
			ShortMessage:       payload,
		}
		resp, err := c.session.Request(ctx, req, CmdSubmitSMResp)
		if err != nil {
			log.Warn("smpp submit_sm failed", zap.Error(err))
			return err
		}
		if resp.CommandStatus != ESMEROK {
			log.Warn("smpp submit_sm rejected", zap.Uint32("command_status", resp.CommandStatus))
			return fmt.Errorf("smpp submit failed: 0x%08x", resp.CommandStatus)
		}
		remoteMessageID = resp.MessageID
		log.Debug("smpp submit_sm acknowledged", zap.String("remote_message_id", remoteMessageID))
		return nil
	})
	if err != nil {
		return "", err
	}
	return remoteMessageID, nil
}

func classifySourceAddress(sourceAddr string) (byte, byte) {
	trimmed := strings.TrimSpace(sourceAddr)
	if trimmed == "" {
		return 0x00, 0x00
	}
	if looksLikeNumericAddress(trimmed) {
		return 0x01, 0x01
	}
	return 0x05, 0x00
}

func looksLikeNumericAddress(value string) bool {
	for i, r := range value {
		if unicode.IsDigit(r) {
			continue
		}
		if i == 0 && r == '+' {
			continue
		}
		return false
	}
	return true
}
