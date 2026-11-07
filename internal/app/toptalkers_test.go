package app_test

import (
	"net/netip"
	"testing"
	"time"

	"survy/internal/ports"
	"survy/internal/testkit"
)

// Addresses from the recorded router tables (testdata/kernel).
const (
	wanIP       = "203.0.113.176"
	wanIP6      = "2001:db8:ff:1:e49b:f0d:46c9:1fe"
	google      = "35.186.224.10"
	cloudflare6 = "2606:4700::6810:84e5"
	// copper: Ubiquiti, DHCP hostname "copper", v4 + EUI-64 v6.
	copperMAC = "28:70:4e:9e:1a:aa"
	copper4   = "192.168.2.109"
	copper6   = "2001:db8:1:1:2a70:4eff:fe9e:1aaa"
	// An Apple laptop with DHCP hostname "maple" and two privacy v6 addresses.
	guestMAC = "14:10:9f:25:9f:2e"
	oma4     = "192.168.2.163"
	omaPriv1 = "2001:db8:1:1:f9dd:a95a:92a5:f06d"
	omaPriv2 = "2001:db8:1:1:b432:4cc7:33e4:51c1"
)

type ttDevice struct {
	ID        string   `json:"id"`
	Name      string   `json:"name"`
	Kind      string   `json:"kind"`
	MAC       *string  `json:"mac"`
	Vendor    *string  `json:"vendor"`
	Addresses []string `json:"addresses"`
	Rx        float64  `json:"rx"`
	Tx        float64  `json:"tx"`
}

type liveTopTalkers struct {
	TopTalkers struct {
		Window       string     `json:"window"`
		Unit         string     `json:"unit"`
		WanRx        float64    `json:"wanRx"`
		WanTx        float64    `json:"wanTx"`
		Devices      []ttDevice `json:"devices"`
		Unattributed struct {
			Rx float64 `json:"rx"`
			Tx float64 `json:"tx"`
		} `json:"unattributed"`
	} `json:"topTalkers"`
}

func (l liveTopTalkers) device(t *testing.T, id string) ttDevice {
	t.Helper()
	for _, d := range l.TopTalkers.Devices {
		if d.ID == id {
			return d
		}
	}
	t.Fatalf("device %q not in top talkers: %+v", id, l.TopTalkers.Devices)
	return ttDevice{}
}

func (l liveTopTalkers) has(id string) bool {
	for _, d := range l.TopTalkers.Devices {
		if d.ID == id {
			return true
		}
	}
	return false
}

// talkers is a dashboard on the recorded router tables with no flows, a Live
// client connected and the first (baseline) conntrack poll done.
type talkers struct {
	t *testing.T
	h *testkit.Harness
	s *testkit.LiveStream
}

func newTalkers(t *testing.T, opts ...testkit.Option) *talkers {
	t.Helper()
	h := testkit.New(t, opts...)
	h.Kernel.ClearFlows()
	return startTalkers(t, h)
}

func startTalkers(t *testing.T, h *testkit.Harness) *talkers {
	t.Helper()
	s := h.OpenLive()
	var snap liveTopTalkers
	s.Next(&snap)
	tk := &talkers{t: t, h: h, s: s}
	tk.second()
	return tk
}

// second advances one second and returns that second's Live Top Talkers.
func (tk *talkers) second() liveTopTalkers {
	tk.t.Helper()
	tk.h.Advance(time.Second)
	var ev liveTopTalkers
	tk.s.Next(&ev)
	return ev
}

func addr(s string) netip.Addr { return netip.MustParseAddr(s) }

// flow builds a conntrack entry: orig is what the originator sent, reply what
// came back. replySrc/replyDst are the reply tuple (after NAT).
func flow(id uint32, proto uint8, src string, sport uint16, dst string, dport uint16,
	replySrc, replyDst string, orig, reply uint64) ports.Flow {
	return ports.Flow{
		ID: id, Proto: proto,
		Orig:      ports.Tuple{Src: addr(src), Dst: addr(dst), SrcPort: sport, DstPort: dport},
		Reply:     ports.Tuple{Src: addr(replySrc), Dst: addr(replyDst), SrcPort: dport, DstPort: sport},
		OrigBytes: orig, ReplyBytes: reply,
	}
}

// snat is a LAN→internet IPv4 TCP flow masqueraded behind the WAN address.
func snat(id uint32, lan, remote string, sent, received uint64) ports.Flow {
	return flow(id, 6, lan, 50000+uint16(id), remote, 443, remote, wanIP, sent, received)
}

// direct is an un-NATed flow (IPv6, or a WireGuard peer's own address).
func direct(id uint32, local, remote string, sent, received uint64) ports.Flow {
	return flow(id, 6, local, 50000+uint16(id), remote, 443, remote, local, sent, received)
}

func TestLANDeviceDownloadShowsAsLiveRateNamedByDHCPHostname(t *testing.T) {
	tk := newTalkers(t)
	tk.h.Kernel.SetFlow(snat(1, copper4, google, 1_000, 125_000))

	ev := tk.second()

	if ev.TopTalkers.Window != "live" || ev.TopTalkers.Unit != "bps" {
		t.Fatalf("window/unit = %q/%q", ev.TopTalkers.Window, ev.TopTalkers.Unit)
	}
	d := ev.device(t, "mac:"+copperMAC)
	if d.Name != "copper" || d.Kind != "lan" {
		t.Fatalf("name/kind = %q/%q", d.Name, d.Kind)
	}
	if d.Rx != 1_000_000 || d.Tx != 8_000 {
		t.Fatalf("rx/tx = %v/%v bps, want 1000000/8000", d.Rx, d.Tx)
	}
	if d.MAC == nil || *d.MAC != copperMAC || d.Vendor == nil || *d.Vendor != "Ubiquiti" {
		t.Fatalf("mac/vendor = %v/%v", d.MAC, d.Vendor)
	}
	if len(d.Addresses) != 2 || d.Addresses[0] != copper4 || d.Addresses[1] != copper6 {
		t.Fatalf("addresses = %v", d.Addresses)
	}
}
