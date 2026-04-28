package mm7

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/xml"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"os"
	"strings"
	"testing"

	"github.com/vectorcore/vectorcore-mmsc/internal/adapt"
	"github.com/vectorcore/vectorcore-mmsc/internal/config"
	"github.com/vectorcore/vectorcore-mmsc/internal/db"
	"github.com/vectorcore/vectorcore-mmsc/internal/message"
	"github.com/vectorcore/vectorcore-mmsc/internal/mmspdu"
	"github.com/vectorcore/vectorcore-mmsc/internal/routing"
	"github.com/vectorcore/vectorcore-mmsc/internal/store"
	"github.com/vectorcore/vectorcore-mmsc/internal/wappush"
)

type fakePushSender struct {
	sourceAddr string
	msisdn     string
	push       []byte
	calls      int
}

func (f *fakePushSender) SubmitWAPPush(_ context.Context, sourceAddr string, msisdn string, push []byte) error {
	f.sourceAddr = sourceAddr
	f.msisdn = msisdn
	f.push = append([]byte(nil), push...)
	f.calls++
	return nil
}

type fakeMM4Sender struct {
	msg   *message.Message
	calls int
}

func (f *fakeMM4Sender) Send(_ context.Context, msg *message.Message) error {
	cloned := msg.Clone()
	f.msg = &cloned
	f.calls++
	return nil
}

type fakeMM3Sender struct {
	msg   *message.Message
	calls int
}

func (f *fakeMM3Sender) Send(_ context.Context, msg *message.Message) error {
	cloned := msg.Clone()
	f.msg = &cloned
	f.calls++
	return nil
}

type classAwareAdapter struct {
	transform func(message.Part, adapt.Constraints) (message.Part, error)
}

func (a classAwareAdapter) Adapt(_ context.Context, parts []message.Part, constraints adapt.Constraints) ([]message.Part, error) {
	out := make([]message.Part, 0, len(parts))
	for _, part := range parts {
		next := part
		if a.transform != nil {
			var err error
			next, err = a.transform(part, constraints)
			if err != nil {
				return nil, err
			}
		}
		out = append(out, next)
	}
	return out, nil
}

func TestSubmitReqStoresAndDispatchesLocalMT(t *testing.T) {
	t.Parallel()

	cfg, repo, contentStore := newMM7TestEnv(t)
	cfg.Adapt.Enabled = true
	if err := repo.UpsertMM7VASP(context.Background(), db.MM7VASP{
		VASPID:       "vasp-001",
		VASID:        "service-001",
		SharedSecret: "secret",
		Active:       true,
	}); err != nil {
		t.Fatalf("upsert vasp: %v", err)
	}

	push := &fakePushSender{}
	server := NewServer(cfg, repo, contentStore, routing.NewEngine(repo), push, nil, nil)

	req := newSubmitReq("txn-001", "vasp-001", "service-001", "12345", "+12025550101", "Hello", "message-content", "text/plain", []byte("hello from vasp"))
	httpReq := httptest.NewRequest(http.MethodPost, cfg.MM7.Path, bytes.NewReader(req.body))
	httpReq.Header.Set("Content-Type", req.contentType)
	httpReq.Header.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte("vasp-001:secret")))

	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, httpReq)
	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected status: %d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "<mm7:SubmitRsp>") {
		t.Fatalf("unexpected response body: %s", rec.Body.String())
	}
	if push.calls != 1 || push.msisdn != "+12025550101" {
		t.Fatalf("unexpected push dispatch: %#v", push)
	}

	messages, err := repo.ListMessages(context.Background(), db.MessageFilter{Limit: 10})
	if err != nil {
		t.Fatalf("list messages: %v", err)
	}
	if len(messages) != 1 {
		t.Fatalf("expected one message, got %d", len(messages))
	}
	if messages[0].Origin != message.InterfaceMM7 || messages[0].OriginHost != "vasp-001" {
		t.Fatalf("unexpected stored message: %#v", messages[0])
	}
	if messages[0].Status != message.StatusDelivering {
		t.Fatalf("expected delivering status, got %v", messages[0].Status)
	}

	reader, _, err := contentStore.Get(context.Background(), messages[0].ContentPath)
	if err != nil {
		t.Fatalf("get stored content: %v", err)
	}
	defer reader.Close()
	raw, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("read stored content: %v", err)
	}
	pdu, err := mmspdu.Decode(raw)
	if err != nil {
		t.Fatalf("decode stored pdu: %v", err)
	}
	if pdu.MessageType != mmspdu.MsgTypeRetrieveConf || pdu.Body == nil || len(pdu.Body.Parts) != 1 {
		t.Fatalf("unexpected stored pdu: %#v", pdu)
	}

	unwrapped, err := wappush.UnwrapMMSPDU(push.push)
	if err != nil {
		t.Fatalf("unwrap push: %v", err)
	}
	notification, err := mmspdu.Decode(unwrapped)
	if err != nil {
		t.Fatalf("decode push pdu: %v", err)
	}
	if notification.MessageType != mmspdu.MsgTypeNotificationInd {
		t.Fatalf("unexpected notification type: %x", notification.MessageType)
	}
}

func TestSubmitReqRelaysMM4Destination(t *testing.T) {
	t.Parallel()

	cfg, repo, contentStore := newMM7TestEnv(t)
	if err := repo.UpsertMM7VASP(context.Background(), db.MM7VASP{
		VASPID:       "vasp-001",
		SharedSecret: "secret",
		Active:       true,
	}); err != nil {
		t.Fatalf("upsert vasp: %v", err)
	}
	if err := repo.UpsertMM4Peer(context.Background(), db.MM4Peer{
		Domain:     "peer.example.net",
		SMTPHost:   "smtp.peer.example.net",
		SMTPPort:   25,
		TLSEnabled: true,
		Active:     true,
	}); err != nil {
		t.Fatalf("upsert peer: %v", err)
	}

	mm4Sender := &fakeMM4Sender{}
	server := NewServer(cfg, repo, contentStore, routing.NewEngine(repo), nil, mm4Sender, nil)

	req := newSubmitReq("txn-002", "vasp-001", "", "12345", "user@peer.example.net", "Relay", "message-content", "text/plain", []byte("relay this"))
	httpReq := httptest.NewRequest(http.MethodPost, cfg.MM7.Path, bytes.NewReader(req.body))
	httpReq.Header.Set("Content-Type", req.contentType)
	httpReq.Header.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte("vasp-001:secret")))

	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, httpReq)
	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected status: %d body=%s", rec.Code, rec.Body.String())
	}
	if mm4Sender.calls != 1 || mm4Sender.msg == nil {
		t.Fatalf("expected mm4 relay, got %#v", mm4Sender)
	}
	if mm4Sender.msg.To[0] != "user@peer.example.net" {
		t.Fatalf("unexpected relayed message: %#v", mm4Sender.msg)
	}
}

func TestSubmitReqRoutesMM3Email(t *testing.T) {
	t.Parallel()

	cfg, repo, contentStore := newMM7TestEnv(t)
	if err := repo.UpsertMM7VASP(context.Background(), db.MM7VASP{
		VASPID:       "vasp-001",
		SharedSecret: "secret",
		Active:       true,
	}); err != nil {
		t.Fatalf("upsert vasp: %v", err)
	}

	mm3Sender := &fakeMM3Sender{}
	server := NewServer(cfg, repo, contentStore, routing.NewEngine(repo), nil, nil, mm3Sender)

	req := newSubmitReq("txn-002b", "vasp-001", "", "12345", "user@example.org", "Relay", "message-content", "text/plain", []byte("relay this"))
	httpReq := httptest.NewRequest(http.MethodPost, cfg.MM7.Path, bytes.NewReader(req.body))
	httpReq.Header.Set("Content-Type", req.contentType)
	httpReq.Header.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte("vasp-001:secret")))

	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, httpReq)
	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected status: %d body=%s", rec.Code, rec.Body.String())
	}
	if mm3Sender.calls != 1 || mm3Sender.msg == nil {
		t.Fatalf("expected mm3 relay, got %#v", mm3Sender)
	}
	if mm3Sender.msg.To[0] != "user@example.org" || mm3Sender.msg.Status != message.StatusForwarded {
		t.Fatalf("unexpected relayed message: %#v", mm3Sender.msg)
	}
}

func TestSubmitReqAppliesAdaptationClassToStoredContent(t *testing.T) {
	t.Parallel()

	cfg, repo, contentStore := newMM7TestEnv(t)
	cfg.Adapt.Enabled = true
	if err := repo.UpsertAdaptationClass(context.Background(), db.AdaptationClass{
		Name:              "default",
		MaxMsgSizeBytes:   307200,
		MaxImageWidth:     640,
		MaxImageHeight:    480,
		AllowedImageTypes: []string{"image/jpeg", "image/gif", "image/png"},
		AllowedAudioTypes: []string{"audio/mpeg"},
		AllowedVideoTypes: []string{"video/3gpp", "video/mp4"},
	}); err != nil {
		t.Fatalf("upsert adaptation class: %v", err)
	}
	if err := repo.UpsertMM7VASP(context.Background(), db.MM7VASP{
		VASPID:       "vasp-001",
		VASID:        "service-001",
		SharedSecret: "secret",
		Active:       true,
	}); err != nil {
		t.Fatalf("upsert vasp: %v", err)
	}

	push := &fakePushSender{}
	server := NewServer(cfg, repo, contentStore, routing.NewEngine(repo), push, nil, nil)
	server.adapt.SetAdapter(classAwareAdapter{
		transform: func(part message.Part, constraints adapt.Constraints) (message.Part, error) {
			if part.ContentType != "audio/ogg" {
				return part, nil
			}
			if len(constraints.AllowedAudioTypes) != 1 || constraints.AllowedAudioTypes[0] != "audio/mpeg" {
				t.Fatalf("unexpected audio constraints: %#v", constraints)
			}
			part.ContentType = "audio/mpeg"
			part.Data = []byte("mp3 payload")
			part.Size = int64(len(part.Data))
			return part, nil
		},
	})

	req := newSubmitReq("txn-004", "vasp-001", "service-001", "12345", "+12025550101", "Audio", "message-content", "audio/ogg", []byte("ogg payload"))
	httpReq := httptest.NewRequest(http.MethodPost, cfg.MM7.Path, bytes.NewReader(req.body))
	httpReq.Header.Set("Content-Type", req.contentType)
	httpReq.Header.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte("vasp-001:secret")))

	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, httpReq)
	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected status: %d body=%s", rec.Code, rec.Body.String())
	}

	messages, err := repo.ListMessages(context.Background(), db.MessageFilter{Limit: 10})
	if err != nil {
		t.Fatalf("list messages: %v", err)
	}
	if len(messages) != 1 {
		t.Fatalf("expected one message, got %d", len(messages))
	}

	reader, _, err := contentStore.Get(context.Background(), messages[0].ContentPath)
	if err != nil {
		t.Fatalf("get stored content: %v", err)
	}
	defer reader.Close()
	raw, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("read stored content: %v", err)
	}
	pdu, err := mmspdu.Decode(raw)
	if err != nil {
		t.Fatalf("decode stored pdu: %v", err)
	}
	if pdu.Body == nil || len(pdu.Body.Parts) != 1 {
		t.Fatalf("unexpected stored pdu: %#v", pdu)
	}
	if pdu.Body.Parts[0].ContentType != "audio/mpeg" || string(pdu.Body.Parts[0].Data) != "mp3 payload" {
		t.Fatalf("unexpected adapted part: %#v", pdu.Body.Parts[0])
	}
}

func TestSubmitReqRejectsUnauthorizedVASP(t *testing.T) {
	t.Parallel()

	cfg, repo, contentStore := newMM7TestEnv(t)
	if err := repo.UpsertMM7VASP(context.Background(), db.MM7VASP{
		VASPID:       "vasp-001",
		SharedSecret: "secret",
		Active:       true,
	}); err != nil {
		t.Fatalf("upsert vasp: %v", err)
	}

	server := NewServer(cfg, repo, contentStore, routing.NewEngine(repo), nil, nil, nil)
	req := newSubmitReq("txn-003", "vasp-001", "", "12345", "+12025550101", "Hello", "message-content", "text/plain", []byte("unauthorized"))
	httpReq := httptest.NewRequest(http.MethodPost, cfg.MM7.Path, bytes.NewReader(req.body))
	httpReq.Header.Set("Content-Type", req.contentType)

	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, httpReq)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected unauthorized, got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestCancelReqRejectsMM7Message(t *testing.T) {
	t.Parallel()

	cfg, repo, contentStore := newMM7TestEnv(t)
	if err := repo.UpsertMM7VASP(context.Background(), db.MM7VASP{
		VASPID:       "vasp-001",
		SharedSecret: "secret",
		Active:       true,
	}); err != nil {
		t.Fatalf("upsert vasp: %v", err)
	}
	if err := repo.CreateMessage(context.Background(), &message.Message{
		ID:            "mid-cancel",
		TransactionID: "txn-cancel",
		Status:        message.StatusQueued,
		Direction:     message.DirectionMT,
		Origin:        message.InterfaceMM7,
		OriginHost:    "vasp-001",
		From:          "12345",
		To:            []string{"+12025550101"},
		ContentType:   "application/vnd.wap.mms-message",
	}); err != nil {
		t.Fatalf("create message: %v", err)
	}

	server := NewServer(cfg, repo, contentStore, routing.NewEngine(repo), nil, nil, nil)
	req := newCancelReq("txn-cancel-ctl", "vasp-001", "mid-cancel")
	httpReq := httptest.NewRequest(http.MethodPost, cfg.MM7.Path, bytes.NewReader(req.body))
	httpReq.Header.Set("Content-Type", req.contentType)
	httpReq.Header.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte("vasp-001:secret")))
	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, httpReq)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "<mm7:CancelRsp>") {
		t.Fatalf("unexpected cancel response: %d %s", rec.Code, rec.Body.String())
	}
	updated, err := repo.GetMessage(context.Background(), "mid-cancel")
	if err != nil {
		t.Fatalf("get cancelled message: %v", err)
	}
	if updated.Status != message.StatusRejected {
		t.Fatalf("expected rejected status, got %v", updated.Status)
	}
}

func TestReplaceReqUpdatesStoredContent(t *testing.T) {
	t.Parallel()

	cfg, repo, contentStore := newMM7TestEnv(t)
	if err := repo.UpsertMM7VASP(context.Background(), db.MM7VASP{
		VASPID:       "vasp-001",
		SharedSecret: "secret",
		Active:       true,
	}); err != nil {
		t.Fatalf("upsert vasp: %v", err)
	}
	originalPDU, err := mmspdu.Encode(mmspdu.NewRetrieveConf("txn-replace", []mmspdu.Part{{
		ContentType: "text/plain",
		Data:        []byte("old body"),
	}}))
	if err != nil {
		t.Fatalf("encode original pdu: %v", err)
	}
	contentPath, err := contentStore.Put(context.Background(), "mid-replace", bytes.NewReader(originalPDU), int64(len(originalPDU)), "application/vnd.wap.mms-message")
	if err != nil {
		t.Fatalf("store original content: %v", err)
	}
	if err := repo.CreateMessage(context.Background(), &message.Message{
		ID:            "mid-replace",
		TransactionID: "txn-replace",
		Status:        message.StatusQueued,
		Direction:     message.DirectionMT,
		Origin:        message.InterfaceMM7,
		OriginHost:    "vasp-001",
		From:          "12345",
		To:            []string{"+12025550101"},
		Subject:       "Old",
		ContentType:   "text/plain",
		ContentPath:   contentPath,
		StoreID:       contentPath,
		MessageSize:   int64(len(originalPDU)),
	}); err != nil {
		t.Fatalf("create message: %v", err)
	}

	server := NewServer(cfg, repo, contentStore, routing.NewEngine(repo), nil, nil, nil)
	req := newReplaceReq("txn-replace-ctl", "vasp-001", "mid-replace", "New Subject", "replacement-content", "text/plain", []byte("new body"))
	httpReq := httptest.NewRequest(http.MethodPost, cfg.MM7.Path, bytes.NewReader(req.body))
	httpReq.Header.Set("Content-Type", req.contentType)
	httpReq.Header.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte("vasp-001:secret")))
	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, httpReq)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "<mm7:ReplaceRsp>") {
		t.Fatalf("unexpected replace response: %d %s", rec.Code, rec.Body.String())
	}

	updated, err := repo.GetMessage(context.Background(), "mid-replace")
	if err != nil {
		t.Fatalf("get replaced message: %v", err)
	}
	if updated.Subject != "New Subject" {
		t.Fatalf("unexpected updated subject: %#v", updated)
	}
	reader, _, err := contentStore.Get(context.Background(), updated.ContentPath)
	if err != nil {
		t.Fatalf("get replaced content: %v", err)
	}
	defer reader.Close()
	raw, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("read replaced content: %v", err)
	}
	pdu, err := mmspdu.Decode(raw)
	if err != nil {
		t.Fatalf("decode replaced pdu: %v", err)
	}
	if pdu.Body == nil || len(pdu.Body.Parts) != 1 || string(pdu.Body.Parts[0].Data) != "new body" {
		t.Fatalf("unexpected replaced pdu: %#v", pdu)
	}
}

type submitFixture struct {
	contentType string
	body        []byte
}

func newSubmitReq(transactionID, vaspID, vasID, sender, recipient, subject, cid, partType string, payload []byte) submitFixture {
	return newMultipartMM7Request(transactionID, "submit", &SubmitReq{
		MM7Version: defaultMM7Ver,
		SenderIdentification: SenderIdentification{
			VASPID: vaspID,
			VASID:  vasID,
			SenderAddress: SenderAddress{
				ShortCode: sender,
			},
		},
		Recipients: Recipients{
			To: []Recipient{{Number: recipient}},
		},
		Subject: subject,
		Content: ContentRef{Href: "cid:" + cid},
	}, cid, partType, payload)
}

func newCancelReq(transactionID, vaspID, messageID string) submitFixture {
	return newSOAPOnlyMM7Request(transactionID, "cancel", &CancelReq{
		MM7Version: defaultMM7Ver,
		VASPID:     vaspID,
		MessageID:  messageID,
	})
}

func newReplaceReq(transactionID, vaspID, messageID, subject, cid, partType string, payload []byte) submitFixture {
	return newMultipartMM7Request(transactionID, "replace", &ReplaceReq{
		MM7Version: defaultMM7Ver,
		VASPID:     vaspID,
		MessageID:  messageID,
		Subject:    subject,
		Content:    ContentRef{Href: "cid:" + cid},
	}, cid, partType, payload)
}

func newMultipartMM7Request(transactionID, operation string, payload any, cid, partType string, attachment []byte) submitFixture {
	var soap bytes.Buffer
	encoder := xml.NewEncoder(&soap)
	soap.WriteString(xml.Header)
	envStart := xml.StartElement{
		Name: xml.Name{Local: "env:Envelope"},
		Attr: []xml.Attr{
			{Name: xml.Name{Local: "xmlns:env"}, Value: soapEnvNamespace},
			{Name: xml.Name{Local: "xmlns:mm7"}, Value: defaultNamespace},
		},
	}
	_ = encoder.EncodeToken(envStart)
	_ = encoder.EncodeToken(xml.StartElement{Name: xml.Name{Local: "env:Header"}})
	_ = encoder.EncodeElement(transactionID, xml.StartElement{
		Name: xml.Name{Local: "mm7:TransactionID"},
		Attr: []xml.Attr{{Name: xml.Name{Local: "env:mustUnderstand"}, Value: "1"}},
	})
	_ = encoder.EncodeToken(xml.EndElement{Name: xml.Name{Local: "env:Header"}})
	_ = encoder.EncodeToken(xml.StartElement{Name: xml.Name{Local: "env:Body"}})
	_ = encoder.EncodeElement(payload, xml.StartElement{Name: xml.Name{Local: "mm7:" + strings.Title(operation) + "Req"}})
	_ = encoder.EncodeToken(xml.EndElement{Name: xml.Name{Local: "env:Body"}})
	_ = encoder.EncodeToken(envStart.End())
	_ = encoder.Flush()

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	rootHeader := textproto.MIMEHeader{}
	rootHeader.Set("Content-Type", "text/xml; charset=utf-8")
	rootPart, _ := writer.CreatePart(rootHeader)
	_, _ = rootPart.Write(soap.Bytes())

	partHeader := textproto.MIMEHeader{}
	partHeader.Set("Content-Type", partType)
	partHeader.Set("Content-ID", "<"+cid+">")
	part, _ := writer.CreatePart(partHeader)
	_, _ = part.Write(attachment)
	_ = writer.Close()

	return submitFixture{
		contentType: "multipart/related; boundary=" + writer.Boundary(),
		body:        body.Bytes(),
	}
}

func newSOAPOnlyMM7Request(transactionID, operation string, payload any) submitFixture {
	var soap bytes.Buffer
	encoder := xml.NewEncoder(&soap)
	soap.WriteString(xml.Header)
	envStart := xml.StartElement{
		Name: xml.Name{Local: "env:Envelope"},
		Attr: []xml.Attr{
			{Name: xml.Name{Local: "xmlns:env"}, Value: soapEnvNamespace},
			{Name: xml.Name{Local: "xmlns:mm7"}, Value: defaultNamespace},
		},
	}
	_ = encoder.EncodeToken(envStart)
	_ = encoder.EncodeToken(xml.StartElement{Name: xml.Name{Local: "env:Header"}})
	_ = encoder.EncodeElement(transactionID, xml.StartElement{
		Name: xml.Name{Local: "mm7:TransactionID"},
		Attr: []xml.Attr{{Name: xml.Name{Local: "env:mustUnderstand"}, Value: "1"}},
	})
	_ = encoder.EncodeToken(xml.EndElement{Name: xml.Name{Local: "env:Header"}})
	_ = encoder.EncodeToken(xml.StartElement{Name: xml.Name{Local: "env:Body"}})
	_ = encoder.EncodeElement(payload, xml.StartElement{Name: xml.Name{Local: "mm7:" + strings.Title(operation) + "Req"}})
	_ = encoder.EncodeToken(xml.EndElement{Name: xml.Name{Local: "env:Body"}})
	_ = encoder.EncodeToken(envStart.End())
	_ = encoder.Flush()

	return submitFixture{
		contentType: "text/xml; charset=utf-8",
		body:        soap.Bytes(),
	}
}

func newMM7TestEnv(t *testing.T) (*config.Config, db.Repository, store.Store) {
	t.Helper()

	cfg := config.Default()
	cfg.Database.Driver = "sqlite"
	cfg.Database.DSN = t.TempDir() + "/mm7.db"
	cfg.Store.Backend = "filesystem"
	cfg.Store.Filesystem.Root = t.TempDir()
	cfg.MM4.Hostname = "mmsc.example.net"
	cfg.MM7.Path = "/mm7"

	repo, err := db.Open(context.Background(), db.OpenOptions{
		Driver: cfg.Database.Driver,
		DSN:    cfg.Database.DSN,
	})
	if err != nil {
		t.Fatalf("open repo: %v", err)
	}
	t.Cleanup(func() { _ = repo.Close() })
	if err := db.RunMigrations(context.Background(), repo, os.DirFS("../..")); err != nil {
		t.Fatalf("run migrations: %v", err)
	}
	addMM7TestRoutes(t, repo)

	contentStore, err := store.New(context.Background(), cfg.Store)
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	t.Cleanup(func() { _ = contentStore.Close() })
	return cfg, repo, contentStore
}

func addMM7TestRoutes(t *testing.T, repo db.Repository) {
	t.Helper()
	routes := []db.MM4Route{
		{
			Name:       "Local test prefix",
			MatchType:  "msisdn_prefix",
			MatchValue: "+1202555",
			EgressType: "local",
			Priority:   100,
			Active:     true,
		},
		{
			Name:         "MM4 test domain",
			MatchType:    "recipient_domain",
			MatchValue:   "peer.example.net",
			EgressType:   "mm4",
			EgressTarget: "peer.example.net",
			Priority:     100,
			Active:       true,
		},
		{
			Name:       "MM3 test domain",
			MatchType:  "recipient_domain",
			MatchValue: "example.org",
			EgressType: "mm3",
			Priority:   100,
			Active:     true,
		},
	}
	for _, route := range routes {
		if err := repo.UpsertMM4Route(context.Background(), route); err != nil {
			t.Fatalf("upsert route %s: %v", route.Name, err)
		}
	}
}
