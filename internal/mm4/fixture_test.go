package mm4

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDecodeForwardEnvelopeFixture(t *testing.T) {
	t.Parallel()

	raw := readFixture(t, "forward_req.eml")
	msg, meta, err := DecodeEnvelopeWithMeta(raw)
	if err != nil {
		t.Fatalf("decode fixture: %v", err)
	}
	if meta.MessageType != mm4ForwardReq || msg.ID != "mid-1" || msg.TransactionID != "txn-1" {
		t.Fatalf("unexpected fixture ids/meta: msg=%#v meta=%#v", msg, meta)
	}
	if msg.From != "+12025550100" || len(msg.To) != 1 || msg.To[0] != "+12025550101" {
		t.Fatalf("unexpected addressing: %#v", msg)
	}
	if len(msg.Parts) != 1 || msg.Parts[0].ContentType != "text/plain" || string(msg.Parts[0].Data) != "hello" {
		t.Fatalf("unexpected parts: %#v", msg.Parts)
	}
}

func TestDecodeDeliveryReportFixture(t *testing.T) {
	t.Parallel()

	raw := readFixture(t, "delivery_report_req.eml")
	msg, meta, err := DecodeEnvelopeWithMeta(raw)
	if err != nil {
		t.Fatalf("decode delivery fixture: %v", err)
	}
	if meta.MessageType != mm4DeliveryReportReq || meta.Status != "Retrieved" || meta.RequestStatusCode != "Ok" {
		t.Fatalf("unexpected delivery report meta: %#v", meta)
	}
	if msg.ID != "mid-report" || msg.TransactionID != "txn-report" {
		t.Fatalf("unexpected delivery report message: %#v", msg)
	}
}

func TestDecodeReadReplyFixture(t *testing.T) {
	t.Parallel()

	raw := readFixture(t, "read_reply_report_req.eml")
	msg, meta, err := DecodeEnvelopeWithMeta(raw)
	if err != nil {
		t.Fatalf("decode read reply fixture: %v", err)
	}
	if meta.MessageType != mm4ReadReplyReportReq || meta.ReadStatus != "Read" || meta.RequestStatusCode != "Ok" {
		t.Fatalf("unexpected read reply meta: %#v", meta)
	}
	if msg.ID != "mid-read" || msg.TransactionID != "txn-read" {
		t.Fatalf("unexpected read reply message: %#v", msg)
	}
}

func readFixture(t *testing.T, name string) []byte {
	t.Helper()

	path := filepath.Join("..", "..", "test", "fixtures", "mm4", name)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	return data
}
