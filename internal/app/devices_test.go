package app_test

import (
	"testing"

	"survy/internal/ports"
)

func TestIPv4AndSeveralIPv6AddressesOfOneMACAreOneDevice(t *testing.T) {
	tk := newTalkers(t)
	k := tk.h.Kernel
	k.SetFlow(snat(1, oma4, google, 1_000, 10_000))
	k.SetFlow(direct(2, omaPriv1, cloudflare6, 2_000, 20_000))
	k.SetFlow(direct(3, omaPriv2, cloudflare6, 3_000, 30_000))

	ev := tk.second()

	if n := len(ev.TopTalkers.Devices); n != 1 {
		t.Fatalf("got %d devices, want 1: %+v", n, ev.TopTalkers.Devices)
	}
	d := ev.device(t, "mac:"+guestMAC)
	if d.Name != "maple" || d.Rx != 60_000*8 || d.Tx != 6_000*8 {
		t.Fatalf("device = %+v", d)
	}
	if len(d.Addresses) < 3 || d.Addresses[0] != oma4 {
		t.Fatalf("addresses = %v, want IPv4 first plus the IPv6 addresses", d.Addresses)
	}
}

func TestPrivacyAddressThatLeftTheNeighbourTableStillResolves(t *testing.T) {
	tk := newTalkers(t)
	k := tk.h.Kernel
	// The privacy address ages out of the neighbour table...
	all, _ := k.Neighbours()
	var kept []ports.Neighbour
	for _, n := range all {
		if n.IP != addr(omaPriv1) {
			kept = append(kept, n)
		}
	}
	k.SetNeighbours(kept)
	tk.second()
	// ...but a long-lived connection from it keeps transferring.
	k.SetFlow(direct(1, omaPriv1, cloudflare6, 500, 5_000))

	ev := tk.second()

	if d := ev.device(t, "mac:"+guestMAC); d.Rx != 40_000 {
		t.Fatalf("rx = %v, want 40000", d.Rx)
	}
}

func TestUnknownLocalAddressIsNotADevice(t *testing.T) {
	tk := newTalkers(t)
	tk.h.Kernel.SetFlow(direct(1, "2001:db8:1:1::dead", cloudflare6, 500, 5_000))

	ev := tk.second()

	if len(ev.TopTalkers.Devices) != 0 {
		t.Fatalf("devices = %+v", ev.TopTalkers.Devices)
	}
}
