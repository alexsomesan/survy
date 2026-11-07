package app

import (
	"net/netip"
	"sort"
	"strings"
	"time"

	"survy/internal/oui"
	"survy/internal/ports"
)

// Device kinds and fixed ids (docs/api.md).
const (
	kindLAN       = "lan"
	kindWG        = "wg"
	kindContainer = "container"
	kindRouter    = "router"
	kindWGTunnels = "wg-tunnels"

	idRouter    = "router"
	idWGTunnels = "wg-tunnels"
)

// neighbourCacheTTL is how long an address→MAC pair outlives its neighbour
// table entry, so rotating IPv6 privacy addresses still resolve.
const neighbourCacheTTL = 24 * time.Hour

// Device is one Device as the API describes it, without traffic figures.
// The JSON form is how History persists it (keyed by ID).
type Device struct {
	ID        string   `json:"-"`
	Name      string   `json:"name"`
	Kind      string   `json:"kind"`
	MAC       string   `json:"mac"`    // "" when not a LAN Device
	Vendor    string   `json:"vendor"` // "" when unknown or not applicable
	Addresses []string `json:"addresses"`
}

type addrClass int

const (
	classRemote addrClass = iota // a Remote Endpoint on the internet
	classLocal                   // LAN, WireGuard, container, link-local…
	classRouter                  // one of the router's own addresses
)

type macSeen struct {
	mac  string
	seen time.Time
}

// devices turns addresses into Devices and names them. It is refreshed from
// the kernel tables and the router files once per conntrack poll.
type devices struct {
	settings Settings

	inventoryFile, aliasFile, leaseFile watchedFile
	inv                                 inventory
	aliases                             map[string]string
	leases                              leases

	localPrefixes []netip.Prefix
	routerAddrs   map[netip.Addr]bool
	wanAddrs      []netip.Addr
	neigh         map[netip.Addr]macSeen
}

func newDevices(s Settings, leasePath string) *devices {
	return &devices{
		settings:      s,
		inventoryFile: watchedFile{path: s.InventoryPath},
		aliasFile:     watchedFile{path: s.AliasPath},
		leaseFile:     watchedFile{path: leasePath},
		aliases:       map[string]string{},
		leases:        parseKeaLeases(nil),
		routerAddrs:   map[netip.Addr]bool{},
		neigh:         map[netip.Addr]macSeen{},
	}
}

// refresh re-reads the kernel tables and any router file that changed.
func (d *devices) refresh(now time.Time, k ports.Kernel) {
	if b, ok := d.inventoryFile.changed(); ok {
		d.inv = parseInventory(b)
	}
	if b, ok := d.aliasFile.changed(); ok {
		d.aliases = parseAliases(b)
	}
	if b, ok := d.leaseFile.changed(); ok {
		d.leases = parseKeaLeases(b)
	}
	if addrs, err := k.Addresses(); err == nil {
		d.localPrefixes = d.localPrefixes[:0]
		d.wanAddrs = d.wanAddrs[:0]
		d.routerAddrs = map[netip.Addr]bool{}
		for _, a := range addrs {
			ip := a.Prefix.Addr().Unmap()
			d.routerAddrs[ip] = true
			if a.Iface == d.settings.WANIface {
				d.wanAddrs = append(d.wanAddrs, ip)
				continue
			}
			d.localPrefixes = append(d.localPrefixes, a.Prefix.Masked())
		}
	}
	if ns, err := k.Neighbours(); err == nil {
		for _, n := range ns {
			if n.MAC != "" && n.IP.IsValid() {
				d.neigh[n.IP.Unmap()] = macSeen{strings.ToLower(n.MAC), now}
			}
		}
	}
	for ip, m := range d.neigh {
		if now.Sub(m.seen) > neighbourCacheTTL {
			delete(d.neigh, ip)
		}
	}
}

func (d *devices) class(a netip.Addr) addrClass {
	a = a.Unmap()
	if d.routerAddrs[a] {
		return classRouter
	}
	if !a.IsValid() || a.IsLoopback() || a.IsLinkLocalUnicast() || a.IsMulticast() ||
		a.IsUnspecified() || a == netip.AddrFrom4([4]byte{255, 255, 255, 255}) {
		return classLocal
	}
	for _, p := range d.localPrefixes {
		if p.Contains(a) {
			return classLocal
		}
	}
	if _, ok := d.peerFor(a); ok {
		return classLocal
	}
	if _, ok := d.containerFor(a); ok {
		return classLocal
	}
	return classRemote
}

func (d *devices) peerFor(a netip.Addr) (*wgPeer, bool) {
	for i := range d.inv.WireGuard.Peers {
		p := &d.inv.WireGuard.Peers[i]
		for _, pf := range p.prefixes {
			if pf.Contains(a) {
				return p, true
			}
		}
	}
	return nil, false
}

func (d *devices) containerFor(a netip.Addr) (*container, bool) {
	for i := range d.inv.Containers {
		c := &d.inv.Containers[i]
		for _, ca := range c.addrs {
			if ca == a {
				return c, true
			}
		}
	}
	return nil, false
}

// resolve returns the Device id of a local (non-router) address, or false if
// no Device can be found for it.
func (d *devices) resolve(a netip.Addr) (string, bool) {
	a = a.Unmap()
	if c, ok := d.containerFor(a); ok {
		return "ctr:" + c.Name, true
	}
	if p, ok := d.peerFor(a); ok {
		return "wg:" + p.PublicKey, true
	}
	if m, ok := d.neigh[a]; ok {
		return "mac:" + m.mac, true
	}
	if mac, ok := d.leases.macByAddr[a]; ok {
		return "mac:" + mac, true
	}
	return "", false
}

// describe builds the Device for an id returned by resolve (or a fixed id).
func (d *devices) describe(id string) Device {
	dev := Device{ID: id, Addresses: []string{}}
	kind, key, _ := strings.Cut(id, ":")
	switch {
	case id == idRouter:
		dev.Kind, dev.Name = kindRouter, "Router"
		for _, a := range d.wanAddrs {
			if !a.IsLinkLocalUnicast() {
				dev.Addresses = append(dev.Addresses, a.String())
			}
		}
	case id == idWGTunnels:
		dev.Kind, dev.Name = kindWGTunnels, "WireGuard tunnels"
	case kind == "mac":
		dev.Kind, dev.MAC = kindLAN, key
		dev.Vendor = oui.Vendor(key)
		var addrs []netip.Addr
		seen := map[netip.Addr]bool{}
		for ip, m := range d.neigh {
			if m.mac == key && !ip.IsLinkLocalUnicast() {
				addrs, seen[ip] = append(addrs, ip), true
			}
		}
		for ip, mac := range d.leases.macByAddr {
			if mac == key && !seen[ip] {
				addrs = append(addrs, ip)
			}
		}
		dev.Addresses = sortedAddrs(addrs)
		switch {
		case d.leases.hostByMAC[key] != "":
			dev.Name = d.leases.hostByMAC[key]
		case dev.Vendor != "":
			dev.Name = dev.Vendor + " " + lastOctets(key)
		case oui.LocallyAdministered(key):
			dev.Name = "Private " + lastOctets(key)
		default:
			dev.Name = "Unknown " + lastOctets(key)
		}
	case kind == "wg":
		dev.Kind, dev.Name = kindWG, key
		for _, p := range d.inv.WireGuard.Peers {
			if p.PublicKey == key {
				dev.Name = p.Name
				for _, pf := range p.prefixes {
					if pf.IsSingleIP() {
						dev.Addresses = append(dev.Addresses, pf.Addr().String())
					} else {
						dev.Addresses = append(dev.Addresses, pf.String())
					}
				}
			}
		}
	case kind == "ctr":
		dev.Kind, dev.Name = kindContainer, key
		for _, c := range d.inv.Containers {
			if c.Name == key {
				dev.Addresses = sortedAddrs(append([]netip.Addr(nil), c.addrs...))
			}
		}
	}
	if alias, ok := d.alias(id, dev.MAC); ok {
		dev.Name = alias
	}
	return dev
}

func (d *devices) alias(id, mac string) (string, bool) {
	if v, ok := d.aliases[id]; ok {
		return v, true
	}
	if v, ok := d.aliases[strings.ToLower(id)]; ok {
		return v, true
	}
	if mac != "" {
		if v, ok := d.aliases[mac]; ok {
			return v, true
		}
	}
	return "", false
}

// sortedAddrs orders addresses IPv4 first, then numerically.
func sortedAddrs(as []netip.Addr) []string {
	sort.Slice(as, func(i, j int) bool {
		if as[i].Is4() != as[j].Is4() {
			return as[i].Is4()
		}
		return as[i].Less(as[j])
	})
	out := make([]string, len(as))
	for i, a := range as {
		out[i] = a.String()
	}
	return out
}

func lastOctets(mac string) string {
	if len(mac) < 5 {
		return mac
	}
	return mac[len(mac)-5:]
}
