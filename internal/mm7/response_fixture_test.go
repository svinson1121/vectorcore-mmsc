package mm7

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriteOperationRspMatchesSOAPFixture(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name        string
		operation   string
		transaction string
		messageID   string
		fixture     string
	}{
		{name: "deliver", operation: "deliver", transaction: "txn-deliver", messageID: "mid-deliver", fixture: "deliver_rsp.soap"},
		{name: "delivery-report", operation: "delivery-report", transaction: "txn-report", messageID: "mid-report", fixture: "delivery_report_rsp.soap"},
		{name: "read-reply", operation: "read-reply", transaction: "txn-read", messageID: "mid-read", fixture: "read_reply_rsp.soap"},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			rec := httptest.NewRecorder()
			if err := WriteOperationRsp(rec, http.StatusOK, tc.transaction, tc.operation, tc.messageID, "5.3.0", defaultNamespace); err != nil {
				t.Fatalf("write operation rsp: %v", err)
			}
			if got := rec.Header().Get("Content-Type"); got != "text/xml; charset=utf-8" {
				t.Fatalf("unexpected content type: %q", got)
			}

			expected := readMM7Fixture(t, tc.fixture)
			if normalizeXML(rec.Body.String()) != normalizeXML(expected) {
				t.Fatalf("fixture mismatch:\nexpected:\n%s\ngot:\n%s", expected, rec.Body.String())
			}
		})
	}
}

func TestWriteEAIFResponseHeaders(t *testing.T) {
	t.Parallel()

	rec := httptest.NewRecorder()
	WriteEAIFResponse(rec, http.StatusNoContent, "3.0", "mid-eaif")

	if rec.Code != http.StatusNoContent {
		t.Fatalf("unexpected status: %d", rec.Code)
	}
	if got := rec.Header().Get("X-NOKIA-MMSC-Version"); got != "3.0" {
		t.Fatalf("unexpected version header: %q", got)
	}
	if got := rec.Header().Get("X-NOKIA-MMSC-Message-Id"); got != "mid-eaif" {
		t.Fatalf("unexpected message id header: %q", got)
	}
}

func TestWriteFaultWithVersionHeadersAndBody(t *testing.T) {
	t.Parallel()

	rec := httptest.NewRecorder()
	if err := WriteFaultWithVersion(rec, http.StatusBadRequest, "txn-fault", statusClientErr, "invalid request", "Client", defaultNamespace); err != nil {
		t.Fatalf("write fault: %v", err)
	}

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("unexpected status: %d", rec.Code)
	}
	if got := rec.Header().Get("Content-Type"); got != "text/xml; charset=utf-8" {
		t.Fatalf("unexpected content type: %q", got)
	}
	if got := rec.Header().Get("X-MM7-Status-Code"); got != statusClientErr {
		t.Fatalf("unexpected mm7 status code header: %q", got)
	}

	payload := normalizeXML(rec.Body.String())
	checks := []string{
		`<mm7:TransactionID soapenv:mustUnderstand="1">txn-fault</mm7:TransactionID>`,
		`<soapenv:Fault>`,
		`<faultcode>Client</faultcode>`,
		`<faultstring>invalid request</faultstring>`,
	}
	for _, check := range checks {
		if !strings.Contains(payload, normalizeXML(check)) {
			t.Fatalf("missing fault fragment %q in payload:\n%s", check, rec.Body.String())
		}
	}
}

func TestWriteEAIFErrorResponseCarriesVersionWithoutMessageID(t *testing.T) {
	t.Parallel()

	rec := httptest.NewRecorder()
	WriteEAIFResponse(rec, http.StatusBadRequest, "3.0", "")

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("unexpected status: %d", rec.Code)
	}
	if got := rec.Header().Get("X-NOKIA-MMSC-Version"); got != "3.0" {
		t.Fatalf("unexpected version header: %q", got)
	}
	if got := rec.Header().Get("X-NOKIA-MMSC-Message-Id"); got != "" {
		t.Fatalf("expected no message id header on error response, got %q", got)
	}
}

func readMM7Fixture(t *testing.T, name string) string {
	t.Helper()
	path := filepath.Join("..", "..", "test", "fixtures", "mm7", name)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	return string(data)
}

func normalizeXML(value string) string {
	replacer := strings.NewReplacer("\r", "", "\n", "", "\t", "", "  ", "")
	for {
		next := replacer.Replace(value)
		if next == value {
			return next
		}
		value = next
	}
}
