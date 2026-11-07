package app_test

import (
	"strconv"
	"strings"
	"testing"
	"time"

	"survy/internal/testkit"
)

type liveIface struct {
	Name  string
	Role  string
	RxBps *float64
	TxBps *float64
}

type liveEvent struct {
	Time        int64
	LineRateBps float64
	Interfaces  []liveIface
}

func addCounter(h *testkit.Harness, path string, delta int64) {
	h.T().Helper()
	n, err := strconv.ParseInt(strings.TrimSpace(h.ReadFile(path)), 10, 64)
	if err != nil {
		h.T().Fatal(err)
	}
	h.WriteFile(path, strconv.FormatInt(n+delta, 10)+"\n")
}

func deref(p *float64) any {
	if p == nil {
		return nil
	}
	return *p
}

func TestLiveInterfaceRatesFromCounterDeltas(t *testing.T) {
	h := testkit.New(t)
	s := h.OpenLive()
	var snap liveEvent
	s.Next(&snap)

	addCounter(h, "/sys/class/net/pppoe0/statistics/rx_bytes", 12_500_000) // 100 Mbit
	addCounter(h, "/sys/class/net/pppoe0/statistics/tx_bytes", 1_250_000)  // 10 Mbit
	addCounter(h, "/sys/class/net/br0/statistics/tx_bytes", 2_500_000)     // 20 Mbit
	addCounter(h, "/sys/class/net/wg0/statistics/rx_bytes", 125)           // 1000 bit
	h.Advance(time.Second)

	var ev liveEvent
	s.Next(&ev)
	if ev.LineRateBps != 1e9 {
		t.Errorf("lineRateBps = %v, want 1e9", ev.LineRateBps)
	}
	want := []struct {
		name, role string
		rx, tx     float64
	}{
		{"pppoe0", "wan", 100e6, 10e6},
		{"br0", "lan", 0, 20e6},
		{"wg0", "wireguard", 1000, 0},
	}
	if len(ev.Interfaces) != len(want) {
		t.Fatalf("interfaces = %+v", ev.Interfaces)
	}
	for i, w := range want {
		got := ev.Interfaces[i]
		if got.Name != w.name || got.Role != w.role || got.RxBps == nil || got.TxBps == nil ||
			*got.RxBps != w.rx || *got.TxBps != w.tx {
			t.Errorf("interface %d = %s/%s %v/%v, want %+v", i, got.Name, got.Role, deref(got.RxBps), deref(got.TxBps), w)
		}
	}
}

type wanHeader struct {
	IPv4         *string
	IPv6         *string
	UptimeSec    *int64
	TodayRxBytes *int64
	TodayTxBytes *int64
}

func TestLiveWANHeaderShowsPublicAddressesAndPPPUptime(t *testing.T) {
	h := testkit.New(t)
	var ev struct{ Wan wanHeader }
	h.OpenLive().Next(&ev)
	w := ev.Wan
	if w.IPv4 == nil || *w.IPv4 != "203.0.113.176" {
		t.Errorf("ipv4 = %v", w.IPv4)
	}
	// The global address, not the link-local one.
	if w.IPv6 == nil || *w.IPv6 != "2001:db8:ff:1:e49b:f0d:46c9:1fe" {
		t.Errorf("ipv6 = %v", w.IPv6)
	}
	// /proc/uptime 366111.51 s; pppd (pid 4003) started 4084 ticks = 40.84 s after boot.
	if w.UptimeSec == nil || *w.UptimeSec != 366070 {
		t.Errorf("uptimeSec = %v, want 366070", w.UptimeSec)
	}
	if w.TodayRxBytes == nil || w.TodayTxBytes == nil {
		t.Errorf("today totals must be present")
	}
}

// In the container /run is not mounted (it holds the podman socket), so the
// pppd pid file is missing; uptime then comes from finding pppd in /proc.
func TestLiveWANUptimeFindsPPPDWithoutPidFile(t *testing.T) {
	h := testkit.New(t)
	h.RemoveFile("/run/pppoe0.pid")
	h.Advance(time.Second)
	var ev struct{ Wan wanHeader }
	h.OpenLive().Next(&ev)
	if ev.Wan.UptimeSec == nil || *ev.Wan.UptimeSec != 366070 {
		t.Errorf("uptimeSec = %v, want 366070", ev.Wan.UptimeSec)
	}
}

func TestLiveWANHeaderIsNullWhenLinkIsDown(t *testing.T) {
	h := testkit.New(t)
	h.Kernel.SetAddresses(nil)
	h.WriteFile("/run/pppoe0.pid", "")
	h.Advance(time.Second)
	var ev struct{ Wan wanHeader }
	h.OpenLive().Next(&ev)
	if ev.Wan.IPv4 != nil || ev.Wan.IPv6 != nil || ev.Wan.UptimeSec != nil {
		t.Errorf("wan = %v %v %v, want all null", ev.Wan.IPv4, ev.Wan.IPv6, ev.Wan.UptimeSec)
	}
}

type disk struct {
	Name       string
	Path       string
	UsedBytes  int64
	TotalBytes int64
}

type resourcesSection struct {
	CPUPct         *float64
	MemUsedBytes   *int64
	MemTotalBytes  *int64
	MemAvailBytes  *int64
	TempC          *float64
	ConntrackCount *int64
	ConntrackMax   *int64
	Load           []float64
	Disks          []disk
	Series         struct {
		Start        int64
		StepMs       int64
		CPUPct       []*float64
		MemUsedBytes []*float64
		TempC        []*float64
		Conntrack    []*float64
		Load1        []*float64
	}
}

// setCPU rewrites the aggregate cpu line of /proc/stat.
func setCPU(h *testkit.Harness, user, system, idle, iowait int) {
	h.WriteFile("/proc/stat", "cpu  "+strconv.Itoa(user)+" 0 "+strconv.Itoa(system)+" "+
		strconv.Itoa(idle)+" "+strconv.Itoa(iowait)+" 0 0 0 0 0\ncpu0 0 0 0 0 0 0 0 0 0 0\n")
}

func TestLiveRouterResources(t *testing.T) {
	h := testkit.New(t)
	setCPU(h, 1000, 500, 8000, 100)
	h.Advance(time.Second)
	// 100 jiffies elapse: 30 user + 10 system busy, 55 idle + 5 iowait.
	setCPU(h, 1030, 510, 8055, 105)
	h.Advance(time.Second)

	var ev struct{ Resources resourcesSection }
	h.OpenLive().Next(&ev)
	r := ev.Resources
	if r.CPUPct == nil || *r.CPUPct != 40 {
		t.Errorf("cpuPct = %v, want 40", deref(r.CPUPct))
	}
	// meminfo: MemTotal 8066224 kB, MemAvailable 5352732 kB.
	if r.MemTotalBytes == nil || *r.MemTotalBytes != 8066224*1024 {
		t.Errorf("memTotalBytes = %v", r.MemTotalBytes)
	}
	if r.MemAvailBytes == nil || *r.MemAvailBytes != 5352732*1024 {
		t.Errorf("memAvailBytes = %v", r.MemAvailBytes)
	}
	if r.MemUsedBytes == nil || *r.MemUsedBytes != (8066224-5352732)*1024 {
		t.Errorf("memUsedBytes = %v", r.MemUsedBytes)
	}
	// coretemp's "Package id 0" is 40000 millidegrees; the nvme sensor is ignored.
	if r.TempC == nil || *r.TempC != 40 {
		t.Errorf("tempC = %v, want 40", deref(r.TempC))
	}
	if r.ConntrackCount == nil || *r.ConntrackCount != 240 || r.ConntrackMax == nil || *r.ConntrackMax != 262144 {
		t.Errorf("conntrack = %v/%v, want 240/262144", r.ConntrackCount, r.ConntrackMax)
	}
	if len(r.Load) != 3 || r.Load[0] != 0.11 || r.Load[1] != 0.03 || r.Load[2] != 0.01 {
		t.Errorf("load = %v", r.Load)
	}
	// Paths are router-side; the system partition is measured through the
	// router's /boot (mounted under Root), the data disk through DataDir.
	wantDisks := []disk{
		{"system", "/", (5 << 30) - (2800 << 20), 5 << 30},
		{"data", "/mnt/dash-data", (10 << 30) - (9 << 30), 10 << 30},
	}
	if len(r.Disks) != 2 || r.Disks[0] != wantDisks[0] || r.Disks[1] != wantDisks[1] {
		t.Errorf("disks = %+v, want %+v", r.Disks, wantDisks)
	}

	sr := r.Series
	if sr.StepMs != 1000 || len(sr.CPUPct) != 60 || len(sr.MemUsedBytes) != 60 || len(sr.TempC) != 60 ||
		len(sr.Conntrack) != 60 || len(sr.Load1) != 60 {
		t.Fatalf("series shape wrong: %+v", sr)
	}
	if v := sr.CPUPct[59]; v == nil || *v != 40 {
		t.Errorf("cpu series last = %v, want 40", deref(v))
	}
	if sr.CPUPct[57] != nil {
		t.Errorf("cpu series before baseline = %v, want null", deref(sr.CPUPct[57]))
	}
	if v := sr.Conntrack[59]; v == nil || *v != 240 {
		t.Errorf("conntrack series last = %v", deref(v))
	}
	if v := sr.Load1[59]; v == nil || *v != 0.11 {
		t.Errorf("load1 series last = %v", deref(v))
	}
	if v := sr.TempC[59]; v == nil || *v != 40 {
		t.Errorf("temp series last = %v", deref(v))
	}
	if v := sr.MemUsedBytes[59]; v == nil || *v != (8066224-5352732)*1024 {
		t.Errorf("mem series last = %v", deref(v))
	}
}

func TestTemperatureFindsCoretempWhereverItIsNumbered(t *testing.T) {
	h := testkit.New(t)
	h.WriteFile("/sys/class/hwmon/hwmon3/name", "acpitz\n")
	h.WriteFile("/sys/class/hwmon/hwmon7/name", "coretemp\n")
	h.WriteFile("/sys/class/hwmon/hwmon7/temp1_label", "Core 0\n")
	h.WriteFile("/sys/class/hwmon/hwmon7/temp1_input", "50000\n")
	h.WriteFile("/sys/class/hwmon/hwmon7/temp2_label", "Package id 0\n")
	h.WriteFile("/sys/class/hwmon/hwmon7/temp2_input", "55500\n")
	h.Advance(time.Second)
	var ev struct{ Resources resourcesSection }
	h.OpenLive().Next(&ev)
	if v := ev.Resources.TempC; v == nil || *v != 55.5 {
		t.Errorf("tempC = %v, want 55.5", deref(v))
	}
}

func TestTemperatureIsNullWithoutCoretemp(t *testing.T) {
	h := testkit.New(t)
	h.WriteFile("/sys/class/hwmon/hwmon3/name", "acpitz\n")
	h.Advance(time.Second)
	var ev struct{ Resources resourcesSection }
	h.OpenLive().Next(&ev)
	if v := ev.Resources.TempC; v != nil {
		t.Errorf("tempC = %v, want null", *v)
	}
}

type container struct {
	Name          string
	CPUPct        *float64
	MemBytes      int64
	MemLimitBytes int64
}

func TestLiveContainersFromCgroups(t *testing.T) {
	h := testkit.New(t)
	const cg = "/sys/fs/cgroup/system.slice/"
	h.WriteFile(cg+"vyos-container-airprint.service/memory.max", "268435456\n")
	h.WriteFile(cg+"vyos-container-unifi-db.service/cpu.stat", "usage_usec 1000000\nuser_usec 0\n")
	h.Advance(time.Second)
	// 350 ms of CPU time in one second.
	h.WriteFile(cg+"vyos-container-unifi-db.service/cpu.stat", "usage_usec 1350000\nuser_usec 0\n")
	h.Advance(time.Second)

	var ev struct{ Containers []container }
	h.OpenLive().Next(&ev)
	names := []string{}
	for _, c := range ev.Containers {
		names = append(names, c.Name)
	}
	want := "airprint unifi-db unifi-network-application vymanager-backend vymanager-frontend vymanager-postgres"
	if strings.Join(names, " ") != want {
		t.Fatalf("containers = %v, want %s", names, want)
	}
	air, db := ev.Containers[0], ev.Containers[1]
	if air.MemBytes != 30576640 || air.MemLimitBytes != 268435456 {
		t.Errorf("airprint mem = %d/%d", air.MemBytes, air.MemLimitBytes)
	}
	if air.CPUPct == nil || *air.CPUPct != 0 {
		t.Errorf("airprint cpu = %v, want 0", deref(air.CPUPct))
	}
	if db.CPUPct == nil || *db.CPUPct != 35 {
		t.Errorf("unifi-db cpu = %v, want 35", deref(db.CPUPct))
	}
	// The service cgroup says "max"; podman puts the limit on the payload child.
	if db.MemBytes != 922124288 || db.MemLimitBytes != 2147483648 {
		t.Errorf("unifi-db mem = %d/%d, want the payload's limit 2147483648", db.MemBytes, db.MemLimitBytes)
	}
	if be := ev.Containers[3]; be.Name != "vymanager-backend" || be.MemLimitBytes != 0 {
		t.Errorf("vymanager-backend = %+v, want limit 0 for max", be)
	}
}

func selfStat(utime, stime int) string {
	return "77 (survy) S 1 77 77 0 -1 4194560 100 0 0 0 " + strconv.Itoa(utime) + " " + strconv.Itoa(stime) +
		" 0 0 20 0 8 0 3000 900000000 9000 18446744073709551615 1 1 0 0 0 0 0 0 0 0 0 0 17 1 0 0 0 0 0\n"
}

func TestLiveSelfUsage(t *testing.T) {
	h := testkit.New(t)
	h.WriteFile("/proc/self/stat", selfStat(100, 50))
	h.WriteFile("/proc/self/status", "Name:\tsurvy\nVmPeak:\t  900000 kB\nVmRSS:\t   40000 kB\n")
	h.Advance(time.Second)
	// 3 user + 2 system ticks in one second: 5% of one core.
	h.WriteFile("/proc/self/stat", selfStat(103, 52))
	h.Advance(time.Second)

	var ev struct {
		Self struct {
			CPUPct    *float64
			MemBytes  *int64
			DataBytes *int64
		}
	}
	h.OpenLive().Next(&ev)
	if v := ev.Self.CPUPct; v == nil || *v != 5 {
		t.Errorf("self cpuPct = %v, want 5", deref(v))
	}
	if v := ev.Self.MemBytes; v == nil || *v != 40000*1024 {
		t.Errorf("self memBytes = %v", v)
	}
	if ev.Self.DataBytes == nil {
		t.Errorf("self dataBytes must be present")
	}
}

func TestLiveSnapshotHasEveryDocumentedKeyBeforeFirstTick(t *testing.T) {
	h := testkit.New(t)
	var ev map[string]any
	h.OpenLive().Next(&ev)

	need := func(obj map[string]any, where string, keys ...string) {
		t.Helper()
		for _, k := range keys {
			if _, ok := obj[k]; !ok {
				t.Errorf("%s: missing key %q", where, k)
			}
		}
	}
	need(ev, "event", "time", "lineRateBps", "interfaces", "wan", "wanSeries", "resources", "containers", "self")
	need(ev["wan"].(map[string]any), "wan", "ipv4", "ipv6", "uptimeSec", "todayRxBytes", "todayTxBytes")
	need(ev["wanSeries"].(map[string]any), "wanSeries", "start", "stepMs", "rxBps", "txBps")
	res := ev["resources"].(map[string]any)
	need(res, "resources", "cpuPct", "memUsedBytes", "memTotalBytes", "memAvailBytes", "tempC",
		"conntrackCount", "conntrackMax", "load", "disks", "series")
	need(res["series"].(map[string]any), "resources.series", "start", "stepMs", "cpuPct", "memUsedBytes", "tempC", "conntrack", "load1")
	need(res["disks"].([]any)[0].(map[string]any), "disk", "name", "path", "usedBytes", "totalBytes")
	need(ev["self"].(map[string]any), "self", "cpuPct", "memBytes", "dataBytes")
	need(ev["containers"].([]any)[0].(map[string]any), "container", "name", "cpuPct", "memBytes", "memLimitBytes")
	iface := ev["interfaces"].([]any)[0].(map[string]any)
	need(iface, "interface", "name", "role", "rxBps", "txBps")
	if iface["rxBps"] != nil {
		t.Errorf("rate before any delta = %v, want null", iface["rxBps"])
	}
}

type series struct {
	Start  int64
	StepMs int64
	RxBps  []*float64
	TxBps  []*float64
}

func TestLiveConnectBackfillsLast60SecondsOfWAN(t *testing.T) {
	h := testkit.New(t)
	h.Advance(time.Second)                                                // second 1: no traffic
	addCounter(h, "/sys/class/net/pppoe0/statistics/rx_bytes", 1_000_000) // 8 Mbit
	h.Advance(time.Second)
	addCounter(h, "/sys/class/net/pppoe0/statistics/tx_bytes", 500_000) // 4 Mbit
	h.Advance(time.Second)

	s := h.OpenLive()
	var ev struct{ WanSeries series }
	s.Next(&ev)
	ws := ev.WanSeries
	now := testkit.Start.Add(3 * time.Second)
	if ws.StepMs != 1000 || ws.Start != now.Add(-59*time.Second).UnixMilli() {
		t.Fatalf("series start/step = %d/%d, want %d/1000", ws.Start, ws.StepMs, now.Add(-59*time.Second).UnixMilli())
	}
	if len(ws.RxBps) != 60 || len(ws.TxBps) != 60 {
		t.Fatalf("series lengths = %d/%d, want 60", len(ws.RxBps), len(ws.TxBps))
	}
	for i := 0; i < 57; i++ {
		if ws.RxBps[i] != nil || ws.TxBps[i] != nil {
			t.Fatalf("sample %d = %v/%v, want null before collection started", i, deref(ws.RxBps[i]), deref(ws.TxBps[i]))
		}
	}
	wantRx := []float64{0, 8e6, 0}
	wantTx := []float64{0, 0, 4e6}
	for i := 0; i < 3; i++ {
		rx, tx := ws.RxBps[57+i], ws.TxBps[57+i]
		if rx == nil || tx == nil || *rx != wantRx[i] || *tx != wantTx[i] {
			t.Errorf("sample %d = %v/%v, want %v/%v", 57+i, deref(rx), deref(tx), wantRx[i], wantTx[i])
		}
	}

	// Seventy seconds later the window has slid: still 60 samples, all filled.
	h.Advance(70 * time.Second)
	var later struct{ WanSeries series }
	h.OpenLive().Next(&later)
	if len(later.WanSeries.RxBps) != 60 || later.WanSeries.RxBps[0] == nil {
		t.Fatalf("after 70s: len %d, first %v", len(later.WanSeries.RxBps), deref(later.WanSeries.RxBps[0]))
	}
}
