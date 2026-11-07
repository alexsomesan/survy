package app

import (
	"context"
	"net/netip"
	"strings"
	"sync"
	"time"

	"survy/internal/ports"
)

// Reverse DNS for Remote Endpoints (spec: Remote Endpoint labelling).
// Lookups are only queued for endpoints a Device detail actually shows, run
// on a few background workers, and cached, so an API call never waits on DNS
// and DNS traffic stays small.
const (
	rdnsWorkers     = 2
	rdnsQueue       = 256 // lookups waiting; more are dropped and retried on a later request
	rdnsCacheMax    = 8192
	rdnsPositiveTTL = 24 * time.Hour
	rdnsNegativeTTL = time.Hour
	rdnsTimeout     = 5 * time.Second
)

type rdnsEntry struct {
	host    string // "" when the lookup failed
	expires time.Time
}

type rdns struct {
	resolver ports.Resolver
	clock    ports.Clock

	mu      sync.Mutex
	cache   map[netip.Addr]rdnsEntry
	pending map[netip.Addr]bool // queued or in flight
	queue   chan netip.Addr

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

func newRDNS(r ports.Resolver, c ports.Clock) *rdns {
	return &rdns{resolver: r, clock: c,
		cache: map[netip.Addr]rdnsEntry{}, pending: map[netip.Addr]bool{},
		queue: make(chan netip.Addr, rdnsQueue)}
}

// start launches the workers (no-op without a Resolver).
func (d *rdns) start() {
	if d.resolver == nil {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	d.mu.Lock()
	d.ctx, d.cancel = ctx, cancel
	d.mu.Unlock()
	for range rdnsWorkers {
		d.wg.Add(1)
		go d.work()
	}
}

func (d *rdns) stop() {
	d.mu.Lock()
	cancel := d.cancel
	d.cancel = nil
	d.mu.Unlock()
	if cancel == nil {
		return
	}
	cancel()
	d.wg.Wait()
}

// host returns the cached name of ip (nil when unknown) and queues a lookup
// when there is no fresh cache entry. Stale names are served while being
// refreshed.
func (d *rdns) host(ip netip.Addr) *string {
	if d.resolver == nil || !lookupable(ip) {
		return nil
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	e, ok := d.cache[ip]
	if (!ok || !d.clock.Now().Before(e.expires)) && !d.pending[ip] && d.cancel != nil {
		select {
		case d.queue <- ip:
			d.pending[ip] = true
		default: // queue full: try again on a later request
		}
	}
	if !ok || e.host == "" {
		return nil
	}
	h := e.host
	return &h
}

func (d *rdns) pendingCount() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return len(d.pending)
}

func (d *rdns) work() {
	defer d.wg.Done()
	for {
		select {
		case <-d.ctx.Done():
			return
		case ip := <-d.queue:
			d.resolve(ip)
		}
	}
}

func (d *rdns) resolve(ip netip.Addr) {
	ctx, cancel := context.WithTimeout(d.ctx, rdnsTimeout)
	names, err := d.resolver.LookupAddr(ctx, ip.String())
	cancel()
	host := ""
	if err == nil && len(names) > 0 {
		host = strings.TrimSuffix(names[0], ".")
	}
	ttl := rdnsPositiveTTL
	if host == "" {
		ttl = rdnsNegativeTTL
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	delete(d.pending, ip)
	if d.ctx.Err() != nil {
		return // shutting down: the failure is ours, not the address's
	}
	now := d.clock.Now()
	if _, ok := d.cache[ip]; !ok && len(d.cache) >= rdnsCacheMax {
		d.evictLocked(now)
	}
	d.cache[ip] = rdnsEntry{host: host, expires: now.Add(ttl)}
}

// evictLocked makes room in a full cache: expired entries go first, then
// arbitrary ones down to three quarters of the cap.
func (d *rdns) evictLocked(now time.Time) {
	for ip, e := range d.cache {
		if !now.Before(e.expires) {
			delete(d.cache, ip)
		}
	}
	for ip := range d.cache {
		if len(d.cache) < rdnsCacheMax*3/4 {
			break
		}
		delete(d.cache, ip)
	}
}

// cgnat is the shared address space (RFC 6598), private in practice.
var cgnat = netip.MustParsePrefix("100.64.0.0/10")

// lookupable is whether ip is a public address worth asking DNS about.
func lookupable(ip netip.Addr) bool {
	ip = ip.Unmap()
	return ip.IsGlobalUnicast() && !ip.IsPrivate() && !cgnat.Contains(ip)
}
