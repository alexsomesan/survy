package app_test

import (
	"fmt"
	"net/http"
	"net/url"
	"testing"
	"time"

	"survy/internal/ports"
	"survy/internal/testkit"
)

type endpointRow struct {
	IP   string  `json:"ip"`
	Host *string `json:"host"`
	Org  *string `json:"org"`
	ASN  *int    `json:"asn"`
	Rx   float64 `json:"rx"`
	Tx   float64 `json:"tx"`
}

type deviceDetail struct {
	Device     ttDevice `json:"device"`
	Window     string   `json:"window"`
	Unit       string   `json:"unit"`
	Resolution string   `json:"resolution"`
	Series     struct {
		Start  int64      `json:"start"`
		StepMs int64      `json:"stepMs"`
		RxBps  []*float64 `json:"rxBps"`
		TxBps  []*float64 `json:"txBps"`
	} `json:"series"`
	Endpoints     []endpointRow `json:"endpoints"`
	EndpointsNote string        `json:"endpointsNote"`
}

// devicePath is how the frontend addresses a Device (encodeURIComponent).
func devicePath(id, window string) string {
	return "/api/devices/" + url.QueryEscape(id) + "?window=" + window
}

func getDevice(h *testkit.Harness, id, window string) deviceDetail {
	h.T().Helper()
	var d deviceDetail
	h.GetJSON(devicePath(id, window), &d)
	if len(d.Series.RxBps) != len(d.Series.TxBps) {
		h.T().Fatalf("%s %s: series lengths differ", id, window)
	}
	return d
}

// at is the series sample covering t (fails when outside).
func (d deviceDetail) at(t *testing.T, at time.Time) int {
	t.Helper()
	i := int((at.UnixMilli() - d.Series.Start) / d.Series.StepMs)
	if at.UnixMilli() < d.Series.Start || i >= len(d.Series.RxBps) {
		t.Fatalf("%s: %v outside series", d.Window, at)
	}
	return i
}

func val(p *float64) any {
	if p == nil {
		return nil
	}
	return *p
}

// advanceWatched moves the clock to t one second at a time (conntrack is
// polled every second while the Live client is connected).
func advanceWatched(h *testkit.Harness, t time.Time) { h.Advance(t.Sub(h.Clock.Now())) }

func TestDeviceDetailSeriesUsesOneMinuteSamplesForOneHourAndOneDay(t *testing.T) {
	tk := newTalkers(t)
	h := tk.h
	lr := "mac:" + copperMAC
	at := testkit.Start.Add(5*time.Minute + 30*time.Second)
	advanceWatched(h, at.Add(-time.Second))
	h.Kernel.SetFlow(snat(1, copper4, google, 60_000, 600_000))
	advanceWatched(h, testkit.Start.Add(10*time.Minute))

	for _, c := range []struct {
		window string
		n      int
	}{{"1h", 60}, {"24h", 1440}} {
		d := getDevice(h, lr, c.window)
		if d.Window != c.window || d.Unit != "bytes" || d.Resolution != "1m" || d.Series.StepMs != 60_000 || len(d.Series.RxBps) != c.n {
			t.Fatalf("%s: window %q unit %q resolution %q step %d len %d", c.window, d.Window, d.Unit, d.Resolution, d.Series.StepMs, len(d.Series.RxBps))
		}
		if d.Device.ID != lr || d.Device.Name != "copper" || d.Device.Rx != 600_000 || d.Device.Tx != 60_000 {
			t.Fatalf("%s: device = %+v", c.window, d.Device)
		}
		i := d.at(t, at)
		// 600 kB in a fully polled minute = 80 kbit/s.
		if v := d.Series.RxBps[i]; v == nil || *v != 80_000 {
			t.Errorf("%s: rx at 01:05 = %v, want 80000", c.window, val(v))
		}
		if v := d.Series.TxBps[i]; v == nil || *v != 8_000 {
			t.Errorf("%s: tx at 01:05 = %v, want 8000", c.window, val(v))
		}
		if v := d.Series.RxBps[i+1]; v == nil || *v != 0 {
			t.Errorf("%s: rx at a quiet minute = %v, want 0", c.window, val(v))
		}
		if v := d.Series.RxBps[d.at(t, testkit.Start.Add(-time.Minute))]; v != nil {
			t.Errorf("%s: rx before the app started = %v, want null", c.window, *v)
		}
	}
}

func TestDeviceDetailSevenAndThirtyDaysUseOneMinuteDataSummedIntoAbout1500Points(t *testing.T) {
	tk := newTalkers(t)
	h := tk.h
	lr := "mac:" + copperMAC
	at := testkit.Start.Add(30*time.Minute + 30*time.Second)
	advanceWatched(h, at.Add(-time.Second))
	h.Kernel.SetFlow(snat(1, copper4, google, 420_000, 4_200_000))
	advanceWatched(h, testkit.Start.Add(2*time.Hour))

	for _, c := range []struct {
		window string
		stepMs int64
		rxBps  float64 // 4.2 MB over a fully polled step
	}{{"7d", 7 * 60_000, 80_000}, {"30d", 29 * 60_000, 4_200_000 * 8 / 1740.0}} {
		d := getDevice(h, lr, c.window)
		n := int64(len(d.Series.RxBps))
		if d.Resolution != "1m" || d.Series.StepMs != c.stepMs || n > 1500 {
			t.Fatalf("%s: resolution %q step %d len %d", c.window, d.Resolution, d.Series.StepMs, n)
		}
		now := h.Clock.Now().UnixMilli()
		span := map[string]int64{"7d": 7 * 86_400_000, "30d": 30 * 86_400_000}[c.window]
		if end := d.Series.Start + n*d.Series.StepMs; d.Series.Start > now-span+d.Series.StepMs || end <= now || end > now+d.Series.StepMs {
			t.Fatalf("%s: series %d..%d does not cover %d..%d to within a step", c.window, d.Series.Start, end, now-span, now)
		}
		if d.Device.Rx != 4_200_000 || d.Device.Tx != 420_000 {
			t.Fatalf("%s: device = %+v", c.window, d.Device)
		}
		i := d.at(t, at)
		if v := d.Series.RxBps[i]; v == nil || *v != c.rxBps {
			t.Errorf("%s: rx at 01:30 = %v, want %v", c.window, val(v), c.rxBps)
		}
		if v := d.Series.RxBps[i+1]; v == nil || *v != 0 {
			t.Errorf("%s: rx after = %v, want 0", c.window, val(v))
		}
		if v := d.Series.RxBps[d.at(t, testkit.Start.Add(-time.Hour))]; v != nil {
			t.Errorf("%s: rx before the app started = %v, want null", c.window, *v)
		}
	}
}

func TestDeviceDetailLiveIsTheLastMinuteOfRatesFromMemory(t *testing.T) {
	tk := newTalkers(t)
	h := tk.h
	lr := "mac:" + copperMAC
	h.Kernel.SetFlow(snat(1, copper4, google, 0, 0))
	tk.second()
	h.Kernel.SetFlow(snat(1, copper4, google, 2_000, 250_000))
	h.Kernel.SetFlow(snat(2, copper4, cloudflare, 0, 125_000))
	tk.second()

	d := getDevice(h, lr, "live")

	if d.Window != "live" || d.Unit != "bps" || d.Series.StepMs != 1000 || len(d.Series.RxBps) != 60 {
		t.Fatalf("window %q unit %q step %d len %d", d.Window, d.Unit, d.Series.StepMs, len(d.Series.RxBps))
	}
	now := h.Clock.Now()
	if d.Series.Start != now.Add(-59*time.Second).UnixMilli() {
		t.Fatalf("start = %d, want now-59s", d.Series.Start)
	}
	if v := d.Series.RxBps[59]; v == nil || *v != 3_000_000 {
		t.Errorf("rx now = %v, want 3000000", val(v))
	}
	if v := d.Series.TxBps[59]; v == nil || *v != 16_000 {
		t.Errorf("tx now = %v, want 16000", val(v))
	}
	if v := d.Series.RxBps[58]; v == nil || *v != 0 {
		t.Errorf("rx a second ago = %v, want 0", val(v))
	}
	if v := d.Series.RxBps[0]; v != nil {
		t.Errorf("rx before the app started = %v, want null", *v)
	}
	if d.Device.Rx != 3_000_000 || d.Device.Tx != 16_000 || d.Device.Name != "copper" {
		t.Errorf("device = %+v", d.Device)
	}
	if len(d.Endpoints) != 2 || d.Endpoints[0].IP != google || d.Endpoints[0].Rx != 2_000_000 ||
		d.Endpoints[1].IP != cloudflare || d.Endpoints[1].Rx != 1_000_000 {
		t.Errorf("endpoints = %+v", d.Endpoints)
	}
}

const cloudflare = "104.16.132.229"

func remote(i int) string { return fmt.Sprintf("198.51.100.%d", i) }

func TestDeviceDetailKeepsTheTopFiftyRemoteEndpointsPerHour(t *testing.T) {
	tk := newTalkers(t)
	h := tk.h
	lr := "mac:" + copperMAC
	// First hour: 60 Remote Endpoints, endpoint i receiving i kB.
	for i := 1; i <= 60; i++ {
		h.Kernel.SetFlow(snat(uint32(100+i), copper4, remote(i), 0, uint64(i)*1000))
	}
	h.FastForward(time.Hour, time.Minute)
	// Second hour: endpoint 1 (dropped from the first hour) receives 1 MB.
	h.Kernel.SetFlow(snat(101, copper4, remote(1), 0, 1_001_000))
	h.FastForward(10*time.Minute, time.Minute)

	d := getDevice(h, lr, "24h")

	if d.EndpointsNote != "hourly, kept 30 days" {
		t.Errorf("note = %q", d.EndpointsNote)
	}
	if len(d.Endpoints) != 50 {
		t.Fatalf("%d endpoints, want 50", len(d.Endpoints))
	}
	// Endpoint 1's first-hour kilobyte was not kept.
	if e := d.Endpoints[0]; e.IP != remote(1) || e.Rx != 1_000_000 {
		t.Errorf("first = %+v, want %s with 1000000", e, remote(1))
	}
	for i := 1; i < 50; i++ {
		e := d.Endpoints[i]
		if want := remote(61 - i); e.IP != want || e.Rx != float64(61-i)*1000 {
			t.Errorf("endpoint %d = %+v, want %s", i, e, want)
		}
		if e.Host != nil || e.Org != nil || e.ASN != nil {
			t.Errorf("endpoint %d labels = %v/%v/%v, want null", i, e.Host, e.Org, e.ASN)
		}
	}
}

func TestDeviceDetailEndpointsStayForThirtyDays(t *testing.T) {
	tk := newTalkers(t)
	h := tk.h
	lr := "mac:" + copperMAC
	h.Kernel.SetFlow(snat(1, copper4, google, 1_000, 50_000))
	h.FastForward(time.Hour, time.Minute)

	h.FastForward(29*24*time.Hour+22*time.Hour, time.Hour)
	if d := getDevice(h, lr, "30d"); len(d.Endpoints) != 1 || d.Endpoints[0].IP != google || d.Endpoints[0].Rx != 50_000 {
		t.Fatalf("after 29 d 23 h: endpoints = %+v", d.Endpoints)
	}

	h.FastForward(2*time.Hour, time.Hour)
	d := getDevice(h, lr, "30d")
	if len(d.Endpoints) != 0 || d.Device.Rx != 0 {
		t.Fatalf("after 30 d: endpoints = %+v, device = %+v", d.Endpoints, d.Device)
	}
}

func TestDeviceDetailUnknownDeviceIs404AndUnknownWindowIs400(t *testing.T) {
	tk := newTalkers(t)
	h := tk.h
	h.Kernel.SetFlow(snat(1, copper4, google, 1, 1))
	tk.second()

	if code, b := h.Get(devicePath("mac:00:11:22:70:2a:02", "24h")); code != http.StatusNotFound {
		t.Errorf("unknown device = %d %s, want 404", code, b)
	}
	for _, w := range []string{"2h", "1y", ""} {
		if code, b := h.Get(devicePath("mac:"+copperMAC, w)); code != http.StatusBadRequest {
			t.Errorf("window %q = %d %s, want 400", w, code, b)
		}
	}
}

func TestDeviceDetailAcceptsURLEncodedWireGuardKeys(t *testing.T) {
	tk := newTalkers(t)
	h := tk.h
	h.WriteData("inventory.json", []byte(inventoryJSON))
	h.Kernel.SetFlow(direct(1, laptopWG, google, 1_000, 2_000))
	tk.second()

	path := devicePath(laptopPeer, "1h")
	if path != "/api/devices/wg%3Apk%2FLaptop%2BKey%3D?window=1h" {
		t.Fatalf("path = %s", path)
	}
	d := getDevice(h, laptopPeer, "1h")
	if d.Device.ID != laptopPeer || d.Device.Name != "Laptop" || d.Device.Kind != "wg" || d.Device.Rx != 2_000 {
		t.Fatalf("device = %+v", d.Device)
	}
}

func TestOfflineDeviceKeepsItsNameAfterARestart(t *testing.T) {
	tk := newTalkers(t)
	h := tk.h
	lr := "mac:" + copperMAC
	h.Kernel.SetFlow(snat(1, copper4, google, 1_000, 50_000))
	tk.second()

	// The Device leaves: its lease, neighbour entries and flows are gone.
	h.Kernel.ClearFlows()
	h.Kernel.SetNeighbours([]ports.Neighbour{})
	h.WriteFile("/config/dhcp/dhcp4-leases.csv", "address,hwaddr,client_id,valid_lifetime,expire,subnet_id,fqdn_fwd,fqdn_rev,hostname,state,user_context,pool_id\n")
	h.RestartAfter(2 * time.Hour)
	h.Advance(time.Minute)

	d := getTalkers(h, "24h").device(t, lr)
	if d.Name != "copper" || d.Kind != "lan" || d.Rx != 50_000 {
		t.Fatalf("top talkers device = %+v", d)
	}
	if len(d.Addresses) != 2 || d.Addresses[0] != copper4 || d.Addresses[1] != copper6 {
		t.Fatalf("addresses = %v, want last known", d.Addresses)
	}
	if d.MAC == nil || *d.MAC != copperMAC || d.Vendor == nil || *d.Vendor != "Ubiquiti" {
		t.Fatalf("mac/vendor = %v/%v", d.MAC, d.Vendor)
	}
	if dd := getDevice(h, lr, "24h"); dd.Device.Name != "copper" {
		t.Fatalf("detail name = %q", dd.Device.Name)
	}
}
