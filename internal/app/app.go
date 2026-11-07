// Package app is the dashboard: collection, storage and the HTTP API.
// Its public surface is New, App.Handler, App.Start and App.Close; tests drive
// it only through the HTTP API (see internal/testkit).
package app

import (
	"errors"
	"net/http"
	"path/filepath"
	"sync"
	"time"

	"survy/internal/ports"
)

// Config is everything the app needs from the outside world.
type Config struct {
	// Root is prepended to every router file path (/proc, /sys, /config, /run).
	// "/" in production; a fixture tree in tests.
	Root string
	// DataDir holds the SQLite History and the files the router writes for us
	// (inventory.json, aliases.json, ip2asn-combined.tsv.gz).
	DataDir string

	Kernel   ports.Kernel
	Clock    ports.Clock
	Resolver ports.Resolver
	// Location is the time zone whose midnight starts "today" (default
	// time.Local).
	Location *time.Location

	Settings Settings
}

// Settings are tunables with production defaults (see DefaultSettings).
type Settings struct {
	WANIface string // "pppoe0"
	LANIface string // "br0"
	WGIface  string // "wg0"
	// LineRateBps is the WAN download line rate in bits per second.
	LineRateBps float64
	// StorageCapBytes is the guard cap on the History database.
	StorageCapBytes int64
	// DataHostPath is where the router mounts DataDir ("/mnt/dash-data"),
	// reported as the data disk's path.
	DataHostPath string
	// InventoryPath, AliasPath and ASNPath default to files in DataDir.
	InventoryPath string
	AliasPath     string
	ASNPath       string
}

// DefaultSettings are the production values from spec 0001.
func DefaultSettings() Settings {
	return Settings{
		WANIface:        "pppoe0",
		LANIface:        "br0",
		WGIface:         "wg0",
		LineRateBps:     1e9,
		StorageCapBytes: 8 << 30,
		DataHostPath:    "/mnt/dash-data",
	}
}

// App is a running dashboard.
type App struct {
	cfg  Config
	mux  *http.ServeMux
	live *liveHub
	res  *resources
	hist *history // nil until Start

	traffic     *traffic
	liveTalkers *liveTalkers
	devHist     *deviceHistory
	labels      *labeller

	mu    sync.Mutex
	stops []func()
}

// New builds an App. It does not start collecting until Start is called.
func New(cfg Config) (*App, error) {
	if cfg.Kernel == nil || cfg.Clock == nil {
		return nil, errors.New("app: Kernel and Clock are required")
	}
	if cfg.Location == nil {
		cfg.Location = time.Local
	}
	if cfg.Root == "" {
		cfg.Root = "/"
	}
	def := DefaultSettings()
	s := &cfg.Settings
	if s.WANIface == "" {
		s.WANIface = def.WANIface
	}
	if s.LANIface == "" {
		s.LANIface = def.LANIface
	}
	if s.WGIface == "" {
		s.WGIface = def.WGIface
	}
	if s.LineRateBps == 0 {
		s.LineRateBps = def.LineRateBps
	}
	if s.StorageCapBytes == 0 {
		s.StorageCapBytes = def.StorageCapBytes
	}
	if s.DataHostPath == "" {
		s.DataHostPath = def.DataHostPath
	}
	if s.InventoryPath == "" {
		s.InventoryPath = filepath.Join(cfg.DataDir, "inventory.json")
	}
	if s.AliasPath == "" {
		s.AliasPath = filepath.Join(cfg.DataDir, "aliases.json")
	}
	if s.ASNPath == "" {
		s.ASNPath = filepath.Join(cfg.DataDir, "ip2asn-combined.tsv.gz")
	}

	a := &App{cfg: cfg, live: newLiveHub()}
	a.res = newResources(a)
	a.initTopTalkers()
	a.labels = newLabeller(cfg.Resolver, cfg.Clock, cfg.Settings.ASNPath)
	a.routes()
	return a, nil
}

// Handler serves the API and the frontend.
func (a *App) Handler() http.Handler { return a.mux }

// Start schedules collection on the Clock.
func (a *App) Start() error {
	now := a.cfg.Clock.Now()
	a.res.collect(now) // baseline for the first second's deltas
	hist, err := openHistory(a, now)
	if err != nil {
		return err
	}
	a.hist = hist
	a.devHist.open(hist.st, now)
	a.labels.start()
	a.every(asnCheckEvery, a.labels.asn.check)
	a.every(time.Second, a.tickSecond)
	a.every(time.Hour, a.hist.maintain)
	return nil
}

// Close stops collection and releases resources.
func (a *App) Close() error {
	a.mu.Lock()
	stops := a.stops
	a.stops = nil
	a.mu.Unlock()
	for _, s := range stops {
		s()
	}
	a.labels.stop()
	if a.hist != nil {
		a.devHist.close(a.cfg.Clock.Now())
		a.hist.close(a.cfg.Clock.Now())
	}
	a.live.closeAll()
	return nil
}

func (a *App) every(d time.Duration, fn func(time.Time)) {
	stop := a.cfg.Clock.Every(d, fn)
	a.mu.Lock()
	a.stops = append(a.stops, stop)
	a.mu.Unlock()
}

// LiveClients is the number of connected Live stream clients (it drives the
// conntrack polling cadence; exposed for tests and diagnostics).
func (a *App) LiveClients() int { return a.live.count() }

// PendingLookups is the number of Remote Endpoint reverse DNS lookups
// queued or in flight (exposed for tests and diagnostics).
func (a *App) PendingLookups() int { return a.labels.dns.pendingCount() }

// path resolves a router file path against Root.
func (a *App) path(p string) string { return filepath.Join(a.cfg.Root, p) }

// tickSecond runs once per second: collectors update their state, then the
// Live event is broadcast.
func (a *App) tickSecond(now time.Time) {
	a.res.collect(now)
	a.hist.record(now, a.res.latest())
	a.traffic.tick(now, a.live.count() > 0)
	a.devHist.tick(now)
	a.live.broadcast(a.buildLive(now))
}
