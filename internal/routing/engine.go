package routing

import (
	"context"
	"fmt"
	"strings"

	"github.com/vectorcore/vectorcore-mmsc/internal/db"
)

type Destination int

const (
	DestinationUnknown Destination = iota
	DestinationLocal
	DestinationMM4
	DestinationMM3
)

type Result struct {
	Destination Destination
	Peer        *db.MM4Peer
}

func (d Destination) String() string {
	switch d {
	case DestinationLocal:
		return "local"
	case DestinationMM4:
		return "mm4"
	case DestinationMM3:
		return "mm3"
	default:
		return "unknown"
	}
}

type Engine struct {
	peers *PeerResolver
}

func NewEngine(repo db.Repository) *Engine {
	return &Engine{
		peers: NewPeerResolver(repo),
	}
}

func (e *Engine) Resolve(ctx context.Context, address string) (*Result, error) {
	addr := normalizeAddress(address)
	if strings.Contains(addr, "@") {
		peer, err := e.peers.Resolve(ctx, addr)
		if err != nil {
			return nil, err
		}
		if peer != nil {
			return &Result{Destination: DestinationMM4, Peer: peer}, nil
		}
		return &Result{Destination: DestinationMM3}, nil
	}
	if addr == "" {
		return &Result{Destination: DestinationUnknown}, nil
	}
	return &Result{Destination: DestinationLocal}, nil
}

func (e *Engine) ResolveRecipients(ctx context.Context, addresses []string) (*Result, error) {
	if len(addresses) == 0 {
		return &Result{Destination: DestinationUnknown}, nil
	}
	first, err := e.Resolve(ctx, addresses[0])
	if err != nil {
		return nil, err
	}
	for _, address := range addresses[1:] {
		next, err := e.Resolve(ctx, address)
		if err != nil {
			return nil, err
		}
		if first.Destination != next.Destination {
			return nil, fmt.Errorf("mixed recipient destinations are not supported in a single message")
		}
		if first.Destination == DestinationMM4 && !samePeer(first.Peer, next.Peer) {
			return nil, fmt.Errorf("multiple MM4 peer routes are not supported in a single message")
		}
	}
	return first, nil
}

func samePeer(a, b *db.MM4Peer) bool {
	if a == nil || b == nil {
		return a == b
	}
	return strings.EqualFold(a.Domain, b.Domain) &&
		strings.EqualFold(a.SMTPHost, b.SMTPHost) &&
		a.SMTPPort == b.SMTPPort
}

func normalizeAddress(address string) string {
	if idx := strings.Index(strings.ToUpper(address), "/TYPE="); idx > 0 {
		return address[:idx]
	}
	return address
}
