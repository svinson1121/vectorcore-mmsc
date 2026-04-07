package mm7

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/vectorcore/vectorcore-mmsc/internal/db"
	"github.com/vectorcore/vectorcore-mmsc/internal/message"
)

func TestNotifierSendDeliveryReport(t *testing.T) {
	t.Parallel()

	_, repo, _ := newMM7TestEnv(t)
	if err := repo.UpsertMM7VASP(context.Background(), db.MM7VASP{
		VASPID:       "vasp-1",
		SharedSecret: "secret",
		ReportURL:    "https://vasp.example.net/report",
		Active:       true,
	}); err != nil {
		t.Fatalf("upsert vasp: %v", err)
	}

	notifier := NewNotifier(repo)
	var got struct {
		url         string
		contentType string
		body        string
		auth        string
	}
	notifier.send = func(_ context.Context, url string, contentType string, body []byte, authHeader string, _ http.Header) ([]byte, string, error) {
		got.url = url
		got.contentType = contentType
		got.body = string(body)
		got.auth = authHeader
		return []byte(`<?xml version="1.0"?><soapenv:Envelope xmlns:soapenv="http://schemas.xmlsoap.org/soap/envelope/" xmlns:mm7="http://www.3gpp.org/ftp/Specs/archive/23_series/23.140/schema/REL-6-MM7-1-4"><soapenv:Header><mm7:TransactionID soapenv:mustUnderstand="1">txn-1</mm7:TransactionID></soapenv:Header><soapenv:Body><mm7:DeliveryReportRsp><MM7Version>6.8.0</MM7Version><Status><StatusCode>1000</StatusCode><StatusText>Success</StatusText></Status></mm7:DeliveryReportRsp></soapenv:Body></soapenv:Envelope>`), "text/xml; charset=utf-8", nil
	}

	msg := &message.Message{
		ID:            "mid-1",
		TransactionID: "txn-1",
		Origin:        message.InterfaceMM7,
		OriginHost:    "vasp-1",
		To:            []string{"+12025550101"},
	}
	if err := notifier.SendDeliveryReport(context.Background(), msg, message.StatusDelivered); err != nil {
		t.Fatalf("send delivery report: %v", err)
	}
	if got.url != "https://vasp.example.net/report" || !strings.Contains(got.body, "<mm7:DeliveryReportReq>") || !strings.Contains(got.body, "<MMStatus>Retrieved</MMStatus>") {
		t.Fatalf("unexpected outbound request: %#v", got)
	}
	if !strings.HasPrefix(got.auth, "Basic ") {
		t.Fatalf("expected basic auth header, got %q", got.auth)
	}
}

func TestNotifierSendReadReply(t *testing.T) {
	t.Parallel()

	_, repo, _ := newMM7TestEnv(t)
	if err := repo.UpsertMM7VASP(context.Background(), db.MM7VASP{
		VASPID:    "vasp-1",
		ReportURL: "https://vasp.example.net/report",
		Active:    true,
	}); err != nil {
		t.Fatalf("upsert vasp: %v", err)
	}

	notifier := NewNotifier(repo)
	var body string
	notifier.send = func(_ context.Context, _ string, _ string, payload []byte, _ string, _ http.Header) ([]byte, string, error) {
		body = string(payload)
		return []byte(`<?xml version="1.0"?><soapenv:Envelope xmlns:soapenv="http://schemas.xmlsoap.org/soap/envelope/" xmlns:mm7="http://www.3gpp.org/ftp/Specs/archive/23_series/23.140/schema/REL-6-MM7-1-4"><soapenv:Header><mm7:TransactionID soapenv:mustUnderstand="1">txn-2</mm7:TransactionID></soapenv:Header><soapenv:Body><mm7:ReadReplyRsp><MM7Version>6.8.0</MM7Version><Status><StatusCode>1000</StatusCode><StatusText>Success</StatusText></Status></mm7:ReadReplyRsp></soapenv:Body></soapenv:Envelope>`), "text/xml; charset=utf-8", nil
	}
	msg := &message.Message{
		ID:            "mid-2",
		TransactionID: "txn-2",
		Origin:        message.InterfaceMM7,
		OriginHost:    "vasp-1",
		To:            []string{"+12025550101"},
	}
	if err := notifier.SendReadReply(context.Background(), msg); err != nil {
		t.Fatalf("send read reply: %v", err)
	}
	if !strings.Contains(body, "<mm7:ReadReplyReq>") || !strings.Contains(body, "<MMStatus>Read</MMStatus>") {
		t.Fatalf("unexpected body: %s", body)
	}
}

func TestNotifierSendDeliverReq(t *testing.T) {
	t.Parallel()

	_, repo, _ := newMM7TestEnv(t)
	if err := repo.UpsertMM7VASP(context.Background(), db.MM7VASP{
		VASPID:       "vasp-1",
		SharedSecret: "secret",
		DeliverURL:   "https://vasp.example.net/deliver",
		Active:       true,
	}); err != nil {
		t.Fatalf("upsert vasp: %v", err)
	}

	notifier := NewNotifier(repo)
	var got struct {
		contentType string
		body        string
	}
	notifier.send = func(_ context.Context, _ string, contentType string, body []byte, _ string, _ http.Header) ([]byte, string, error) {
		got.contentType = contentType
		got.body = string(body)
		return []byte(`<?xml version="1.0"?><soapenv:Envelope xmlns:soapenv="http://schemas.xmlsoap.org/soap/envelope/" xmlns:mm7="http://www.3gpp.org/ftp/Specs/archive/23_series/23.140/schema/REL-6-MM7-1-4"><soapenv:Header><mm7:TransactionID soapenv:mustUnderstand="1">txn-3</mm7:TransactionID></soapenv:Header><soapenv:Body><mm7:DeliverRsp><MM7Version>6.8.0</MM7Version><Status><StatusCode>1000</StatusCode><StatusText>Success</StatusText></Status></mm7:DeliverRsp></soapenv:Body></soapenv:Envelope>`), "text/xml; charset=utf-8", nil
	}
	msg := &message.Message{
		ID:            "mid-3",
		TransactionID: "txn-3",
		From:          "12345",
		To:            []string{"+12025550101"},
		Subject:       "hello",
		Parts: []message.Part{{
			ContentType: "text/plain",
			Data:        []byte("hello"),
		}},
	}
	if err := notifier.SendDeliverReq(context.Background(), "vasp-1", msg); err != nil {
		t.Fatalf("send deliver req: %v", err)
	}
	if !strings.HasPrefix(got.contentType, "multipart/related;") || !strings.Contains(got.body, "<mm7:DeliverReq>") || !strings.Contains(got.body, "Content-Id: <message-content>") {
		t.Fatalf("unexpected deliver request: %#v", got)
	}
}

func TestNotifierRejectsMM7FaultResponse(t *testing.T) {
	t.Parallel()

	_, repo, _ := newMM7TestEnv(t)
	if err := repo.UpsertMM7VASP(context.Background(), db.MM7VASP{
		VASPID:     "vasp-1",
		DeliverURL: "https://vasp.example.net/deliver",
		Active:     true,
	}); err != nil {
		t.Fatalf("upsert vasp: %v", err)
	}

	notifier := NewNotifier(repo)
	notifier.send = func(_ context.Context, _ string, _ string, _ []byte, _ string, _ http.Header) ([]byte, string, error) {
		return []byte(`<?xml version="1.0"?><soapenv:Envelope xmlns:soapenv="http://schemas.xmlsoap.org/soap/envelope/" xmlns:mm7="http://www.3gpp.org/ftp/Specs/archive/23_series/23.140/schema/REL-6-MM7-1-4"><soapenv:Body><soapenv:Fault><faultcode>soapenv:Server</faultcode><faultstring>Rejected</faultstring></soapenv:Fault></soapenv:Body></soapenv:Envelope>`), "text/xml; charset=utf-8", nil
	}

	err := notifier.SendDeliverReq(context.Background(), "vasp-1", &message.Message{
		ID:            "mid-4",
		TransactionID: "txn-4",
		From:          "12345",
		To:            []string{"+12025550101"},
		Parts: []message.Part{{
			ContentType: "text/plain",
			Data:        []byte("hello"),
		}},
	})
	if err == nil || !strings.Contains(err.Error(), "mm7 fault") {
		t.Fatalf("expected mm7 fault, got %v", err)
	}
}

func TestNotifierSendEAIFDeliveryReport(t *testing.T) {
	t.Parallel()

	_, repo, _ := newMM7TestEnv(t)
	if err := repo.UpsertMM7VASP(context.Background(), db.MM7VASP{
		VASPID:       "vasp-1",
		Protocol:     "eaif",
		Version:      "3.0",
		SharedSecret: "secret",
		ReportURL:    "https://vasp.example.net/report",
		Active:       true,
	}); err != nil {
		t.Fatalf("upsert vasp: %v", err)
	}

	notifier := NewNotifier(repo)
	var got struct {
		contentType string
		auth        string
		headers     http.Header
		body        []byte
	}
	notifier.send = func(_ context.Context, _ string, contentType string, body []byte, authHeader string, headers http.Header) ([]byte, string, error) {
		got.contentType = contentType
		got.auth = authHeader
		got.headers = headers.Clone()
		got.body = append([]byte(nil), body...)
		return []byte("ok"), "text/plain", nil
	}

	msg := &message.Message{
		ID:            "mid-1",
		TransactionID: "txn-1",
		Origin:        message.InterfaceMM7,
		OriginHost:    "vasp-1",
		From:          "12345",
		To:            []string{"+12025550101"},
		Subject:       "hello",
		Parts: []message.Part{{
			ContentType: "text/plain",
			Data:        []byte("hello"),
		}},
	}
	if err := notifier.SendDeliveryReport(context.Background(), msg, message.StatusDelivered); err != nil {
		t.Fatalf("send eaif delivery report: %v", err)
	}
	if got.contentType != "application/vnd.wap.mms-message" {
		t.Fatalf("unexpected content type: %q", got.contentType)
	}
	if got.headers.Get("X-NOKIA-MMSC-Message-Type") != "DeliveryReport" || got.headers.Get("X-NOKIA-MMSC-Version") != "3.0" {
		t.Fatalf("unexpected eaif headers: %#v", got.headers)
	}
	if !strings.HasPrefix(got.auth, "Basic ") {
		t.Fatalf("expected auth header, got %q", got.auth)
	}
	if len(got.body) == 0 {
		t.Fatal("expected eaif body")
	}
}

func TestNotifierSendEAIFDeliverReq(t *testing.T) {
	t.Parallel()

	_, repo, _ := newMM7TestEnv(t)
	if err := repo.UpsertMM7VASP(context.Background(), db.MM7VASP{
		VASPID:     "vasp-1",
		Protocol:   "eaif",
		Version:    "3.0",
		DeliverURL: "https://vasp.example.net/deliver",
		Active:     true,
	}); err != nil {
		t.Fatalf("upsert vasp: %v", err)
	}

	notifier := NewNotifier(repo)
	var got struct {
		contentType string
		headers     http.Header
	}
	notifier.send = func(_ context.Context, _ string, contentType string, body []byte, _ string, headers http.Header) ([]byte, string, error) {
		got.contentType = contentType
		got.headers = headers.Clone()
		if len(body) == 0 {
			t.Fatal("expected eaif pdu body")
		}
		return []byte("ok"), "text/plain", nil
	}
	msg := &message.Message{
		ID:            "mid-3",
		TransactionID: "txn-3",
		From:          "12345",
		To:            []string{"+12025550101"},
		Subject:       "hello",
		Parts: []message.Part{{
			ContentType: "text/plain",
			Data:        []byte("hello"),
		}},
	}
	if err := notifier.SendDeliverReq(context.Background(), "vasp-1", msg); err != nil {
		t.Fatalf("send eaif deliver req: %v", err)
	}
	if got.contentType != "application/vnd.wap.mms-message" {
		t.Fatalf("unexpected content type: %q", got.contentType)
	}
	if got.headers.Get("X-NOKIA-MMSC-Message-Type") != "MultiMediaMessage" || got.headers.Get("X-NOKIA-MMSC-To") != "+12025550101" {
		t.Fatalf("unexpected eaif headers: %#v", got.headers)
	}
}
