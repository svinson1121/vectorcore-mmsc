package mm7

import (
	"bytes"
	"context"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/vectorcore/vectorcore-mmsc/internal/db"
	"github.com/vectorcore/vectorcore-mmsc/internal/message"
	"github.com/vectorcore/vectorcore-mmsc/internal/mmspdu"
	"github.com/vectorcore/vectorcore-mmsc/internal/routing"
)

func TestEAIFStoresAndDispatchesLocalMT(t *testing.T) {
	t.Parallel()

	cfg, repo, contentStore := newMM7TestEnv(t)
	cfg.MM7.EAIFPath = "/eaif"
	cfg.MM7.EAIFVersion = "3.0"
	if err := repo.UpsertMM7VASP(context.Background(), db.MM7VASP{
		VASPID:       "vasp-001",
		SharedSecret: "secret",
		Active:       true,
	}); err != nil {
		t.Fatalf("upsert vasp: %v", err)
	}

	push := &fakePushSender{}
	server := NewServer(cfg, repo, contentStore, routing.NewEngine(repo), push, nil, nil)

	reqPDU := mmspdu.NewSendReqWithParts("txn-eaif", "12345", []string{"+12025550101"}, []mmspdu.Part{
		{ContentType: "text/plain", Data: []byte("hello from eaif")},
	})
	reqBody, err := mmspdu.Encode(reqPDU)
	if err != nil {
		t.Fatalf("encode req: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, cfg.MM7.EAIFPath, bytes.NewReader(reqBody))
	req.Header.Set("Content-Type", "application/vnd.wap.mms-message")
	req.Header.Set("X-NOKIA-MMSC-From", "12345")
	req.Header.Set("X-NOKIA-MMSC-To", "+12025550101")
	req.Header.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte("vasp-001:secret")))
	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("unexpected status: %d body=%s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("X-NOKIA-MMSC-Version"); got != "3.0" {
		t.Fatalf("unexpected eaif version header: %q", got)
	}
	if rec.Header().Get("X-NOKIA-MMSC-Message-Id") == "" {
		t.Fatal("expected eaif message id header")
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
}

func TestEAIFRejectsUnauthorizedVASP(t *testing.T) {
	t.Parallel()

	cfg, repo, contentStore := newMM7TestEnv(t)
	cfg.MM7.EAIFPath = "/eaif"
	cfg.MM7.EAIFVersion = "3.0"
	if err := repo.UpsertMM7VASP(context.Background(), db.MM7VASP{
		VASPID:       "vasp-001",
		SharedSecret: "secret",
		Active:       true,
	}); err != nil {
		t.Fatalf("upsert vasp: %v", err)
	}

	server := NewServer(cfg, repo, contentStore, routing.NewEngine(repo), nil, nil, nil)
	reqPDU := mmspdu.NewSendReqWithParts("txn-eaif-auth", "12345", []string{"+12025550101"}, []mmspdu.Part{
		{ContentType: "text/plain", Data: []byte("unauthorized")},
	})
	reqBody, err := mmspdu.Encode(reqPDU)
	if err != nil {
		t.Fatalf("encode req: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, cfg.MM7.EAIFPath, bytes.NewReader(reqBody))
	req.Header.Set("Content-Type", "application/vnd.wap.mms-message")
	req.Header.Set("X-NOKIA-MMSC-From", "12345")
	req.Header.Set("X-NOKIA-MMSC-To", "+12025550101")
	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected unauthorized, got %d body=%s", rec.Code, rec.Body.String())
	}
}
