package smpp

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sync"
	"time"

	"go.uber.org/zap"
)

type SessionState int

const (
	StateDisconnected SessionState = iota
	StateConnecting
	StateBound
)

type Config struct {
	Host          string
	Port          int
	SystemID      string
	Password      string
	SystemType    string
	BindMode      string
	ReconnectWait time.Duration
	EnquireLink   time.Duration
}

type Session struct {
	cfg                    Config
	conn                   *Conn
	state                  SessionState
	dialFn                 func(context.Context, string, string) (net.Conn, error)
	pending                map[uint32]chan *PDU
	done                   chan struct{}
	deliverSMHandler       func(*PDU)
	deliveryReceiptHandler func(*DeliveryReceipt)
	mu                     sync.Mutex
}

func NewSession(cfg Config) *Session {
	return &Session{
		cfg:     cfg,
		state:   StateDisconnected,
		pending: make(map[uint32]chan *PDU),
		dialFn: func(ctx context.Context, network, address string) (net.Conn, error) {
			dialer := &net.Dialer{Timeout: 10 * time.Second}
			return dialer.DialContext(ctx, network, address)
		},
	}
}

func (s *Session) State() SessionState {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.state
}

func (s *Session) Connect(ctx context.Context) error {
	log := zap.L().With(zap.String("interface", "smpp"), zap.String("host", s.cfg.Host), zap.Int("port", s.cfg.Port), zap.String("system_id", s.cfg.SystemID), zap.String("bind_mode", s.cfg.BindMode))
	s.mu.Lock()
	if s.state == StateBound && s.conn != nil {
		s.mu.Unlock()
		log.Debug("smpp session already bound")
		return nil
	}
	s.state = StateConnecting
	s.mu.Unlock()

	addr := net.JoinHostPort(s.cfg.Host, fmt.Sprintf("%d", s.cfg.Port))
	log.Debug("smpp dial starting", zap.String("address", addr))
	netConn, err := s.dialFn(ctx, "tcp", addr)
	if err != nil {
		s.setState(StateDisconnected)
		log.Warn("smpp dial failed", zap.Error(err), zap.String("address", addr))
		return fmt.Errorf("smpp dial: %w", err)
	}

	conn := NewConn(netConn)
	if err := s.bind(ctx, conn); err != nil {
		netConn.Close()
		s.setState(StateDisconnected)
		log.Warn("smpp bind failed", zap.Error(err))
		return err
	}

	s.mu.Lock()
	s.conn = conn
	s.state = StateBound
	s.done = make(chan struct{})
	s.mu.Unlock()
	log.Debug("smpp session bound")
	go s.readLoop(conn)
	return nil
}

func (s *Session) Close() error {
	log := zap.L().With(zap.String("interface", "smpp"), zap.String("host", s.cfg.Host), zap.Int("port", s.cfg.Port), zap.String("system_id", s.cfg.SystemID))
	s.mu.Lock()
	conn := s.conn
	s.conn = nil
	done := s.done
	s.done = nil
	s.state = StateDisconnected
	pending := s.pending
	s.pending = make(map[uint32]chan *PDU)
	s.mu.Unlock()

	if done != nil {
		close(done)
	}
	for seq, ch := range pending {
		delete(pending, seq)
		close(ch)
	}
	if conn == nil {
		log.Debug("smpp session closed without active connection")
		return nil
	}
	log.Debug("smpp session closing")
	return conn.Close()
}

func (s *Session) WithConn(ctx context.Context, fn func(*Conn) error) error {
	if err := s.Connect(ctx); err != nil {
		return err
	}

	s.mu.Lock()
	conn := s.conn
	s.mu.Unlock()
	if conn == nil {
		return errors.New("smpp: no active connection")
	}
	return fn(conn)
}

func (s *Session) Request(ctx context.Context, pdu *PDU, expect uint32) (*PDU, error) {
	if err := s.Connect(ctx); err != nil {
		return nil, err
	}

	s.mu.Lock()
	conn := s.conn
	if conn == nil {
		s.mu.Unlock()
		return nil, errors.New("smpp: no active connection")
	}
	ch := make(chan *PDU, 1)
	s.pending[pdu.SequenceNumber] = ch
	done := s.done
	s.mu.Unlock()

	if err := conn.WritePDU(pdu); err != nil {
		s.unregisterPending(pdu.SequenceNumber)
		return nil, err
	}

	select {
	case resp, ok := <-ch:
		if !ok || resp == nil {
			return nil, errors.New("smpp: connection closed")
		}
		if expect != 0 && resp.CommandID != expect {
			return nil, fmt.Errorf("smpp response mismatch: got 0x%08x want 0x%08x", resp.CommandID, expect)
		}
		return resp, nil
	case <-done:
		s.unregisterPending(pdu.SequenceNumber)
		return nil, errors.New("smpp: session closed")
	case <-ctx.Done():
		s.unregisterPending(pdu.SequenceNumber)
		return nil, ctx.Err()
	}
}

func (s *Session) bind(ctx context.Context, conn *Conn) error {
	log := zap.L().With(zap.String("interface", "smpp"), zap.String("host", s.cfg.Host), zap.Int("port", s.cfg.Port), zap.String("system_id", s.cfg.SystemID), zap.String("bind_mode", s.cfg.BindMode))
	reqCmd, respCmd := bindCommands(s.cfg.BindMode)
	req := &PDU{
		CommandID:        reqCmd,
		CommandStatus:    ESMEROK,
		SequenceNumber:   conn.NextSeq(),
		SystemID:         s.cfg.SystemID,
		Password:         s.cfg.Password,
		SystemType:       s.cfg.SystemType,
		InterfaceVersion: 0x34,
	}
	if err := conn.WritePDU(req); err != nil {
		log.Warn("smpp bind write failed", zap.Error(err))
		return fmt.Errorf("smpp write bind: %w", err)
	}

	if deadlineConn, ok := conn.conn.(interface{ SetDeadline(time.Time) error }); ok {
		_ = deadlineConn.SetDeadline(time.Now().Add(10 * time.Second))
		defer deadlineConn.SetDeadline(time.Time{})
	}

	resp, err := conn.ReadPDU()
	if err != nil {
		log.Warn("smpp bind response read failed", zap.Error(err))
		return fmt.Errorf("smpp read bind response: %w", err)
	}
	if resp.CommandID != respCmd {
		log.Warn("smpp bind response mismatch", zap.Uint32("command_id", resp.CommandID), zap.Uint32("expected_command_id", respCmd))
		return fmt.Errorf("smpp bind response mismatch: got 0x%08x want 0x%08x", resp.CommandID, respCmd)
	}
	if resp.CommandStatus != ESMEROK {
		log.Warn("smpp bind rejected", zap.Uint32("command_status", resp.CommandStatus))
		return fmt.Errorf("smpp bind rejected: 0x%08x", resp.CommandStatus)
	}
	log.Debug("smpp bind accepted")
	return nil
}

func (s *Session) setState(state SessionState) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.state = state
}

func (s *Session) unregisterPending(seq uint32) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.pending, seq)
}

func (s *Session) readLoop(conn *Conn) {
	log := zap.L().With(zap.String("interface", "smpp"), zap.String("host", s.cfg.Host), zap.Int("port", s.cfg.Port), zap.String("system_id", s.cfg.SystemID))
	for {
		pdu, err := conn.ReadPDU()
		if err != nil {
			log.Warn("smpp read loop ended", zap.Error(err))
			_ = s.Close()
			return
		}
		if s.handleIncoming(conn, pdu) {
			continue
		}

		s.mu.Lock()
		ch := s.pending[pdu.SequenceNumber]
		if ch != nil {
			delete(s.pending, pdu.SequenceNumber)
		}
		s.mu.Unlock()
		if ch != nil {
			ch <- pdu
			close(ch)
		}
	}
}

func (s *Session) handleIncoming(conn *Conn, pdu *PDU) bool {
	log := zap.L().With(zap.String("interface", "smpp"), zap.String("host", s.cfg.Host), zap.Int("port", s.cfg.Port), zap.String("system_id", s.cfg.SystemID))
	switch pdu.CommandID {
	case CmdEnquireLink:
		log.Debug("smpp enquire_link received", zap.Uint32("sequence_number", pdu.SequenceNumber))
		go func() {
			_ = conn.WritePDU(&PDU{
				CommandID:      CmdEnquireLinkResp,
				CommandStatus:  ESMEROK,
				SequenceNumber: pdu.SequenceNumber,
			})
		}()
		return true
	case CmdDeliverSM:
		log.Debug("smpp deliver_sm received", zap.Uint32("sequence_number", pdu.SequenceNumber), zap.Int("short_message_bytes", len(pdu.ShortMessage)))
		s.mu.Lock()
		handler := s.deliverSMHandler
		receiptHandler := s.deliveryReceiptHandler
		s.mu.Unlock()
		if receiptHandler != nil {
			if receipt, err := ParseDeliveryReceipt(string(pdu.ShortMessage)); err == nil {
				log.Debug("smpp delivery receipt parsed", zap.String("receipt_id", receipt.ID), zap.String("receipt_stat", receipt.Stat))
				go receiptHandler(receipt)
			}
		}
		if handler != nil {
			go handler(pdu)
		}
		go func() {
			_ = conn.WritePDU(&PDU{
				CommandID:      CmdDeliverSMResp,
				CommandStatus:  ESMEROK,
				SequenceNumber: pdu.SequenceNumber,
				MessageID:      "",
			})
		}()
		return true
	case CmdUnbind:
		log.Debug("smpp unbind received", zap.Uint32("sequence_number", pdu.SequenceNumber))
		go func() {
			_ = conn.WritePDU(&PDU{
				CommandID:      CmdUnbindResp,
				CommandStatus:  ESMEROK,
				SequenceNumber: pdu.SequenceNumber,
			})
			_ = s.Close()
		}()
		return true
	default:
		return false
	}
}

func bindCommands(mode string) (uint32, uint32) {
	switch mode {
	case "receiver":
		return CmdBindReceiver, CmdBindReceiverResp
	case "transmitter":
		return CmdBindTransmitter, CmdBindTransmitterResp
	default:
		return CmdBindTransceiver, CmdBindTransceiverResp
	}
}
