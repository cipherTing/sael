package gateway

import (
	"net"
	"net/http"
	"net/netip"
	"strings"
)

func (s *Server) clientIP(r *http.Request) string {
	peer, err := peerIP(r)
	if err != nil {
		return ""
	}
	if !s.trustsProxy(peer) {
		return peer.String()
	}
	forwarded := r.Header.Get("X-Forwarded-For")
	if forwarded == "" {
		return peer.String()
	}
	chain := strings.Split(forwarded, ",")
	for i := len(chain) - 1; i >= 0; i-- {
		if !s.trustsProxy(peer) {
			break
		}
		candidate, err := netip.ParseAddr(strings.TrimSpace(chain[i]))
		if err != nil {
			break
		}
		peer = candidate.Unmap()
	}
	return peer.String()
}

func peerIP(r *http.Request) (netip.Addr, error) {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	peer, err := netip.ParseAddr(host)
	return peer.Unmap(), err
}

func (s *Server) trustsProxy(ip netip.Addr) bool {
	for _, prefix := range s.TrustedProxies {
		if prefix.Contains(ip) {
			return true
		}
	}
	return false
}
