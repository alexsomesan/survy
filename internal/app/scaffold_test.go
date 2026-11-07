package app_test

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"survy/internal/testkit"
)

func TestLiveStreamEmitsOneEventPerSecond(t *testing.T) {
	h := testkit.New(t)
	s := h.OpenLive()

	var snap struct{ Time int64 }
	s.Next(&snap)
	if snap.Time != testkit.Start.UnixMilli() {
		t.Fatalf("snapshot time = %d, want %d", snap.Time, testkit.Start.UnixMilli())
	}

	h.Advance(3 * time.Second)
	for i := 1; i <= 3; i++ {
		var ev struct{ Time int64 }
		s.Next(&ev)
		want := testkit.Start.Add(time.Duration(i) * time.Second).UnixMilli()
		if ev.Time != want {
			t.Fatalf("event %d time = %d, want %d", i, ev.Time, want)
		}
	}
}

func TestRootServesSomething(t *testing.T) {
	h := testkit.New(t)
	code, body := h.Get("/")
	if code != http.StatusOK || len(strings.TrimSpace(string(body))) == 0 {
		t.Fatalf("GET / = %d %q", code, body)
	}
}
