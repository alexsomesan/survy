package app

import (
	"log"
	"math"
	"net/http"
	"path/filepath"
	"sync"
	"time"

	"survy/internal/store"
)

// sysFamily is the interfaces + Router Resources History. Byte columns are
// deltas; gauges are (sum, n) pairs so averages survive rollups; secs is how
// many seconds of WAN counter deltas the slot covers (the rate divisor).
var sysFamily = &store.Family{
	Name: "sys",
	Values: []string{"secs", "wan_rx", "wan_tx", "lan_rx", "lan_tx", "wg_rx", "wg_tx",
		"cpu_sum", "cpu_n", "mem_sum", "mem_n", "ct_sum", "ct_n"},
	Resolutions: []store.Resolution{store.Res10s, store.Res1m, store.Res1h},
}

// Column indexes into sysFamily.Values.
const (
	cSecs = iota
	cWanRx
	cWanTx
	cLanRx
	cLanTx
	cWgRx
	cWgTx
	cCPUSum
	cCPUN
	cMemSum
	cMemN
	cConntrackSum
	cConntrackN
	nSys
)

// historyRanges are the /api/history ranges (spans in windowSpans).
var historyRanges = []string{"1h", "24h", "7d", "30d", "1y"}

// history accumulates one-second samples into 10-second slots, writes each
// completed slot to the store (one transaction per slot) and keeps today's
// WAN totals.
type history struct {
	a  *App
	st *store.Store

	mu      sync.Mutex
	lastAt  time.Time // time of the previous sample (for byte deltas)
	slot    time.Time // slot being accumulated; zero when empty
	acc     [nSys]float64
	today   time.Time // local midnight that todayRx/Tx count from
	todayRx float64
	todayTx float64
}

func openHistory(a *App, now time.Time) (*history, error) {
	st, err := store.Open(filepath.Join(a.cfg.DataDir, "history.db"), a.cfg.Settings.StorageCapBytes,
		sysFamily, deviceFamily, endpointFamily, talkersFamily)
	if err != nil {
		return nil, err
	}
	h := &history{a: a, st: st, lastAt: now}
	if err := st.Maintain(now); err != nil {
		log.Printf("history: maintain: %v", err)
	}
	h.today = h.midnight(now)
	rows, err := st.Query(sysFamily, store.Res10s, h.today, now.Add(store.Res10s.Step))
	if err != nil {
		log.Printf("history: today's totals: %v", err)
	}
	for _, r := range rows {
		h.todayRx += r.Values[cWanRx]
		h.todayTx += r.Values[cWanTx]
	}
	return h, nil
}

func (h *history) midnight(t time.Time) time.Time {
	t = t.In(h.a.cfg.Location)
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, t.Location())
}

// record adds the resources sample taken at now. A sample covers the time
// since the previous one and belongs to the 10 s slot holding its last second.
func (h *history) record(now time.Time, s resourceSample) {
	h.mu.Lock()
	defer h.mu.Unlock()
	dt := now.Sub(h.lastAt).Seconds()
	h.lastAt = now
	slot := store.Floor(now.Add(-time.Second), store.Res10s.Step)
	if !h.slot.IsZero() && !h.slot.Equal(slot) {
		h.flushLocked(now)
	}
	h.slot = slot

	var v [nSys]float64
	bytes := func(p *float64) float64 {
		if p == nil {
			return 0
		}
		return math.Round(*p * dt / 8)
	}
	for _, ifc := range s.ifaces {
		switch ifc.Role {
		case roleWAN:
			if ifc.RxBps != nil && ifc.TxBps != nil && dt > 0 {
				v[cSecs] = dt
			}
			v[cWanRx], v[cWanTx] = bytes(ifc.RxBps), bytes(ifc.TxBps)
		case roleLAN:
			v[cLanRx], v[cLanTx] = bytes(ifc.RxBps), bytes(ifc.TxBps)
		case roleWireGuard:
			v[cWgRx], v[cWgTx] = bytes(ifc.RxBps), bytes(ifc.TxBps)
		}
	}
	g := s.resources
	if g.CPUPct != nil {
		v[cCPUSum], v[cCPUN] = *g.CPUPct, 1
	}
	if g.MemUsedBytes != nil && g.MemTotalBytes != nil && *g.MemTotalBytes > 0 {
		v[cMemSum], v[cMemN] = 100*float64(*g.MemUsedBytes)/float64(*g.MemTotalBytes), 1
	}
	if g.ConntrackCount != nil {
		v[cConntrackSum], v[cConntrackN] = float64(*g.ConntrackCount), 1
	}
	for i := range v {
		h.acc[i] += v[i]
	}

	if day := h.midnight(slot); !day.Equal(h.today) {
		h.today, h.todayRx, h.todayTx = day, 0, 0
	}
	h.todayRx += v[cWanRx]
	h.todayTx += v[cWanTx]

	if !now.Before(slot.Add(store.Res10s.Step)) {
		h.flushLocked(now)
	}
}

func (h *history) flushLocked(now time.Time) {
	if h.slot.IsZero() {
		return
	}
	row := store.Row{Slot: h.slot, Values: append([]float64(nil), h.acc[:]...)}
	h.slot, h.acc = time.Time{}, [nSys]float64{}
	if err := h.st.Write(sysFamily, now, []store.Row{row}); err != nil {
		log.Printf("history: write: %v", err)
	}
}

func (h *history) maintain(now time.Time) {
	if err := h.st.Maintain(now); err != nil {
		log.Printf("history: maintain: %v", err)
	}
}

// close writes the partial slot and closes the store.
func (h *history) close(now time.Time) {
	h.mu.Lock()
	h.flushLocked(now)
	h.mu.Unlock()
	_ = h.st.Close()
}

// fillLive writes today's WAN totals and the database size.
func (h *history) fillLive(ev *LiveEvent, now time.Time) {
	h.mu.Lock()
	rx, tx := h.todayRx, h.todayTx
	if !h.midnight(now).Equal(h.today) {
		rx, tx = 0, 0
	}
	h.mu.Unlock()
	ev.WAN.TodayRxBytes, ev.WAN.TodayTxBytes = int64(rx), int64(tx)
	ev.Self.DataBytes = h.st.Size()
}

// HistoryResponse is /api/history.
type HistoryResponse struct {
	Range      string     `json:"range"`
	Resolution string     `json:"resolution"`
	Start      int64      `json:"start"`
	StepMs     int64      `json:"stepMs"`
	WanRxBps   []*float64 `json:"wanRxBps"`
	WanTxBps   []*float64 `json:"wanTxBps"`
	CPUPct     []*float64 `json:"cpuPct"`
	MemUsedPct []*float64 `json:"memUsedPct"`
	Conntrack  []*float64 `json:"conntrack"`
}

func (a *App) handleHistory(w http.ResponseWriter, r *http.Request) {
	name := r.URL.Query().Get("range")
	win, ok := newWindow(name, sysFamily, a.cfg.Clock.Now(), historyRanges...)
	if !ok {
		writeError(w, http.StatusBadRequest, "unknown range")
		return
	}
	if a.hist == nil {
		writeError(w, http.StatusServiceUnavailable, "not started")
		return
	}
	rows, err := a.hist.st.Query(sysFamily, win.res, win.start, win.end())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	n := win.n
	buckets := make([][nSys]float64, n)
	for _, row := range rows {
		if i, ok := win.index(row.Slot); ok {
			for c := range row.Values {
				buckets[i][c] += row.Values[c]
			}
		}
	}
	resp := HistoryResponse{Range: name, Resolution: win.res.Name, Start: win.start.UnixMilli(), StepMs: win.step.Milliseconds(),
		WanRxBps: make([]*float64, n), WanTxBps: make([]*float64, n), CPUPct: make([]*float64, n),
		MemUsedPct: make([]*float64, n), Conntrack: make([]*float64, n)}
	ratio := func(num, den float64) *float64 {
		if den <= 0 {
			return nil
		}
		v := num / den
		return &v
	}
	for i, b := range buckets {
		if b[cSecs] > 0 {
			rx, tx := bps(b[cWanRx], b[cSecs]), bps(b[cWanTx], b[cSecs])
			resp.WanRxBps[i], resp.WanTxBps[i] = &rx, &tx
		}
		resp.CPUPct[i] = ratio(b[cCPUSum], b[cCPUN])
		resp.MemUsedPct[i] = ratio(b[cMemSum], b[cMemN])
		resp.Conntrack[i] = ratio(b[cConntrackSum], b[cConntrackN])
	}
	writeJSON(w, resp)
}
