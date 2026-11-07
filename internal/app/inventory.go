package app

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"io"
	"net/netip"
	"os"
	"strings"
	"time"
)

// watchedFile re-parses a file only when its size or mtime changes. A missing
// or unreadable file is parsed as empty.
type watchedFile struct {
	path  string
	mod   time.Time
	size  int64
	known bool
}

// changed reports whether the file differs from the last call, returning its
// content (nil when missing).
func (w *watchedFile) changed() ([]byte, bool) {
	st, err := os.Stat(w.path)
	if err != nil {
		if w.known && w.size < 0 {
			return nil, false
		}
		w.known, w.size, w.mod = true, -1, time.Time{}
		return nil, true
	}
	if w.known && st.Size() == w.size && st.ModTime().Equal(w.mod) {
		return nil, false
	}
	b, err := os.ReadFile(w.path)
	if err != nil {
		b = nil
	}
	w.known, w.size, w.mod = true, st.Size(), st.ModTime()
	return b, true
}

// inventory is the router inventory file written by the VyOS commit hook
// (docs/api.md, "Router inventory file").
type inventory struct {
	WireGuard struct {
		Interface  string   `json:"interface"`
		ListenPort uint16   `json:"listenPort"`
		Peers      []wgPeer `json:"peers"`
	} `json:"wireguard"`
	Containers []container `json:"containers"`
}

type wgPeer struct {
	Name       string   `json:"name"`
	PublicKey  string   `json:"publicKey"`
	AllowedIPs []string `json:"allowedIps"`

	prefixes []netip.Prefix
}

type container struct {
	Name      string   `json:"name"`
	Addresses []string `json:"addresses"`

	addrs []netip.Addr
}

func parseInventory(b []byte) inventory {
	var inv inventory
	if len(b) == 0 || json.Unmarshal(b, &inv) != nil {
		return inventory{}
	}
	for i := range inv.WireGuard.Peers {
		p := &inv.WireGuard.Peers[i]
		for _, s := range p.AllowedIPs {
			if pf, err := netip.ParsePrefix(s); err == nil {
				p.prefixes = append(p.prefixes, pf.Masked())
			} else if a, err := netip.ParseAddr(s); err == nil {
				p.prefixes = append(p.prefixes, netip.PrefixFrom(a, a.BitLen()))
			}
		}
	}
	for i := range inv.Containers {
		c := &inv.Containers[i]
		for _, s := range c.Addresses {
			if a, err := netip.ParseAddr(s); err == nil {
				c.addrs = append(c.addrs, a.Unmap())
			}
		}
	}
	return inv
}

// parseAliases reads {"<mac or device id>": "friendly name"}. Keys are
// lower-cased unless they are WireGuard ids (base64 is case-sensitive).
func parseAliases(b []byte) map[string]string {
	raw := map[string]string{}
	if len(b) > 0 {
		_ = json.Unmarshal(b, &raw)
	}
	out := make(map[string]string, len(raw))
	for k, v := range raw {
		k = strings.TrimSpace(k)
		if !strings.HasPrefix(k, "wg:") {
			k = strings.ToLower(k)
		}
		if v = strings.TrimSpace(v); v != "" {
			out[k] = v
		}
	}
	return out
}

// leases is the useful part of Kea's DHCPv4 memfile.
type leases struct {
	hostByMAC map[string]string
	macByAddr map[netip.Addr]string
}

// parseKeaLeases reads Kea's lease CSV. Kea appends a row per lease update,
// so the last row for an address wins; only state 0 (valid) rows count.
func parseKeaLeases(b []byte) leases {
	l := leases{hostByMAC: map[string]string{}, macByAddr: map[netip.Addr]string{}}
	r := csv.NewReader(bytes.NewReader(b))
	r.FieldsPerRecord = -1
	header, err := r.Read()
	if err != nil {
		return l
	}
	col := map[string]int{}
	for i, h := range header {
		col[strings.TrimSpace(h)] = i
	}
	get := func(row []string, name string) string {
		i, ok := col[name]
		if !ok || i >= len(row) {
			return ""
		}
		return strings.TrimSpace(row[i])
	}
	type lease struct{ mac, host, state string }
	last := map[netip.Addr]lease{}
	var order []netip.Addr
	for {
		row, err := r.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			continue
		}
		a, err := netip.ParseAddr(get(row, "address"))
		if err != nil {
			continue
		}
		if _, seen := last[a]; !seen {
			order = append(order, a)
		}
		last[a] = lease{strings.ToLower(get(row, "hwaddr")), get(row, "hostname"), get(row, "state")}
	}
	for _, a := range order {
		ls := last[a]
		if ls.state != "0" || ls.mac == "" {
			continue
		}
		l.macByAddr[a] = ls.mac
		if h := strings.TrimSuffix(ls.host, "."); h != "" {
			l.hostByMAC[ls.mac] = h
		}
	}
	return l
}
