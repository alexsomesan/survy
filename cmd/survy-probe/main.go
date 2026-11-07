// Command survy-probe prints one sample of each real adapter's output as JSON
// and exits. It is read-only: it dumps conntrack, neighbours and addresses,
// statfs's the system and data disks, does one reverse lookup and waits one
// clock tick. Errors are reported in the JSON rather than aborting.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"time"

	"survy/internal/adapters"
	"survy/internal/ports"
)

type section struct {
	Count  *int   `json:"count,omitempty"`
	Sample any    `json:"sample,omitempty"`
	Error  string `json:"error,omitempty"`
}

func errStr(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func head[T any](xs []T, n int) []T {
	if len(xs) > n {
		return xs[:n]
	}
	return xs
}

func main() {
	root := flag.String("root", envOr("DASH_ROOT", "/"), "router filesystem root")
	data := flag.String("data", envOr("DASH_DATA", "/data"), "data directory")
	dns := flag.String("dns", envOr("DASH_DNS", adapters.DefaultDNSServer), "DNS server host:port")
	lookup := flag.String("lookup", "1.1.1.1", "address to reverse-resolve")
	wait := flag.Duration("wait", 3*time.Second, "how long to collect destroy events")
	n := flag.Int("n", 5, "max sample entries per list")
	flag.Parse()

	out := map[string]any{}
	k := adapters.NewKernel(adapters.KernelConfig{})
	_ = k.Open() // errors are reported per section below
	defer k.Close()

	start := time.Now()

	flows, err := k.Conntrack()
	c := len(flows)
	out["conntrack"] = section{Count: &c, Sample: head(flows, *n), Error: errStr(err)}

	neigh, err := k.Neighbours()
	nn := len(neigh)
	out["neighbours"] = section{Count: &nn, Sample: head(neigh, *n), Error: errStr(err)}

	addrs, err := k.Addresses()
	na := len(addrs)
	out["addresses"] = section{Count: &na, Sample: addrs, Error: errStr(err)}

	disks := map[string]any{}
	for name, p := range map[string]string{"system": filepath.Join(*root, "boot"), "data": *data} {
		d, err := k.Statfs(p)
		disks[name] = struct {
			Path  string          `json:"path"`
			Usage ports.DiskUsage `json:"usage"`
			Error string          `json:"error,omitempty"`
		}{p, d, errStr(err)}
	}
	out["statfs"] = disks

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	names, err := adapters.NewResolver(*dns, 2*time.Second).LookupAddr(ctx, *lookup)
	cancel()
	out["resolver"] = map[string]any{"server": *dns, "addr": *lookup, "names": names, "error": errStr(err)}

	clk := adapters.Clock{}
	ticks := make(chan time.Time, 1)
	stop := clk.Every(100*time.Millisecond, func(now time.Time) {
		select {
		case ticks <- now:
		default:
		}
	})
	var tick time.Time
	select {
	case tick = <-ticks:
	case <-time.After(2 * time.Second):
	}
	stop()
	out["clock"] = map[string]any{"now": clk.Now(), "firstTick": tick}

	if rem := *wait - time.Since(start); rem > 0 {
		time.Sleep(rem)
	}
	destroyed := k.DrainDestroyed()
	nd := len(destroyed)
	out["destroyed"] = section{Count: &nd, Sample: head(destroyed, *n), Error: errStr(k.ListenErr())}
	out["destroyedDropped"] = k.DroppedDestroyed()

	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	_ = enc.Encode(out)
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
