package mm4

import (
	"context"
	"fmt"
	"net"
	"strings"

	"github.com/vectorcore/vectorcore-mmsc/internal/db"
	"github.com/vectorcore/vectorcore-mmsc/internal/routing"
)

type MXResolver func(context.Context, string) ([]*net.MX, error)

type PeerRouter struct {
	repo       db.Repository
	mxResolver MXResolver
}

func NewPeerRouter(repo db.Repository) *PeerRouter {
	return &PeerRouter{
		repo: repo,
		mxResolver: func(_ context.Context, domain string) ([]*net.MX, error) {
			return net.LookupMX(domain)
		},
	}
}

func (r *PeerRouter) Resolve(ctx context.Context, address string) (host string, port int, err error) {
	peer, ok, err := r.ResolvePeer(ctx, address)
	if err != nil {
		return "", 0, err
	}
	if ok {
		return peer.SMTPHost, peer.SMTPPort, nil
	}

	domain := domainOf(address)
	mxs, err := r.mxResolver(ctx, domain)
	if err != nil || len(mxs) == 0 {
		return "", 0, fmt.Errorf("mm4: resolve peer for %s: %w", domain, err)
	}
	return strings.TrimSuffix(mxs[0].Host, "."), 25, nil
}

func (r *PeerRouter) ResolvePeer(ctx context.Context, address string) (db.MM4Peer, bool, error) {
	peers, err := r.repo.ListMM4Peers(ctx)
	if err != nil {
		return db.MM4Peer{}, false, err
	}
	routes, err := r.repo.ListMM4Routes(ctx)
	if err != nil {
		return db.MM4Peer{}, false, err
	}
	if peer := routing.MatchRoutePeer(address, routes, peers); peer != nil {
		return *peer, true, nil
	}
	domain := domainOf(address)
	for _, peer := range peers {
		if peer.Active && strings.EqualFold(peer.Domain, domain) {
			return peer, true, nil
		}
	}
	return db.MM4Peer{}, false, nil
}

func domainOf(address string) string {
	if idx := strings.LastIndex(address, "@"); idx >= 0 && idx < len(address)-1 {
		return strings.ToLower(address[idx+1:])
	}
	return ""
}
