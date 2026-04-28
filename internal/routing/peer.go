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
	_, peer, err := r.ResolveRoute(ctx, address)
	return peer, err
}

func (r *PeerResolver) ResolveRoute(ctx context.Context, address string) (*db.MM4Route, *db.MM4Peer, error) {
	peers, err := r.repo.ListMM4Peers(ctx)
	if err != nil {
		return nil, nil, err
	}
	routes, err := r.repo.ListMM4Routes(ctx)
	if err != nil {
		return nil, nil, err
	}
	route := MatchRoute(address, routes)
	if route == nil {
		return nil, nil, nil
	}
	if route.EgressType != "mm4" {
		return route, nil, nil
	}
	for _, peer := range peers {
		if peer.Active && strings.EqualFold(peer.Domain, route.EgressTarget) {
			return route, &peer, nil
		}
	}
	return &db.MM4Route{EgressType: "reject"}, nil, nil
}

func MatchRoutePeer(address string, routes []db.MM4Route, peers []db.MM4Peer) *db.MM4Peer {
	route := MatchRoute(address, routes)
	if route == nil || route.EgressType != "mm4" {
		return nil
	}
	for _, peer := range peers {
		if peer.Active && strings.EqualFold(peer.Domain, route.EgressTarget) {
			return &peer
		}
	}
	return nil
}

func MatchRoute(address string, routes []db.MM4Route) *db.MM4Route {
	var best *db.MM4Route
	for i := range routes {
		route := &routes[i]
		if !route.Active || !routeMatches(address, *route) {
			continue
		}
		if best == nil || betterRoute(*route, *best) {
			best = route
		}
	}
	if best == nil {
		return nil
	}
	return best
}

func routeMatches(address string, route db.MM4Route) bool {
	switch route.MatchType {
	case "recipient_domain":
		return strings.EqualFold(domainOf(address), route.MatchValue)
	case "msisdn_exact":
		if strings.Contains(address, "@") {
			return false
		}
		return canonicalMSISDN(address) == canonicalMSISDN(route.MatchValue)
	case "msisdn_prefix":
		if strings.Contains(address, "@") {
			return false
		}
		return strings.HasPrefix(canonicalMSISDN(address), canonicalMSISDN(route.MatchValue))
	default:
		return false
	}
}

func betterRoute(candidate, current db.MM4Route) bool {
	candidateRank := routeRank(candidate.MatchType)
	currentRank := routeRank(current.MatchType)
	if candidateRank != currentRank {
		return candidateRank > currentRank
	}
	if candidate.MatchType == "msisdn_prefix" && len(canonicalMSISDN(candidate.MatchValue)) != len(canonicalMSISDN(current.MatchValue)) {
		return len(canonicalMSISDN(candidate.MatchValue)) > len(canonicalMSISDN(current.MatchValue))
	}
	if candidate.Priority != current.Priority {
		return candidate.Priority > current.Priority
	}
	return candidate.ID < current.ID
}

func routeRank(matchType string) int {
	switch matchType {
	case "msisdn_exact":
		return 3
	case "msisdn_prefix":
		return 2
	case "recipient_domain":
		return 1
	default:
		return 0
	}
}

func canonicalMSISDN(value string) string {
	if idx := strings.Index(strings.ToUpper(value), "/TYPE="); idx > 0 {
		value = value[:idx]
	}
	value = strings.TrimSpace(value)
	var out strings.Builder
	for i, r := range value {
		if r >= '0' && r <= '9' {
			out.WriteRune(r)
		} else if r == '+' && i == 0 {
			out.WriteRune(r)
		}
	}
	return out.String()
}

func domainOf(address string) string {
	if idx := strings.LastIndex(address, "@"); idx >= 0 && idx < len(address)-1 {
		return strings.ToLower(address[idx+1:])
	}
	return ""
}
