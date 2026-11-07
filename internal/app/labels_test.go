package app_test

import (
	"bytes"
	"compress/gzip"
	"fmt"
	"testing"
	"time"

	"survy/internal/testkit"
)

const lrID = "mac:" + copperMAC

// endpoint returns the Device detail's row for ip (fails when absent).
func (d deviceDetail) endpoint(t *testing.T, ip string) endpointRow {
	t.Helper()
	for _, e := range d.Endpoints {
		if e.IP == ip {
			return e
		}
	}
	t.Fatalf("%s: no endpoint %s in %+v", d.Window, ip, d.Endpoints)
	return endpointRow{}
}

func str(p *string) any {
	if p == nil {
		return nil
	}
	return *p
}

func TestEndpointHostIsFilledByReverseDNSAfterTheFirstRequest(t *testing.T) {
	tk := newTalkers(t)
	h := tk.h
	h.Resolver.Set(google, "lb-in-f24.1e100.net.")
	h.Kernel.SetFlow(snat(1, copper4, google, 1_000, 10_000))
	tk.second()

	if e := getDevice(h, lrID, "live").endpoint(t, google); e.Host != nil {
		t.Fatalf("first request: host = %q, want null (lookup is async)", *e.Host)
	}
	h.WaitLabels()
	if e := getDevice(h, lrID, "live").endpoint(t, google); str(e.Host) != "lb-in-f24.1e100.net" {
		t.Fatalf("after lookup: host = %v, want lb-in-f24.1e100.net", str(e.Host))
	}
	if e := getDevice(h, lrID, "1h").endpoint(t, google); str(e.Host) != "lb-in-f24.1e100.net" {
		t.Fatalf("1h window: host = %v", str(e.Host))
	}
	if n := h.Resolver.CallCount(); n != 1 {
		t.Fatalf("resolver calls = %d, want 1 (cached)", n)
	}
}

func TestFailedReverseDNSIsCachedForAnHourAndThenRetried(t *testing.T) {
	tk := newTalkers(t)
	h := tk.h
	h.Kernel.SetFlow(snat(1, copper4, google, 1_000, 10_000))
	tk.second()

	getDevice(h, lrID, "1h")
	h.WaitLabels()
	h.FastForward(50*time.Minute, time.Minute)
	if e := getDevice(h, lrID, "1h").endpoint(t, google); e.Host != nil {
		t.Fatalf("host = %q, want null (no PTR)", *e.Host)
	}
	h.WaitLabels()
	if n := h.Resolver.CallCount(); n != 1 {
		t.Fatalf("resolver calls within the hour = %d, want 1", n)
	}

	h.Resolver.Set(google, "lb-in-f24.1e100.net.")
	h.FastForward(11*time.Minute, time.Minute)
	getDevice(h, lrID, "1h")
	h.WaitLabels()
	if e := getDevice(h, lrID, "1h").endpoint(t, google); str(e.Host) != "lb-in-f24.1e100.net" {
		t.Fatalf("after retry: host = %v", str(e.Host))
	}
	if n := h.Resolver.CallCount(); n != 2 {
		t.Fatalf("resolver calls = %d, want 2", n)
	}
}

func TestResolvedNamesAreRefreshedAfterADayAndServedWhileRefreshing(t *testing.T) {
	tk := newTalkers(t)
	h := tk.h
	h.Resolver.Set(google, "old.example.")
	h.Kernel.SetFlow(snat(1, copper4, google, 1_000, 10_000))
	tk.second()
	getDevice(h, lrID, "24h")
	h.WaitLabels()

	h.Resolver.Set(google, "new.example.")
	h.FastForward(23*time.Hour, time.Minute)
	if e := getDevice(h, lrID, "24h").endpoint(t, google); str(e.Host) != "old.example" {
		t.Fatalf("within a day: host = %v, want old.example", str(e.Host))
	}
	h.WaitLabels()
	if n := h.Resolver.CallCount(); n != 1 {
		t.Fatalf("resolver calls within a day = %d, want 1", n)
	}
	h.FastForward(2*time.Hour, time.Minute)
	if e := getDevice(h, lrID, "7d").endpoint(t, google); str(e.Host) != "old.example" {
		t.Fatalf("stale: host = %v, want old.example while refreshing", str(e.Host))
	}
	h.WaitLabels()
	if e := getDevice(h, lrID, "7d").endpoint(t, google); str(e.Host) != "new.example" {
		t.Fatalf("refreshed: host = %v, want new.example", str(e.Host))
	}
}

func TestManyRequestsCauseOneLookupPerEndpoint(t *testing.T) {
	tk := newTalkers(t)
	h := tk.h
	const n = 20
	for i := range n {
		ip := fmt.Sprintf("35.186.224.%d", i+1)
		h.Resolver.Set(ip, fmt.Sprintf("host%d.example.", i+1))
		h.Kernel.SetFlow(snat(uint32(i+1), copper4, ip, 1_000, 10_000))
	}
	tk.second()
	for range 10 {
		getDevice(h, lrID, "live")
		getDevice(h, lrID, "1h")
	}
	h.WaitLabels()
	d := getDevice(h, lrID, "1h")
	if len(d.Endpoints) != n {
		t.Fatalf("endpoints = %d, want %d", len(d.Endpoints), n)
	}
	for _, e := range d.Endpoints {
		if e.Host == nil {
			t.Errorf("%s: host null after lookups finished", e.IP)
		}
	}
	if c := h.Resolver.CallCount(); c != n {
		t.Fatalf("resolver calls = %d, want %d (one per endpoint)", c, n)
	}
}

func TestNonPublicEndpointsAreNeverLookedUp(t *testing.T) {
	tk := newTalkers(t)
	h := tk.h
	cgnat := "100.64.12.34" // the ISP's shared address space
	h.Resolver.Set(cgnat, "cgnat.isp.example.")
	h.Kernel.SetFlow(snat(1, copper4, cgnat, 1_000, 10_000))
	tk.second()
	getDevice(h, lrID, "live").endpoint(t, cgnat)
	h.WaitLabels()
	if e := getDevice(h, lrID, "live").endpoint(t, cgnat); e.Host != nil {
		t.Fatalf("host = %q, want null", *e.Host)
	}
	if n := h.Resolver.CallCount(); n != 0 {
		t.Fatalf("resolver calls = %d, want 0", n)
	}
}

// asnDB is an ip2asn-combined.tsv.gz with the given rows.
func asnDB(t *testing.T, rows ...string) []byte {
	t.Helper()
	var b bytes.Buffer
	z := gzip.NewWriter(&b)
	for _, r := range rows {
		fmt.Fprintln(z, r)
	}
	if err := z.Close(); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

const asnFile = "ip2asn-combined.tsv.gz"

var asnRows = []string{
	"1.0.0.0\t1.0.0.255\t13335\tUS\tCLOUDFLARENET - Cloudflare, Inc.",
	"35.186.0.0\t35.186.127.255\t0\tNone\tNot routed",
	"35.186.128.0\t35.186.255.255\t15169\tUS\tGOOGLE",
	"45.57.0.0\t45.57.127.255\t2906\tUS\tAS-SSI - Netflix Streaming Services Inc.",
	"2606:4700::\t2606:4700:ffff:ffff:ffff:ffff:ffff:ffff\t13335\tUS\tCLOUDFLARENET - Cloudflare, Inc.",
}

func TestEndpointOrgAndASNComeFromTheOfflineDatabase(t *testing.T) {
	h := testkit.New(t)
	h.WriteData(asnFile, asnDB(t, asnRows...))
	h.Restart()
	h.Kernel.ClearFlows()
	tk := startTalkers(t, h)
	google2 := "35.186.224.25"
	notRouted := "35.186.1.1"
	unknown := "8.8.8.8"
	h.Kernel.SetFlow(snat(1, copper4, google, 1_000, 10_000))
	h.Kernel.SetFlow(snat(2, copper4, notRouted, 1_000, 10_000))
	h.Kernel.SetFlow(snat(3, copper4, unknown, 1_000, 10_000))
	h.Kernel.SetFlow(direct(4, copper6, cloudflare6, 1_000, 10_000))
	h.Kernel.SetFlow(snat(5, copper4, google2, 1_000, 10_000))
	tk.second()

	d := getDevice(h, lrID, "live")
	for _, c := range []struct {
		ip  string
		org any
		asn any
	}{
		{google, "GOOGLE", 15169},
		{google2, "GOOGLE", 15169},
		{cloudflare6, "Cloudflare, Inc.", 13335},
		{notRouted, nil, nil},
		{unknown, nil, nil},
	} {
		e := d.endpoint(t, c.ip)
		var asn any
		if e.ASN != nil {
			asn = *e.ASN
		}
		if str(e.Org) != c.org || asn != c.asn {
			t.Errorf("%s: org %v asn %v, want %v %v", c.ip, str(e.Org), asn, c.org, c.asn)
		}
	}
}

func TestEndpointOrgIsNullWithoutDatabaseAndAppearsWhenItIsInstalled(t *testing.T) {
	tk := newTalkers(t)
	h := tk.h
	h.Kernel.SetFlow(snat(1, copper4, google, 1_000, 10_000))
	tk.second()
	if e := getDevice(h, lrID, "1h").endpoint(t, google); e.Org != nil || e.ASN != nil {
		t.Fatalf("no database: org %v asn %v, want null", str(e.Org), e.ASN)
	}

	h.WriteData(asnFile, asnDB(t, asnRows...))
	h.FastForward(10*time.Minute, time.Minute)
	if e := getDevice(h, lrID, "1h").endpoint(t, google); str(e.Org) != "GOOGLE" {
		t.Fatalf("after install: org %v, want GOOGLE", str(e.Org))
	}

	// The monthly refresh replaces the file.
	h.WriteData(asnFile, asnDB(t, "35.186.0.0\t35.186.255.255\t396982\tUS\tGOOGLE-CLOUD-PLATFORM"))
	h.FastForward(10*time.Minute, time.Minute)
	e := getDevice(h, lrID, "1h").endpoint(t, google)
	if str(e.Org) != "GOOGLE-CLOUD-PLATFORM" || e.ASN == nil || *e.ASN != 396982 {
		t.Fatalf("after refresh: org %v asn %v", str(e.Org), e.ASN)
	}
}
