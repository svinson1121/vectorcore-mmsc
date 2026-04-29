package smpp

import (
	"context"
	"errors"
	"sort"
	"sync"

	"go.uber.org/zap"

	"github.com/vectorcore/vectorcore-mmsc/internal/config"
	"github.com/vectorcore/vectorcore-mmsc/internal/db"
)

type Manager struct {
	mu             sync.RWMutex
	clients        map[string]*Client
	order          []string
	repo           db.Repository
	runCtx         context.Context
	receiptHandler func(string, *DeliveryReceipt)
}

type Status struct {
	Name   string       `json:"name"`
	Host   string       `json:"host"`
	Port   int          `json:"port"`
	State  SessionState `json:"state"`
	System string       `json:"system_id"`
}

func NewManager(repo ...db.Repository) *Manager {
	var repository db.Repository
	if len(repo) > 0 {
		repository = repo[0]
	}
	return &Manager{
		clients: make(map[string]*Client),
		repo:    repository,
	}
}

func (m *Manager) Refresh(snapshot config.RuntimeSnapshot) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	log := zap.L().With(zap.String("interface", "smpp-manager"))

	desired := make(map[string]db.SMPPUpstream)
	for _, upstream := range snapshot.SMPPUpstreams {
		if upstream.Active {
			desired[upstream.Name] = upstream
		}
	}

	for name, client := range m.clients {
		upstream, ok := desired[name]
		if !ok || !sameRuntimeConfig(client.cfg, upstream) {
			log.Debug("smpp upstream removed", zap.String("upstream", name))
			_ = client.Close()
			delete(m.clients, name)
		}
	}

	names := make([]string, 0, len(desired))
	for name, upstream := range desired {
		if _, ok := m.clients[name]; !ok {
			upstreamName := name
			client := NewClientFromUpstream(upstream)
			log.Debug("smpp upstream activated", zap.String("upstream", name), zap.String("host", upstream.Host), zap.Int("port", upstream.Port), zap.String("bind_mode", upstream.BindMode))
			if m.receiptHandler != nil {
				client.SetDeliveryReceiptHandler(func(receipt *DeliveryReceipt) {
					m.receiptHandler(upstreamName, receipt)
				})
			}
			if m.runCtx != nil {
				client.Start(m.runCtx)
			}
			m.clients[name] = client
		}
		names = append(names, name)
	}
	sort.Strings(names)
	m.order = names
	log.Debug("smpp runtime refresh applied", zap.Int("active_upstreams", len(names)))
	return nil
}

func (m *Manager) Names() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return append([]string(nil), m.order...)
}

func (m *Manager) Default() (*Client, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if len(m.order) == 0 {
		return nil, errors.New("smpp: no active upstream configured")
	}
	return m.clients[m.order[0]], nil
}

func (m *Manager) Get(name string) (*Client, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	client, ok := m.clients[name]
	return client, ok
}

func (m *Manager) Statuses() []Status {
	m.mu.RLock()
	defer m.mu.RUnlock()

	out := make([]Status, 0, len(m.order))
	for _, name := range m.order {
		client := m.clients[name]
		if client == nil {
			continue
		}
		out = append(out, Status{
			Name:   name,
			Host:   client.cfg.Host,
			Port:   client.cfg.Port,
			State:  client.session.State(),
			System: client.cfg.SystemID,
		})
	}
	return out
}

func (m *Manager) StartAutoRefresh(ctx context.Context, snapshots <-chan config.RuntimeSnapshot) {
	m.Start(ctx)
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case snapshot, ok := <-snapshots:
				if !ok {
					return
				}
				_ = m.Refresh(snapshot)
			}
		}
	}()
}

func (m *Manager) SubmitWAPPush(ctx context.Context, sourceAddr string, msisdn string, pushPDU []byte) error {
	zap.L().Debug("smpp submit requested", zap.String("interface", "smpp-manager"), zap.String("source_addr", sourceAddr), zap.String("recipient", msisdn), zap.Int("payload_bytes", len(pushPDU)))
	client, err := m.Default()
	if err != nil {
		return err
	}
	return client.SubmitWAPPush(ctx, sourceAddr, msisdn, pushPDU)
}

func (m *Manager) SubmitWAPPushForMessage(ctx context.Context, internalMessageID string, sourceAddr string, msisdn string, pushPDU []byte) error {
	zap.L().Debug("smpp tracked submit requested", zap.String("interface", "smpp-manager"), zap.String("message_id", internalMessageID), zap.String("source_addr", sourceAddr), zap.String("recipient", msisdn), zap.Int("payload_bytes", len(pushPDU)))
	m.mu.RLock()
	if len(m.order) == 0 {
		m.mu.RUnlock()
		return errors.New("smpp: no active upstream configured")
	}
	name := m.order[0]
	client := m.clients[name]
	repo := m.repo
	m.mu.RUnlock()
	if client == nil {
		return errors.New("smpp: no active upstream configured")
	}
	return client.SubmitWAPPushTracked(ctx, sourceAddr, msisdn, pushPDU, func(segmentIndex int, segmentCount int, remoteMessageID string) error {
		if repo == nil || remoteMessageID == "" || internalMessageID == "" {
			return nil
		}
		return repo.CreateSMPPSubmission(ctx, db.SMPPSubmission{
			UpstreamName:      name,
			SMPPMessageID:     remoteMessageID,
			InternalMessageID: internalMessageID,
			Recipient:         msisdn,
			SegmentIndex:      segmentIndex,
			SegmentCount:      segmentCount,
			State:             db.SMPPSubmissionPending,
		})
	})
}

func (m *Manager) SetDeliveryReceiptHandler(handler func(string, *DeliveryReceipt)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.receiptHandler = handler
	for name, client := range m.clients {
		if client == nil {
			continue
		}
		upstreamName := name
		if handler == nil {
			client.SetDeliveryReceiptHandler(nil)
			continue
		}
		client.SetDeliveryReceiptHandler(func(receipt *DeliveryReceipt) {
			handler(upstreamName, receipt)
		})
	}
}

func (m *Manager) Start(ctx context.Context) {
	m.mu.Lock()
	m.runCtx = ctx
	clients := make([]*Client, 0, len(m.clients))
	for _, client := range m.clients {
		clients = append(clients, client)
	}
	m.mu.Unlock()
	for _, client := range clients {
		if client != nil {
			client.Start(ctx)
		}
	}
}

func (m *Manager) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for name, client := range m.clients {
		_ = client.Close()
		delete(m.clients, name)
	}
	m.order = nil
	return nil
}

func sameRuntimeConfig(cfg Config, upstream db.SMPPUpstream) bool {
	return cfg.Host == upstream.Host &&
		cfg.Port == upstream.Port &&
		cfg.SystemID == upstream.SystemID &&
		cfg.Password == upstream.Password &&
		cfg.SystemType == upstream.SystemType &&
		cfg.BindMode == upstream.BindMode &&
		cfg.RegisteredDelivery == byte(upstream.RegisteredDelivery) &&
		cfg.ReconnectWait == durationSeconds(upstream.ReconnectWait) &&
		cfg.EnquireLink == durationSeconds(upstream.EnquireLink)
}
