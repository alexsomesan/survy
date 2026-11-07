package app

import (
	"sync"
	"time"
)

// TopTalkers is the API's Top Talkers object (docs/api.md). In the Live
// window rx/tx are rates in bits per second.
type TopTalkers struct {
	Window       string      `json:"window"`
	Unit         string      `json:"unit"`
	WanRx        float64     `json:"wanRx"`
	WanTx        float64     `json:"wanTx"`
	Devices      []DeviceRow `json:"devices"`
	Unattributed RxTx        `json:"unattributed"`
}

// DeviceRow is the API's Device object.
type DeviceRow struct {
	ID        string   `json:"id"`
	Name      string   `json:"name"`
	Kind      string   `json:"kind"`
	MAC       *string  `json:"mac"`
	Vendor    *string  `json:"vendor"`
	Addresses []string `json:"addresses"`
	Rx        float64  `json:"rx"`
	Tx        float64  `json:"tx"`
}

// RxTx is a received/sent pair (rates or bytes, per the enclosing unit).
type RxTx struct {
	Rx float64 `json:"rx"`
	Tx float64 `json:"tx"`
}

func newDeviceRow(d Device, rx, tx float64) DeviceRow {
	r := DeviceRow{ID: d.ID, Name: d.Name, Kind: d.Kind, Addresses: d.Addresses, Rx: rx, Tx: tx}
	if r.Addresses == nil {
		r.Addresses = []string{}
	}
	if d.MAC != "" {
		mac := d.MAC
		r.MAC = &mac
	}
	if d.Vendor != "" {
		v := d.Vendor
		r.Vendor = &v
	}
	return r
}

// initTopTalkers wires Device identification, WAN Traffic accounting and the
// Live Top Talkers together. Other modules (History) subscribe to the same
// accounting feed with a.traffic.subscribe.
func (a *App) initTopTalkers() {
	s := a.cfg.Settings
	d := newDevices(s, a.path("/config/dhcp/dhcp4-leases.csv"))
	a.traffic = newTraffic(a.cfg.Kernel, d, a.path("/sys/class/net"), s.WANIface)
	a.liveTalkers = newLiveTalkers()
	a.traffic.subscribe(a.liveTalkers.record)
	a.devHist = newDeviceHistory()
	a.traffic.subscribe(a.devHist.record)
}

// liveTalkers holds the Live Top Talkers: the rates from the latest poll.
type liveTalkers struct {
	mu  sync.Mutex
	cur TopTalkers
}

func newLiveTalkers() *liveTalkers {
	return &liveTalkers{cur: TopTalkers{Window: windowLive, Unit: unitBps, Devices: []DeviceRow{}}}
}

// record is a TrafficDelta subscriber.
func (l *liveTalkers) record(d TrafficDelta) {
	secs := d.To.Sub(d.From).Seconds()
	if secs <= 0 {
		secs = time.Second.Seconds()
	}
	rate := func(b uint64) float64 { return bps(float64(b), secs) }
	tt := TopTalkers{
		Window: windowLive, Unit: unitBps,
		WanRx: rate(d.WAN.Rx), WanTx: rate(d.WAN.Tx),
		Devices:      make([]DeviceRow, 0, len(d.Devices)),
		Unattributed: RxTx{Rx: rate(d.Unattributed.Rx), Tx: rate(d.Unattributed.Tx)},
	}
	for _, dd := range d.Devices {
		tt.Devices = append(tt.Devices, newDeviceRow(dd.Device, rate(dd.Rx), rate(dd.Tx)))
	}
	l.mu.Lock()
	l.cur = tt
	l.mu.Unlock()
}

func (l *liveTalkers) snapshot() TopTalkers {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.cur
}
