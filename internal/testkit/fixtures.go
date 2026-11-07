package testkit

import (
	"bufio"
	"encoding/json"
	"net/netip"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"survy/internal/ports"
)

// TestdataDir is the absolute path of internal/testkit/testdata.
func TestdataDir() string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(file), "testdata")
}

// LoadRecordedKernel fills k with the neighbour table, interface addresses and
// conntrack table recorded from the real router (testdata/kernel). The
// recorded conntrack table has zero byte counters (accounting was off).
func LoadRecordedKernel(k *Kernel) error {
	dir := filepath.Join(TestdataDir(), "kernel")
	n, err := ParseIPNeighJSON(filepath.Join(dir, "neigh.json"))
	if err != nil {
		return err
	}
	a, err := ParseIPAddrJSON(filepath.Join(dir, "addr.json"))
	if err != nil {
		return err
	}
	f, err := ParseProcConntrack(filepath.Join(dir, "conntrack.txt"))
	if err != nil {
		return err
	}
	k.SetNeighbours(n)
	k.SetAddresses(a)
	for _, fl := range f {
		k.SetFlow(fl)
	}
	return nil
}

// ParseIPNeighJSON reads `ip -j neigh` output.
func ParseIPNeighJSON(path string) ([]ports.Neighbour, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var raw []struct {
		Dst    string   `json:"dst"`
		Dev    string   `json:"dev"`
		LLAddr string   `json:"lladdr"`
		State  []string `json:"state"`
	}
	if err := json.Unmarshal(b, &raw); err != nil {
		return nil, err
	}
	var out []ports.Neighbour
	for _, r := range raw {
		ip, err := netip.ParseAddr(r.Dst)
		if err != nil {
			continue
		}
		st := ""
		if len(r.State) > 0 {
			st = r.State[0]
		}
		out = append(out, ports.Neighbour{Iface: r.Dev, IP: ip, MAC: strings.ToLower(r.LLAddr), State: st})
	}
	return out, nil
}

// ParseIPAddrJSON reads `ip -j addr` output.
func ParseIPAddrJSON(path string) ([]ports.IfAddr, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var raw []struct {
		IfName   string `json:"ifname"`
		AddrInfo []struct {
			Local     string `json:"local"`
			PrefixLen int    `json:"prefixlen"`
		} `json:"addr_info"`
	}
	if err := json.Unmarshal(b, &raw); err != nil {
		return nil, err
	}
	var out []ports.IfAddr
	for _, r := range raw {
		for _, ai := range r.AddrInfo {
			ip, err := netip.ParseAddr(ai.Local)
			if err != nil {
				continue
			}
			out = append(out, ports.IfAddr{Iface: r.IfName, Prefix: netip.PrefixFrom(ip, ai.PrefixLen)})
		}
	}
	return out, nil
}

// ParseProcConntrack reads /proc/net/nf_conntrack. Flow IDs are line numbers.
func ParseProcConntrack(path string) ([]ports.Flow, error) {
	fh, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer fh.Close()
	var out []ports.Flow
	sc := bufio.NewScanner(fh)
	id := uint32(0)
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) < 4 {
			continue
		}
		id++
		proto, _ := strconv.Atoi(fields[3])
		f := ports.Flow{ID: id, Proto: uint8(proto)}
		dir := -1
		for _, tok := range fields[4:] {
			k, v, ok := strings.Cut(tok, "=")
			if !ok {
				continue
			}
			if k == "src" {
				dir++
			}
			t := &f.Orig
			bytes := &f.OrigBytes
			if dir >= 1 {
				t = &f.Reply
				bytes = &f.ReplyBytes
			}
			switch k {
			case "src":
				t.Src, _ = netip.ParseAddr(v)
			case "dst":
				t.Dst, _ = netip.ParseAddr(v)
			case "sport":
				p, _ := strconv.Atoi(v)
				t.SrcPort = uint16(p)
			case "dport":
				p, _ := strconv.Atoi(v)
				t.DstPort = uint16(p)
			case "bytes":
				n, _ := strconv.ParseUint(v, 10, 64)
				*bytes = n
			}
		}
		out = append(out, f)
	}
	return out, sc.Err()
}
