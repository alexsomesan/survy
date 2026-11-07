package app

import (
	"net/netip"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"survy/internal/ports"
)

// Conntrack polling cadence (spec: Sampling cadence).
const (
	pollWatched = time.Second
	pollIdle    = 10 * time.Second
)

// Bytes is a pair of byte counts from the named thing's point of view: Rx is
// what it received, Tx what it sent.
type Bytes struct{ Rx, Tx uint64 }

func (b *Bytes) add(o Bytes) { b.Rx += o.Rx; b.Tx += o.Tx }

// TrafficDelta is the WAN Traffic counted by one conntrack poll, covering
// (From, To]. It is the accounting feed that History persists.
type TrafficDelta struct {
	From, To time.Time
	// WAN is the WAN interface's byte delta (Rx is download).
	WAN Bytes
	// Unattributed is WAN minus the sum of Devices, floored at zero.
	Unattributed Bytes
	// Devices have non-zero traffic, sorted by Rx+Tx descending.
	Devices []DeviceDelta
	// Endpoints are per Device × Remote Endpoint, non-zero only.
	Endpoints []EndpointDelta
}

// DeviceDelta is one Device's WAN Traffic in a TrafficDelta.
type DeviceDelta struct {
	Device Device
	Bytes
}

// EndpointDelta is one Device's traffic with one Remote Endpoint.
type EndpointDelta struct {
	DeviceID string
	Endpoint netip.Addr
	Bytes
}

type flowKey struct {
	id    uint32
	proto uint8
	orig  ports.Tuple
}

type flowCounters struct{ orig, reply uint64 }

// traffic polls conntrack, turns counters into per-Device WAN Traffic deltas
// and hands them to subscribers.
type traffic struct {
	kernel  ports.Kernel
	devices *devices
	wanRx   string // sysfs counter paths of the WAN interface
	wanTx   string

	feed        sync.Mutex // held across a poll and its delivery, so subscribers see deltas in order
	mu          sync.Mutex
	polled      bool
	lastPoll    time.Time
	prev        map[flowKey]flowCounters
	wanPrev     Bytes
	wanPrevOK   bool
	subscribers []func(TrafficDelta)
}

func newTraffic(k ports.Kernel, d *devices, sysNet string, wanIface string) *traffic {
	stats := filepath.Join(sysNet, wanIface, "statistics")
	return &traffic{
		kernel: k, devices: d,
		wanRx: filepath.Join(stats, "rx_bytes"), wanTx: filepath.Join(stats, "tx_bytes"),
		prev: map[flowKey]flowCounters{},
	}
}

// subscribe registers fn to receive every TrafficDelta. Register before Start.
func (t *traffic) subscribe(fn func(TrafficDelta)) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.subscribers = append(t.subscribers, fn)
}

// tick polls conntrack if one is due: every second while watched, else every
// ten seconds.
func (t *traffic) tick(now time.Time, watched bool) {
	t.feed.Lock()
	defer t.feed.Unlock()
	t.mu.Lock()
	interval := pollIdle
	if watched {
		interval = pollWatched
	}
	if t.polled && now.Sub(t.lastPoll) < interval {
		t.mu.Unlock()
		return
	}
	delta, ok := t.poll(now)
	subs := t.subscribers
	t.mu.Unlock()
	if ok {
		for _, fn := range subs {
			fn(delta)
		}
	}
}

// poll reads conntrack and destroyed flows. The first poll only records a
// baseline (counters accumulated before we started are not attributable to a
// period); later polls count new flows in full.
func (t *traffic) poll(now time.Time) (TrafficDelta, bool) {
	flows, err := t.kernel.Conntrack()
	if err != nil {
		return TrafficDelta{}, false
	}
	destroyed := t.kernel.DrainDestroyed()
	t.devices.refresh(now, t.kernel)

	baseline := !t.polled
	from := t.lastPoll
	t.polled, t.lastPoll = true, now

	devBytes := map[string]*Bytes{}
	epBytes := map[EndpointDelta]*Bytes{}
	count := func(f ports.Flow, d flowCounters) {
		if baseline || (d.orig == 0 && d.reply == 0) {
			return
		}
		id, remote, localIsOrig, ok := t.attribute(f)
		if !ok {
			return
		}
		b := Bytes{Rx: d.reply, Tx: d.orig}
		if !localIsOrig {
			b = Bytes{Rx: d.orig, Tx: d.reply}
		}
		if devBytes[id] == nil {
			devBytes[id] = &Bytes{}
		}
		devBytes[id].add(b)
		k := EndpointDelta{DeviceID: id, Endpoint: remote}
		if epBytes[k] == nil {
			epBytes[k] = &Bytes{}
		}
		epBytes[k].add(b)
	}

	seen := make(map[flowKey]flowCounters, len(flows))
	for _, f := range flows {
		k := keyOf(f)
		cur := flowCounters{f.OrigBytes, f.ReplyBytes}
		count(f, diff(t.prev[k], cur))
		seen[k] = cur
	}
	for _, f := range destroyed {
		k := keyOf(f)
		prev, ok := seen[k]
		if !ok {
			prev = t.prev[k]
		}
		count(f, diff(prev, flowCounters{f.OrigBytes, f.ReplyBytes}))
		delete(seen, k)
	}
	t.prev = seen

	wan, wanOK := t.wanDelta()
	if baseline {
		return TrafficDelta{}, false
	}

	out := TrafficDelta{From: from, To: now, Devices: []DeviceDelta{}, Endpoints: []EndpointDelta{}}
	var attributed Bytes
	for id, b := range devBytes {
		out.Devices = append(out.Devices, DeviceDelta{Device: t.devices.describe(id), Bytes: *b})
		attributed.add(*b)
	}
	sort.Slice(out.Devices, func(i, j int) bool {
		a, b := out.Devices[i], out.Devices[j]
		if a.Rx+a.Tx != b.Rx+b.Tx {
			return a.Rx+a.Tx > b.Rx+b.Tx
		}
		return a.Device.ID < b.Device.ID
	})
	for k, b := range epBytes {
		k.Bytes = *b
		out.Endpoints = append(out.Endpoints, k)
	}
	if wanOK {
		out.WAN = wan
		out.Unattributed = Bytes{Rx: floorSub(wan.Rx, attributed.Rx), Tx: floorSub(wan.Tx, attributed.Tx)}
	}
	return out, true
}

// attribute decides whether a flow is WAN Traffic and whose. The originator
// is Orig.Src and the responder Reply.Src (both real addresses, after DNAT);
// exactly one of them must be a Remote Endpoint.
func (t *traffic) attribute(f ports.Flow) (id string, remote netip.Addr, localIsOrig, ok bool) {
	orig, resp := f.Orig.Src.Unmap(), f.Reply.Src.Unmap()
	co, cr := t.devices.class(orig), t.devices.class(resp)
	if (co == classRemote) == (cr == classRemote) {
		return "", remote, false, false
	}
	local, localClass, localPort := orig, co, f.Orig.SrcPort
	remote, localIsOrig = resp, true
	if co == classRemote {
		local, localClass, localPort = resp, cr, f.Reply.SrcPort
		remote, localIsOrig = orig, false
	}
	if localClass == classRouter {
		lp := t.devices.inv.WireGuard.ListenPort
		if f.Proto == 17 && lp != 0 && localPort == lp {
			return idWGTunnels, remote, localIsOrig, true
		}
		return idRouter, remote, localIsOrig, true
	}
	id, ok = t.devices.resolve(local)
	return id, remote, localIsOrig, ok
}

func (t *traffic) wanDelta() (Bytes, bool) {
	rx, err1 := readUint(t.wanRx)
	tx, err2 := readUint(t.wanTx)
	if err1 != nil || err2 != nil {
		t.wanPrevOK = false
		return Bytes{}, false
	}
	cur := Bytes{Rx: rx, Tx: tx}
	prev, ok := t.wanPrev, t.wanPrevOK
	t.wanPrev, t.wanPrevOK = cur, true
	if !ok {
		return Bytes{}, false
	}
	// A PPPoE reconnect recreates the interface with fresh counters.
	d := cur
	if cur.Rx >= prev.Rx {
		d.Rx = cur.Rx - prev.Rx
	}
	if cur.Tx >= prev.Tx {
		d.Tx = cur.Tx - prev.Tx
	}
	return d, true
}

func keyOf(f ports.Flow) flowKey { return flowKey{f.ID, f.Proto, f.Orig} }

// diff is the counter growth since prev; a counter that went backwards
// belongs to a new connection that reused the slot, so it counts in full.
func diff(prev, cur flowCounters) flowCounters {
	if cur.orig < prev.orig || cur.reply < prev.reply {
		return cur
	}
	return flowCounters{cur.orig - prev.orig, cur.reply - prev.reply}
}

func floorSub(a, b uint64) uint64 {
	if b >= a {
		return 0
	}
	return a - b
}

func readUint(path string) (uint64, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	return strconv.ParseUint(strings.TrimSpace(string(b)), 10, 64)
}
