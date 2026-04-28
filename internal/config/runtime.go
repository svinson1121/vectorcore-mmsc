package config

import (
	"context"
	"sync"

	"github.com/vectorcore/vectorcore-mmsc/internal/db"
)

type RuntimeSnapshot struct {
	Peers         []db.MM4Peer
	MM4Routes     []db.MM4Route
	MM3Relay      *db.MM3Relay
	VASPs         []db.MM7VASP
	SMPPUpstreams []db.SMPPUpstream
	Adaptation    []db.AdaptationClass
}

type RuntimeStore struct {
	mu          sync.RWMutex
	snapshot    RuntimeSnapshot
	subscribers []chan RuntimeSnapshot
}

func NewRuntimeStore() *RuntimeStore {
	return &RuntimeStore{}
}

func (s *RuntimeStore) Snapshot() RuntimeSnapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return RuntimeSnapshot{
		Peers:         append([]db.MM4Peer(nil), s.snapshot.Peers...),
		MM4Routes:     append([]db.MM4Route(nil), s.snapshot.MM4Routes...),
		MM3Relay:      cloneMM3Relay(s.snapshot.MM3Relay),
		VASPs:         append([]db.MM7VASP(nil), s.snapshot.VASPs...),
		SMPPUpstreams: append([]db.SMPPUpstream(nil), s.snapshot.SMPPUpstreams...),
		Adaptation:    append([]db.AdaptationClass(nil), s.snapshot.Adaptation...),
	}
}

func (s *RuntimeStore) Replace(snapshot RuntimeSnapshot) {
	s.mu.Lock()
	s.snapshot = RuntimeSnapshot{
		Peers:         append([]db.MM4Peer(nil), snapshot.Peers...),
		MM4Routes:     append([]db.MM4Route(nil), snapshot.MM4Routes...),
		MM3Relay:      cloneMM3Relay(snapshot.MM3Relay),
		VASPs:         append([]db.MM7VASP(nil), snapshot.VASPs...),
		SMPPUpstreams: append([]db.SMPPUpstream(nil), snapshot.SMPPUpstreams...),
		Adaptation:    append([]db.AdaptationClass(nil), snapshot.Adaptation...),
	}
	current := s.snapshot
	subscribers := append([]chan RuntimeSnapshot(nil), s.subscribers...)
	s.mu.Unlock()

	for _, ch := range subscribers {
		select {
		case ch <- current:
		default:
		}
	}
}

func (s *RuntimeStore) Subscribe(buffer int) <-chan RuntimeSnapshot {
	if buffer <= 0 {
		buffer = 1
	}
	ch := make(chan RuntimeSnapshot, buffer)

	s.mu.Lock()
	s.subscribers = append(s.subscribers, ch)
	current := s.snapshot
	s.mu.Unlock()

	if len(current.Peers) > 0 || len(current.MM4Routes) > 0 || current.MM3Relay != nil || len(current.VASPs) > 0 || len(current.SMPPUpstreams) > 0 || len(current.Adaptation) > 0 {
		ch <- current
	}
	return ch
}

func LoadRuntimeSnapshot(ctx context.Context, repo db.Repository) (RuntimeSnapshot, error) {
	peers, err := repo.ListMM4Peers(ctx)
	if err != nil {
		return RuntimeSnapshot{}, err
	}
	routes, err := repo.ListMM4Routes(ctx)
	if err != nil {
		return RuntimeSnapshot{}, err
	}
	mm3Relay, err := repo.GetMM3Relay(ctx)
	if err != nil {
		return RuntimeSnapshot{}, err
	}
	vasps, err := repo.ListMM7VASPs(ctx)
	if err != nil {
		return RuntimeSnapshot{}, err
	}
	upstreams, err := repo.ListSMPPUpstreams(ctx)
	if err != nil {
		return RuntimeSnapshot{}, err
	}
	adaptation, err := repo.ListAdaptationClasses(ctx)
	if err != nil {
		return RuntimeSnapshot{}, err
	}
	return RuntimeSnapshot{
		Peers:         peers,
		MM4Routes:     routes,
		MM3Relay:      mm3Relay,
		VASPs:         vasps,
		SMPPUpstreams: upstreams,
		Adaptation:    adaptation,
	}, nil
}

func cloneMM3Relay(relay *db.MM3Relay) *db.MM3Relay {
	if relay == nil {
		return nil
	}
	cloned := *relay
	return &cloned
}
