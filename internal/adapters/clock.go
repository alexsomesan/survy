package adapters

import (
	"sync"
	"time"

	"survy/internal/ports"
)

// Clock is the wall-clock ports.Clock.
type Clock struct{}

var _ ports.Clock = Clock{}

func (Clock) Now() time.Time { return time.Now() }

// Every runs fn on its own goroutine each tick. Ticks are dropped (not queued)
// if fn runs longer than d. stop is idempotent and returns only once any
// in-flight fn has finished, so callers may release what fn uses (it must
// not be called from fn itself).
func (Clock) Every(d time.Duration, fn func(now time.Time)) (stop func()) {
	t := time.NewTicker(d)
	done := make(chan struct{})
	exited := make(chan struct{})
	go func() {
		defer close(exited)
		defer t.Stop()
		for {
			select {
			case <-done:
				return
			case now := <-t.C:
				fn(now)
			}
		}
	}()
	var once sync.Once
	return func() {
		once.Do(func() { close(done) })
		<-exited
	}
}
