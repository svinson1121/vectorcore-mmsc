package config

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"time"

	"github.com/jackc/pgx/v5"
	"go.uber.org/zap"

	"github.com/vectorcore/vectorcore-mmsc/internal/db"
)

type Watcher struct {
	dbConfig DatabaseConfig
	logger   *zap.Logger
	repo     db.Repository
	runtime  *RuntimeStore
}

func NewWatcher(dbConfig DatabaseConfig, repo db.Repository, runtime *RuntimeStore, logger *zap.Logger) *Watcher {
	if logger == nil {
		logger = zap.NewNop()
	}
	return &Watcher{dbConfig: dbConfig, repo: repo, runtime: runtime, logger: logger}
}

func (w *Watcher) Run(ctx context.Context) error {
	if w.repo != nil && w.runtime != nil {
		if err := w.reload(ctx, "startup"); err != nil {
			return err
		}
	}
	if w.dbConfig.Driver != "postgres" {
		return w.poll(ctx)
	}

	for {
		if err := w.listenOnce(ctx); err != nil {
			if errors.Is(err, context.Canceled) {
				return err
			}
			w.logger.Warn("config watcher reconnecting", zap.Error(err))
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(5 * time.Second):
		}
	}
}

func (w *Watcher) poll(ctx context.Context) error {
	interval := w.dbConfig.RuntimeReloadInterval
	if interval <= 0 {
		interval = 5 * time.Second
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if err := w.reload(ctx, "poll"); err != nil {
				w.logger.Warn("reload runtime snapshot", zap.Error(err), zap.String("reason", "poll"))
			}
		}
	}
}

func (w *Watcher) listenOnce(ctx context.Context) error {
	conn, err := pgx.Connect(ctx, w.dbConfig.DSN)
	if err != nil {
		return fmt.Errorf("connect watcher: %w", err)
	}
	defer conn.Close(ctx)

	if _, err := conn.Exec(ctx, "listen config_changed"); err != nil {
		return fmt.Errorf("listen config_changed: %w", err)
	}

	for {
		notification, err := conn.WaitForNotification(ctx)
		if err != nil {
			return err
		}
		w.logger.Info("received config change notification", zap.String("payload", notification.Payload))
		if err := w.reload(ctx, notification.Payload); err != nil {
			w.logger.Warn("reload runtime snapshot", zap.Error(err), zap.String("payload", notification.Payload))
		}
	}
}

func (w *Watcher) reload(ctx context.Context, reason string) error {
	if w.repo == nil || w.runtime == nil {
		return nil
	}
	snapshot, err := LoadRuntimeSnapshot(ctx, w.repo)
	if err != nil {
		return fmt.Errorf("load runtime snapshot: %w", err)
	}
	current := w.runtime.Snapshot()
	if snapshotsEqual(current, snapshot) {
		return nil
	}
	w.runtime.Replace(snapshot)
	w.logger.Info(
		"runtime snapshot refreshed",
		zap.String("reason", reason),
		zap.Int("mm4_peers", len(snapshot.Peers)),
		zap.Bool("mm3_enabled", snapshot.MM3Relay != nil && snapshot.MM3Relay.Enabled),
		zap.Int("mm7_vasps", len(snapshot.VASPs)),
		zap.Int("smpp_upstreams", len(snapshot.SMPPUpstreams)),
	)
	return nil
}

func snapshotsEqual(a RuntimeSnapshot, b RuntimeSnapshot) bool {
	return reflect.DeepEqual(a.Peers, b.Peers) &&
		reflect.DeepEqual(a.MM3Relay, b.MM3Relay) &&
		reflect.DeepEqual(a.VASPs, b.VASPs) &&
		reflect.DeepEqual(a.SMPPUpstreams, b.SMPPUpstreams) &&
		reflect.DeepEqual(a.Adaptation, b.Adaptation)
}
