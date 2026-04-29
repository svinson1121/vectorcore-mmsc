package sweep

import (
	"context"
	"io"
	"os"
	"testing"
	"time"

	"github.com/vectorcore/vectorcore-mmsc/internal/config"
	"github.com/vectorcore/vectorcore-mmsc/internal/db"
	"github.com/vectorcore/vectorcore-mmsc/internal/message"
)

type fakeContentStore struct {
	deleted []string
}

func (f *fakeContentStore) Put(context.Context, string, io.Reader, int64, string) (string, error) {
	return "", nil
}

func (f *fakeContentStore) Get(context.Context, string) (io.ReadCloser, int64, error) {
	return nil, 0, os.ErrNotExist
}

func (f *fakeContentStore) Delete(_ context.Context, key string) error {
	f.deleted = append(f.deleted, key)
	return nil
}

func (f *fakeContentStore) Exists(context.Context, string) (bool, error) {
	return false, nil
}

func (f *fakeContentStore) Close() error {
	return nil
}

type recordingReporter struct {
	t       *testing.T
	repo    db.Repository
	calls   int
	status  message.Status
	message *message.Message
}

func (r *recordingReporter) SendDeliveryReport(ctx context.Context, msg *message.Message, status message.Status) error {
	r.calls++
	r.status = status
	if msg != nil {
		cloned := msg.Clone()
		r.message = &cloned
	}
	stored, err := r.repo.GetMessage(ctx, msg.ID)
	if err != nil {
		r.t.Fatalf("get message during report: %v", err)
	}
	if stored.Status != message.StatusExpired {
		r.t.Fatalf("expected message marked expired before report, got %v", stored.Status)
	}
	return nil
}

func TestSweeperMarksExpiredAndReportsBeforePurge(t *testing.T) {
	t.Parallel()

	repo := newSweepRepo(t)
	expiredAt := time.Now().UTC().Add(-time.Minute)
	msg := &message.Message{
		ID:             "mid-expired-report",
		TransactionID:  "txn-expired-report",
		Status:         message.StatusDelivering,
		Direction:      message.DirectionMO,
		From:           "+12025550100",
		To:             []string{"+12025550101"},
		ContentType:    "application/vnd.wap.mms-message",
		MMSVersion:     "1.3",
		DeliveryReport: true,
		Expiry:         &expiredAt,
		MessageSize:    12,
		ContentPath:    "content-key",
		StoreID:        "store-key",
		Origin:         message.InterfaceMM1,
		Parts:          []message.Part{{ContentType: "text/plain", Data: []byte("hello")}},
	}
	if err := repo.CreateMessage(context.Background(), msg); err != nil {
		t.Fatalf("create message: %v", err)
	}

	contentStore := &fakeContentStore{}
	reporter := &recordingReporter{t: t, repo: repo}
	New(config.LimitsConfig{}, repo, contentStore, reporter).sweep(context.Background())

	if reporter.calls != 1 || reporter.status != message.StatusExpired || reporter.message == nil || reporter.message.ID != msg.ID {
		t.Fatalf("unexpected reporter state: %#v", reporter)
	}
	if len(contentStore.deleted) != 2 || contentStore.deleted[0] != "content-key" || contentStore.deleted[1] != "store-key" {
		t.Fatalf("unexpected deleted store keys: %#v", contentStore.deleted)
	}
	if _, err := repo.GetMessage(context.Background(), msg.ID); err == nil {
		t.Fatal("expected expired message to be purged")
	}
}

func newSweepRepo(t *testing.T) db.Repository {
	t.Helper()
	repo, err := db.Open(context.Background(), db.OpenOptions{
		Driver: "sqlite",
		DSN:    t.TempDir() + "/sweep.db",
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
