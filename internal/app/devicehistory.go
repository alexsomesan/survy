package app

import (
	"encoding/json"
	"errors"
	"log"
	"maps"
	"net/http"
	"net/netip"
	"sort"
	"sync"
	"time"

	"survy/internal/store"
)

// Device History Families (spec: History store and rollups). Byte columns
// are byte deltas. Device and Remote Endpoint rows exist only for non-zero
// traffic; a talkers row exists for every minute in which conntrack was
// polled, so its secs column tells "no traffic" (0) from "no data" (null).
var (
	deviceFamily = &store.Family{
		Name: "device", Keys: []string{"device"}, Values: []string{"rx", "tx"},
		Resolutions: []store.Resolution{store.Res1m, store.Res1h},
	}
	endpointFamily = &store.Family{
		Name: "endpoint", Keys: []string{"device", "ip"}, Values: []string{"rx", "tx"},
		Resolutions: []store.Resolution{{Name: "1h", Step: time.Hour, Keep: 30 * 24 * time.Hour}},
	}
	talkersFamily = &store.Family{
		Name:        "talkers",
		Values:      []string{"secs", "wan_rx", "wan_tx", "unattr_rx", "unattr_tx"},
		Resolutions: []store.Resolution{store.Res1m, store.Res1h},
	}
)

// Column indexes into talkersFamily.Values.
const (
	tSecs = iota
	tWanRx
	tWanTx
	tUnattrRx
	tUnattrTx
	talkerCols
)

const (
	// endpointsKept is how many Remote Endpoints are kept per Device per
	// hour, and the most a Device detail lists.
	endpointsKept = 50
	// liveSamples is the length of the Live Device series (1 s each).
	liveSamples  = 60
	endpointNote = "hourly, kept 30 days"
	liveNote     = "current rates"
	recordDevice = "device"
)

// talkerWindows are the stored windows of Top Talkers and Device detail.
var talkerWindows = []string{"1h", "24h", "7d", "30d"}

// errNotStarted is returned while the store is not attached.
var errNotStarted = errors.New("not started")

// deviceHistory persists the accounting feed: per-Device bytes per minute,
// per-Device × Remote Endpoint bytes per hour (top 50), WAN and Unattributed
// totals per minute, and Device metadata. It also keeps the last minute of
// polls in memory for the Live Device detail.
type deviceHistory struct {
	mu  sync.Mutex
	st  *store.Store // nil until open
	gen uint64       // bumped whenever in-memory rows move to the store

	// meta is every Device seen, persisted so a Device that went offline
	// keeps its name and identifiers.
	meta map[string]Device

	lastFlush time.Time        // minute boundary of the last minute flush
	minSlot   time.Time        // minute being accumulated; zero when empty
	minDev    map[string]Bytes // per Device
	minTalk   [talkerCols]float64

	hourSlot time.Time // hour of endpoints being accumulated; zero when empty
	hourEp   map[string]map[netip.Addr]Bytes

	recent []TrafficDelta // polls of the last minute, oldest first
}

func newDeviceHistory() *deviceHistory {
	return &deviceHistory{meta: map[string]Device{},
		minDev: map[string]Bytes{}, hourEp: map[string]map[netip.Addr]Bytes{}}
}

// open attaches the store and loads Device metadata. Rollups need no catch
// up: close rolled up every hour completed before the restart.
func (h *deviceHistory) open(st *store.Store, now time.Time) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.st = st
	recs, err := st.Records(recordDevice)
	if err != nil {
		log.Printf("device history: load metadata: %v", err)
	}
	for id, b := range recs {
		var d Device
		if json.Unmarshal(b, &d) == nil {
			d.ID = id
			h.meta[id] = d
		}
	}
	h.lastFlush = store.Floor(now, time.Minute)
}

// record is a TrafficDelta subscriber. A poll belongs to the minute (and
// hour) holding its last second.
func (h *deviceHistory) record(d TrafficDelta) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.st == nil {
		return
	}
	at := d.To.Add(-time.Second)
	if slot := store.Floor(at, time.Minute); !h.minSlot.Equal(slot) {
		if !h.minSlot.IsZero() {
			h.flushMinuteLocked(d.To)
		}
		h.minSlot = slot
	}
	for _, dd := range d.Devices {
		b := h.minDev[dd.Device.ID]
		b.add(dd.Bytes)
		h.minDev[dd.Device.ID] = b
		h.rememberLocked(dd.Device)
	}
	secs := d.To.Sub(d.From).Seconds()
	for i, v := range [talkerCols]float64{secs, float64(d.WAN.Rx), float64(d.WAN.Tx),
		float64(d.Unattributed.Rx), float64(d.Unattributed.Tx)} {
		h.minTalk[i] += v
	}

	if slot := store.Floor(at, time.Hour); !h.hourSlot.Equal(slot) {
		h.flushHourLocked(d.To)
		h.hourSlot = slot
	}
	for _, e := range d.Endpoints {
		m := h.hourEp[e.DeviceID]
		if m == nil {
			m = map[netip.Addr]Bytes{}
			h.hourEp[e.DeviceID] = m
		}
		b := m[e.Endpoint]
		b.add(e.Bytes)
		m[e.Endpoint] = b
	}

	h.recent = append(h.recent, d)
	cut := d.To.Add(-(liveSamples + 1) * time.Second)
	i := 0
	for i < len(h.recent) && !h.recent[i].To.After(cut) {
		i++
	}
	h.recent = h.recent[i:]
}

// rememberLocked persists d's metadata when it changed. Addresses that are
// no longer known keep their last-known value.
func (h *deviceHistory) rememberLocked(m Device) {
	old, ok := h.meta[m.ID]
	if len(m.Addresses) == 0 && ok {
		m.Addresses = old.Addresses
	}
	if m.Addresses == nil {
		m.Addresses = []string{}
	}
	if ok && sameDevice(old, m) {
		return
	}
	h.meta[m.ID] = m
	b, _ := json.Marshal(m)
	if err := h.st.PutRecord(recordDevice, m.ID, b); err != nil {
		log.Printf("device history: save metadata: %v", err)
	}
}

func sameDevice(a, b Device) bool {
	if a.Name != b.Name || a.Kind != b.Kind || a.MAC != b.MAC || a.Vendor != b.Vendor || len(a.Addresses) != len(b.Addresses) {
		return false
	}
	for i := range a.Addresses {
		if a.Addresses[i] != b.Addresses[i] {
			return false
		}
	}
	return true
}

// tick runs every second after the conntrack poll: it writes the completed
// minute (and hour of endpoints). A minute flush happens at every minute
// boundary, even without traffic, so coarser rollups stay current.
func (h *deviceHistory) tick(now time.Time) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.st == nil {
		return
	}
	if store.Floor(now, time.Minute).After(h.lastFlush) {
		h.flushMinuteLocked(now)
	}
	if !h.hourSlot.IsZero() && store.Floor(now, time.Hour).After(h.hourSlot) {
		h.flushHourLocked(now)
	}
}

func (h *deviceHistory) flushMinuteLocked(now time.Time) {
	var devRows, talkRows []store.Row
	if !h.minSlot.IsZero() {
		devRows, talkRows = h.pendingLocked()
	}
	h.minSlot, h.minDev, h.minTalk = time.Time{}, map[string]Bytes{}, [talkerCols]float64{}
	h.gen++
	h.lastFlush = store.Floor(now, time.Minute)
	if err := h.st.Write(deviceFamily, now, devRows); err != nil {
		log.Printf("device history: write: %v", err)
	}
	if err := h.st.Write(talkersFamily, now, talkRows); err != nil {
		log.Printf("device history: write: %v", err)
	}
}

// pendingLocked is the minute being accumulated, as store rows.
func (h *deviceHistory) pendingLocked() (dev, talk []store.Row) {
	if h.minSlot.IsZero() {
		return nil, nil
	}
	for id, b := range h.minDev {
		if b.Rx+b.Tx > 0 {
			dev = append(dev, store.Row{Slot: h.minSlot, Keys: []string{id}, Values: []float64{float64(b.Rx), float64(b.Tx)}})
		}
	}
	talk = []store.Row{{Slot: h.minSlot, Values: append([]float64(nil), h.minTalk[:]...)}}
	return dev, talk
}

// flushHourLocked writes the hour's endpoints, keeping the top
// endpointsKept per Device.
func (h *deviceHistory) flushHourLocked(now time.Time) {
	if h.hourSlot.IsZero() {
		return
	}
	var rows []store.Row
	for id, m := range h.hourEp {
		for _, e := range topEndpoints(m, endpointsKept) {
			rows = append(rows, store.Row{Slot: h.hourSlot, Keys: []string{id, e.ip.String()},
				Values: []float64{float64(e.Rx), float64(e.Tx)}})
		}
	}
	h.hourSlot, h.hourEp = time.Time{}, map[string]map[netip.Addr]Bytes{}
	h.gen++
	if err := h.st.Write(endpointFamily, now, rows); err != nil {
		log.Printf("device history: write endpoints: %v", err)
	}
}

type ipBytes struct {
	ip netip.Addr
	Bytes
}

// topEndpoints sorts by rx+tx descending (then address) and keeps n.
func topEndpoints(m map[netip.Addr]Bytes, n int) []ipBytes {
	out := make([]ipBytes, 0, len(m))
	for ip, b := range m {
		if b.Rx+b.Tx > 0 {
			out = append(out, ipBytes{ip, b})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.Rx+a.Tx != b.Rx+b.Tx {
			return a.Rx+a.Tx > b.Rx+b.Tx
		}
		return a.ip.Less(b.ip)
	})
	if len(out) > n {
		out = out[:n]
	}
	return out
}

// close writes the partial minute and hour.
func (h *deviceHistory) close(now time.Time) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.st == nil {
		return
	}
	h.flushMinuteLocked(now)
	h.flushHourLocked(now)
	h.st = nil
}

// memView is what a window request needs from the in-memory state, copied
// under the lock so that the store is queried without it.
type memView struct {
	st       *store.Store
	gen      uint64
	dev      []store.Row // the minute being accumulated
	talk     []store.Row
	hourSlot time.Time
	hourEp   map[netip.Addr]Bytes // one Device's endpoints of the hour in memory
}

// read runs fn on a snapshot of the in-memory state. fn queries the store
// without holding h.mu, so a slow store read or a slow client never stalls
// the per-second writer. If rows moved from memory to the store meanwhile
// (fn may then have counted them twice), fn runs again.
func (h *deviceHistory) read(id string, fn func(memView) error) error {
	for attempt := 0; ; attempt++ {
		h.mu.Lock()
		if h.st == nil {
			h.mu.Unlock()
			return errNotStarted
		}
		v := memView{st: h.st, gen: h.gen, hourSlot: h.hourSlot}
		v.dev, v.talk = h.pendingLocked()
		if id != "" {
			v.hourEp = maps.Clone(h.hourEp[id])
		}
		h.mu.Unlock()
		if err := fn(v); err != nil {
			return err
		}
		h.mu.Lock()
		same := h.gen == v.gen
		h.mu.Unlock()
		if same || attempt == 2 {
			return nil
		}
	}
}

// rows returns f's (deviceFamily or talkersFamily) one-minute rows with
// slots in [from, to), including the minute still in memory. Every stored
// window is served at one minute: that Resolution is kept 30 days.
func (v memView) rows(f *store.Family, from, to time.Time, match ...string) ([]store.Row, error) {
	rows, err := v.st.Query(f, store.Res1m, from, to, match...)
	if err != nil {
		return nil, err
	}
	pending := v.talk
	if f == deviceFamily {
		pending = v.dev
	}
	for _, r := range pending {
		if len(match) > 0 && r.Keys[0] != match[0] {
			continue
		}
		if !r.Slot.Before(from) && r.Slot.Before(to) {
			rows = append(rows, r)
		}
	}
	return rows, nil
}

// minuteEnd is the end of the minute holding t.
func minuteEnd(t time.Time) time.Time { return store.Floor(t, time.Minute).Add(time.Minute) }

var errUnknownDevice = errors.New("unknown device")

// known reports whether id was ever seen.
func (h *deviceHistory) known(id string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.st == nil {
		return errNotStarted
	}
	if _, ok := h.meta[id]; !ok {
		return errUnknownDevice
	}
	return nil
}

func (h *deviceHistory) deviceRow(id string, rx, tx float64) DeviceRow {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.deviceRowLocked(id, rx, tx)
}

func (h *deviceHistory) deviceRowLocked(id string, rx, tx float64) DeviceRow {
	d, ok := h.meta[id]
	if !ok {
		d = Device{ID: id, Name: id}
	}
	return newDeviceRow(d, rx, tx)
}

// writeReadError answers a failed read.
func writeReadError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, errNotStarted):
		writeError(w, http.StatusServiceUnavailable, err.Error())
	case errors.Is(err, errUnknownDevice):
		writeError(w, http.StatusNotFound, err.Error())
	default:
		writeError(w, http.StatusInternalServerError, err.Error())
	}
}

// WindowTalkers is /api/top-talkers.
type WindowTalkers struct {
	Window       string      `json:"window"`
	Unit         string      `json:"unit"`
	From         int64       `json:"from"`
	To           int64       `json:"to"`
	WanRx        float64     `json:"wanRx"`
	WanTx        float64     `json:"wanTx"`
	Devices      []DeviceRow `json:"devices"`
	Unattributed RxTx        `json:"unattributed"`
}

func (a *App) handleTopTalkers(w http.ResponseWriter, r *http.Request) {
	now := a.cfg.Clock.Now()
	win, ok := newWindow(r.URL.Query().Get("window"), talkersFamily, now, talkerWindows...)
	if !ok {
		writeError(w, http.StatusBadRequest, "unknown window")
		return
	}
	h := a.devHist
	var resp WindowTalkers
	err := h.read("", func(v memView) error {
		devRows, err := v.rows(deviceFamily, win.from, minuteEnd(now))
		if err != nil {
			return err
		}
		talkRows, err := v.rows(talkersFamily, win.from, minuteEnd(now))
		if err != nil {
			return err
		}
		resp = WindowTalkers{Window: win.name, Unit: unitBytes, From: win.from.UnixMilli(), To: now.UnixMilli(), Devices: []DeviceRow{}}
		for _, row := range talkRows {
			resp.WanRx += row.Values[tWanRx]
			resp.WanTx += row.Values[tWanTx]
			resp.Unattributed.Rx += row.Values[tUnattrRx]
			resp.Unattributed.Tx += row.Values[tUnattrTx]
		}
		sums := map[string]*RxTx{}
		for _, row := range devRows {
			s := sums[row.Keys[0]]
			if s == nil {
				s = &RxTx{}
				sums[row.Keys[0]] = s
			}
			s.Rx += row.Values[0]
			s.Tx += row.Values[1]
		}
		for id, s := range sums {
			if s.Rx+s.Tx > 0 {
				resp.Devices = append(resp.Devices, h.deviceRow(id, s.Rx, s.Tx))
			}
		}
		return nil
	})
	if err != nil {
		writeReadError(w, err)
		return
	}
	sortDeviceRows(resp.Devices)
	writeJSON(w, resp)
}

func sortDeviceRows(rows []DeviceRow) {
	sort.Slice(rows, func(i, j int) bool {
		a, b := rows[i], rows[j]
		if a.Rx+a.Tx != b.Rx+b.Tx {
			return a.Rx+a.Tx > b.Rx+b.Tx
		}
		return a.ID < b.ID
	})
}

// DeviceDetail is /api/devices/{id}.
type DeviceDetail struct {
	Device        DeviceRow     `json:"device"`
	Window        string        `json:"window"`
	Unit          string        `json:"unit"`
	Resolution    string        `json:"resolution"`
	Series        DeviceSeries  `json:"series"`
	Endpoints     []EndpointRow `json:"endpoints"`
	EndpointsNote string        `json:"endpointsNote"`
}

// DeviceSeries is a Device's received/sent rates over the window.
type DeviceSeries struct {
	Start  int64      `json:"start"`
	StepMs int64      `json:"stepMs"`
	RxBps  []*float64 `json:"rxBps"`
	TxBps  []*float64 `json:"txBps"`
}

// EndpointRow is one Remote Endpoint of a Device detail. Host, Org and ASN
// are null until labelled.
type EndpointRow struct {
	IP   string  `json:"ip"`
	Host *string `json:"host"`
	Org  *string `json:"org"`
	ASN  *int    `json:"asn"`
	Rx   float64 `json:"rx"`
	Tx   float64 `json:"tx"`
}

func (a *App) handleDevice(w http.ResponseWriter, r *http.Request) {
	now := a.cfg.Clock.Now()
	id := r.PathValue("id")
	name := r.URL.Query().Get("window")
	var resp DeviceDetail
	var err error
	if name == windowLive {
		resp, err = a.devHist.liveDetail(id, now)
	} else {
		win, ok := newWindow(name, deviceFamily, now, talkerWindows...)
		if !ok {
			writeError(w, http.StatusBadRequest, "unknown window")
			return
		}
		resp, err = a.devHist.windowDetail(id, win)
	}
	if err != nil {
		writeReadError(w, err)
		return
	}
	a.labels.label(resp.Endpoints)
	writeJSON(w, resp)
}

func (h *deviceHistory) liveDetail(id string, now time.Time) (DeviceDetail, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.st == nil {
		return DeviceDetail{}, errNotStarted
	}
	if _, ok := h.meta[id]; !ok {
		return DeviceDetail{}, errUnknownDevice
	}
	last := store.Floor(now, time.Second)
	start := last.Add(-(liveSamples - 1) * time.Second)
	s := DeviceSeries{Start: start.UnixMilli(), StepMs: 1000,
		RxBps: make([]*float64, liveSamples), TxBps: make([]*float64, liveSamples)}
	var cur RxTx
	eps := map[netip.Addr]Bytes{}
	var curSecs float64
	for k, d := range h.recent {
		secs := d.To.Sub(d.From).Seconds()
		if secs <= 0 {
			continue
		}
		var b Bytes
		for _, dd := range d.Devices {
			if dd.Device.ID == id {
				b = dd.Bytes
			}
		}
		for i := range s.RxBps {
			t := start.Add(time.Duration(i) * time.Second)
			if t.After(d.From) && !t.After(d.To) {
				rx, tx := bps(float64(b.Rx), secs), bps(float64(b.Tx), secs)
				s.RxBps[i], s.TxBps[i] = &rx, &tx
			}
		}
		if k == len(h.recent)-1 {
			cur = RxTx{Rx: bps(float64(b.Rx), secs), Tx: bps(float64(b.Tx), secs)}
			curSecs = secs
			for _, e := range d.Endpoints {
				if e.DeviceID == id {
					eps[e.Endpoint] = e.Bytes
				}
			}
		}
	}
	resp := DeviceDetail{Device: h.deviceRowLocked(id, cur.Rx, cur.Tx), Window: windowLive, Unit: unitBps,
		Resolution: "1s", Series: s, Endpoints: []EndpointRow{}, EndpointsNote: liveNote}
	for _, e := range topEndpoints(eps, endpointsKept) {
		resp.Endpoints = append(resp.Endpoints, EndpointRow{IP: e.ip.String(),
			Rx: bps(float64(e.Rx), curSecs), Tx: bps(float64(e.Tx), curSecs)})
	}
	return resp, nil
}

// windowDetail is a Device's stored window: its rates on w's grid (one-minute
// data summed into steps of w.step), its totals since w.from, and the Remote
// Endpoints of the hours overlapping the window.
func (h *deviceHistory) windowDetail(id string, w window) (DeviceDetail, error) {
	if err := h.known(id); err != nil {
		return DeviceDetail{}, err
	}
	var resp DeviceDetail
	err := h.read(id, func(v memView) error {
		to := minuteEnd(w.to)
		devRows, err := v.rows(deviceFamily, w.start, to, id)
		if err != nil {
			return err
		}
		talkRows, err := v.rows(talkersFamily, w.start, to)
		if err != nil {
			return err
		}
		secs := make([]float64, w.n)
		bytes := make([]Bytes, w.n)
		for _, r := range talkRows {
			if i, ok := w.index(r.Slot); ok {
				secs[i] += r.Values[tSecs]
			}
		}
		var total RxTx
		for _, r := range devRows {
			if i, ok := w.index(r.Slot); ok {
				bytes[i].Rx += uint64(r.Values[0])
				bytes[i].Tx += uint64(r.Values[1])
			}
			if !r.Slot.Before(w.from) {
				total.Rx += r.Values[0]
				total.Tx += r.Values[1]
			}
		}
		s := DeviceSeries{Start: w.start.UnixMilli(), StepMs: w.step.Milliseconds(),
			RxBps: make([]*float64, w.n), TxBps: make([]*float64, w.n)}
		for i := range secs {
			if secs[i] <= 0 {
				continue
			}
			rx, tx := bps(float64(bytes[i].Rx), secs[i]), bps(float64(bytes[i].Tx), secs[i])
			s.RxBps[i], s.TxBps[i] = &rx, &tx
		}

		// Endpoints: the stored hours overlapping the window plus the hour
		// in memory.
		epFrom := store.Floor(w.from, time.Hour)
		epRows, err := v.st.Query(endpointFamily, endpointFamily.Resolutions[0], epFrom, to, id)
		if err != nil {
			return err
		}
		eps := map[netip.Addr]Bytes{}
		for _, r := range epRows {
			ip, err := netip.ParseAddr(r.Keys[1])
			if err != nil {
				continue
			}
			b := eps[ip]
			b.add(Bytes{Rx: uint64(r.Values[0]), Tx: uint64(r.Values[1])})
			eps[ip] = b
		}
		if !v.hourSlot.IsZero() && !v.hourSlot.Before(epFrom) {
			for ip, b := range v.hourEp {
				e := eps[ip]
				e.add(b)
				eps[ip] = e
			}
		}
		resp = DeviceDetail{Device: h.deviceRow(id, total.Rx, total.Tx), Window: w.name, Unit: unitBytes,
			Resolution: w.res.Name, Series: s, Endpoints: []EndpointRow{}, EndpointsNote: endpointNote}
		for _, e := range topEndpoints(eps, endpointsKept) {
			resp.Endpoints = append(resp.Endpoints, EndpointRow{IP: e.ip.String(), Rx: float64(e.Rx), Tx: float64(e.Tx)})
		}
		return nil
	})
	return resp, err
}
