package mm7

import (
	"bytes"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestParseSOAPFixture(t *testing.T) {
	t.Parallel()

	path := filepath.Join("..", "..", "test", "fixtures", "mm7", "submit_req.multipart")
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}

	req := httptest.NewRequest("POST", "/mm7", bytes.NewReader(body))
	req.Header.Set("Content-Type", "multipart/related; boundary=soap-boundary")

	parsed, err := ParseRequest(req)
	if err != nil {
		t.Fatalf("parse fixture: %v", err)
	}
	if parsed.Envelope.Header.TransactionID.Value != "txn-fixture" {
		t.Fatalf("unexpected transaction id: %q", parsed.Envelope.Header.TransactionID.Value)
	}
	if parsed.Envelope.Body.SubmitReq == nil || parsed.Envelope.Body.SubmitReq.MM7Version != "5.3.0" {
		t.Fatalf("unexpected submit req: %#v", parsed.Envelope.Body.SubmitReq)
	}
	if parsed.Envelope.Body.SubmitReq.SenderIdentification.VASPID != "vasp-001" {
		t.Fatalf("unexpected vasp id: %#v", parsed.Envelope.Body.SubmitReq.SenderIdentification)
	}
	attachment, ok := parsed.Attachments["message-content"]
	if !ok || attachment.ContentType != "text/plain" || string(attachment.Data) != "hello from fixture" {
		t.Fatalf("unexpected attachment: %#v", attachment)
	}
}
