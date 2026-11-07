package app_test

import (
	"testing"
)

const inventoryJSON = `{
  "wireguard": {"interface": "wg0", "listenPort": 51820, "peers": [
    {"name": "Laptop", "publicKey": "pk/Laptop+Key=", "allowedIps": ["10.0.0.21/32"]},
    {"name": "Phone", "publicKey": "pk/Phone=", "allowedIps": ["10.0.0.22/32", "fd00:10::22/128"]}
  ]},
  "containers": [
    {"name": "unifi-db", "addresses": ["172.31.254.2"]},
    {"name": "unifi-network-application", "addresses": ["172.31.254.3"]},
    {"name": "airprint", "addresses": ["192.168.2.2"]}
  ]
}`

const (
	laptopPeer = "wg:pk/Laptop+Key="
	laptopWG   = "10.0.0.21"
	unifi      = "172.31.254.3"
	unifiID    = "ctr:unifi-network-application"
)

func TestDeviceWithoutHostnameIsNamedByVendorAndMACEnd(t *testing.T) {
	tk := newTalkers(t)
	tk.h.Kernel.SetFlow(snat(1, "192.168.2.139", google, 1, 1)) // Apple, no DHCP lease
	tk.h.Kernel.SetFlow(snat(2, "192.168.2.131", google, 1, 1)) // randomised MAC

	ev := tk.second()

	if d := ev.device(t, "mac:b4:55:75:fa:1b:0a"); d.Name != "Apple 1b:0a" || *d.Vendor != "Apple" {
		t.Fatalf("apple device = %+v", d)
	}
	if d := ev.device(t, "mac:c6:71:8c:63:42:0a"); d.Name != "Private 42:0a" || d.Vendor != nil {
		t.Fatalf("private device = %+v", d)
	}
}

func TestAliasBeatsDHCPHostname(t *testing.T) {
	tk := newTalkers(t)
	tk.h.WriteData("aliases.json", []byte(`{"28:70:4E:9E:1A:AA": "Living room TV", "`+laptopPeer+`": "Laptop (travel)"}`))
	tk.h.WriteData("inventory.json", []byte(inventoryJSON))
	tk.h.Kernel.SetFlow(snat(1, copper4, google, 1, 1))
	tk.h.Kernel.SetFlow(direct(2, laptopWG, google, 1, 1))

	ev := tk.second()

	if d := ev.device(t, "mac:"+copperMAC); d.Name != "Living room TV" {
		t.Fatalf("name = %q", d.Name)
	}
	if d := ev.device(t, laptopPeer); d.Name != "Laptop (travel)" {
		t.Fatalf("peer name = %q", d.Name)
	}
}

func TestDHCPHostnameComesFromTheLastValidLeaseRow(t *testing.T) {
	tk := newTalkers(t)
	leases := tk.h.ReadFile("/config/dhcp/dhcp4-leases.csv")
	leases += "192.168.2.109,28:70:4e:9e:1a:aa,01:28:70:4e:9e:1a:aa,3600,1794020000,1,0,0,lounge-tv,0,,0\n"
	// falcon's lease was reclaimed (state 2): its hostname no longer applies.
	leases += "192.168.2.107,8c:30:66:a1:5f:9a,01:8c:30:66:a1:5f:9a,3600,1794020000,1,0,0,falcon,2,,0\n"
	tk.h.WriteFile("/config/dhcp/dhcp4-leases.csv", leases)
	tk.h.Kernel.SetFlow(snat(1, copper4, google, 1, 1))
	tk.h.Kernel.SetFlow(snat(2, "192.168.2.107", google, 1, 1))

	ev := tk.second()

	if d := ev.device(t, "mac:"+copperMAC); d.Name != "lounge-tv" {
		t.Fatalf("copper name = %q", d.Name)
	}
	if d := ev.device(t, "mac:8c:30:66:a1:5f:9a"); d.Name != "Ubiquiti 5f:9a" {
		t.Fatalf("falcon name = %q", d.Name)
	}
}

func TestWireGuardPeersAndContainersAreNamedFromTheInventory(t *testing.T) {
	tk := newTalkers(t)
	tk.h.WriteData("inventory.json", []byte(inventoryJSON))
	tk.h.Kernel.SetFlow(direct(1, laptopWG, google, 1_000, 2_000))
	tk.h.Kernel.SetFlow(snat(2, unifi, google, 3_000, 4_000))

	ev := tk.second()

	p := ev.device(t, laptopPeer)
	if p.Name != "Laptop" || p.Kind != "wg" || p.MAC != nil || len(p.Addresses) != 1 || p.Addresses[0] != laptopWG {
		t.Fatalf("peer = %+v", p)
	}
	if p.Rx != 16_000 || p.Tx != 8_000 {
		t.Fatalf("peer rx/tx = %v/%v", p.Rx, p.Tx)
	}
	c := ev.device(t, unifiID)
	if c.Name != "unifi-network-application" || c.Kind != "container" || c.Addresses[0] != unifi {
		t.Fatalf("container = %+v", c)
	}
}

func TestInventoryChangesApplyWithoutRestart(t *testing.T) {
	tk := newTalkers(t)
	tk.h.Kernel.SetFlow(direct(1, "10.0.0.23", google, 1_000, 2_000))
	if ev := tk.second(); len(ev.TopTalkers.Devices) != 0 {
		t.Fatalf("unknown peer attributed: %+v", ev.TopTalkers.Devices)
	}

	tk.h.WriteData("inventory.json", []byte(`{"wireguard":{"interface":"wg0","listenPort":51820,
		"peers":[{"name":"NewTablet","publicKey":"pk/New=","allowedIps":["10.0.0.23/32"]}]},"containers":[]}`))
	tk.h.Kernel.SetFlow(direct(1, "10.0.0.23", google, 2_000, 4_000))

	ev := tk.second()
	if d := ev.device(t, "wg:pk/New="); d.Name != "NewTablet" {
		t.Fatalf("name = %q", d.Name)
	}
}
