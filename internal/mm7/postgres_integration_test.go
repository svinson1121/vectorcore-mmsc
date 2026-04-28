package mm7

import (
	"bytes"
	"context"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/vectorcore/vectorcore-mmsc/internal/config"
	"github.com/vectorcore/vectorcore-mmsc/internal/db"
	"github.com/vectorcore/vectorcore-mmsc/internal/message"
	"github.com/vectorcore/vectorcore-mmsc/internal/routing"
	"github.com/vectorcore/vectorcore-mmsc/internal/store"
	"github.com/vectorcore/vectorcore-mmsc/internal/testpg"
)

func TestSubmitReqStoresAndDispatchesLocalMTPostgres(t *testing.T) {
	repo := testpg.OpenRepository(t)
	addMM7TestRoutes(t, repo)

	cfg := config.Default()
	cfg.Store.Backend = "filesystem"
	cfg.Store.Filesystem.Root = t.TempDir()
	cfg.MM4.Hostname = "mmsc.example.net"
	cfg.MM7.Path = "/mm7"

	if err := repo.UpsertMM7VASP(context.Background(), db.MM7VASP{
		VASPID:       "vasp-001",
		VASID:        "service-001",
		SharedSecret: "secret",
		Active:       true,
	}); err != nil {
		t.Fatalf("upsert vasp: %v", err)
	}

	contentStore, err := store.New(context.Background(), cfg.Store)
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	defer contentStore.Close()

	push := &fakePushSender{}
	server := NewServer(cfg, repo, contentStore, routing.NewEngine(repo), push, nil, nil)

	req := newSubmitReq("txn-pg-mm7", "vasp-001", "service-001", "12345", "+12025550101", "Hello", "message-content", "text/plain", []byte("hello from postgres vasp"))
	httpReq := httptest.NewRequest(http.MethodPost, cfg.MM7.Path, bytes.NewReader(req.body))
	httpReq.Header.Set("Content-Type", req.contentType)
	httpReq.Header.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte("vasp-001:secret")))

	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, httpReq)
	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected status: %d body=%s", rec.Code, rec.Body.String())
	}

	items, err := repo.ListMessages(context.Background(), db.MessageFilter{Limit: 10})
	if err != nil {
		t.Fatalf("list messages: %v", err)
	}
	if len(items) != 1 || items[0].Origin != message.InterfaceMM7 || items[0].Status != message.StatusDelivering {
		t.Fatalf("unexpected stored messages: %#v", items)
	}
	if push.calls != 1 || push.msisdn != "+12025550101" {
		t.Fatalf("unexpected push state: %#v", push)
	}
}
