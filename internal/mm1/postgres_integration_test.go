package mm1

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/vectorcore/vectorcore-mmsc/internal/config"
	"github.com/vectorcore/vectorcore-mmsc/internal/db"
	"github.com/vectorcore/vectorcore-mmsc/internal/message"
	"github.com/vectorcore/vectorcore-mmsc/internal/mmspdu"
	"github.com/vectorcore/vectorcore-mmsc/internal/routing"
	"github.com/vectorcore/vectorcore-mmsc/internal/store"
	"github.com/vectorcore/vectorcore-mmsc/internal/testpg"
)

func TestMOHandlerStoresAndRespondsPostgres(t *testing.T) {
	repo := testpg.OpenRepository(t)

	cfg := config.Default()
	cfg.Store.Backend = "filesystem"
	cfg.Store.Filesystem.Root = t.TempDir()
	cfg.MM4.Hostname = "mmsc.example.net"

	contentStore, err := store.New(context.Background(), cfg.Store)
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	defer contentStore.Close()

	pushSender := &fakePushSender{}
	server := NewServer(cfg, repo, contentStore, routing.NewEngine(repo), nil, nil, nil, nil)
	server.handler = NewMOHandler(cfg, repo, contentStore, routing.NewEngine(repo), pushSender, nil, nil, nil)

	reqPDU := mmspdu.NewSendReqWithParts("txn-pg-mm1", "+12025550100", []string{"+12025550101"}, []mmspdu.Part{{
		ContentType: "text/plain",
		Data:        []byte("hello postgres mm1"),
	}})
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

	items, err := repo.ListMessages(context.Background(), db.MessageFilter{Limit: 10})
	if err != nil {
		t.Fatalf("list messages: %v", err)
	}
	if len(items) != 1 || items[0].Status != message.StatusDelivering {
		t.Fatalf("unexpected stored messages: %#v", items)
	}
	if pushSender.calls != 1 || pushSender.msisdn != "+12025550101" {
		t.Fatalf("unexpected push state: %#v", pushSender)
	}
}
