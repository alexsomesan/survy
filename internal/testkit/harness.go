package testkit

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"survy/internal/app"
	"survy/internal/ports"
)

// Start is the fake clock's initial instant in every harness.
var Start = time.Date(2026, 11, 7, 1, 0, 0, 0, time.UTC)

// Harness is a running dashboard wired to fakes and a fixture root.
type Harness struct {
	t        testing.TB
	App      *app.App
	Server   *httptest.Server
	Kernel   *Kernel
	Clock    *Clock
	Resolver *Resolver
	Root     string // writable copy of testdata/root
	DataDir  string
	Settings app.Settings
}

// Option tweaks a harness before the app is built.
type Option func(*Harness)

// WithSettings overrides app settings (zero fields keep defaults).
func WithSettings(s app.Settings) Option { return func(h *Harness) { h.Settings = s } }

// WithEmptyKernel skips loading the recorded kernel tables.
func WithEmptyKernel() Option { return func(h *Harness) { h.Kernel = NewKernel() } }

// New builds and starts a dashboard against a fresh copy of the fixtures.
func New(t testing.TB, opts ...Option) *Harness {
	t.Helper()
	h := &Harness{
		t:        t,
		Clock:    NewClock(Start),
		Resolver: NewResolver(),
		Root:     t.TempDir(),
		DataDir:  t.TempDir(),
	}
	for _, o := range opts {
		o(h)
	}
	if h.Kernel == nil {
		h.Kernel = NewKernel()
		if err := LoadRecordedKernel(h.Kernel); err != nil {
			t.Fatalf("load recorded kernel: %v", err)
		}
	}
	// Statfs paths are this process's paths: the router's system partition
	// is reached through its /boot under Root.
	h.Kernel.SetDisk(filepath.Join(h.Root, "boot"), ports.DiskUsage{Total: 5 << 30, Free: 2800 << 20, Avail: 2600 << 20})
	h.Kernel.SetDisk(h.DataDir, ports.DiskUsage{Total: 10 << 30, Free: 9 << 30, Avail: 9 << 30})
	if err := copyTree(filepath.Join(TestdataDir(), "root"), h.Root); err != nil {
		t.Fatalf("copy fixture root: %v", err)
	}
	h.start()
	t.Cleanup(h.stop)
	return h
}

func (h *Harness) start() {
	a, err := app.New(app.Config{
		Root: h.Root, DataDir: h.DataDir,
		Kernel: h.Kernel, Clock: h.Clock, Resolver: h.Resolver,
		Location: time.UTC, Settings: h.Settings,
	})
	if err != nil {
		h.t.Fatalf("app.New: %v", err)
	}
	if err := a.Start(); err != nil {
		h.t.Fatalf("app.Start: %v", err)
	}
	h.App = a
	h.Server = httptest.NewServer(stallable(a.Handler()))
}

// stallHeader marks a request whose response writes block until released
// (see StalledGet).
const stallHeader = "X-Testkit-Stall"

var stalls sync.Map // stall id -> *stall

type stall struct {
	writing chan struct{} // closed when the handler first writes
	release chan struct{}
	once    sync.Once
}

// stallable wraps the app's handler so a test can play a client that stops
// reading: the handler's first Write blocks until the test releases it.
func stallable(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if id := r.Header.Get(stallHeader); id != "" {
			if v, ok := stalls.Load(id); ok {
				w = &stallWriter{ResponseWriter: w, s: v.(*stall)}
			}
		}
		next.ServeHTTP(w, r)
	})
}

type stallWriter struct {
	http.ResponseWriter
	s *stall
}

func (w *stallWriter) Write(b []byte) (int, error) {
	w.s.once.Do(func() { close(w.s.writing) })
	<-w.s.release
	return w.ResponseWriter.Write(b)
}

// StalledGet starts a GET whose response the client does not read: the
// handler blocks on its first write until release is called. StalledGet
// returns once the handler has started writing. release waits for the
// request to finish.
func (h *Harness) StalledGet(path string) (release func()) {
	h.t.Helper()
	id := fmt.Sprintf("%p", h) + path + time.Now().String()
	s := &stall{writing: make(chan struct{}), release: make(chan struct{})}
	stalls.Store(id, s)
	req, _ := http.NewRequest(http.MethodGet, h.Server.URL+path, nil)
	req.Header.Set(stallHeader, id)
	done := make(chan struct{})
	go func() {
		defer close(done)
		resp, err := http.DefaultClient.Do(req)
		if err == nil {
			_, _ = io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
		}
	}()
	select {
	case <-s.writing:
	case <-time.After(2 * time.Second):
		h.t.Fatalf("GET %s: handler did not start writing", path)
	}
	var once sync.Once
	release = func() {
		once.Do(func() {
			close(s.release)
			<-done
			stalls.Delete(id)
		})
	}
	h.t.Cleanup(release)
	return release
}

// AdvanceWithin is Advance(d), failing the test if it takes longer than
// limit of real time (e.g. because collection is blocked).
func (h *Harness) AdvanceWithin(d, limit time.Duration) {
	h.t.Helper()
	done := make(chan struct{})
	go func() {
		defer close(done)
		h.Clock.Advance(d)
	}()
	select {
	case <-done:
	case <-time.After(limit):
		h.t.Fatalf("advancing the clock by %v took longer than %v: collection is blocked", d, limit)
	}
}

func (h *Harness) stop() {
	if h.Server != nil {
		h.Server.CloseClientConnections()
		h.Server.Close()
		h.Server = nil
	}
	if h.App != nil {
		_ = h.App.Close()
		h.App = nil
	}
}

// Restart closes the app and starts a new one on the same root and data dir
// (simulates a container restart).
func (h *Harness) Restart() {
	h.stop()
	h.start()
}

// RestartAfter stops the app, lets d pass with nothing running, and starts
// a new app on the same root and data dir (an outage of length d).
func (h *Harness) RestartAfter(d time.Duration) {
	h.stop()
	h.Clock.Skip(d)
	h.start()
}

// FastForward moves the fake clock forward by d in steps of step, running
// due jobs once per step (see Clock.FastForward).
func (h *Harness) FastForward(d, step time.Duration) { h.Clock.FastForward(d, step) }

// T is the test the harness belongs to.
func (h *Harness) T() testing.TB { return h.t }

// Advance moves the fake clock forward (see Clock.Advance).
func (h *Harness) Advance(d time.Duration) { h.Clock.Advance(d) }

// WriteFile replaces a file under the fixture root (path like "/proc/stat").
func (h *Harness) WriteFile(path, content string) {
	h.t.Helper()
	p := filepath.Join(h.Root, path)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		h.t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		h.t.Fatal(err)
	}
}

// RemoveFile deletes a file under the fixture root.
func (h *Harness) RemoveFile(path string) {
	h.t.Helper()
	if err := os.Remove(filepath.Join(h.Root, path)); err != nil {
		h.t.Fatal(err)
	}
}

// ReadFile returns a file under the fixture root.
func (h *Harness) ReadFile(path string) string {
	h.t.Helper()
	b, err := os.ReadFile(filepath.Join(h.Root, path))
	if err != nil {
		h.t.Fatal(err)
	}
	return string(b)
}

// WaitLiveClients waits (up to 2 s) until exactly n Live clients are
// registered, e.g. after LiveStream.Close, whose unregistration is async.
func (h *Harness) WaitLiveClients(n int) {
	h.t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for h.App.LiveClients() != n {
		if time.Now().After(deadline) {
			h.t.Fatalf("live clients = %d, want %d", h.App.LiveClients(), n)
		}
		time.Sleep(time.Millisecond)
	}
}

// WaitLabels waits (up to 2 s) until every queued Remote Endpoint lookup
// (reverse DNS) has finished and been cached.
func (h *Harness) WaitLabels() {
	h.t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for h.App.PendingLookups() != 0 {
		if time.Now().After(deadline) {
			h.t.Fatalf("pending lookups = %d after 2 s", h.App.PendingLookups())
		}
		time.Sleep(time.Millisecond)
	}
}

// WriteData writes a file into the data dir (inventory.json, aliases.json, ...).
func (h *Harness) WriteData(name string, content []byte) {
	h.t.Helper()
	if err := os.WriteFile(filepath.Join(h.DataDir, name), content, 0o644); err != nil {
		h.t.Fatal(err)
	}
}

// Get performs a GET and returns status and body.
func (h *Harness) Get(path string) (int, []byte) {
	h.t.Helper()
	resp, err := http.Get(h.Server.URL + path)
	if err != nil {
		h.t.Fatalf("GET %s: %v", path, err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, b
}

// GetJSON GETs path, requires 200 and decodes into v.
func (h *Harness) GetJSON(path string, v any) {
	h.t.Helper()
	code, b := h.Get(path)
	if code != http.StatusOK {
		h.t.Fatalf("GET %s: status %d: %s", path, code, b)
	}
	if err := json.Unmarshal(b, v); err != nil {
		h.t.Fatalf("GET %s: decode: %v\n%s", path, err, b)
	}
}

// LiveStream is an open /api/live connection.
type LiveStream struct {
	h      *Harness
	resp   *http.Response
	events chan []byte
}

// OpenLive connects to /api/live. The connection is registered (and its
// snapshot event received) before OpenLive returns.
func (h *Harness) OpenLive() *LiveStream {
	h.t.Helper()
	resp, err := http.Get(h.Server.URL + "/api/live")
	if err != nil {
		h.t.Fatalf("open live: %v", err)
	}
	s := &LiveStream{h: h, resp: resp, events: make(chan []byte, 64)}
	go s.read()
	h.t.Cleanup(s.Close)
	return s
}

func (s *LiveStream) read() {
	defer close(s.events)
	r := bufio.NewReader(s.resp.Body)
	var data strings.Builder
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return
		}
		line = strings.TrimRight(line, "\r\n")
		switch {
		case strings.HasPrefix(line, "data: "):
			data.WriteString(strings.TrimPrefix(line, "data: "))
		case line == "" && data.Len() > 0:
			s.events <- []byte(data.String())
			data.Reset()
		}
	}
}

// Next decodes the next Live event into v (fails after 2 s).
func (s *LiveStream) Next(v any) {
	s.h.t.Helper()
	select {
	case b, ok := <-s.events:
		if !ok {
			s.h.t.Fatal("live stream closed")
		}
		if err := json.Unmarshal(b, v); err != nil {
			s.h.t.Fatalf("decode live event: %v\n%s", err, b)
		}
	case <-time.After(2 * time.Second):
		s.h.t.Fatal("timed out waiting for live event")
	}
}

// Latest drains buffered events and decodes the most recent one into v,
// waiting for at least one.
func (s *LiveStream) Latest(v any) {
	s.h.t.Helper()
	var last []byte
	select {
	case b, ok := <-s.events:
		if !ok {
			s.h.t.Fatal("live stream closed")
		}
		last = b
	case <-time.After(2 * time.Second):
		s.h.t.Fatal("timed out waiting for live event")
	}
drain:
	for {
		select {
		case b, ok := <-s.events:
			if !ok {
				break drain
			}
			last = b
		default:
			break drain
		}
	}
	if err := json.Unmarshal(last, v); err != nil {
		s.h.t.Fatalf("decode live event: %v", err)
	}
}

// Close disconnects the stream and waits until the server has unregistered it.
func (s *LiveStream) Close() {
	if s.resp != nil {
		s.resp.Body.Close()
		s.resp = nil
		for range s.events {
		}
	}
}

func copyTree(src, dst string) error {
	return filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(target, b, 0o644)
	})
}
