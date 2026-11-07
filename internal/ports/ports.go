// Package ports defines everything the dashboard reads from the outside world
// that is not a plain file under the root directory. Tests replace each of
// these with fakes (see internal/testkit); production uses internal/adapters.
package ports

import (
	"context"
	"net/netip"
	"time"
)

// Tuple is one direction of a tracked connection.
type Tuple struct {
	Src, Dst         netip.Addr
	SrcPort, DstPort uint16
}

// Flow is one conntrack entry. OrigBytes are bytes sent by Orig.Src (the
// connection's originator); ReplyBytes are bytes sent back towards it.
// Counters are cumulative for the flow's lifetime and are zero when
// nf_conntrack_acct is off.
type Flow struct {
	ID         uint32
	Proto      uint8 // IANA protocol number: 6 tcp, 17 udp, 1 icmp, 58 icmpv6
	Orig       Tuple
	Reply      Tuple
	OrigBytes  uint64
	ReplyBytes uint64
}

// Neighbour is one ARP/NDP entry. MAC is lower-case colon-separated and
// empty when the entry is unresolved (FAILED/INCOMPLETE).
type Neighbour struct {
	Iface string
	IP    netip.Addr
	MAC   string
	State string
}

// IfAddr is one address assigned to one of the router's interfaces.
type IfAddr struct {
	Iface  string
	Prefix netip.Prefix
}

// DiskUsage is a statfs result in bytes.
type DiskUsage struct {
	Total, Free, Avail uint64
}

// Kernel is the router's kernel tables, read over netlink in production.
type Kernel interface {
	// Conntrack returns every currently tracked connection.
	Conntrack() ([]Flow, error)
	// DrainDestroyed returns the final state of every connection destroyed
	// since the previous call, and forgets them.
	DrainDestroyed() []Flow
	Neighbours() ([]Neighbour, error)
	Addresses() ([]IfAddr, error)
	Statfs(path string) (DiskUsage, error)
}

// Clock is the dashboard's only source of time and scheduling.
type Clock interface {
	Now() time.Time
	// Every calls fn every d until stop is called. A fake clock calls fn
	// synchronously from Advance.
	Every(d time.Duration, fn func(now time.Time)) (stop func())
}

// Resolver does reverse DNS lookups.
type Resolver interface {
	LookupAddr(ctx context.Context, addr string) ([]string, error)
}
