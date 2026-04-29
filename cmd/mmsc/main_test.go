package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"

	"github.com/vectorcore/vectorcore-mmsc/api/rest"
	"github.com/vectorcore/vectorcore-mmsc/internal/config"
	"github.com/vectorcore/vectorcore-mmsc/internal/db"
	"github.com/vectorcore/vectorcore-mmsc/internal/message"
	"github.com/vectorcore/vectorcore-mmsc/internal/smpp"
)

func TestAdminMuxReadyAndRuntime(t *testing.T) {
	t.Parallel()

	repo := newTestRepo(t)
	runtimeStore := config.NewRuntimeStore()
	runtimeStore.Replace(config.RuntimeSnapshot{
		Peers:         []db.MM4Peer{{Domain: "peer.example.net"}},
		MM3Relay:      &db.MM3Relay{Enabled: true, SMTPHost: "smtp.example.net"},
		VASPs:         []db.MM7VASP{{VASPID: "vasp-1"}},
		SMPPUpstreams: []db.SMPPUpstream{{Name: "primary"}},
	})
	smppManager := smpp.NewManager()
	if err := smppManager.Refresh(runtimeStore.Snapshot()); err != nil {
		t.Fatalf("refresh smpp manager: %v", err)
	}

	handler := rest.NewRouter(config.Default(), repo, runtimeStore, smppManager, nil, appVersion, time.Unix(0, 0))

	readyReq := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	readyResp := httptest.NewRecorder()
	handler.ServeHTTP(readyResp, readyReq)
	if readyResp.Code != http.StatusOK {
		t.Fatalf("unexpected readyz status: %d", readyResp.Code)
	}

	var readyBody map[string]any
	if err := json.Unmarshal(readyResp.Body.Bytes(), &readyBody); err != nil {
		t.Fatalf("decode readyz body: %v", err)
	}
	if readyBody["status"] != "ready" {
		t.Fatalf("unexpected readyz body: %#v", readyBody)
	}

	runtimeReq := httptest.NewRequest(http.MethodGet, "/api/v1/runtime", nil)
	runtimeResp := httptest.NewRecorder()
	handler.ServeHTTP(runtimeResp, runtimeReq)
	if runtimeResp.Code != http.StatusOK {
		t.Fatalf("unexpected runtime status: %d", runtimeResp.Code)
	}

	smppReq := httptest.NewRequest(http.MethodGet, "/api/v1/smpp/status", nil)
	smppResp := httptest.NewRecorder()
	handler.ServeHTTP(smppResp, smppReq)
	if smppResp.Code != http.StatusOK {
		t.Fatalf("unexpected smpp status code: %d", smppResp.Code)
	}

	systemReq := httptest.NewRequest(http.MethodGet, "/api/v1/system/status", nil)
	systemResp := httptest.NewRecorder()
	handler.ServeHTTP(systemResp, systemReq)
	if systemResp.Code != http.StatusOK {
		t.Fatalf("unexpected system status code: %d", systemResp.Code)
	}
}

func TestParseFlagsSupportsDebugAndVersion(t *testing.T) {
	t.Parallel()

	opts := parseFlags([]string{"-c", "config.yaml", "-d", "-v"})
	if opts.configPath != "config.yaml" {
		t.Fatalf("unexpected config path: %q", opts.configPath)
	}
	if !opts.debug {
		t.Fatal("expected debug flag to be enabled")
	}
	if !opts.version {
		t.Fatal("expected version flag to be enabled")
	}
}

func TestNewLoggerWritesFile(t *testing.T) {
	t.Parallel()

	logPath := filepath.Join(t.TempDir(), "mmsc.log")
	logger, cleanup, err := newLogger(config.LogConfig{
		Level:  "info",
		Format: "json",
		File:   logPath,
	}, false)
	if err != nil {
		t.Fatalf("new logger: %v", err)
	}
	logger.Info("test-entry")
	cleanup()

	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read log file: %v", err)
	}
	if len(data) == 0 {
		t.Fatal("expected log file to contain output")
	}
}

func TestConfigValidateRequiresLogFile(t *testing.T) {
	t.Parallel()

	cfg := config.Default()
	cfg.Database.DSN = filepath.Join(t.TempDir(), "cfg.db")
	cfg.Store.Filesystem.Root = t.TempDir()
	cfg.Log.File = ""
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected log.file validation failure")
	}
}

func TestWithHTTPLoggingEmitsCompletionFields(t *testing.T) {
	t.Parallel()

	core, logs := observer.New(zap.DebugLevel)
	original := zap.L()
	replaced := zap.New(core)
	zap.ReplaceGlobals(replaced)
	defer zap.ReplaceGlobals(original)

	handler := withHTTPLogging("mm1", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte("ok"))
	}))

	req := httptest.NewRequest(http.MethodPost, "/mms?id=123", nil)
	req.RemoteAddr = "10.0.0.1:12345"
	req.Header.Set("User-Agent", "unit-test")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("unexpected status: %d", rec.Code)
	}

	entries := logs.FilterMessage("http request completed").All()
	if len(entries) != 1 {
		t.Fatalf("expected one completion log, got %d", len(entries))
	}
	fields := entries[0].ContextMap()
	if fields["interface"] != "mm1" {
		t.Fatalf("unexpected interface field: %#v", fields)
	}
	if fields["status"] != int64(http.StatusCreated) {
		t.Fatalf("unexpected status field: %#v", fields)
	}
	if fields["bytes"] != int64(2) {
		t.Fatalf("unexpected bytes field: %#v", fields)
	}
}

func TestHandleSMPPDeliveryReceiptMarksMessageDelivered(t *testing.T) {
	t.Parallel()

	repo := newTestRepo(t)
	msg := &message.Message{
		ID:            "msg-delivered-1",
		TransactionID: "txn-delivered-1",
		Status:        message.StatusDelivering,
		Direction:     message.DirectionMO,
		Origin:        message.InterfaceMM1,
		From:          "+12025550100",
		To:            []string{"+12025550101"},
		ContentType:   "application/vnd.wap.mms-message",
	}
	if err := repo.CreateMessage(context.Background(), msg); err != nil {
		t.Fatalf("create message: %v", err)
	}
	if err := repo.CreateSMPPSubmission(context.Background(), db.SMPPSubmission{
		UpstreamName:      "primary",
		SMPPMessageID:     "smpp-1",
		InternalMessageID: msg.ID,
		Recipient:         "+12025550101",
		SegmentIndex:      0,
		SegmentCount:      2,
		State:             db.SMPPSubmissionPending,
	}); err != nil {
		t.Fatalf("create smpp submission: %v", err)
	}
	if err := repo.CreateSMPPSubmission(context.Background(), db.SMPPSubmission{
		UpstreamName:      "primary",
		SMPPMessageID:     "smpp-2",
		InternalMessageID: msg.ID,
		Recipient:         "+12025550101",
		SegmentIndex:      1,
		SegmentCount:      2,
		State:             db.SMPPSubmissionPending,
	}); err != nil {
		t.Fatalf("create second smpp submission: %v", err)
	}

	if err := handleSMPPDeliveryReceipt(context.Background(), repo, nil, "primary", &smpp.DeliveryReceipt{
		ID:   "smpp-1",
		Stat: "DELIVRD",
	}); err != nil {
		t.Fatalf("handle delivery receipt: %v", err)
	}

	got, err := repo.GetMessage(context.Background(), msg.ID)
	if err != nil {
		t.Fatalf("get message: %v", err)
	}
	if got.Status != message.StatusDelivering {
		t.Fatalf("expected delivering status until all segments complete, got %v", got.Status)
	}

	if err := handleSMPPDeliveryReceipt(context.Background(), repo, nil, "primary", &smpp.DeliveryReceipt{
		ID:   "smpp-2",
		Stat: "DELIVRD",
	}); err != nil {
		t.Fatalf("handle second delivery receipt: %v", err)
	}

	got, err = repo.GetMessage(context.Background(), msg.ID)
	if err != nil {
		t.Fatalf("get message after second receipt: %v", err)
	}
	if got.Status != message.StatusDelivered {
		t.Fatalf("expected delivered status, got %v", got.Status)
	}
}

func TestHandleSMPPDeliveryReceiptMarksMessageUnreachableWhenAnySegmentFails(t *testing.T) {
	t.Parallel()

	repo := newTestRepo(t)
	msg := &message.Message{
		ID:            "msg-failed-1",
		TransactionID: "txn-failed-1",
		Status:        message.StatusDelivering,
		Direction:     message.DirectionMO,
		Origin:        message.InterfaceMM1,
		From:          "+12025550100",
		To:            []string{"+12025550101"},
		ContentType:   "application/vnd.wap.mms-message",
	}
	if err := repo.CreateMessage(context.Background(), msg); err != nil {
		t.Fatalf("create message: %v", err)
	}
	if err := repo.CreateSMPPSubmission(context.Background(), db.SMPPSubmission{
		UpstreamName:      "primary",
		SMPPMessageID:     "smpp-fail-1",
		InternalMessageID: msg.ID,
		Recipient:         "+12025550101",
		SegmentIndex:      0,
		SegmentCount:      2,
		State:             db.SMPPSubmissionPending,
	}); err != nil {
		t.Fatalf("create submission: %v", err)
	}
	if err := repo.CreateSMPPSubmission(context.Background(), db.SMPPSubmission{
		UpstreamName:      "primary",
		SMPPMessageID:     "smpp-fail-2",
		InternalMessageID: msg.ID,
		Recipient:         "+12025550101",
		SegmentIndex:      1,
		SegmentCount:      2,
		State:             db.SMPPSubmissionPending,
	}); err != nil {
		t.Fatalf("create second submission: %v", err)
	}

	reporter := &recordingDeliveryReporter{}
	if err := handleSMPPDeliveryReceipt(context.Background(), repo, reporter, "primary", &smpp.DeliveryReceipt{
		ID:   "smpp-fail-2",
		Stat: "UNDELIV",
		Err:  "255",
	}); err != nil {
		t.Fatalf("handle failed delivery receipt: %v", err)
	}

	got, err := repo.GetMessage(context.Background(), msg.ID)
	if err != nil {
		t.Fatalf("get message: %v", err)
	}
	if got.Status != message.StatusUnreachable {
		t.Fatalf("expected unreachable status, got %v", got.Status)
	}
	if reporter.messageID != msg.ID || reporter.status != message.StatusUnreachable {
		t.Fatalf("expected failure report for message, got id=%q status=%v", reporter.messageID, reporter.status)
	}
}

type recordingDeliveryReporter struct {
	messageID string
	status    message.Status
}

func (r *recordingDeliveryReporter) SendDeliveryReport(_ context.Context, msg *message.Message, status message.Status) error {
	if msg != nil {
		r.messageID = msg.ID
	}
	r.status = status
	return nil
}

func newTestRepo(t *testing.T) db.Repository {
	t.Helper()

	path := t.TempDir() + "/main.db"
	repo, err := db.Open(context.Background(), db.OpenOptions{
		Driver: "sqlite",
		DSN:    path,
	})
	if err != nil {
		t.Fatalf("open repo: %v", err)
	}
	t.Cleanup(func() { _ = repo.Close() })
	if err := db.RunMigrations(context.Background(), repo, os.DirFS("../..")); err != nil {
		t.Fatalf("run migrations: %v", err)
	}
	return repo
}
