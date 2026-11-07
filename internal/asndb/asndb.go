// Package asndb maps IP addresses to their autonomous system and owning
// organisation using the iptoasn.com ip2asn-combined.tsv.gz database.
//
// The file is a gzip TSV of "range_start range_end AS_number country_code
// AS_description", IPv4 and IPv6 mixed. AS 0 marks unrouted space. The whole
// file is held in memory as sorted range tables (fixed-width integers plus an
// index into a deduplicated organisation table) and searched by bisection.
package asndb

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"errors"
	"io"
	"net/netip"
	"slices"
	"sort"
	"strconv"
)

// Org is an autonomous system and the organisation that owns it.
type Org struct {
	ASN  uint32
	Name string
}

type u128 struct{ hi, lo uint64 }

func (a u128) less(b u128) bool { return a.hi < b.hi || a.hi == b.hi && a.lo < b.lo }

// DB is an immutable, loaded database. The zero value finds nothing.
type DB struct {
	v4Start, v4End []uint32
	v4Org          []uint32
	v6Start, v6End []u128
	v6Org          []uint32
	orgs           []Org
}

// Lookup returns the Org announcing ip, if any.
func (db *DB) Lookup(ip netip.Addr) (Org, bool) {
	if db == nil {
		return Org{}, false
	}
	ip = ip.Unmap()
	if ip.Is4() {
		k := v4key(ip)
		i := sort.Search(len(db.v4Start), func(i int) bool { return db.v4Start[i] > k }) - 1
		if i < 0 || k > db.v4End[i] {
			return Org{}, false
		}
		return db.orgs[db.v4Org[i]], true
	}
	k := v6key(ip)
	i := sort.Search(len(db.v6Start), func(i int) bool { return k.less(db.v6Start[i]) }) - 1
	if i < 0 || db.v6End[i].less(k) {
		return Org{}, false
	}
	return db.orgs[db.v6Org[i]], true
}

// Ranges is the number of routed ranges held.
func (db *DB) Ranges() int { return len(db.v4Start) + len(db.v6Start) }

func v4key(ip netip.Addr) uint32 {
	b := ip.As4()
	return uint32(b[0])<<24 | uint32(b[1])<<16 | uint32(b[2])<<8 | uint32(b[3])
}

func v6key(ip netip.Addr) u128 {
	b := ip.As16()
	var k u128
	for i := range 8 {
		k.hi = k.hi<<8 | uint64(b[i])
		k.lo = k.lo<<8 | uint64(b[8+i])
	}
	return k
}

// Load reads a gzip-compressed ip2asn-combined TSV. Malformed rows are
// skipped; a file with none valid is an error.
func Load(r io.Reader) (*DB, error) {
	z, err := gzip.NewReader(r)
	if err != nil {
		return nil, err
	}
	defer z.Close()
	br := bufio.NewReaderSize(z, 64<<10)
	db := &DB{}
	orgIdx := map[Org]uint32{}
	valid := 0
	for {
		line, err := br.ReadSlice('\n')
		if len(line) > 0 {
			if ok := db.addLine(line, orgIdx); ok {
				valid++
			}
		}
		if err == io.EOF {
			break
		}
		if errors.Is(err, bufio.ErrBufferFull) {
			// Absurdly long line: skip the rest of it.
			for errors.Is(err, bufio.ErrBufferFull) {
				_, err = br.ReadSlice('\n')
			}
			if err == io.EOF {
				break
			}
		}
		if err != nil && err != io.EOF {
			return nil, err
		}
	}
	if valid == 0 {
		return nil, errors.New("asndb: no valid rows")
	}
	db.compact()
	return db, nil
}

// addLine parses one row; unrouted (AS 0) rows are valid but not stored.
func (db *DB) addLine(line []byte, orgIdx map[Org]uint32) bool {
	line = bytes.TrimRight(line, "\r\n")
	var f [5][]byte
	for i := range 4 {
		var ok bool
		f[i], line, ok = bytes.Cut(line, []byte{'\t'})
		if !ok {
			return false
		}
	}
	f[4] = line
	start, err1 := netip.ParseAddr(string(f[0]))
	end, err2 := netip.ParseAddr(string(f[1]))
	asn, err3 := strconv.ParseUint(string(f[2]), 10, 32)
	if err1 != nil || err2 != nil || err3 != nil || start.Is4() != end.Is4() {
		return false
	}
	if asn == 0 {
		return true
	}
	o := Org{ASN: uint32(asn), Name: orgName(f[4])}
	idx, ok := orgIdx[o]
	if !ok {
		idx = uint32(len(db.orgs))
		db.orgs = append(db.orgs, o)
		orgIdx[o] = idx
	}
	if start.Is4() {
		db.v4Start = append(db.v4Start, v4key(start))
		db.v4End = append(db.v4End, v4key(end))
		db.v4Org = append(db.v4Org, idx)
	} else {
		db.v6Start = append(db.v6Start, v6key(start))
		db.v6End = append(db.v6End, v6key(end))
		db.v6Org = append(db.v6Org, idx)
	}
	return true
}

// orgName is the organisation part of an AS description: iptoasn writes
// "HANDLE - Organisation Name" when the registry has one, else the handle.
func orgName(desc []byte) string {
	if _, after, ok := bytes.Cut(desc, []byte(" - ")); ok && len(bytes.TrimSpace(after)) > 0 {
		desc = after
	}
	return string(bytes.TrimSpace(desc))
}

// compact sorts the tables (the file is normally sorted already) and drops
// spare slice capacity.
func (db *DB) compact() {
	if !slices.IsSorted(db.v4Start) {
		sort.Sort(v4sorter{db})
	}
	if !slices.IsSortedFunc(db.v6Start, cmp128) {
		sort.Sort(v6sorter{db})
	}
	db.v4Start, db.v4End, db.v4Org = slices.Clone(db.v4Start), slices.Clone(db.v4End), slices.Clone(db.v4Org)
	db.v6Start, db.v6End, db.v6Org = slices.Clone(db.v6Start), slices.Clone(db.v6End), slices.Clone(db.v6Org)
	db.orgs = slices.Clone(db.orgs)
}

func cmp128(a, b u128) int {
	switch {
	case a.less(b):
		return -1
	case b.less(a):
		return 1
	}
	return 0
}

type v4sorter struct{ db *DB }

func (s v4sorter) Len() int           { return len(s.db.v4Start) }
func (s v4sorter) Less(i, j int) bool { return s.db.v4Start[i] < s.db.v4Start[j] }
func (s v4sorter) Swap(i, j int) {
	d := s.db
	d.v4Start[i], d.v4Start[j] = d.v4Start[j], d.v4Start[i]
	d.v4End[i], d.v4End[j] = d.v4End[j], d.v4End[i]
	d.v4Org[i], d.v4Org[j] = d.v4Org[j], d.v4Org[i]
}

type v6sorter struct{ db *DB }

func (s v6sorter) Len() int           { return len(s.db.v6Start) }
func (s v6sorter) Less(i, j int) bool { return s.db.v6Start[i].less(s.db.v6Start[j]) }
func (s v6sorter) Swap(i, j int) {
	d := s.db
	d.v6Start[i], d.v6Start[j] = d.v6Start[j], d.v6Start[i]
	d.v6End[i], d.v6End[j] = d.v6End[j], d.v6End[i]
	d.v6Org[i], d.v6Org[j] = d.v6Org[j], d.v6Org[i]
}
