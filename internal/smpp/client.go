package smpp

import (
	"context"
	"fmt"
	"net"
	"sync"
	"time"

	"go.uber.org/zap"

	"github.com/vectorcore/vectorcore-mmsc/internal/db"
)

type Client struct {
	cfg       Config
	session   *Session
	mu        sync.Mutex
	runCancel context.CancelFunc
	runDone   chan struct{}
}

func NewClient(cfg Config) *Client {
	return &Client{
		cfg:     cfg,
		session: NewSession(cfg),
	}
}

func (c *Client) SetDialFunc(fn func(context.Context, string, string) (net.Conn, error)) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.session.dialFn = fn
}

func (c *Client) SetDeliverSMHandler(fn func(*PDU)) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.session.deliverSMHandler = fn
}

func (c *Client) SetDeliveryReceiptHandler(fn func(*DeliveryReceipt)) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.session.deliveryReceiptHandler = fn
}

func NewClientFromUpstream(upstream db.SMPPUpstream) *Client {
	return NewClient(Config{
		Host:               upstream.Host,
		Port:               upstream.Port,
		SystemID:           upstream.SystemID,
		Password:           upstream.Password,
		SystemType:         upstream.SystemType,
		BindMode:           upstream.BindMode,
		ReconnectWait:      durationSeconds(upstream.ReconnectWait),
		EnquireLink:        durationSeconds(upstream.EnquireLink),
		RegisteredDelivery: byte(upstream.RegisteredDelivery),
	})
}

func (c *Client) Connect(ctx context.Context) error {
	c.mu.Lock()
	session := c.session
	c.mu.Unlock()
	return session.Connect(ctx)
}

func (c *Client) Close() error {
	c.mu.Lock()
	if c.runCancel != nil {
		c.runCancel()
		c.runCancel = nil
	}
	done := c.runDone
	c.runDone = nil
	session := c.session
	c.mu.Unlock()
	if done != nil {
		<-done
	}
	return session.Close()
}

func (c *Client) EnquireLink(ctx context.Context) error {
	return c.session.WithConn(ctx, func(conn *Conn) error {
		req := &PDU{
			CommandID:      CmdEnquireLink,
			CommandStatus:  ESMEROK,
			SequenceNumber: conn.NextSeq(),
		}
		resp, err := c.session.Request(ctx, req, CmdEnquireLinkResp)
		if err != nil {
			return err
		}
		if resp.CommandStatus != ESMEROK {
			return fmt.Errorf("smpp enquire_link failed: cmd=0x%08x status=0x%08x", resp.CommandID, resp.CommandStatus)
		}
		return nil
	})
}

func durationSeconds(v int) (d time.Duration) {
	if v <= 0 {
		return 0
	}
	return time.Duration(v) * time.Second
}

func (c *Client) Start(ctx context.Context) {
	c.mu.Lock()
	if c.runCancel != nil {
		c.mu.Unlock()
		return
	}
	runCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	c.runCancel = cancel
	c.runDone = done
	c.mu.Unlock()

	go func() {
		defer close(done)
		c.runLoop(runCtx)
	}()
}

func (c *Client) runLoop(ctx context.Context) {
	log := zap.L().With(zap.String("interface", "smpp"), zap.String("host", c.cfg.Host), zap.Int("port", c.cfg.Port), zap.String("system_id", c.cfg.SystemID))
	interval := c.cfg.EnquireLink
	if interval <= 0 {
		interval = 30 * time.Second
	}
	reconnectWait := c.cfg.ReconnectWait
	if reconnectWait <= 0 {
		reconnectWait = 5 * time.Second
	}

	timer := time.NewTimer(0)
	defer timer.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}

		if err := c.Connect(ctx); err != nil {
			log.Debug("smpp connect retry scheduled", zap.Error(err), zap.Duration("retry_in", reconnectWait))
			timer.Reset(reconnectWait)
			continue
		}

		pingCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		err := c.EnquireLink(pingCtx)
		cancel()
		if err != nil {
			log.Warn("smpp enquire_link failed", zap.Error(err), zap.Duration("retry_in", reconnectWait))
			_ = c.session.Close()
			timer.Reset(reconnectWait)
			continue
		}
		log.Debug("smpp enquire_link succeeded", zap.Duration("next_in", interval))
		timer.Reset(interval)
	}
}
