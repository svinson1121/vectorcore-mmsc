package sweep

import (
	"context"
	"io/fs"
	"time"

	"go.uber.org/zap"

	"github.com/vectorcore/vectorcore-mmsc/internal/config"
	"github.com/vectorcore/vectorcore-mmsc/internal/db"
	"github.com/vectorcore/vectorcore-mmsc/internal/message"
	"github.com/vectorcore/vectorcore-mmsc/internal/store"
)

const sweepInterval = time.Minute

// Sweeper periodically removes messages that have passed their expiry from
// both the store and the database. The expiry column is the single point of
// truth — it is always set at ingestion time.
type Sweeper struct {
	repo     db.Repository
	store    store.Store
	reporter DeliveryReporter
	log      *zap.Logger
}

type DeliveryReporter interface {
	SendDeliveryReport(context.Context, *message.Message, message.Status) error
}

func New(_ config.LimitsConfig, repo db.Repository, contentStore store.Store, reporters ...DeliveryReporter) *Sweeper {
	var reporter DeliveryReporter
	if len(reporters) > 0 {
		reporter = reporters[0]
	}
	return &Sweeper{
		repo:     repo,
		store:    contentStore,
		reporter: reporter,
		log:      zap.L().With(zap.String("component", "sweep")),
	}
}

// Run starts the sweep loop and blocks until ctx is cancelled.
func (s *Sweeper) Run(ctx context.Context) {
	s.log.Info("message expiry sweeper started",
		zap.Duration("interval", sweepInterval),
	)
	ticker := time.NewTicker(sweepInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			s.log.Info("message expiry sweeper stopped")
			return
		case <-ticker.C:
			s.sweep(ctx)
		}
	}
}

func (s *Sweeper) sweep(ctx context.Context) {
	now := time.Now().UTC()
	expired, err := s.repo.ListExpiredMessages(ctx, now)
	if err != nil {
		s.log.Warn("sweep: list expired messages failed", zap.Error(err))
		return
	}
	if len(expired) == 0 {
		return
	}
	s.log.Info("sweep: expiring messages", zap.Int("count", len(expired)))
	for _, msg := range expired {
		if err := s.repo.UpdateMessageStatus(ctx, msg.ID, message.StatusExpired); err != nil {
			s.log.Warn("sweep: mark expired failed", zap.String("message_id", msg.ID), zap.Error(err))
			continue
		}
		msg.Status = message.StatusExpired
		if s.reporter != nil && msg.DeliveryReport {
			if err := s.reporter.SendDeliveryReport(ctx, &msg, message.StatusExpired); err != nil {
				s.log.Warn("sweep: delivery report failed", zap.String("message_id", msg.ID), zap.Error(err))
				_ = s.repo.AppendMessageEvent(ctx, db.MessageEvent{
					MessageID: msg.ID,
					Source:    "sweep",
					Type:      "expiry-report-failed",
					Summary:   "Expiry delivery report failed",
					Detail:    err.Error(),
				})
			}
		}
		s.purge(ctx, msg.ID, msg.ContentPath, msg.StoreID)
	}
}

func (s *Sweeper) purge(ctx context.Context, id, contentPath, storeID string) {
	log := s.log.With(zap.String("message_id", id))

	// Delete store content — deduplicate if content_path == store_id.
	seen := map[string]struct{}{}
	for _, key := range []string{contentPath, storeID} {
		if key == "" {
			continue
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		if err := s.store.Delete(ctx, key); err != nil && !isNotExist(err) {
			log.Warn("sweep: store delete failed", zap.String("key", key), zap.Error(err))
		}
	}

	if err := s.repo.DeleteMessage(ctx, id); err != nil {
		log.Warn("sweep: db delete failed", zap.Error(err))
		return
	}
	log.Debug("sweep: message purged")
}

func isNotExist(err error) bool {
	return err != nil && (err == fs.ErrNotExist || err.Error() == "file does not exist")
}
