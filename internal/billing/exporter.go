package billing

import (
	"context"
	"encoding/csv"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	"go.uber.org/zap"

	"github.com/vectorcore/vectorcore-mmsc/internal/config"
	"github.com/vectorcore/vectorcore-mmsc/internal/db"
	"github.com/vectorcore/vectorcore-mmsc/internal/message"
)

const (
	watermarkFile = ".watermark"
	exportLimit   = 10000 // max rows per export run
)

// Exporter periodically writes CGRateS-compatible CSV CDR files to the
// configured export directory. The watermark file tracks the last exported
// received_at timestamp so each run only exports new records.
type Exporter struct {
	cfg    config.BillingConfig
	nodeID string
	repo   db.Repository
	log    *zap.Logger
	seq    atomic.Uint64
}

func New(cfg config.BillingConfig, nodeID string, repo db.Repository) *Exporter {
	if nodeID == "" {
		nodeID = "mmsc"
	}
	return &Exporter{
		cfg:    cfg,
		nodeID: nodeID,
		repo:   repo,
		log:    zap.L().With(zap.String("component", "billing")),
	}
}

// Run starts the export loop and blocks until ctx is cancelled.
func (e *Exporter) Run(ctx context.Context) {
	if !e.cfg.Enabled {
		return
	}
	if err := os.MkdirAll(e.cfg.ExportDir, 0o755); err != nil {
		e.log.Error("billing: failed to create export dir", zap.String("dir", e.cfg.ExportDir), zap.Error(err))
		return
	}
	e.log.Info("billing CDR exporter started",
		zap.String("export_dir", e.cfg.ExportDir),
		zap.Duration("interval", e.cfg.Interval),
	)
	ticker := time.NewTicker(e.cfg.Interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			e.log.Info("billing CDR exporter stopped")
			return
		case <-ticker.C:
			e.export(ctx)
		}
	}
}

func (e *Exporter) export(ctx context.Context) {
	watermark := e.readWatermark()
	messages, err := e.repo.ListMessagesAfter(ctx, watermark, exportLimit)
	if err != nil {
		e.log.Warn("billing: list messages failed", zap.Error(err))
		return
	}
	if len(messages) == 0 {
		return
	}

	filename := e.nextFilename()
	path := filepath.Join(e.cfg.ExportDir, filename)
	if err := e.writeCSV(path, messages); err != nil {
		e.log.Warn("billing: write CSV failed", zap.String("file", filename), zap.Error(err))
		return
	}

	// Advance watermark to the received_at of the last exported record.
	last := messages[len(messages)-1]
	if err := e.writeWatermark(last.ReceivedAt); err != nil {
		e.log.Warn("billing: write watermark failed", zap.Error(err))
	}
	e.log.Info("billing: CDR file written",
		zap.String("file", filename),
		zap.Int("records", len(messages)),
		zap.Time("watermark", last.ReceivedAt),
	)
}

// writeCSV writes CGRateS cdrc-compatible CSV rows.
// Column order matches the CGRateS default CDR CSV format:
// cgrid, tor, accid, orighost, reqtype, tenant, category,
// account, destination, setuptime, answertime, usage, cost, direction
func (e *Exporter) writeCSV(path string, messages []message.Message) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()

	w := csv.NewWriter(f)
	// Header — CGRateS cdrc can be configured to skip it.
	if err := w.Write([]string{
		"cgrid", "tor", "accid", "orighost", "reqtype", "tenant", "category",
		"account", "destination", "setuptime", "answertime", "usage", "cost", "direction",
	}); err != nil {
		return err
	}

	tenant := e.cfg.Tenant
	if tenant == "" {
		tenant = "cgrates.org"
	}
	reqType := e.cfg.ReqType
	if reqType == "" {
		reqType = "*postpaid"
	}

	for _, msg := range messages {
		answerTime := ""
		if msg.DeliveryTime != nil && !msg.DeliveryTime.IsZero() {
			answerTime = msg.DeliveryTime.UTC().Format(time.RFC3339)
		}
		direction := "out"
		if msg.Direction == message.DirectionMT {
			direction = "in"
		}
		if err := w.Write([]string{
			msg.ID,                                       // cgrid
			"*mms",                                       // tor (type of record)
			msg.TransactionID,                            // accid
			e.nodeID,                                     // orighost
			reqType,                                      // reqtype
			tenant,                                       // tenant
			"mms",                                        // category
			msg.From,                                     // account (who to charge)
			strings.Join(msg.To, ";"),                    // destination
			msg.ReceivedAt.UTC().Format(time.RFC3339),    // setuptime
			answerTime,                                   // answertime
			fmt.Sprintf("%d", msg.MessageSize),           // usage (bytes)
			"",                                           // cost (CGRateS calculates this)
			direction,                                    // direction
		}); err != nil {
			return err
		}
	}
	w.Flush()
	return w.Error()
}

func (e *Exporter) nextFilename() string {
	seq := e.seq.Add(1)
	ts := time.Now().UTC().Format("20060102_1504")
	return fmt.Sprintf("MMSC_CDR_%s_%s_%04d.csv", e.nodeID, ts, seq)
}

func (e *Exporter) watermarkPath() string {
	return filepath.Join(e.cfg.ExportDir, watermarkFile)
}

func (e *Exporter) readWatermark() time.Time {
	data, err := os.ReadFile(e.watermarkPath())
	if err != nil {
		// No watermark yet — start from the beginning of time.
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(string(data)))
	if err != nil {
		return time.Time{}
	}
	return t
}

func (e *Exporter) writeWatermark(t time.Time) error {
	return os.WriteFile(e.watermarkPath(), []byte(t.UTC().Format(time.RFC3339Nano)), 0o644)
}
