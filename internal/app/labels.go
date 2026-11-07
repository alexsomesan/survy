package app

import (
	"errors"
	"io/fs"
	"log"
	"net/netip"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"survy/internal/asndb"
	"survy/internal/ports"
)

// asnCheckEvery is how often the ASN database file is checked for changes.
const asnCheckEvery = 5 * time.Minute

// labeller fills in the host, org and asn of Remote Endpoints a Device
// detail shows (spec: Remote Endpoint labelling).
type labeller struct {
	dns *rdns
	asn *asnFile
}

func newLabeller(r ports.Resolver, c ports.Clock, asnPath string) *labeller {
	return &labeller{dns: newRDNS(r, c), asn: &asnFile{path: asnPath}}
}

func (l *labeller) start() {
	l.asn.check(time.Time{})
	l.dns.start()
}

func (l *labeller) stop() { l.dns.stop() }

// label sets what is known about each row's address; it never blocks on
// the network.
func (l *labeller) label(rows []EndpointRow) {
	db := l.asn.db.Load()
	for i := range rows {
		ip, err := netip.ParseAddr(rows[i].IP)
		if err != nil {
			continue
		}
		rows[i].Host = l.dns.host(ip)
		if o, ok := db.Lookup(ip); ok {
			name, asn := o.Name, int(o.ASN)
			rows[i].Org, rows[i].ASN = &name, &asn
		}
	}
}

// asnFile is the offline IP→ASN/organisation database, reloaded when the
// file changes and dropped when it disappears.
type asnFile struct {
	path string
	db   atomic.Pointer[asndb.DB]

	mu      sync.Mutex // serialises check
	modTime time.Time
	size    int64
	present bool
	warned  bool // a missing file was logged
}

// check loads the file if its modification time or size changed since the
// last load. Runs on the Clock.
func (f *asnFile) check(time.Time) {
	f.mu.Lock()
	defer f.mu.Unlock()
	st, err := os.Stat(f.path)
	if err != nil {
		if f.present || !f.warned {
			if errors.Is(err, fs.ErrNotExist) {
				log.Printf("asn database %s missing; endpoint organisations unavailable", f.path)
			} else {
				log.Printf("asn database: %v", err)
			}
		}
		f.present, f.warned = false, true
		f.db.Store(nil)
		return
	}
	if f.present && st.ModTime().Equal(f.modTime) && st.Size() == f.size {
		return
	}
	file, err := os.Open(f.path)
	if err != nil {
		// Not recorded as seen, so the next check retries.
		log.Printf("asn database: %v", err)
		return
	}
	f.present, f.modTime, f.size = true, st.ModTime(), st.Size()
	defer file.Close()
	db, err := asndb.Load(file)
	if err != nil {
		// Keep the previous table; retried when the file changes again.
		log.Printf("asn database %s: %v", f.path, err)
		return
	}
	f.db.Store(db)
	log.Printf("asn database loaded: %d routed ranges", db.Ranges())
}
