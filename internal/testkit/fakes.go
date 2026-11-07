// Package testkit is the single test seam for the dashboard: it builds an
// app.App wired to a fixture root directory, a fake Kernel, a fake Clock and a
// fake Resolver, and offers helpers to drive it through its HTTP API.
package testkit

import (
	"context"
	"errors"
	"sort"
	"sync"
	"time"

	"survy/internal/ports"
)

// Kernel is a fake ports.Kernel. Tests mutate it between clock advances.
type Kernel struct {
	mu        sync.Mutex
	flows     map[uint32]ports.Flow
	destroyed []ports.Flow
	neigh     []ports.Neighbour
	addrs     []ports.IfAddr
	disks     map[string]ports.DiskUsage
	// ConntrackCalls counts Conntrack() calls (to observe polling cadence).
	ConntrackCalls int
}

func NewKernel() *Kernel {
	return &Kernel{flows: map[uint32]ports.Flow{}, disks: map[string]ports.DiskUsage{}}
}

func (k *Kernel) Conntrack() ([]ports.Flow, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.ConntrackCalls++
	out := make([]ports.Flow, 0, len(k.flows))
	for _, f := range k.flows {
		out = append(out, f)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

func (k *Kernel) DrainDestroyed() []ports.Flow {
	k.mu.Lock()
	defer k.mu.Unlock()
	d := k.destroyed
	k.destroyed = nil
	return d
}

func (k *Kernel) Neighbours() ([]ports.Neighbour, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	return append([]ports.Neighbour(nil), k.neigh...), nil
}

func (k *Kernel) Addresses() ([]ports.IfAddr, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	return append([]ports.IfAddr(nil), k.addrs...), nil
}

func (k *Kernel) Statfs(path string) (ports.DiskUsage, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	d, ok := k.disks[path]
	if !ok {
		return ports.DiskUsage{}, errors.New("statfs: no such path in fake: " + path)
	}
	return d, nil
}

// ConntrackCallCount is ConntrackCalls, read safely while the app runs.
func (k *Kernel) ConntrackCallCount() int {
	k.mu.Lock()
	defer k.mu.Unlock()
	return k.ConntrackCalls
}

// SetFlow adds or replaces a live flow (keyed by ID).
func (k *Kernel) SetFlow(f ports.Flow) {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.flows[f.ID] = f
}

// DestroyFlow removes a live flow and queues its final state (f) as destroyed.
func (k *Kernel) DestroyFlow(f ports.Flow) {
	k.mu.Lock()
	defer k.mu.Unlock()
	delete(k.flows, f.ID)
	k.destroyed = append(k.destroyed, f)
}

// ClearFlows removes every live flow without destroy events.
func (k *Kernel) ClearFlows() {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.flows = map[uint32]ports.Flow{}
}

func (k *Kernel) SetNeighbours(n []ports.Neighbour) {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.neigh = append([]ports.Neighbour(nil), n...)
}

func (k *Kernel) SetAddresses(a []ports.IfAddr) {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.addrs = append([]ports.IfAddr(nil), a...)
}

func (k *Kernel) SetDisk(path string, d ports.DiskUsage) {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.disks[path] = d
}

// Clock is a fake ports.Clock. Advance moves time forward one second at a
// time, synchronously running every scheduled function that falls due.
type Clock struct {
	mu   sync.Mutex
	now  time.Time
	jobs []*job
	seq  int
}

type job struct {
	id     int
	every  time.Duration
	next   time.Time
	fn     func(time.Time)
	active bool
}

// NewClock starts at the given instant.
func NewClock(start time.Time) *Clock { return &Clock{now: start} }

func (c *Clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *Clock) Every(d time.Duration, fn func(time.Time)) func() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.seq++
	j := &job{id: c.seq, every: d, next: c.now.Add(d), fn: fn, active: true}
	c.jobs = append(c.jobs, j)
	return func() {
		c.mu.Lock()
		defer c.mu.Unlock()
		j.active = false
	}
}

// Advance moves the clock forward by d in one-second steps, running due jobs
// in registration order at each step.
func (c *Clock) Advance(d time.Duration) {
	end := c.Now().Add(d)
	for {
		c.mu.Lock()
		step := c.now.Add(time.Second)
		if step.After(end) {
			c.now = end
			c.mu.Unlock()
			return
		}
		c.now = step
		var due []*job
		for _, j := range c.jobs {
			if j.active && !j.next.After(step) {
				due = append(due, j)
				j.next = j.next.Add(j.every)
			}
		}
		c.mu.Unlock()
		for _, j := range due {
			j.fn(step)
		}
	}
}

// FastForward moves the clock forward by d in steps of step (instead of one
// second), running each job at most once per step: a job that fell due one or
// more times during the step runs once, at the step's instant, the way a
// real ticker drops ticks it could not deliver. Use it to cover days or years
// quickly; per-second jobs then see step-long intervals.
func (c *Clock) FastForward(d, step time.Duration) {
	if step <= 0 {
		panic("testkit: FastForward step must be positive")
	}
	end := c.Now().Add(d)
	for {
		c.mu.Lock()
		at := c.now.Add(step)
		if at.After(end) {
			at = end
		}
		if !at.After(c.now) {
			c.mu.Unlock()
			return
		}
		c.now = at
		var due []*job
		for _, j := range c.jobs {
			if j.active && !j.next.After(at) {
				due = append(due, j)
				missed := at.Sub(j.next) / j.every
				j.next = j.next.Add((missed + 1) * j.every)
			}
		}
		c.mu.Unlock()
		for _, j := range due {
			j.fn(at)
		}
	}
}

// Skip moves the clock forward by d without running any job (time passing
// while nothing is scheduled, e.g. while the app is down).
func (c *Clock) Skip(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
	for _, j := range c.jobs {
		if j.active && j.next.Before(c.now) {
			missed := c.now.Sub(j.next) / j.every
			j.next = j.next.Add((missed + 1) * j.every)
		}
	}
}

// Resolver is a fake ports.Resolver answering from a map.
type Resolver struct {
	mu    sync.Mutex
	names map[string][]string
	Calls int
}

func NewResolver() *Resolver { return &Resolver{names: map[string][]string{}} }

func (r *Resolver) Set(addr string, names ...string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.names[addr] = names
}

// CallCount is Calls, read safely while the app runs.
func (r *Resolver) CallCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.Calls
}

func (r *Resolver) LookupAddr(_ context.Context, addr string) ([]string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.Calls++
	n, ok := r.names[addr]
	if !ok {
		return nil, errors.New("no PTR")
	}
	return n, nil
}
