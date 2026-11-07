// Package adapters implements internal/ports against the real Linux kernel,
// wall-clock time and DNS. None of this is exercised by the seam tests; see
// cmd/survy-probe for a smoke check.
package adapters

import (
	"errors"
	"fmt"
	"log"
	"net"
	"net/netip"
	"strings"
	"sync"

	mdnetlink "github.com/mdlayher/netlink"
	"github.com/ti-mo/conntrack"
	"github.com/ti-mo/netfilter"
	"github.com/vishvananda/netlink"
	"golang.org/x/sys/unix"

	"survy/internal/ports"
)

// KernelConfig configures Kernel.
type KernelConfig struct {
	// DestroyBuffer bounds the number of destroyed flows held between drains.
	// Default 65536.
	DestroyBuffer int
}

// Kernel is the netlink-backed ports.Kernel.
//
// Its conntrack sockets need CAP_NET_ADMIN to open but not to use (the
// kernel checks the opener's credentials), so Open dials them while the
// process is still privileged and they are never redialled afterwards.
type Kernel struct {
	cfg KernelConfig

	dumpMu      sync.Mutex
	dump        *conntrack.Conn // nil when Open failed to dial it
	dumpErr     error           // why dump is nil
	lastDumpErr string          // last logged dump error, to log changes only

	events    *conntrack.Conn
	destroyed *destroyedBuffer
	listenErr error
}

var _ ports.Kernel = (*Kernel)(nil)

var errNotOpen = errors.New("conntrack: not open")

// NewKernel builds a Kernel. Call Open before using conntrack.
func NewKernel(cfg KernelConfig) *Kernel {
	if cfg.DestroyBuffer <= 0 {
		cfg.DestroyBuffer = 65536
	}
	return &Kernel{cfg: cfg, destroyed: newDestroyedBuffer(cfg.DestroyBuffer),
		dumpErr: errNotOpen, listenErr: errNotOpen}
}

// Open dials the conntrack dump socket and starts the destroy-event
// listener. Failures (e.g. missing CAP_NET_ADMIN) are not fatal: they are
// logged and returned, Conntrack then reports the error, and DrainDestroyed
// returns nothing. Call it once, before dropping privileges.
func (k *Kernel) Open() error {
	k.dumpMu.Lock()
	if c, err := conntrack.Dial(nil); err != nil {
		k.dumpErr = fmt.Errorf("conntrack dial: %w", err)
		log.Printf("adapters: conntrack dump socket unavailable: %v", err)
	} else {
		k.dump, k.dumpErr = c, nil
	}
	dumpErr := k.dumpErr
	k.dumpMu.Unlock()
	k.listenErr = k.listen()
	if k.listenErr != nil {
		log.Printf("adapters: conntrack destroy listener unavailable: %v", k.listenErr)
	}
	return errors.Join(dumpErr, k.listenErr)
}

// ListenErr reports why the destroy-event listener is not running, if so.
func (k *Kernel) ListenErr() error { return k.listenErr }

// DroppedDestroyed counts destroyed flows discarded because the buffer was full.
func (k *Kernel) DroppedDestroyed() uint64 { return k.destroyed.dropped() }

func (k *Kernel) listen() error {
	c, err := conntrack.Dial(nil)
	if err != nil {
		return fmt.Errorf("dial: %w", err)
	}
	// Large receive buffer so bursts of destroys don't overflow the socket;
	// best effort, the kernel caps it at rmem_max without CAP_NET_ADMIN.
	_ = c.SetReadBuffer(8 << 20)
	// On overflow the kernel would otherwise return ENOBUFS, which is fatal
	// to the listener worker; prefer losing a few events to losing all.
	_ = c.SetOption(mdnetlink.NoENOBUFS, true)
	evCh := make(chan conntrack.Event, 4096)
	errCh, err := c.Listen(evCh, 1, []netfilter.NetlinkGroup{netfilter.GroupCTDestroy})
	if err != nil {
		c.Close()
		return fmt.Errorf("listen: %w", err)
	}
	k.events = c
	go func() {
		for ev := range evCh {
			if ev.Type == conntrack.EventDestroy && ev.Flow != nil {
				k.destroyed.push(flowFromCT(*ev.Flow))
			}
		}
	}()
	go func() {
		for err := range errCh {
			if err != nil {
				log.Printf("adapters: conntrack listener stopped: %v", err)
			}
			return
		}
	}()
	return nil
}

// Close releases the netlink sockets.
func (k *Kernel) Close() error {
	var errs []error
	if k.events != nil {
		errs = append(errs, k.events.Close())
	}
	k.dumpMu.Lock()
	if k.dump != nil {
		errs = append(errs, k.dump.Close())
		k.dump = nil
		k.dumpErr = errNotOpen
	}
	k.dumpMu.Unlock()
	return errors.Join(errs...)
}

// Conntrack dumps the table on the socket Open dialled. A failed dump is
// reported (and logged when the error changes) but the socket is kept: after
// a privilege drop it could not be reopened.
func (k *Kernel) Conntrack() ([]ports.Flow, error) {
	k.dumpMu.Lock()
	defer k.dumpMu.Unlock()
	if k.dump == nil {
		return nil, k.dumpErr
	}
	cts, err := k.dump.Dump(nil)
	if err != nil {
		if msg := err.Error(); msg != k.lastDumpErr {
			k.lastDumpErr = msg
			log.Printf("adapters: conntrack dump: %v", err)
		}
		return nil, fmt.Errorf("conntrack dump: %w", err)
	}
	k.lastDumpErr = ""
	out := make([]ports.Flow, len(cts))
	for i, f := range cts {
		out[i] = flowFromCT(f)
	}
	return out, nil
}

func (k *Kernel) DrainDestroyed() []ports.Flow { return k.destroyed.drain() }

func (k *Kernel) Neighbours() ([]ports.Neighbour, error) {
	names, err := linkNames()
	if err != nil {
		return nil, err
	}
	ns, err := netlink.NeighList(0, netlink.FAMILY_ALL)
	if err != nil && !errors.Is(err, netlink.ErrDumpInterrupted) {
		return nil, fmt.Errorf("neigh list: %w", err)
	}
	out := make([]ports.Neighbour, 0, len(ns))
	for _, n := range ns {
		if !listedNeigh(n) {
			continue
		}
		out = append(out, neighFromNL(n, names[n.LinkIndex]))
	}
	return out, nil
}

func (k *Kernel) Addresses() ([]ports.IfAddr, error) {
	names, err := linkNames()
	if err != nil {
		return nil, err
	}
	as, err := netlink.AddrList(nil, netlink.FAMILY_ALL)
	if err != nil && !errors.Is(err, netlink.ErrDumpInterrupted) {
		return nil, fmt.Errorf("addr list: %w", err)
	}
	out := make([]ports.IfAddr, 0, len(as))
	for _, a := range as {
		if a.IPNet == nil {
			continue
		}
		ip, ok := netip.AddrFromSlice(a.IP)
		if !ok {
			continue
		}
		ones, _ := a.Mask.Size()
		out = append(out, ports.IfAddr{Iface: names[a.LinkIndex], Prefix: netip.PrefixFrom(ip.Unmap(), ones)})
	}
	return out, nil
}

// Statfs measures the file system holding path (a path in this process's
// mount namespace).
func (k *Kernel) Statfs(path string) (ports.DiskUsage, error) {
	var st unix.Statfs_t
	if err := unix.Statfs(path, &st); err != nil {
		return ports.DiskUsage{}, fmt.Errorf("statfs %s: %w", path, err)
	}
	bs := uint64(st.Bsize)
	return ports.DiskUsage{Total: st.Blocks * bs, Free: st.Bfree * bs, Avail: st.Bavail * bs}, nil
}

func linkNames() (map[int]string, error) {
	links, err := netlink.LinkList()
	if err != nil && !errors.Is(err, netlink.ErrDumpInterrupted) {
		return nil, fmt.Errorf("link list: %w", err)
	}
	m := make(map[int]string, len(links))
	for _, l := range links {
		a := l.Attrs()
		m[a.Index] = a.Name
	}
	return m, nil
}

var nudNames = []struct {
	bit  int
	name string
}{
	{netlink.NUD_INCOMPLETE, "INCOMPLETE"},
	{netlink.NUD_REACHABLE, "REACHABLE"},
	{netlink.NUD_STALE, "STALE"},
	{netlink.NUD_DELAY, "DELAY"},
	{netlink.NUD_PROBE, "PROBE"},
	{netlink.NUD_FAILED, "FAILED"},
	{netlink.NUD_NOARP, "NOARP"},
	{netlink.NUD_PERMANENT, "PERMANENT"},
}

// listedNeigh mirrors `ip neigh show`'s default filter, which hides NOARP
// entries (multicast, loopback, point-to-point links).
func listedNeigh(n netlink.Neigh) bool {
	return n.IP != nil && n.State&netlink.NUD_NOARP == 0
}

// neighFromNL translates a netlink neighbour; State is the first state name
// as `ip -j neigh` prints it.
func neighFromNL(n netlink.Neigh, iface string) ports.Neighbour {
	ip, _ := netip.AddrFromSlice(n.IP)
	state := "NONE"
	for _, s := range nudNames {
		if n.State&s.bit != 0 {
			state = s.name
			break
		}
	}
	mac := ""
	if n.State&(netlink.NUD_FAILED|netlink.NUD_INCOMPLETE) == 0 && len(n.HardwareAddr) > 0 {
		mac = strings.ToLower(net.HardwareAddr(n.HardwareAddr).String())
	}
	return ports.Neighbour{Iface: iface, IP: ip.Unmap(), MAC: mac, State: state}
}
