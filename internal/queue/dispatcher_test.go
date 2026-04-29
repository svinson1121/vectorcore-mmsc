package queue

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/vectorcore/vectorcore-mmsc/internal/config"
	"github.com/vectorcore/vectorcore-mmsc/internal/db"
	"github.com/vectorcore/vectorcore-mmsc/internal/message"
)

func TestDispatcherRetriesQueuedLocalNotification(t *testing.T) {
	t.Parallel()

	repo := newQueueTestRepo(t)
	msg := &message.Message{
		ID:             "msg-queued",
		TransactionID:  "txn-queued",
		Status:         message.StatusQueued,
		Direction:      message.DirectionMO,
		Origin:         message.InterfaceMM1,
		From:           "+12025550100",
		To:             []string{"+12025550101"},
		Subject:        "queued",
		ContentType:    "application/vnd.wap.mms-message",
		MMSVersion:     "1.3",
		DeliveryReport: true,
		MessageSize:    128,
		ContentPath:    "store/msg-queued",
		StoreID:        "store/msg-queued",
	}
	message.ApplyDefaultExpiry(msg, time.Hour, 24*time.Hour)
	if err := repo.CreateMessage(context.Background(), msg); err != nil {
		t.Fatalf("create message: %v", err)
	}

	pusher := &recordingPusher{}
	dispatcher := New(config.MM1Config{RetrieveBaseURL: "http://mmsc.example.net/retrieve"}, repo, pusher)
	dispatcher.dispatchQueued(context.Background())

	if pusher.messageID != msg.ID || pusher.recipient != "+12025550101" {
		t.Fatalf("unexpected pusher call: %#v", pusher)
	}
	got, err := repo.GetMessage(context.Background(), msg.ID)
	if err != nil {
		t.Fatalf("get message: %v", err)
	}
	if got.Status != message.StatusDelivering {
		t.Fatalf("expected delivering status, got %v", got.Status)
	}
	events, err := repo.ListMessageEvents(context.Background(), msg.ID, 10)
	if err != nil {
		t.Fatalf("list events: %v", err)
	}
	if !hasEvent(events, "queue", "local-notification-retry-submitted") {
		t.Fatalf("expected retry submitted event, got %#v", events)
	}
}

func TestDispatcherLeavesQueuedMessageOnSubmitFailure(t *testing.T) {
	t.Parallel()

	repo := newQueueTestRepo(t)
	msg := &message.Message{
		ID:            "msg-failed",
		TransactionID: "txn-failed",
		Status:        message.StatusQueued,
		Direction:     message.DirectionMO,
		Origin:        message.InterfaceMM1,
		From:          "+12025550100",
		To:            []string{"+12025550101"},
		ContentType:   "application/vnd.wap.mms-message",
		MessageSize:   128,
		ContentPath:   "store/msg-failed",
		StoreID:       "store/msg-failed",
	}
	message.ApplyDefaultExpiry(msg, time.Hour, 24*time.Hour)
	if err := repo.CreateMessage(context.Background(), msg); err != nil {
		t.Fatalf("create message: %v", err)
	}

	dispatcher := New(config.MM1Config{}, repo, &recordingPusher{err: errors.New("smsc down")})
	dispatcher.dispatchQueued(context.Background())

	got, err := repo.GetMessage(context.Background(), msg.ID)
	if err != nil {
		t.Fatalf("get message: %v", err)
	}
	if got.Status != message.StatusQueued {
		t.Fatalf("expected queued status, got %v", got.Status)
	}
	events, err := repo.ListMessageEvents(context.Background(), msg.ID, 10)
	if err != nil {
		t.Fatalf("list events: %v", err)
	}
	if !hasEvent(events, "queue", "local-notification-retry-failed") {
		t.Fatalf("expected retry failed event, got %#v", events)
	}
}

type recordingPusher struct {
	messageID string
	recipient string
	err       error
}

func (p *recordingPusher) SubmitWAPPush(_ context.Context, _ string, msisdn string, _ []byte) error {
	p.recipient = msisdn
	return p.err
}

func (p *recordingPusher) SubmitWAPPushForMessage(_ context.Context, internalMessageID string, _ string, msisdn string, _ []byte) error {
	p.messageID = internalMessageID
	p.recipient = msisdn
	return p.err
}

func hasEvent(events []db.MessageEvent, source string, eventType string) bool {
	for _, event := range events {
		if event.Source == source && event.Type == eventType {
			return true
		}
	}
	return false
}

func newQueueTestRepo(t *testing.T) db.Repository {
	t.Helper()

	repo, err := db.Open(context.Background(), db.OpenOptions{
		Driver: "sqlite",
		DSN:    t.TempDir() + "/queue.db",
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
