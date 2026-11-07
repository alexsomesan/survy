package app

import (
	"sync"
	"time"
)

// liveWindow is how many one-second samples Live state keeps.
const liveWindow = 60

// IfaceRate is one interface's current rates in the Live event.
type IfaceRate struct {
	Name  string   `json:"name"`
	Role  string   `json:"role"`
	RxBps *float64 `json:"rxBps"`
	TxBps *float64 `json:"txBps"`
}

// WANInfo is the Live header's WAN section. Today's totals come from the
// History (see history.fillLive).
type WANInfo struct {
	IPv4         *string `json:"ipv4"`
	IPv6         *string `json:"ipv6"`
	UptimeSec    *int64  `json:"uptimeSec"`
	TodayRxBytes int64   `json:"todayRxBytes"`
	TodayTxBytes int64   `json:"todayTxBytes"`
}

// WANSeries is the last liveWindow one-second WAN rates, oldest first.
type WANSeries struct {
	Start  int64      `json:"start"`
	StepMs int64      `json:"stepMs"`
	RxBps  []*float64 `json:"rxBps"`
	TxBps  []*float64 `json:"txBps"`
}

// Disk is one partition's usage.
type Disk struct {
	Name       string `json:"name"`
	Path       string `json:"path"`
	UsedBytes  *int64 `json:"usedBytes"`
	TotalBytes *int64 `json:"totalBytes"`
}

// RouterResources is the Live event's resources section.
type RouterResources struct {
	CPUPct         *float64       `json:"cpuPct"`
	MemUsedBytes   *int64         `json:"memUsedBytes"`
	MemTotalBytes  *int64         `json:"memTotalBytes"`
	MemAvailBytes  *int64         `json:"memAvailBytes"`
	TempC          *float64       `json:"tempC"`
	ConntrackCount *int64         `json:"conntrackCount"`
	ConntrackMax   *int64         `json:"conntrackMax"`
	Load           []float64      `json:"load"`
	Disks          []Disk         `json:"disks"`
	Series         ResourceSeries `json:"series"`
}

// ResourceSeries is the last liveWindow one-second Router Resource gauges.
type ResourceSeries struct {
	Start        int64      `json:"start"`
	StepMs       int64      `json:"stepMs"`
	CPUPct       []*float64 `json:"cpuPct"`
	MemUsedBytes []*float64 `json:"memUsedBytes"`
	TempC        []*float64 `json:"tempC"`
	Conntrack    []*float64 `json:"conntrack"`
	Load1        []*float64 `json:"load1"`
}

// ContainerStat is one container's usage in the Live event.
type ContainerStat struct {
	Name          string   `json:"name"`
	CPUPct        *float64 `json:"cpuPct"`
	MemBytes      int64    `json:"memBytes"`
	MemLimitBytes int64    `json:"memLimitBytes"`
}

// SelfUsage is the dashboard's own usage. DataBytes is the History database
// size (see history.fillLive).
type SelfUsage struct {
	CPUPct    *float64 `json:"cpuPct"`
	MemBytes  *int64   `json:"memBytes"`
	DataBytes int64    `json:"dataBytes"`
}

// resourceSample is everything the per-second collector derived at one tick.
type resourceSample struct {
	at         time.Time
	ifaces     []IfaceRate
	wan        WANInfo
	resources  RouterResources // without Series
	containers []ContainerStat
	self       SelfUsage
}

// counters are cumulative readings kept from the previous tick for deltas.
type counters struct {
	at       time.Time
	ifaces   map[string][2]uint64 // name -> rx, tx bytes
	cpuBusy  uint64
	cpuTotal uint64
	cpuOK    bool
	ctrUsec  map[string]uint64 // container name -> cgroup usage_usec
	selfTick uint64
	selfOK   bool
}

// resources reads interface counters and Router Resources every second and
// keeps the last liveWindow samples in memory (Live state, never stored).
type resources struct {
	a *App

	mu   sync.Mutex
	prev counters
	ring [liveWindow]resourceSample // indexed by unix second mod liveWindow
	last resourceSample
	// pppdPid caches the pppd process found in /proc (0 = unknown); only
	// touched from the collection tick.
	pppdPid uint64
}

func newResources(a *App) *resources { return &resources{a: a} }

func (r *resources) ifaceRoles() [][2]string {
	s := r.a.cfg.Settings
	return [][2]string{{s.WANIface, roleWAN}, {s.LANIface, roleLAN}, {s.WGIface, roleWireGuard}}
}

// collect takes one reading at now and records the derived sample.
func (r *resources) collect(now time.Time) {
	fs := procFS{root: r.a.cfg.Root}
	cur := counters{at: now, ifaces: map[string][2]uint64{}}
	for _, ir := range r.ifaceRoles() {
		if rx, tx, ok := fs.ifaceBytes(ir[0]); ok {
			cur.ifaces[ir[0]] = [2]uint64{rx, tx}
		}
	}
	cur.cpuBusy, cur.cpuTotal, cur.cpuOK = fs.cpuJiffies()
	cur.selfTick, cur.selfOK = fs.selfCPUTicks()
	ctrs := fs.containers()
	cur.ctrUsec = map[string]uint64{}
	for _, c := range ctrs {
		if c.usageOK {
			cur.ctrUsec[c.name] = c.usageUsec
		}
	}

	s := resourceSample{at: now, wan: r.readWAN(fs), resources: r.readGauges(fs),
		self: SelfUsage{MemBytes: fs.selfRSS()}}

	r.mu.Lock()
	defer r.mu.Unlock()
	prev := r.prev
	dt := now.Sub(prev.at).Seconds()
	for _, ir := range r.ifaceRoles() {
		rate := IfaceRate{Name: ir[0], Role: ir[1]}
		c, okC := cur.ifaces[ir[0]]
		p, okP := prev.ifaces[ir[0]]
		if okC && okP && dt > 0 {
			rate.RxBps = counterRate(p[0], c[0], dt)
			rate.TxBps = counterRate(p[1], c[1], dt)
		}
		s.ifaces = append(s.ifaces, rate)
	}
	if cur.cpuOK && prev.cpuOK && cur.cpuTotal > prev.cpuTotal && cur.cpuBusy >= prev.cpuBusy {
		v := 100 * float64(cur.cpuBusy-prev.cpuBusy) / float64(cur.cpuTotal-prev.cpuTotal)
		s.resources.CPUPct = &v
	}
	s.containers = make([]ContainerStat, 0, len(ctrs))
	for _, c := range ctrs {
		st := ContainerStat{Name: c.name, MemBytes: c.memBytes, MemLimitBytes: c.memLimitBytes}
		p, okP := prev.ctrUsec[c.name]
		if c.usageOK && okP && dt > 0 && c.usageUsec >= p {
			v := 100 * float64(c.usageUsec-p) / 1e6 / dt
			st.CPUPct = &v
		}
		s.containers = append(s.containers, st)
	}
	if cur.selfOK && prev.selfOK && dt > 0 && cur.selfTick >= prev.selfTick {
		v := 100 * float64(cur.selfTick-prev.selfTick) / userHZ / dt
		s.self.CPUPct = &v
	}
	r.prev = cur
	r.last = s
	r.ring[slot(now)] = s
}

// readGauges reads the Router Resources that need no delta.
func (r *resources) readGauges(fs procFS) RouterResources {
	var g RouterResources
	if total, avail, ok := fs.meminfo(); ok {
		used := total - avail
		g.MemTotalBytes, g.MemAvailBytes, g.MemUsedBytes = &total, &avail, &used
	}
	g.TempC = fs.coretempC()
	if n, ok := fs.readInt("/proc/sys/net/netfilter/nf_conntrack_count"); ok {
		g.ConntrackCount = &n
	}
	if n, ok := fs.readInt("/proc/sys/net/netfilter/nf_conntrack_max"); ok {
		g.ConntrackMax = &n
	}
	g.Load = fs.loadavg()
	// The system partition is measured through the router's /boot (on the
	// same partition, mounted under Root); the data disk through DataDir.
	// Path is what the router calls them.
	for _, d := range [][3]string{
		{"system", "/", r.a.path("/boot")},
		{"data", r.a.cfg.Settings.DataHostPath, r.a.cfg.DataDir},
	} {
		disk := Disk{Name: d[0], Path: d[1]}
		if u, err := r.a.cfg.Kernel.Statfs(d[2]); err == nil {
			total, used := int64(u.Total), int64(u.Total-u.Free)
			disk.TotalBytes, disk.UsedBytes = &total, &used
		}
		g.Disks = append(g.Disks, disk)
	}
	return g
}

func (r *resources) readWAN(fs procFS) WANInfo {
	var w WANInfo
	iface := r.a.cfg.Settings.WANIface
	if addrs, err := r.a.cfg.Kernel.Addresses(); err == nil {
		for _, a := range addrs {
			ip := a.Prefix.Addr()
			if a.Iface != iface || !ip.IsGlobalUnicast() {
				continue
			}
			v := ip.String()
			if ip.Is4() && w.IPv4 == nil {
				w.IPv4 = &v
			} else if ip.Is6() && w.IPv6 == nil {
				w.IPv6 = &v
			}
		}
	}
	if w.IPv4 == nil && w.IPv6 == nil {
		return w // link down: pppd may be redialling, but there is no uptime
	}
	if up := fs.pidfileUptime("/run/" + iface + ".pid"); up != nil {
		w.UptimeSec = up
		return w
	}
	// No pid file (the container does not mount /run): find pppd in /proc,
	// remembering its pid so the scan only repeats after pppd restarts.
	if r.pppdPid == 0 || !fs.isPPPD(r.pppdPid, iface) {
		r.pppdPid, _ = fs.findPPPD(iface)
	}
	if r.pppdPid != 0 {
		w.UptimeSec = fs.pidUptime(r.pppdPid)
	}
	return w
}

// counterRate is bits/s between two byte counter readings, or nil when the
// counter went backwards (interface reset).
func counterRate(prev, cur uint64, dt float64) *float64 {
	if cur < prev {
		return nil
	}
	v := bps(float64(cur-prev), dt)
	return &v
}

// latest is the most recent sample.
func (r *resources) latest() resourceSample {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.last
}

func slot(t time.Time) int {
	s := t.Unix() % liveWindow
	if s < 0 {
		s += liveWindow
	}
	return int(s)
}

func seriesStart(now time.Time) int64 {
	return now.Truncate(time.Second).Add(-(liveWindow - 1) * time.Second).UnixMilli()
}

// window returns the samples for the liveWindow seconds ending at now,
// oldest first; ok[i] is false for seconds without a sample.
func (r *resources) window(now time.Time) (out [liveWindow]resourceSample, ok [liveWindow]bool, last resourceSample) {
	r.mu.Lock()
	defer r.mu.Unlock()
	end := now.Unix()
	for i := range out {
		sec := end - liveWindow + 1 + int64(i)
		s := r.ring[slot(time.Unix(sec, 0))]
		if !s.at.IsZero() && s.at.Unix() == sec {
			out[i], ok[i] = s, true
		}
	}
	return out, ok, r.last
}

func pick(w [liveWindow]resourceSample, ok [liveWindow]bool, get func(resourceSample) *float64) []*float64 {
	out := make([]*float64, liveWindow)
	for i := range w {
		if ok[i] {
			out[i] = get(w[i])
		}
	}
	return out
}

func intAsFloat(p *int64) *float64 {
	if p == nil {
		return nil
	}
	v := float64(*p)
	return &v
}

// fillLive writes the resources sections of the Live event.
func (r *resources) fillLive(ev *LiveEvent, now time.Time) {
	w, ok, cur := r.window(now)
	ev.LineRateBps = r.a.cfg.Settings.LineRateBps

	ev.Interfaces = cur.ifaces
	if len(ev.Interfaces) == 0 {
		for _, ir := range r.ifaceRoles() {
			ev.Interfaces = append(ev.Interfaces, IfaceRate{Name: ir[0], Role: ir[1]})
		}
	}
	ev.WAN = cur.wan

	wanRate := func(s resourceSample) *IfaceRate {
		if len(s.ifaces) == 0 {
			return &IfaceRate{}
		}
		return &s.ifaces[0]
	}
	start := seriesStart(now)
	ev.WANSeries = WANSeries{Start: start, StepMs: 1000,
		RxBps: pick(w, ok, func(s resourceSample) *float64 { return wanRate(s).RxBps }),
		TxBps: pick(w, ok, func(s resourceSample) *float64 { return wanRate(s).TxBps }),
	}

	ev.Self = cur.self
	ev.Containers = cur.containers
	if ev.Containers == nil {
		ev.Containers = []ContainerStat{}
	}
	ev.Resources = cur.resources
	if ev.Resources.Disks == nil {
		ev.Resources.Disks = []Disk{}
	}
	if ev.Resources.Load == nil {
		ev.Resources.Load = []float64{}
	}
	ev.Resources.Series = ResourceSeries{Start: start, StepMs: 1000,
		CPUPct:       pick(w, ok, func(s resourceSample) *float64 { return s.resources.CPUPct }),
		MemUsedBytes: pick(w, ok, func(s resourceSample) *float64 { return intAsFloat(s.resources.MemUsedBytes) }),
		TempC:        pick(w, ok, func(s resourceSample) *float64 { return s.resources.TempC }),
		Conntrack:    pick(w, ok, func(s resourceSample) *float64 { return intAsFloat(s.resources.ConntrackCount) }),
		Load1: pick(w, ok, func(s resourceSample) *float64 {
			if len(s.resources.Load) == 0 {
				return nil
			}
			v := s.resources.Load[0]
			return &v
		}),
	}
}
