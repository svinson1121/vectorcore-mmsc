package mm4

import (
	"strings"
	"testing"
	"time"

	"github.com/vectorcore/vectorcore-mmsc/internal/message"
)

func TestEncodeEnvelopeGoldenHeaders(t *testing.T) {
	t.Parallel()

	expiry := time.Date(2026, 4, 2, 12, 0, 0, 0, time.UTC)
	msg := &message.Message{
		ID:             "mid-golden",
		TransactionID:  "txn-golden",
		From:           "+12025550100",
		To:             []string{"+12025550101"},
		Subject:        "golden",
		DeliveryReport: true,
		Expiry:         &expiry,
		Parts: []message.Part{{
			ContentType:     "text/plain",
			ContentID:       "<text1>",
			ContentLocation: "text1.txt",
			Data:            []byte("hello"),
		}},
	}

	envelope, err := EncodeEnvelope(msg, "mmsc.example.net")
	if err != nil {
		t.Fatalf("encode envelope: %v", err)
	}
	payload := strings.ToLower(string(envelope))
	checks := []string{
		"from: +12025550100\r\n",
		"to: +12025550101\r\n",
		"subject: golden\r\n",
		"x-mms-3gpp-mms-version: 6.3.0\r\n",
		"x-mms-message-type: mm4_forward.req\r\n",
		"x-mms-transaction-id: txn-golden\r\n",
		"x-mms-message-id: mid-golden\r\n",
		"x-mms-originator-system: system=mmsc.example.net;party=+12025550100\r\n",
		"x-mms-sender-address: +12025550100/type=plmn\r\n",
		"x-mms-to: +12025550101/type=plmn\r\n",
		"x-mms-expiry: 2026-04-02t12:00:00z\r\n",
		"x-mms-delivery-report: yes\r\n",
		"content-id: <text1>\r\n",
		"content-location: text1.txt\r\n",
		"\r\nhello\r\n",
	}
	for _, check := range checks {
		if !strings.Contains(payload, check) {
			t.Fatalf("missing golden fragment %q in envelope:\n%s", check, payload)
		}
	}
}

func TestEncodeReportGoldenHeaders(t *testing.T) {
	t.Parallel()

	msg := &message.Message{
		ID:            "mid-report",
		TransactionID: "txn-report",
		From:          "+12025550100",
		To:            []string{"+12025550101"},
	}

	delivery, err := EncodeDeliveryReportEnvelope(msg, "mmsc.example.net", "Retrieved", "Ok")
	if err != nil {
		t.Fatalf("encode delivery report: %v", err)
	}
	readReply, err := EncodeReadReplyEnvelope(msg, "mmsc.example.net", "Read", "Ok")
	if err != nil {
		t.Fatalf("encode read reply: %v", err)
	}

	deliveryPayload := strings.ToLower(string(delivery))
	if !strings.Contains(deliveryPayload, "x-mms-message-type: mm4_delivery_report.req\r\n") ||
		!strings.Contains(deliveryPayload, "x-mms-status: retrieved\r\n") ||
		!strings.Contains(deliveryPayload, "to: +12025550100/type=plmn\r\n") {
		t.Fatalf("unexpected delivery report envelope:\n%s", deliveryPayload)
	}

	readPayload := strings.ToLower(string(readReply))
	if !strings.Contains(readPayload, "x-mms-message-type: mm4_read_reply_report.req\r\n") ||
		!strings.Contains(readPayload, "x-mms-read-status: read\r\n") ||
		!strings.Contains(readPayload, "to: +12025550100/type=plmn\r\n") {
		t.Fatalf("unexpected read reply envelope:\n%s", readPayload)
	}
}

func TestEncodeResponseEnvelopeGoldenHeaders(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name        string
		messageType string
	}{
		{name: "forward response", messageType: mm4ForwardRes},
		{name: "delivery response", messageType: mm4DeliveryReportRes},
		{name: "read reply response", messageType: mm4ReadReplyReportRes},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			envelope, err := EncodeResponseEnvelope(tc.messageType, "txn-resp", "mid-resp", "Ok", "mmsc.example.net", "+12025550100/TYPE=PLMN")
			if err != nil {
				t.Fatalf("encode response envelope: %v", err)
			}

			payload := strings.ToLower(string(envelope))
			checks := []string{
				"from: mmsc.example.net\r\n",
				"to: +12025550100/type=plmn\r\n",
				"x-mms-3gpp-mms-version: 6.3.0\r\n",
				"x-mms-message-type: " + strings.ToLower(tc.messageType) + "\r\n",
				"x-mms-transaction-id: txn-resp\r\n",
				"x-mms-message-id: mid-resp\r\n",
				"x-mms-request-status-code: ok\r\n",
				"\r\nok",
			}
			for _, check := range checks {
				if !strings.Contains(payload, check) {
					t.Fatalf("missing golden fragment %q in response envelope:\n%s", check, payload)
				}
			}
		})
	}
}
