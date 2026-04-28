package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"

	"github.com/vectorcore/vectorcore-mmsc/api/rest"
	"github.com/vectorcore/vectorcore-mmsc/internal/adapt"
	"github.com/vectorcore/vectorcore-mmsc/internal/billing"
	"github.com/vectorcore/vectorcore-mmsc/internal/config"
	"github.com/vectorcore/vectorcore-mmsc/internal/db"
	"github.com/vectorcore/vectorcore-mmsc/internal/message"
	"github.com/vectorcore/vectorcore-mmsc/internal/mm1"
	"github.com/vectorcore/vectorcore-mmsc/internal/mm3"
	"github.com/vectorcore/vectorcore-mmsc/internal/mm4"
	"github.com/vectorcore/vectorcore-mmsc/internal/mm7"
	"github.com/vectorcore/vectorcore-mmsc/internal/routing"
	"github.com/vectorcore/vectorcore-mmsc/internal/smpp"
	"github.com/vectorcore/vectorcore-mmsc/internal/store"
	"github.com/vectorcore/vectorcore-mmsc/internal/sweep"
	"github.com/vectorcore/vectorcore-mmsc/migrations"
)

var appVersion = "dev"

func main() {
	startedAt := time.Now().UTC()
	opts := parseFlags(os.Args[1:])
	if opts.version {
		fmt.Fprintln(os.Stdout, appVersion)
		return
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	cfg, err := config.Load(opts.configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "load config %q: %v\n", opts.configPath, err)
		os.Exit(1)
	}

	logger, cleanup, err := newLogger(cfg.Log, opts.debug)
	if err != nil {
		fmt.Fprintf(os.Stderr, "create logger: %v\n", err)
		os.Exit(1)
	}
	defer cleanup()
	zap.ReplaceGlobals(logger)

	adaptValidation, err := adapt.ValidateEnvironment(cfg.Adapt)
	if err != nil {
		logger.Fatal("validate adaptation environment", zap.Error(err))
	}
	for _, warning := range adaptValidation.Warnings {
		logger.Warn("adaptation environment warning", zap.String("warning", warning))
	}

	repo, err := db.Open(ctx, db.OpenOptions{
		Driver:       cfg.Database.Driver,
		DSN:          cfg.Database.DSN,
		MaxOpenConns: cfg.Database.MaxOpenConns,
		MaxIdleConns: cfg.Database.MaxIdleConns,
	})
	if err != nil {
		logger.Fatal("open database", zap.Error(err))
	}
	defer repo.Close()

	if err := db.RunMigrations(ctx, repo, migrations.FS); err != nil {
		logger.Fatal("run migrations", zap.Error(err))
	}

	contentStore, err := store.New(ctx, cfg.Store)
	if err != nil {
		logger.Fatal("create content store", zap.Error(err))
	}
	defer contentStore.Close()

	runtimeStore := config.NewRuntimeStore()
	initialSnapshot, err := config.LoadRuntimeSnapshot(ctx, repo)
	if err != nil {
		logger.Fatal("load runtime snapshot", zap.Error(err))
	}
	runtimeStore.Replace(initialSnapshot)

	smppManager := smpp.NewManager(repo)
	if err := smppManager.Refresh(initialSnapshot); err != nil {
		logger.Fatal("refresh smpp manager", zap.Error(err))
	}
	smppManager.SetDeliveryReceiptHandler(func(upstream string, receipt *smpp.DeliveryReceipt) {
		if err := handleSMPPDeliveryReceipt(context.Background(), repo, upstream, receipt); err != nil {
			logger.Warn("failed to process smpp delivery receipt", zap.String("upstream", upstream), zap.Error(err))
		}
	})
	defer smppManager.Close()
	smppManager.StartAutoRefresh(ctx, runtimeStore.Subscribe(4))

	watcher := config.NewWatcher(cfg.Database, repo, runtimeStore, logger)
	go func() {
		if err := watcher.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
			logger.Error("config watcher exited", zap.Error(err))
		}
	}()

	adminServer := &http.Server{
		Addr:              cfg.API.Listen,
		Handler:           withHTTPLogging("admin", rest.NewRouter(cfg, repo, runtimeStore, smppManager, contentStore, appVersion, startedAt)),
		ReadHeaderTimeout: 5 * time.Second,
	}
	router := routing.NewEngine(repo)
	mm4Router := mm4.NewPeerRouter(repo)
	mm4Outbound := mm4.NewOutbound(mm4Router, cfg.MM4.Hostname)
	mm4Outbound.SetEnvelopeOptions(cfg.MM4.SMTPEnvelopeFrom, cfg.MM4.SMTPEnvelopeRecipientDomain, cfg.MM4.RequestForwardAcknowledgement)
	mm3Outbound := mm3.NewOutbound(runtimeStore, cfg.MM4.Hostname)
	mm7Notifier := mm7.NewNotifier(repo, mm7.WithProtocol(cfg.MM7.Version, cfg.MM7.Namespace))
	reporter := combinedReporter{repo: repo, mm1: smppManager, mm7: mm7Notifier, mm4: mm4Outbound}
	mm1Server := &http.Server{
		Addr:              cfg.MM1.Listen,
		Handler:           withHTTPLogging("mm1", mm1.NewServer(cfg, repo, contentStore, router, smppManager, mm4Outbound, mm3Outbound, reporter)),
		ReadHeaderTimeout: 5 * time.Second,
	}
	mm7Server := &http.Server{
		Addr:              cfg.MM7.Listen,
		Handler:           withHTTPLogging("mm7", mm7.NewServer(cfg, repo, contentStore, router, smppManager, mm4Outbound, mm3Outbound)),
		ReadHeaderTimeout: 5 * time.Second,
	}

	mm4Listener, err := net.Listen("tcp", cfg.MM4.InboundListen)
	if err != nil {
		logger.Fatal("listen mm4", zap.Error(err), zap.String("listen", cfg.MM4.InboundListen))
	}
	defer mm4Listener.Close()
	mm3Listener, err := net.Listen("tcp", cfg.MM3.InboundListen)
	if err != nil {
		logger.Fatal("listen mm3", zap.Error(err), zap.String("listen", cfg.MM3.InboundListen))
	}
	defer mm3Listener.Close()

	mm4Inbound := mm4.NewInboundServer(cfg, repo, contentStore, router, smppManager, cfg.MM4.Hostname)
	mm4Inbound.SetForwardResponder(mm4Outbound)
	mm3Inbound := mm3.NewInboundServer(cfg, repo, contentStore, router, smppManager, cfg.MM4.Hostname)

	var servers serverGroup
	servers.startHTTP(logger, "admin", adminServer)
	servers.startHTTP(logger, "mm1", mm1Server)
	servers.startHTTP(logger, "mm7", mm7Server)
	servers.startMM4(logger, mm4Listener, mm4Inbound)
	servers.startMM3(logger, mm3Listener, mm3Inbound)

	sweeper := sweep.New(cfg.Limits, repo, contentStore, reporter)
	go sweeper.Run(ctx)

	billingNodeID := cfg.Billing.NodeID
	if billingNodeID == "" {
		billingNodeID = cfg.MM4.Hostname
	}
	cdrExporter := billing.New(cfg.Billing, billingNodeID, repo)
	go cdrExporter.Run(ctx)

	<-ctx.Done()
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()

	servers.shutdown(logger, shutdownCtx, []net.Listener{mm3Listener, mm4Listener}, adminServer, mm1Server, mm7Server)
}

type cliOptions struct {
	configPath string
	debug      bool
	version    bool
}

func parseFlags(args []string) cliOptions {
	opts := cliOptions{configPath: "config.yaml"}
	fs := flag.NewFlagSet("mmsc", flag.ExitOnError)
	fs.StringVar(&opts.configPath, "c", "config.yaml", "path to config file")
	fs.StringVar(&opts.configPath, "config-file", "config.yaml", "path to config file")
	fs.BoolVar(&opts.debug, "d", false, "enable console logging at debug level")
	fs.BoolVar(&opts.version, "v", false, "display version and exit")
	fs.Parse(args)
	return opts
}

func newLogger(cfg config.LogConfig, debug bool) (*zap.Logger, func(), error) {
	level := parseLogLevel(cfg.Level)
	if debug {
		level = zap.DebugLevel
	}

	if err := os.MkdirAll(filepath.Dir(cfg.File), 0o755); err != nil {
		return nil, nil, fmt.Errorf("create log directory: %w", err)
	}
	file, err := os.OpenFile(cfg.File, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, nil, fmt.Errorf("open log file: %w", err)
	}

	encoderCfg := zap.NewProductionEncoderConfig()
	encoderCfg.TimeKey = "ts"
	encoderCfg.EncodeTime = zapcore.ISO8601TimeEncoder

	var encoder zapcore.Encoder
	switch cfg.Format {
	case "console":
		encoder = zapcore.NewConsoleEncoder(encoderCfg)
	default:
		encoder = zapcore.NewJSONEncoder(encoderCfg)
	}

	cores := []zapcore.Core{
		zapcore.NewCore(encoder, zapcore.AddSync(file), level),
	}
	if debug {
		consoleCfg := zap.NewDevelopmentEncoderConfig()
		consoleCfg.EncodeTime = zapcore.ISO8601TimeEncoder
		cores = append(cores, zapcore.NewCore(zapcore.NewConsoleEncoder(consoleCfg), zapcore.AddSync(os.Stdout), zap.DebugLevel))
	}

	logger := zap.New(zapcore.NewTee(cores...), zap.AddCaller())
	cleanup := func() {
		_ = logger.Sync()
		_ = file.Close()
	}
	return logger, cleanup, nil
}

func parseLogLevel(value string) zapcore.Level {
	var level zapcore.Level
	if err := level.Set(value); err != nil {
		return zap.InfoLevel
	}
	return level
}

type serverGroup struct {
	wg sync.WaitGroup
}

type combinedReporter struct {
	mm7 *mm7.Notifier
	mm4 *mm4.Outbound
}

func (r combinedReporter) SendDeliveryReport(ctx context.Context, msg *message.Message, status message.Status) error {
	if msg == nil {
		return nil
	}
	switch msg.Origin {
	case message.InterfaceMM7:
		if r.mm7 != nil {
			return r.mm7.SendDeliveryReport(ctx, msg, status)
		}
	case message.InterfaceMM4:
		if r.mm4 != nil {
			return r.mm4.SendDeliveryReport(ctx, msg, status)
		}
	}
	return nil
}

func (r combinedReporter) SendReadReply(ctx context.Context, msg *message.Message) error {
	if msg == nil {
		return nil
	}
	switch msg.Origin {
	case message.InterfaceMM7:
		if r.mm7 != nil {
			return r.mm7.SendReadReply(ctx, msg)
		}
	case message.InterfaceMM4:
		if r.mm4 != nil {
			return r.mm4.SendReadReply(ctx, msg)
		}
	}
	return nil
}

func (g *serverGroup) startHTTP(logger *zap.Logger, name string, server *http.Server) {
	g.wg.Add(1)
	go func() {
		defer g.wg.Done()
		logger.Info("starting listener", zap.String("name", name), zap.String("listen", server.Addr))
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("listener failed", zap.String("name", name), zap.Error(err))
		}
	}()
}

func (g *serverGroup) startMM4(logger *zap.Logger, listener net.Listener, inbound *mm4.InboundServer) {
	g.wg.Add(1)
	go func() {
		defer g.wg.Done()
		logger.Info("starting listener", zap.String("name", "mm4"), zap.String("listen", listener.Addr().String()))
		for {
			conn, err := listener.Accept()
			if err != nil {
				if ne, ok := err.(net.Error); ok && ne.Temporary() {
					logger.Warn("temporary mm4 accept error", zap.Error(err))
					continue
				}
				if isClosedListener(err) {
					return
				}
				logger.Error("mm4 listener failed", zap.Error(err))
				return
			}
			logger.Debug("accepted mm4 session", zap.String("remote", conn.RemoteAddr().String()), zap.String("local", conn.LocalAddr().String()))
			go func() {
				if err := inbound.Handle(conn); err != nil && !isClosedListener(err) {
					logger.Warn("mm4 session ended with error", zap.Error(err))
				}
			}()
		}
	}()
}

func (g *serverGroup) startMM3(logger *zap.Logger, listener net.Listener, inbound *mm3.InboundServer) {
	g.wg.Add(1)
	go func() {
		defer g.wg.Done()
		logger.Info("starting listener", zap.String("name", "mm3"), zap.String("listen", listener.Addr().String()))
		for {
			conn, err := listener.Accept()
			if err != nil {
				if ne, ok := err.(net.Error); ok && ne.Temporary() {
					logger.Warn("temporary mm3 accept error", zap.Error(err))
					continue
				}
				if isClosedListener(err) {
					return
				}
				logger.Error("mm3 listener failed", zap.Error(err))
				return
			}
			logger.Debug("accepted mm3 session", zap.String("remote", conn.RemoteAddr().String()), zap.String("local", conn.LocalAddr().String()))
			go func() {
				if err := inbound.Handle(conn); err != nil && !isClosedListener(err) {
					logger.Warn("mm3 session ended with error", zap.Error(err))
				}
			}()
		}
	}()
}

func (g *serverGroup) shutdown(logger *zap.Logger, ctx context.Context, listeners []net.Listener, adminServer, mm1Server, mm7Server *http.Server) {
	for _, listener := range listeners {
		if listener == nil {
			continue
		}
		if err := listener.Close(); err != nil && !isClosedListener(err) {
			logger.Error("shutdown listener", zap.String("listen", listener.Addr().String()), zap.Error(err))
		}
	}
	for _, server := range []*http.Server{adminServer, mm1Server, mm7Server} {
		if err := server.Shutdown(ctx); err != nil {
			logger.Error("shutdown listener", zap.String("listen", server.Addr), zap.Error(err))
		}
	}
	g.wg.Wait()
}

func isClosedListener(err error) bool {
	return errors.Is(err, net.ErrClosed)
}

func handleSMPPDeliveryReceipt(ctx context.Context, repo db.Repository, upstream string, receipt *smpp.DeliveryReceipt) error {
	if repo == nil || receipt == nil || receipt.ID == "" {
		return nil
	}
	submissionState, errorText := mapSMPPReceiptStatus(receipt)
	submission, err := repo.UpdateSMPPSubmissionReceipt(ctx, upstream, receipt.ID, submissionState, errorText)
	if err != nil {
		return err
	}
	if submission == nil {
		return nil
	}
	submissions, err := repo.ListSMPPSubmissions(ctx, submission.InternalMessageID)
	if err != nil {
		return err
	}
	messageStatus := aggregateMessageStatusFromSMPP(submissions)
	if messageStatus == nil {
		return nil
	}
	return repo.UpdateMessageStatus(ctx, submission.InternalMessageID, *messageStatus)
}

func mapSMPPReceiptStatus(receipt *smpp.DeliveryReceipt) (db.SMPPSubmissionState, string) {
	stat := strings.ToUpper(strings.TrimSpace(receipt.Stat))
	switch stat {
	case "DELIVRD":
		return db.SMPPSubmissionDelivered, ""
	case "ACCEPTD", "ENROUTE", "BUFFERD":
		return db.SMPPSubmissionPending, ""
	case "EXPIRED", "DELETED", "UNDELIV", "REJECTD", "UNKNOWN":
		errText := receipt.Stat
		if receipt.Err != "" {
			errText += " err:" + receipt.Err
		}
		return db.SMPPSubmissionFailed, strings.TrimSpace(errText)
	default:
		return db.SMPPSubmissionPending, ""
	}
}

func aggregateMessageStatusFromSMPP(submissions []db.SMPPSubmission) *message.Status {
	if len(submissions) == 0 {
		return nil
	}

	type submissionGroup struct {
		expected  int
		delivered map[int]struct{}
		failed    bool
	}

	groups := map[string]*submissionGroup{}
	for _, submission := range submissions {
		key := submission.UpstreamName + "\x00" + submission.Recipient
		group := groups[key]
		if group == nil {
			group = &submissionGroup{delivered: map[int]struct{}{}}
			groups[key] = group
		}
		if submission.SegmentCount > group.expected {
			group.expected = submission.SegmentCount
		}
		switch submission.State {
		case db.SMPPSubmissionFailed:
			group.failed = true
		case db.SMPPSubmissionDelivered:
			group.delivered[submission.SegmentIndex] = struct{}{}
		}
	}

	allDelivered := len(groups) > 0
	for _, group := range groups {
		if group.failed {
			status := message.StatusUnreachable
			return &status
		}
		if group.expected <= 0 || len(group.delivered) != group.expected {
			allDelivered = false
		}
	}
	if allDelivered {
		status := message.StatusDelivered
		return &status
	}
	return nil
}
