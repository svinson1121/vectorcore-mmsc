package routing

import (
	"context"
	"strings"

	"github.com/vectorcore/vectorcore-mmsc/internal/db"
)

type PeerResolver struct {
	repo db.Repository
}

func NewPeerResolver(repo db.Repository) *PeerResolver {
	return &PeerResolver{repo: repo}
}

func (r *PeerResolver) Resolve(ctx context.Context, address string) (*db.MM4Peer, error) {
	peers, err := r.repo.ListMM4Peers(ctx)
	if err != nil {
		return nil, err
	}
	domain := domainOf(address)
	for _, peer := range peers {
		if strings.EqualFold(peer.Domain, domain) && peer.Active {
			return &peer, nil
		}
	}
	return nil, nil
}

func domainOf(address string) string {
	if idx := strings.LastIndex(address, "@"); idx >= 0 && idx < len(address)-1 {
		return strings.ToLower(address[idx+1:])
	}
	return ""
}
