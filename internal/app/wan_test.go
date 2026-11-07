package app_test

import (
	"testing"
)

const wgRemote = "203.0.113.50" // a WireGuard peer's current internet address

func TestDNATFromLANToContainerIsNotWANTraffic(t *testing.T) {
	tk := newTalkers(t)
	tk.h.WriteData("inventory.json", []byte(inventoryJSON))
	// Recorded: copper → 192.168.2.3:8080, DNAT'd to the UniFi container.
	tk.h.Kernel.SetFlow(flow(1, 6, copper4, 49396, "192.168.2.3", 8080, unifi, copper4, 50_000, 900_000))

	ev := tk.second()

	if len(ev.TopTalkers.Devices) != 0 {
		t.Fatalf("LAN↔container counted: %+v", ev.TopTalkers.Devices)
	}
}

func TestPortForwardFromInternetIsTheLANDevicesTraffic(t *testing.T) {
	tk := newTalkers(t)
	// A remote client reaches the WAN address and is DNAT'd to copper.
	tk.h.Kernel.SetFlow(flow(1, 6, google, 40000, wanIP, 8443, copper4, google, 1_000, 50_000))

	ev := tk.second()

	d := ev.device(t, "mac:"+copperMAC)
	if d.Rx != 8_000 || d.Tx != 400_000 {
		t.Fatalf("rx/tx = %v/%v, want 8000/400000", d.Rx, d.Tx)
	}
}

func TestRouterOwnWANTrafficIsTheRouterDevice(t *testing.T) {
	tk := newTalkers(t)
	// Recorded: the router's DNS forwarder querying upstream from the WAN address.
	tk.h.Kernel.SetFlow(flow(1, 17, wanIP, 32393, "198.51.100.2", 53, "198.51.100.2", wanIP, 100, 400))
	tk.h.Kernel.SetFlow(direct(2, wanIP6, cloudflare6, 100, 600))

	ev := tk.second()

	r := ev.device(t, "router")
	if r.Kind != "router" || r.Rx != 8_000 || r.Tx != 1_600 {
		t.Fatalf("router = %+v", r)
	}
	if len(ev.TopTalkers.Devices) != 1 {
		t.Fatalf("devices = %+v", ev.TopTalkers.Devices)
	}
}

func TestWireGuardTransportIsTheTunnelsPseudoDevice(t *testing.T) {
	tk := newTalkers(t)
	tk.h.WriteData("inventory.json", []byte(inventoryJSON))
	// A peer's encrypted transport to the router's listen port...
	tk.h.Kernel.SetFlow(flow(1, 17, wgRemote, 61000, wanIP, 51820, wanIP, wgRemote, 10_000, 200_000))
	// ...carrying that peer's browsing, which is the peer's own traffic.
	tk.h.Kernel.SetFlow(snat(2, laptopWG, google, 9_000, 190_000))

	ev := tk.second()

	w := ev.device(t, "wg-tunnels")
	if w.Kind != "wg-tunnels" || w.Name != "WireGuard tunnels" || w.Rx != 80_000 || w.Tx != 1_600_000 {
		t.Fatalf("wg-tunnels = %+v", w)
	}
	if p := ev.device(t, laptopPeer); p.Rx != 1_520_000 || p.Tx != 72_000 {
		t.Fatalf("peer = %+v", p)
	}
	if ev.has("router") {
		t.Fatalf("WireGuard transport counted as router: %+v", ev.TopTalkers.Devices)
	}
}

func TestTrafficBetweenLANAndWireGuardPeerIsExcluded(t *testing.T) {
	tk := newTalkers(t)
	tk.h.WriteData("inventory.json", []byte(inventoryJSON))
	tk.h.Kernel.SetFlow(direct(1, laptopWG, copper4, 5_000, 500_000))
	tk.h.Kernel.SetFlow(direct(2, copper6, "2001:db8:1:1:8e30:66ff:fea1:5f9a", 5_000, 500_000))

	ev := tk.second()

	if len(ev.TopTalkers.Devices) != 0 {
		t.Fatalf("local traffic counted: %+v", ev.TopTalkers.Devices)
	}
}
