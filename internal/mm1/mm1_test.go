package mm1

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

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
	recipients []string
	err        error
	wait       <-chan struct{}
}

func (f *fakePushSender) SubmitWAPPush(_ context.Context, sourceAddr string, msisdn string, push []byte) error {
	if f.wait != nil {
		<-f.wait
	}
	if f.err != nil {
		return f.err
	}
	f.sourceAddr = sourceAddr
	f.msisdn = msisdn
	f.push = append([]byte(nil), push...)
	f.calls++
	f.recipients = append(f.recipients, msisdn)
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

type fakeMTReporter struct {
	deliveryMsg    *message.Message
	deliveryStatus message.Status
	deliveryCalls  int
	readMsg        *message.Message
	readCalls      int
}

func (f *fakeMTReporter) SendDeliveryReport(_ context.Context, msg *message.Message, status message.Status) error {
	cloned := msg.Clone()
	f.deliveryMsg = &cloned
	f.deliveryStatus = status
	f.deliveryCalls++
	return nil
}

func (f *fakeMTReporter) SendReadReply(_ context.Context, msg *message.Message) error {
	cloned := msg.Clone()
	f.readMsg = &cloned
	f.readCalls++
	return nil
}

func TestMOHandlerStoresAndResponds(t *testing.T) {
	t.Parallel()

	cfg := config.Default()
	cfg.Database.Driver = "sqlite"
	cfg.Database.DSN = t.TempDir() + "/mm1.db"
	cfg.Store.Backend = "filesystem"
	cfg.Store.Filesystem.Root = t.TempDir()
	cfg.MM4.Hostname = "mmsc.example.net"

	repo, err := db.Open(context.Background(), db.OpenOptions{
		Driver: cfg.Database.Driver,
		DSN:    cfg.Database.DSN,
	})
	if err != nil {
		t.Fatalf("open repo: %v", err)
	}
	defer repo.Close()
	if err := db.RunMigrations(context.Background(), repo, os.DirFS("../..")); err != nil {
		t.Fatalf("run migrations: %v", err)
	}

	contentStore, err := store.New(context.Background(), cfg.Store)
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	defer contentStore.Close()

	pushSender := &fakePushSender{}
	server := NewServer(cfg, repo, contentStore, routing.NewEngine(repo), nil, nil, nil, nil)
	server.handler = NewMOHandler(cfg, repo, contentStore, routing.NewEngine(repo), pushSender, nil, nil, nil)
	reqPDU := mmspdu.NewSendReqWithParts("txn-1", "+12025550100", []string{"+12025550101"}, []mmspdu.Part{
		{
			ContentType: "text/plain",
			Headers: map[string]string{
				"Content-ID": "<text1>",
			},
			Data: []byte("hello"),
		},
	})
	reqBody, err := mmspdu.Encode(reqPDU)
	if err != nil {
		t.Fatalf("encode req: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/mms", bytes.NewReader(reqBody))
	req.Header.Set("Content-Type", "application/vnd.wap.mms-message")
	rec := httptest.NewRecorder()

	server.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected status: %d body=%s", rec.Code, rec.Body.String())
	}

	respPDU, err := mmspdu.Decode(rec.Body.Bytes())
	if err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if respPDU.MessageType != mmspdu.MsgTypeSendConf {
		t.Fatalf("unexpected response type: %x", respPDU.MessageType)
	}

	messages, err := repo.ListMessages(context.Background(), db.MessageFilter{Limit: 10})
	if err != nil {
		t.Fatalf("list messages: %v", err)
	}
	if len(messages) != 1 {
		t.Fatalf("expected one stored message, got %d", len(messages))
	}
	if messages[0].From != "+12025550100" || messages[0].To[0] != "+12025550101" {
		t.Fatalf("unexpected stored message: %#v", messages[0])
	}
	if messages[0].ContentPath == "" {
		t.Fatal("expected stored content path")
	}
	if messages[0].MessageSize <= 0 {
		t.Fatalf("expected stored message size, got %d", messages[0].MessageSize)
	}
	deadline := time.Now().Add(500 * time.Millisecond)
	for pushSender.calls != 1 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if pushSender.calls != 1 || pushSender.msisdn != "+12025550101" {
		t.Fatalf("unexpected push sender state: %#v", pushSender)
	}
	unwrapped, err := wappush.UnwrapMMSPDU(pushSender.push)
	if err != nil {
		t.Fatalf("unwrap push: %v", err)
	}
	notification, err := mmspdu.Decode(unwrapped)
	if err != nil {
		t.Fatalf("decode notification: %v", err)
	}
	if notification.MessageType != mmspdu.MsgTypeNotificationInd {
		t.Fatalf("unexpected notification type: %x", notification.MessageType)
	}
	if notification.MMSVersion != "1.2" {
		t.Fatalf("unexpected notification version: %q", notification.MMSVersion)
	}
	if got, err := notification.Headers[mmspdu.FieldFrom].Text(); err != nil || got != "+12025550100/TYPE=PLMN" {
		t.Fatalf("unexpected notification from: %q err=%v", got, err)
	}
	if got, err := notification.Headers[mmspdu.FieldMessageSize].Integer(); err != nil || int64(got) != messages[0].MessageSize {
		t.Fatalf("unexpected notification message size: %d err=%v", got, err)
	}
	if got, err := notification.Headers[mmspdu.FieldMessageClass].Token(); err != nil || got != mmspdu.MessageClassPersonal {
		t.Fatalf("unexpected notification message class: %x err=%v", got, err)
	}
	if _, err := notification.Headers[mmspdu.FieldExpiry].Time(); err != nil {
		t.Fatalf("expected notification expiry: %v", err)
	}
	if _, ok := notification.Headers[mmspdu.FieldSubject]; ok {
		t.Fatal("expected notification to omit subject when message subject is empty")
	}

	storedAfterSubmit, err := repo.GetMessage(context.Background(), messages[0].ID)
	if err != nil {
		t.Fatalf("get message after submit: %v", err)
	}
	if storedAfterSubmit.Status != message.StatusDelivering {
		t.Fatalf("expected delivering status after push, got %v", storedAfterSubmit.Status)
	}

	retrieveReq := httptest.NewRequest(http.MethodGet, "/mms?id="+messages[0].ID, nil)
	retrieveRec := httptest.NewRecorder()
	server.ServeHTTP(retrieveRec, retrieveReq)
	if retrieveRec.Code != http.StatusOK {
		t.Fatalf("unexpected retrieve status: %d body=%s", retrieveRec.Code, retrieveRec.Body.String())
	}
	retrievePDU, err := mmspdu.Decode(retrieveRec.Body.Bytes())
	if err != nil {
		t.Fatalf("decode retrieve pdu: %v", err)
	}
	if retrievePDU.MessageType != mmspdu.MsgTypeRetrieveConf || retrievePDU.Body == nil || len(retrievePDU.Body.Parts) != 1 {
		t.Fatalf("unexpected retrieve response: %#v", retrievePDU)
	}

	notifyReqPDU := mmspdu.NewNotifyRespInd("txn-1", mmspdu.ResponseStatusOk)
	notifyBody, err := mmspdu.Encode(notifyReqPDU)
	if err != nil {
		t.Fatalf("encode notify resp: %v", err)
	}
	notifyReq := httptest.NewRequest(http.MethodPost, "/mms", bytes.NewReader(notifyBody))
	notifyReq.Header.Set("Content-Type", "application/vnd.wap.mms-message")
	notifyRec := httptest.NewRecorder()
	server.ServeHTTP(notifyRec, notifyReq)
	if notifyRec.Code != http.StatusNoContent {
		t.Fatalf("unexpected notify response status: %d body=%s", notifyRec.Code, notifyRec.Body.String())
	}

	updated, err := repo.GetMessage(context.Background(), messages[0].ID)
	if err != nil {
		t.Fatalf("get updated message: %v", err)
	}
	if updated.Status != message.StatusDelivered {
		t.Fatalf("expected delivered status, got %v", updated.Status)
	}

	deliveryReqPDU := mmspdu.NewDeliveryInd(messages[0].ID, "+12025550101", mmspdu.ResponseStatusOk)
	deliveryBody, err := mmspdu.Encode(deliveryReqPDU)
	if err != nil {
		t.Fatalf("encode delivery ind: %v", err)
	}
	deliveryReq := httptest.NewRequest(http.MethodPost, "/mms", bytes.NewReader(deliveryBody))
	deliveryReq.Header.Set("Content-Type", "application/vnd.wap.mms-message")
	deliveryRec := httptest.NewRecorder()
	server.ServeHTTP(deliveryRec, deliveryReq)
	if deliveryRec.Code != http.StatusNoContent {
		t.Fatalf("unexpected delivery response status: %d body=%s", deliveryRec.Code, deliveryRec.Body.String())
	}

	readReqPDU := mmspdu.NewReadRecInd("txn-1", messages[0].ID)
	readBody, err := mmspdu.Encode(readReqPDU)
	if err != nil {
		t.Fatalf("encode read rec ind: %v", err)
	}
	readReq := httptest.NewRequest(http.MethodPost, "/mms", bytes.NewReader(readBody))
	readReq.Header.Set("Content-Type", "application/vnd.wap.mms-message")
	readRec := httptest.NewRecorder()
	server.ServeHTTP(readRec, readReq)
	if readRec.Code != http.StatusNoContent {
		t.Fatalf("unexpected read response status: %d body=%s", readRec.Code, readRec.Body.String())
	}
}

func TestMOHandlerRejectsSendReqWithoutParts(t *testing.T) {
	t.Parallel()

	cfg := config.Default()
	cfg.Database.Driver = "sqlite"
	cfg.Database.DSN = t.TempDir() + "/mm1-empty-send.db"
	cfg.Store.Backend = "filesystem"
	cfg.Store.Filesystem.Root = t.TempDir()
	cfg.MM4.Hostname = "mmsc.example.net"

	repo, err := db.Open(context.Background(), db.OpenOptions{
		Driver: cfg.Database.Driver,
		DSN:    cfg.Database.DSN,
	})
	if err != nil {
		t.Fatalf("open repo: %v", err)
	}
	defer repo.Close()
	if err := db.RunMigrations(context.Background(), repo, os.DirFS("../..")); err != nil {
		t.Fatalf("run migrations: %v", err)
	}

	contentStore, err := store.New(context.Background(), cfg.Store)
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	defer contentStore.Close()

	server := NewServer(cfg, repo, contentStore, routing.NewEngine(repo), nil, nil, nil, nil)

	reqPDU := mmspdu.NewSendReq("txn-empty", "+12025550100", []string{"+12025550101"})
	reqBody, err := mmspdu.Encode(reqPDU)
	if err != nil {
		t.Fatalf("encode req: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/mms", bytes.NewReader(reqBody))
	req.Header.Set("Content-Type", "application/vnd.wap.mms-message")
	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d body=%s", rec.Code, rec.Body.String())
	}
	if !bytes.Contains(rec.Body.Bytes(), []byte("missing message content")) {
		t.Fatalf("expected missing message content error, got %s", rec.Body.String())
	}

	messages, err := repo.ListMessages(context.Background(), db.MessageFilter{Limit: 10})
	if err != nil {
		t.Fatalf("list messages: %v", err)
	}
	if len(messages) != 0 {
		t.Fatalf("expected no stored messages, got %d", len(messages))
	}
}

func TestMOHandlerUsesProxyMSISDNForInsertAddress(t *testing.T) {
	t.Parallel()

	cfg := config.Default()
	cfg.Database.Driver = "sqlite"
	cfg.Database.DSN = t.TempDir() + "/mm1-insert-address.db"
	cfg.Store.Backend = "filesystem"
	cfg.Store.Filesystem.Root = t.TempDir()
	cfg.MM4.Hostname = "mmsc.example.net"

	repo, err := db.Open(context.Background(), db.OpenOptions{
		Driver: cfg.Database.Driver,
		DSN:    cfg.Database.DSN,
	})
	if err != nil {
		t.Fatalf("open repo: %v", err)
	}
	defer repo.Close()
	if err := db.RunMigrations(context.Background(), repo, os.DirFS("../..")); err != nil {
		t.Fatalf("run migrations: %v", err)
	}

	contentStore, err := store.New(context.Background(), cfg.Store)
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	defer contentStore.Close()

	pushSender := &fakePushSender{}
	server := NewServer(cfg, repo, contentStore, routing.NewEngine(repo), nil, nil, nil, nil)
	server.handler = NewMOHandler(cfg, repo, contentStore, routing.NewEngine(repo), pushSender, nil, nil, nil)

	reqPDU := mmspdu.NewSendReqWithParts("txn-insert", "+12025550100", []string{"+12025550101"}, []mmspdu.Part{
		{ContentType: "text/plain", Data: []byte("hello")},
	})
	reqPDU.Headers[mmspdu.FieldFrom] = mmspdu.HeaderValue{
		Field: mmspdu.FieldFrom,
		Kind:  mmspdu.FieldKindAddress,
		Raw:   []byte{0x01, 0x81},
	}
	reqBody, err := mmspdu.Encode(reqPDU)
	if err != nil {
		t.Fatalf("encode req: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/3342012832", bytes.NewReader(reqBody))
	req.Header.Set("Content-Type", "application/vnd.wap.mms-message")
	req.Header.Set("X-WAP-Network-Client-MSISDN", "3342012832")
	rec := httptest.NewRecorder()

	server.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected status: %d body=%s", rec.Code, rec.Body.String())
	}

	messages, err := repo.ListMessages(context.Background(), db.MessageFilter{Limit: 10})
	if err != nil {
		t.Fatalf("list messages: %v", err)
	}
	if len(messages) != 1 {
		t.Fatalf("expected one stored message, got %d", len(messages))
	}
	if messages[0].From != "3342012832" {
		t.Fatalf("expected proxy msisdn as sender, got %#v", messages[0].From)
	}
}

func TestMOHandlerPushesEachLocalRecipient(t *testing.T) {
	t.Parallel()

	cfg := config.Default()
	cfg.Database.Driver = "sqlite"
	cfg.Database.DSN = t.TempDir() + "/mm1-multi.db"
	cfg.Store.Backend = "filesystem"
	cfg.Store.Filesystem.Root = t.TempDir()
	cfg.MM4.Hostname = "mmsc.example.net"

	repo, err := db.Open(context.Background(), db.OpenOptions{
		Driver: cfg.Database.Driver,
		DSN:    cfg.Database.DSN,
	})
	if err != nil {
		t.Fatalf("open repo: %v", err)
	}
	defer repo.Close()
	if err := db.RunMigrations(context.Background(), repo, os.DirFS("../..")); err != nil {
		t.Fatalf("run migrations: %v", err)
	}

	contentStore, err := store.New(context.Background(), cfg.Store)
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	defer contentStore.Close()

	pushSender := &fakePushSender{}
	handler := NewMOHandler(cfg, repo, contentStore, routing.NewEngine(repo), pushSender, nil, nil, nil)
	msg := &message.Message{
		ID:            "mid-multi",
		TransactionID: "txn-multi",
		Status:        message.StatusQueued,
		Direction:     message.DirectionMO,
		From:          "+12025550100",
		To:            []string{"+12025550101", "+12025550102"},
		Subject:       "hello",
		MessageSize:   128,
	}
	result := &routing.Result{Destination: routing.DestinationLocal}

	if err := handler.dispatchLocalNotification(context.Background(), msg, result); err != nil {
		t.Fatalf("dispatch local notification: %v", err)
	}

	if pushSender.calls != 2 {
		t.Fatalf("expected two push submissions, got %d", pushSender.calls)
	}
	if len(pushSender.recipients) != 2 || pushSender.recipients[0] != "+12025550101" || pushSender.recipients[1] != "+12025550102" {
		t.Fatalf("unexpected push recipients: %#v", pushSender.recipients)
	}
	if msg.Status != message.StatusDelivering {
		t.Fatalf("expected message status updated to delivering, got %v", msg.Status)
	}
}

func TestDispatchLocalNotificationEncodesAtInContentLocation(t *testing.T) {
	t.Parallel()

	cfg := config.Default()
	cfg.Database.Driver = "sqlite"
	cfg.Database.DSN = t.TempDir() + "/mm1-at-encode.db"
	cfg.Store.Backend = "filesystem"
	cfg.Store.Filesystem.Root = t.TempDir()
	cfg.MM4.Hostname = "mmsc.example.net"
	cfg.MM1.RetrieveBaseURL = "http://10.90.250.56:9090/mms/retrieve"

	repo, err := db.Open(context.Background(), db.OpenOptions{
		Driver: cfg.Database.Driver,
		DSN:    cfg.Database.DSN,
	})
	if err != nil {
		t.Fatalf("open repo: %v", err)
	}
	defer repo.Close()
	if err := db.RunMigrations(context.Background(), repo, os.DirFS("../..")); err != nil {
		t.Fatalf("run migrations: %v", err)
	}

	contentStore, err := store.New(context.Background(), cfg.Store)
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	defer contentStore.Close()

	pushSender := &fakePushSender{}
	handler := NewMOHandler(cfg, repo, contentStore, routing.NewEngine(repo), pushSender, nil, nil, nil)
	msg := &message.Message{
		ID:            "95863709-72b2-4c8a-af7a-b33676cdb9cc@mmsc.epc.mnc435.mcc311.3gppnetwork.org",
		TransactionID: "txn-at",
		Status:        message.StatusQueued,
		Direction:     message.DirectionMO,
		From:          "+12025550100",
		To:            []string{"+12025550101"},
		MessageSize:   128,
	}
	result := &routing.Result{Destination: routing.DestinationLocal}

	if err := handler.dispatchLocalNotification(context.Background(), msg, result); err != nil {
		t.Fatalf("dispatch local notification: %v", err)
	}

	raw, err := wappush.UnwrapMMSPDU(pushSender.push)
	if err != nil {
		t.Fatalf("unwrap push PDU: %v", err)
	}
	pdu, err := mmspdu.Decode(raw)
	if err != nil {
		t.Fatalf("decode push PDU: %v", err)
	}
	cl, err := pdu.Headers[mmspdu.FieldContentLocation].Text()
	if err != nil {
		t.Fatalf("decode Content-Location: %v", err)
	}
	if strings.Contains(cl, "@") {
		t.Fatalf("Content-Location contains unencoded '@': %q", cl)
	}
	if !strings.Contains(cl, "%40") {
		t.Fatalf("Content-Location missing '%%40' encoding: %q", cl)
	}
}

func TestMOHandlerAcceptsAndQueuesWhenLocalNotificationDispatchFails(t *testing.T) {
	t.Parallel()

	cfg := config.Default()
	cfg.Database.Driver = "sqlite"
	cfg.Database.DSN = t.TempDir() + "/mm1-dispatch-failure.db"
	cfg.Store.Backend = "filesystem"
	cfg.Store.Filesystem.Root = t.TempDir()
	cfg.MM4.Hostname = "mmsc.example.net"

	repo, err := db.Open(context.Background(), db.OpenOptions{
		Driver: cfg.Database.Driver,
		DSN:    cfg.Database.DSN,
	})
	if err != nil {
		t.Fatalf("open repo: %v", err)
	}
	defer repo.Close()
	if err := db.RunMigrations(context.Background(), repo, os.DirFS("../..")); err != nil {
		t.Fatalf("run migrations: %v", err)
	}

	contentStore, err := store.New(context.Background(), cfg.Store)
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	defer contentStore.Close()

	pushSender := &fakePushSender{err: errors.New("smpp unavailable")}
	server := NewServer(cfg, repo, contentStore, routing.NewEngine(repo), nil, nil, nil, nil)
	server.handler = NewMOHandler(cfg, repo, contentStore, routing.NewEngine(repo), pushSender, nil, nil, nil)

	reqPDU := mmspdu.NewSendReqWithParts("txn-dispatch-fail", "+12025550100", []string{"+12025550101"}, []mmspdu.Part{
		{ContentType: "text/plain", Data: []byte("hello")},
	})
	reqBody, err := mmspdu.Encode(reqPDU)
	if err != nil {
		t.Fatalf("encode req: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/mms", bytes.NewReader(reqBody))
	req.Header.Set("Content-Type", "application/vnd.wap.mms-message")
	rec := httptest.NewRecorder()

	server.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected status: %d body=%s", rec.Code, rec.Body.String())
	}

	messages, err := repo.ListMessages(context.Background(), db.MessageFilter{Limit: 10})
	if err != nil {
		t.Fatalf("list messages: %v", err)
	}
	if len(messages) != 1 {
		t.Fatalf("expected one stored message, got %d", len(messages))
	}
	if messages[0].Status != message.StatusQueued {
		t.Fatalf("expected queued status after deferred dispatch, got %v", messages[0].Status)
	}

	events, err := repo.ListMessageEvents(context.Background(), messages[0].ID, 10)
	if err != nil {
		t.Fatalf("list message events: %v", err)
	}
	deadline := time.Now().Add(500 * time.Millisecond)
	for {
		found := false
		for _, event := range events {
			if event.Type == "local-notification-deferred" {
				found = true
				break
			}
		}
		if found {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("expected deferred dispatch event, got %#v", events)
		}
		time.Sleep(10 * time.Millisecond)
		events, err = repo.ListMessageEvents(context.Background(), messages[0].ID, 10)
		if err != nil {
			t.Fatalf("list message events: %v", err)
		}
	}
}

func TestMOHandlerRespondsBeforeLocalNotificationDispatchCompletes(t *testing.T) {
	t.Parallel()

	cfg := config.Default()
	cfg.Database.Driver = "sqlite"
	cfg.Database.DSN = t.TempDir() + "/mm1-async-dispatch.db"
	cfg.Store.Backend = "filesystem"
	cfg.Store.Filesystem.Root = t.TempDir()
	cfg.MM4.Hostname = "mmsc.example.net"

	repo, err := db.Open(context.Background(), db.OpenOptions{
		Driver: cfg.Database.Driver,
		DSN:    cfg.Database.DSN,
	})
	if err != nil {
		t.Fatalf("open repo: %v", err)
	}
	defer repo.Close()
	if err := db.RunMigrations(context.Background(), repo, os.DirFS("../..")); err != nil {
		t.Fatalf("run migrations: %v", err)
	}

	contentStore, err := store.New(context.Background(), cfg.Store)
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	defer contentStore.Close()

	releasePush := make(chan struct{})
	pushSender := &fakePushSender{wait: releasePush}
	server := NewServer(cfg, repo, contentStore, routing.NewEngine(repo), nil, nil, nil, nil)
	server.handler = NewMOHandler(cfg, repo, contentStore, routing.NewEngine(repo), pushSender, nil, nil, nil)

	reqPDU := mmspdu.NewSendReqWithParts("txn-async-dispatch", "+12025550100", []string{"+12025550101"}, []mmspdu.Part{
		{ContentType: "text/plain", Data: []byte("hello")},
	})
	reqBody, err := mmspdu.Encode(reqPDU)
	if err != nil {
		t.Fatalf("encode req: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/mms", bytes.NewReader(reqBody))
	req.Header.Set("Content-Type", "application/vnd.wap.mms-message")
	rec := httptest.NewRecorder()

	done := make(chan struct{})
	go func() {
		server.ServeHTTP(rec, req)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("expected MM1 response before local notification dispatch completed")
	}

	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected status: %d body=%s", rec.Code, rec.Body.String())
	}

	close(releasePush)
}

type emptyAdapter struct{}

func (emptyAdapter) Adapt(_ context.Context, _ []message.Part, _ adapt.Constraints) ([]message.Part, error) {
	return nil, nil
}

func TestMOHandlerPreservesMessageSizeWhenAdaptationReturnsNoParts(t *testing.T) {
	t.Parallel()

	cfg := config.Default()
	cfg.Database.Driver = "sqlite"
	cfg.Database.DSN = t.TempDir() + "/mm1-empty-adapt.db"
	cfg.Store.Backend = "filesystem"
	cfg.Store.Filesystem.Root = t.TempDir()
	cfg.MM4.Hostname = "mmsc.example.net"
	cfg.Adapt.Enabled = true

	repo, err := db.Open(context.Background(), db.OpenOptions{
		Driver: cfg.Database.Driver,
		DSN:    cfg.Database.DSN,
	})
	if err != nil {
		t.Fatalf("open repo: %v", err)
	}
	defer repo.Close()
	if err := db.RunMigrations(context.Background(), repo, os.DirFS("../..")); err != nil {
		t.Fatalf("run migrations: %v", err)
	}

	contentStore, err := store.New(context.Background(), cfg.Store)
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	defer contentStore.Close()

	pushSender := &fakePushSender{}
	pipeline := adapt.NewPipeline(cfg.Adapt, repo)
	pipeline.SetAdapter(emptyAdapter{})
	server := NewServer(cfg, repo, contentStore, routing.NewEngine(repo), nil, nil, nil, nil)
	server.handler = NewMOHandler(cfg, repo, contentStore, routing.NewEngine(repo), pushSender, nil, nil, pipeline)

	reqPDU := mmspdu.NewSendReqWithParts("txn-empty-adapt", "+12025550100", []string{"+12025550101"}, []mmspdu.Part{
		{ContentType: "text/plain", Data: []byte("hello")},
	})
	reqBody, err := mmspdu.Encode(reqPDU)
	if err != nil {
		t.Fatalf("encode req: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/mms", bytes.NewReader(reqBody))
	req.Header.Set("Content-Type", "application/vnd.wap.mms-message")
	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected status: %d body=%s", rec.Code, rec.Body.String())
	}

	messages, err := repo.ListMessages(context.Background(), db.MessageFilter{Limit: 10})
	if err != nil {
		t.Fatalf("list messages: %v", err)
	}
	if len(messages) != 1 {
		t.Fatalf("expected one stored message, got %d", len(messages))
	}
	if messages[0].MessageSize != int64(len(reqBody)) {
		t.Fatalf("expected stored message size %d, got %d", len(reqBody), messages[0].MessageSize)
	}
}

func TestMOHandlerRelaysMM4Route(t *testing.T) {
	t.Parallel()

	cfg := config.Default()
	cfg.Database.Driver = "sqlite"
	cfg.Database.DSN = t.TempDir() + "/mm1-mm4.db"
	cfg.Store.Backend = "filesystem"
	cfg.Store.Filesystem.Root = t.TempDir()
	cfg.MM4.Hostname = "mmsc.example.net"

	repo, err := db.Open(context.Background(), db.OpenOptions{
		Driver: cfg.Database.Driver,
		DSN:    cfg.Database.DSN,
	})
	if err != nil {
		t.Fatalf("open repo: %v", err)
	}
	defer repo.Close()
	if err := db.RunMigrations(context.Background(), repo, os.DirFS("../..")); err != nil {
		t.Fatalf("run migrations: %v", err)
	}
	if err := repo.UpsertMM4Peer(context.Background(), db.MM4Peer{
		Domain:     "peer.example.net",
		SMTPHost:   "smtp.peer.example.net",
		SMTPPort:   25,
		TLSEnabled: true,
		Active:     true,
	}); err != nil {
		t.Fatalf("upsert mm4 peer: %v", err)
	}

	contentStore, err := store.New(context.Background(), cfg.Store)
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	defer contentStore.Close()

	mm4Sender := &fakeMM4Sender{}
	server := NewServer(cfg, repo, contentStore, routing.NewEngine(repo), nil, nil, nil, nil)
	server.handler = NewMOHandler(cfg, repo, contentStore, routing.NewEngine(repo), nil, mm4Sender, nil, nil)

	reqPDU := mmspdu.NewSendReqWithParts("txn-mm4", "+12025550100", []string{"user@peer.example.net"}, []mmspdu.Part{
		{ContentType: "text/plain", Data: []byte("relay me")},
	})
	reqBody, err := mmspdu.Encode(reqPDU)
	if err != nil {
		t.Fatalf("encode req: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/mms", bytes.NewReader(reqBody))
	req.Header.Set("Content-Type", "application/vnd.wap.mms-message")
	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, req)
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

func TestForwardReqReturnsForwardConf(t *testing.T) {
	t.Parallel()

	cfg := config.Default()
	cfg.Database.Driver = "sqlite"
	cfg.Database.DSN = t.TempDir() + "/mm1-forward.db"
	cfg.Store.Backend = "filesystem"
	cfg.Store.Filesystem.Root = t.TempDir()
	cfg.MM4.Hostname = "mmsc.example.net"

	repo, err := db.Open(context.Background(), db.OpenOptions{
		Driver: cfg.Database.Driver,
		DSN:    cfg.Database.DSN,
	})
	if err != nil {
		t.Fatalf("open repo: %v", err)
	}
	defer repo.Close()
	if err := db.RunMigrations(context.Background(), repo, os.DirFS("../..")); err != nil {
		t.Fatalf("run migrations: %v", err)
	}

	contentStore, err := store.New(context.Background(), cfg.Store)
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	defer contentStore.Close()

	pushSender := &fakePushSender{}
	server := NewServer(cfg, repo, contentStore, routing.NewEngine(repo), nil, nil, nil, nil)
	server.handler = NewMOHandler(cfg, repo, contentStore, routing.NewEngine(repo), pushSender, nil, nil, nil)

	reqPDU := mmspdu.NewForwardReq("txn-forward", []string{"+12025550101"}, []mmspdu.Part{
		{ContentType: "text/plain", Data: []byte("forwarded content")},
	})
	reqPDU.Headers[mmspdu.FieldFrom] = mmspdu.NewAddressValue(mmspdu.FieldFrom, "+12025550100")
	reqBody, err := mmspdu.Encode(reqPDU)
	if err != nil {
		t.Fatalf("encode forward req: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/mms", bytes.NewReader(reqBody))
	req.Header.Set("Content-Type", "application/vnd.wap.mms-message")
	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected status: %d body=%s", rec.Code, rec.Body.String())
	}

	respPDU, err := mmspdu.Decode(rec.Body.Bytes())
	if err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if respPDU.MessageType != mmspdu.MsgTypeForwardConf {
		t.Fatalf("unexpected response type: %x", respPDU.MessageType)
	}
}

func TestMOHandlerRoutesMM3Email(t *testing.T) {
	t.Parallel()

	cfg := config.Default()
	cfg.Database.Driver = "sqlite"
	cfg.Database.DSN = t.TempDir() + "/mm1-mm3.db"
	cfg.Store.Backend = "filesystem"
	cfg.Store.Filesystem.Root = t.TempDir()
	cfg.MM4.Hostname = "mmsc.example.net"

	repo, err := db.Open(context.Background(), db.OpenOptions{
		Driver: cfg.Database.Driver,
		DSN:    cfg.Database.DSN,
	})
	if err != nil {
		t.Fatalf("open repo: %v", err)
	}
	defer repo.Close()
	if err := db.RunMigrations(context.Background(), repo, os.DirFS("../..")); err != nil {
		t.Fatalf("run migrations: %v", err)
	}

	contentStore, err := store.New(context.Background(), cfg.Store)
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	defer contentStore.Close()

	mm3Sender := &fakeMM3Sender{}
	server := NewServer(cfg, repo, contentStore, routing.NewEngine(repo), nil, nil, nil, nil)
	server.handler = NewMOHandler(cfg, repo, contentStore, routing.NewEngine(repo), nil, nil, mm3Sender, nil)

	reqPDU := mmspdu.NewSendReqWithParts("txn-mm3", "+12025550100", []string{"user@example.org"}, []mmspdu.Part{
		{ContentType: "text/plain", Data: []byte("send email")},
	})
	reqBody, err := mmspdu.Encode(reqPDU)
	if err != nil {
		t.Fatalf("encode req: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/mms", bytes.NewReader(reqBody))
	req.Header.Set("Content-Type", "application/vnd.wap.mms-message")
	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected status: %d body=%s", rec.Code, rec.Body.String())
	}
	if mm3Sender.calls != 1 || mm3Sender.msg == nil {
		t.Fatalf("expected mm3 dispatch, got %#v", mm3Sender)
	}
	if mm3Sender.msg.To[0] != "user@example.org" || mm3Sender.msg.Status != message.StatusForwarded {
		t.Fatalf("unexpected email message: %#v", mm3Sender.msg)
	}
}

func TestMOHandlerAppliesAdaptationClassToRetrievedContent(t *testing.T) {
	t.Parallel()

	cfg := config.Default()
	cfg.Database.Driver = "sqlite"
	cfg.Database.DSN = t.TempDir() + "/mm1-adapt.db"
	cfg.Store.Backend = "filesystem"
	cfg.Store.Filesystem.Root = t.TempDir()
	cfg.MM4.Hostname = "mmsc.example.net"
	cfg.Adapt.Enabled = true

	repo, err := db.Open(context.Background(), db.OpenOptions{
		Driver: cfg.Database.Driver,
		DSN:    cfg.Database.DSN,
	})
	if err != nil {
		t.Fatalf("open repo: %v", err)
	}
	defer repo.Close()
	if err := db.RunMigrations(context.Background(), repo, os.DirFS("../..")); err != nil {
		t.Fatalf("run migrations: %v", err)
	}
	if err := repo.UpsertAdaptationClass(context.Background(), db.AdaptationClass{
		Name:              "default",
		MaxMsgSizeBytes:   307200,
		MaxImageWidth:     320,
		MaxImageHeight:    240,
		AllowedImageTypes: []string{"image/png"},
		AllowedAudioTypes: []string{"audio/amr", "audio/mpeg", "audio/mp4"},
		AllowedVideoTypes: []string{"video/3gpp", "video/mp4"},
	}); err != nil {
		t.Fatalf("upsert adaptation class: %v", err)
	}
	contentStore, err := store.New(context.Background(), cfg.Store)
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	defer contentStore.Close()

	pushSender := &fakePushSender{}
	server := NewServer(cfg, repo, contentStore, routing.NewEngine(repo), nil, nil, nil, nil)
	server.handler = NewMOHandler(cfg, repo, contentStore, routing.NewEngine(repo), pushSender, nil, nil, adapt.NewPipeline(cfg.Adapt, repo))

	var payload bytes.Buffer
	img := image.NewRGBA(image.Rect(0, 0, 1200, 900))
	for y := 0; y < 900; y++ {
		for x := 0; x < 1200; x++ {
			img.Set(x, y, color.RGBA{R: uint8(x % 255), G: uint8(y % 255), B: 100, A: 255})
		}
	}
	if err := png.Encode(&payload, img); err != nil {
		t.Fatalf("encode source image: %v", err)
	}

	reqPDU := mmspdu.NewSendReqWithParts("txn-adapt", "+12025550100", []string{"+12025550101"}, []mmspdu.Part{
		{
			ContentType: "image/png",
			Headers: map[string]string{
				"Content-ID": "<img1>",
			},
			Data: payload.Bytes(),
		},
	})
	reqBody, err := mmspdu.Encode(reqPDU)
	if err != nil {
		t.Fatalf("encode req: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/mms", bytes.NewReader(reqBody))
	req.Header.Set("Content-Type", "application/vnd.wap.mms-message")
	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected submit status: %d body=%s", rec.Code, rec.Body.String())
	}

	messages, err := repo.ListMessages(context.Background(), db.MessageFilter{Limit: 10})
	if err != nil {
		t.Fatalf("list messages: %v", err)
	}
	if len(messages) != 1 {
		t.Fatalf("expected one stored message, got %d", len(messages))
	}

	retrieveReq := httptest.NewRequest(http.MethodGet, "/mms?id="+messages[0].ID, nil)
	retrieveRec := httptest.NewRecorder()
	server.ServeHTTP(retrieveRec, retrieveReq)
	if retrieveRec.Code != http.StatusOK {
		t.Fatalf("unexpected retrieve status: %d body=%s", retrieveRec.Code, retrieveRec.Body.String())
	}

	retrievePDU, err := mmspdu.Decode(retrieveRec.Body.Bytes())
	if err != nil {
		t.Fatalf("decode retrieve pdu: %v", err)
	}
	if retrievePDU.Body == nil || len(retrievePDU.Body.Parts) != 1 {
		t.Fatalf("unexpected retrieve pdu: %#v", retrievePDU)
	}
	decoded, _, err := image.Decode(bytes.NewReader(retrievePDU.Body.Parts[0].Data))
	if err != nil {
		t.Fatalf("decode adapted image: %v", err)
	}
	if decoded.Bounds().Dx() > 320 || decoded.Bounds().Dy() > 240 {
		t.Fatalf("expected retrieved image within 320x240, got %dx%d", decoded.Bounds().Dx(), decoded.Bounds().Dy())
	}
}

func TestMTHandlerReportsBackToMM7Origin(t *testing.T) {
	t.Parallel()

	cfg := config.Default()
	cfg.Database.Driver = "sqlite"
	cfg.Database.DSN = t.TempDir() + "/mm1-mm7.db"
	cfg.Store.Backend = "filesystem"
	cfg.Store.Filesystem.Root = t.TempDir()
	cfg.MM4.Hostname = "mmsc.example.net"

	repo, err := db.Open(context.Background(), db.OpenOptions{
		Driver: cfg.Database.Driver,
		DSN:    cfg.Database.DSN,
	})
	if err != nil {
		t.Fatalf("open repo: %v", err)
	}
	defer repo.Close()
	if err := db.RunMigrations(context.Background(), repo, os.DirFS("../..")); err != nil {
		t.Fatalf("run migrations: %v", err)
	}

	contentStore, err := store.New(context.Background(), cfg.Store)
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	defer contentStore.Close()

	msg := &message.Message{
		ID:            "mid-mm7",
		TransactionID: "txn-mm7",
		Status:        message.StatusDelivering,
		Direction:     message.DirectionMT,
		Origin:        message.InterfaceMM7,
		OriginHost:    "vasp-001",
		From:          "12345",
		To:            []string{"+12025550101"},
		ContentType:   "application/vnd.wap.mms-message",
	}
	if err := repo.CreateMessage(context.Background(), msg); err != nil {
		t.Fatalf("create message: %v", err)
	}

	reporter := &fakeMTReporter{}
	server := NewServer(cfg, repo, contentStore, routing.NewEngine(repo), nil, nil, nil, reporter)

	notifyReqPDU := mmspdu.NewNotifyRespInd("txn-mm7", mmspdu.ResponseStatusOk)
	notifyBody, err := mmspdu.Encode(notifyReqPDU)
	if err != nil {
		t.Fatalf("encode notify resp: %v", err)
	}
	notifyReq := httptest.NewRequest(http.MethodPost, "/mms", bytes.NewReader(notifyBody))
	notifyReq.Header.Set("Content-Type", "application/vnd.wap.mms-message")
	notifyRec := httptest.NewRecorder()
	server.ServeHTTP(notifyRec, notifyReq)
	if notifyRec.Code != http.StatusNoContent {
		t.Fatalf("unexpected notify response status: %d body=%s", notifyRec.Code, notifyRec.Body.String())
	}
	if reporter.deliveryCalls != 1 || reporter.deliveryStatus != message.StatusDelivered {
		t.Fatalf("unexpected delivery reporter state: %#v", reporter)
	}

	readReqPDU := mmspdu.NewReadRecInd("txn-mm7", "mid-mm7")
	readBody, err := mmspdu.Encode(readReqPDU)
	if err != nil {
		t.Fatalf("encode read rec ind: %v", err)
	}
	readReq := httptest.NewRequest(http.MethodPost, "/mms", bytes.NewReader(readBody))
	readReq.Header.Set("Content-Type", "application/vnd.wap.mms-message")
	readRec := httptest.NewRecorder()
	server.ServeHTTP(readRec, readReq)
	if readRec.Code != http.StatusNoContent {
		t.Fatalf("unexpected read response status: %d body=%s", readRec.Code, readRec.Body.String())
	}
	if reporter.readCalls != 1 || reporter.readMsg == nil || reporter.readMsg.ID != "mid-mm7" {
		t.Fatalf("unexpected read reporter state: %#v", reporter)
	}
}

func TestAcknowledgeIndActsAsRetrievedStatus(t *testing.T) {
	t.Parallel()

	cfg := config.Default()
	cfg.Database.Driver = "sqlite"
	cfg.Database.DSN = t.TempDir() + "/mm1-ack.db"
	cfg.Store.Backend = "filesystem"
	cfg.Store.Filesystem.Root = t.TempDir()
	cfg.MM4.Hostname = "mmsc.example.net"

	repo, err := db.Open(context.Background(), db.OpenOptions{
		Driver: cfg.Database.Driver,
		DSN:    cfg.Database.DSN,
	})
	if err != nil {
		t.Fatalf("open repo: %v", err)
	}
	defer repo.Close()
	if err := db.RunMigrations(context.Background(), repo, os.DirFS("../..")); err != nil {
		t.Fatalf("run migrations: %v", err)
	}

	contentStore, err := store.New(context.Background(), cfg.Store)
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	defer contentStore.Close()

	msg := &message.Message{
		ID:            "mid-ack",
		TransactionID: "txn-ack",
		Status:        message.StatusDelivering,
		Direction:     message.DirectionMT,
		Origin:        message.InterfaceMM1,
		From:          "12345",
		To:            []string{"+12025550101"},
		ContentType:   "application/vnd.wap.mms-message",
	}
	if err := repo.CreateMessage(context.Background(), msg); err != nil {
		t.Fatalf("create message: %v", err)
	}

	reporter := &fakeMTReporter{}
	server := NewServer(cfg, repo, contentStore, routing.NewEngine(repo), nil, nil, nil, reporter)

	ackPDU := mmspdu.NewAcknowledgeInd("txn-ack")
	ackBody, err := mmspdu.Encode(ackPDU)
	if err != nil {
		t.Fatalf("encode acknowledge ind: %v", err)
	}
	ackReq := httptest.NewRequest(http.MethodPost, "/mms", bytes.NewReader(ackBody))
	ackReq.Header.Set("Content-Type", "application/vnd.wap.mms-message")
	ackRec := httptest.NewRecorder()
	server.ServeHTTP(ackRec, ackReq)
	if ackRec.Code != http.StatusNoContent {
		t.Fatalf("unexpected acknowledge response status: %d body=%s", ackRec.Code, ackRec.Body.String())
	}

	updated, err := repo.GetMessage(context.Background(), "mid-ack")
	if err != nil {
		t.Fatalf("get updated message: %v", err)
	}
	if updated.Status != message.StatusDelivered {
		t.Fatalf("expected delivered status after acknowledge, got %v", updated.Status)
	}
	if reporter.deliveryCalls != 1 || reporter.deliveryStatus != message.StatusDelivered {
		t.Fatalf("unexpected delivery reporter state: %#v", reporter)
	}
}

func TestRetrieveRejectsStoredMessageWithoutParts(t *testing.T) {
	t.Parallel()

	cfg := config.Default()
	cfg.Database.Driver = "sqlite"
	cfg.Database.DSN = t.TempDir() + "/mm1-empty-retrieve.db"
	cfg.Store.Backend = "filesystem"
	cfg.Store.Filesystem.Root = t.TempDir()
	cfg.MM4.Hostname = "mmsc.example.net"

	repo, err := db.Open(context.Background(), db.OpenOptions{
		Driver: cfg.Database.Driver,
		DSN:    cfg.Database.DSN,
	})
	if err != nil {
		t.Fatalf("open repo: %v", err)
	}
	defer repo.Close()
	if err := db.RunMigrations(context.Background(), repo, os.DirFS("../..")); err != nil {
		t.Fatalf("run migrations: %v", err)
	}

	contentStore, err := store.New(context.Background(), cfg.Store)
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	defer contentStore.Close()

	rawPDU, err := mmspdu.Encode(mmspdu.NewRetrieveConf("txn-empty", nil))
	if err != nil {
		t.Fatalf("encode retrieve conf: %v", err)
	}
	contentPath, err := contentStore.Put(context.Background(), "mid-empty", bytes.NewReader(rawPDU), int64(len(rawPDU)), "application/vnd.wap.mms-message")
	if err != nil {
		t.Fatalf("store retrieve conf: %v", err)
	}

	msg := &message.Message{
		ID:            "mid-empty",
		TransactionID: "txn-empty",
		Status:        message.StatusDelivering,
		Direction:     message.DirectionMT,
		Origin:        message.InterfaceMM4,
		From:          "12345",
		To:            []string{"+12025550101"},
		ContentType:   "application/vnd.wap.mms-message",
		ContentPath:   contentPath,
		StoreID:       contentPath,
	}
	if err := repo.CreateMessage(context.Background(), msg); err != nil {
		t.Fatalf("create message: %v", err)
	}

	server := NewServer(cfg, repo, contentStore, routing.NewEngine(repo), nil, nil, nil, nil)
	req := httptest.NewRequest(http.MethodGet, "/mms?id=mid-empty", nil)
	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500 for empty retrieve content, got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestMOHandlerRejectsLocalMessageWhenAdaptationFails(t *testing.T) {
	t.Parallel()

	cfg := config.Default()
	cfg.Database.Driver = "sqlite"
	cfg.Database.DSN = t.TempDir() + "/mm1-adapt.db"
	cfg.Store.Backend = "filesystem"
	cfg.Store.Filesystem.Root = t.TempDir()
	cfg.MM4.Hostname = "mmsc.example.net"
	cfg.Adapt.Enabled = true

	repo, err := db.Open(context.Background(), db.OpenOptions{
		Driver: cfg.Database.Driver,
		DSN:    cfg.Database.DSN,
	})
	if err != nil {
		t.Fatalf("open repo: %v", err)
	}
	defer repo.Close()
	if err := db.RunMigrations(context.Background(), repo, os.DirFS("../..")); err != nil {
		t.Fatalf("run migrations: %v", err)
	}
	if err := repo.UpsertAdaptationClass(context.Background(), db.AdaptationClass{
		Name:              "default",
		MaxMsgSizeBytes:   4,
		MaxImageWidth:     640,
		MaxImageHeight:    480,
		AllowedImageTypes: []string{"image/jpeg", "image/gif", "image/png"},
		AllowedAudioTypes: []string{"audio/amr", "audio/mpeg", "audio/mp4"},
		AllowedVideoTypes: []string{"video/3gpp", "video/mp4"},
	}); err != nil {
		t.Fatalf("upsert adaptation class: %v", err)
	}

	contentStore, err := store.New(context.Background(), cfg.Store)
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	defer contentStore.Close()

	server := NewServer(cfg, repo, contentStore, routing.NewEngine(repo), nil, nil, nil, nil)
	server.handler = NewMOHandler(cfg, repo, contentStore, routing.NewEngine(repo), nil, nil, nil, adapt.NewPipeline(cfg.Adapt, repo))

	reqPDU := mmspdu.NewSendReqWithParts("txn-adapt", "+12025550100", []string{"+12025550101"}, []mmspdu.Part{
		{ContentType: "text/plain", Data: []byte("hello")},
	})
	reqBody, err := mmspdu.Encode(reqPDU)
	if err != nil {
		t.Fatalf("encode req: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/mms", bytes.NewReader(reqBody))
	req.Header.Set("Content-Type", "application/vnd.wap.mms-message")
	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected adaptation rejection, got %d body=%s", rec.Code, rec.Body.String())
	}

	messages, err := repo.ListMessages(context.Background(), db.MessageFilter{Limit: 10})
	if err != nil {
		t.Fatalf("list messages: %v", err)
	}
	if len(messages) != 0 {
		t.Fatalf("expected no persisted message on adaptation failure, got %#v", messages)
	}
}
