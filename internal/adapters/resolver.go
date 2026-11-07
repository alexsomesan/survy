package adapters

import (
	"context"
	"net"
	"time"

	"survy/internal/ports"
)

// DefaultDNSServer is the router's DNS forwarder on the LAN.
const DefaultDNSServer = "192.168.2.1:53"

// NewResolver returns a ports.Resolver that sends every query to server
// (host:port) using Go's own resolver, with a short per-query timeout.
func NewResolver(server string, timeout time.Duration) ports.Resolver {
	if server == "" {
		server = DefaultDNSServer
	}
	if timeout <= 0 {
		timeout = 2 * time.Second
	}
	r := &net.Resolver{
		PreferGo: true,
		Dial: func(ctx context.Context, network, _ string) (net.Conn, error) {
			d := net.Dialer{Timeout: timeout}
			return d.DialContext(ctx, network, server)
		},
	}
	return &resolver{r: r, timeout: timeout}
}

type resolver struct {
	r       *net.Resolver
	timeout time.Duration
}

func (r *resolver) LookupAddr(ctx context.Context, addr string) ([]string, error) {
	ctx, cancel := context.WithTimeout(ctx, r.timeout)
	defer cancel()
	return r.r.LookupAddr(ctx, addr)
}
