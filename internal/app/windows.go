package app

import (
	"time"

	"survy/internal/store"
)

// Named windows (Top Talkers, Device detail) and History ranges share one
// table of spans. Each is served at the finest Resolution of the Family that
// still keeps the whole span (spec: History store and rollups), so the
// window covers exactly from = now − span.
var windowSpans = map[string]time.Duration{
	"1h":  time.Hour,
	"24h": 24 * time.Hour,
	"7d":  7 * 24 * time.Hour,
	"30d": 30 * 24 * time.Hour,
	"1y":  365 * 24 * time.Hour,
}

// Interface roles (IfaceRate.Role).
const (
	roleWAN       = "wan"
	roleLAN       = "lan"
	roleWireGuard = "wireguard"
)

const (
	// windowLive is the Live window: current rates, not a stored span.
	windowLive = "live"
	// Units of a response's rx/tx values.
	unitBps   = "bps"
	unitBytes = "bytes"
	// maxSeriesPoints is roughly how many points a stored series serves at
	// most; finer data is merged by an integer factor.
	maxSeriesPoints = 1500
)

// window is one request's stored window: [from, to) at Resolution res, and
// the series grid of n steps of step from start, the last one holding to
// (so the grid spans the window to within one step).
type window struct {
	name     string
	res      store.Resolution
	from, to time.Time
	start    time.Time
	step     time.Duration
	n        int
}

// newWindow resolves a window name for f at now; ok is false when the name is
// unknown or not among allowed.
func newWindow(name string, f *store.Family, now time.Time, allowed ...string) (window, bool) {
	span, ok := windowSpans[name]
	if ok && len(allowed) > 0 {
		ok = false
		for _, a := range allowed {
			ok = ok || a == name
		}
	}
	if !ok {
		return window{}, false
	}
	res := f.Resolutions[len(f.Resolutions)-1]
	for _, r := range f.Resolutions {
		if r.Keep >= span {
			res = r
			break
		}
	}
	// Merge by an integer factor so at most ~maxSeriesPoints are served.
	fine := int64(span / res.Step)
	k := (fine + maxSeriesPoints - 1) / maxSeriesPoints
	step := res.Step * time.Duration(k)
	n := int((span + step - 1) / step)
	last := store.Floor(now, step)
	return window{name: name, res: res, from: now.Add(-span), to: now,
		start: last.Add(-time.Duration(n-1) * step), step: step, n: n}, true
}

// end is the end of the series grid (after to).
func (w window) end() time.Time { return w.start.Add(time.Duration(w.n) * w.step) }

// index is the grid step holding t.
func (w window) index(t time.Time) (int, bool) {
	i := int(t.Sub(w.start) / w.step)
	return i, !t.Before(w.start) && i < w.n
}

// bps converts bytes over secs seconds to bits per second.
func bps(bytes, secs float64) float64 { return bytes * 8 / secs }
