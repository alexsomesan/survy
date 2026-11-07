// Package oui maps MAC addresses to network-card manufacturers using a
// trimmed copy of the IEEE MA-L registry (regenerate with gen/main.go).
package oui

import (
	"bufio"
	"bytes"
	"compress/gzip"
	_ "embed"
	"strings"
	"sync"
)

//go:embed oui.tsv.gz
var tableGz []byte

var (
	once  sync.Once
	table map[string]string
)

func load() {
	table = map[string]string{}
	zr, err := gzip.NewReader(bytes.NewReader(tableGz))
	if err != nil {
		return
	}
	sc := bufio.NewScanner(zr)
	for sc.Scan() {
		k, v, ok := strings.Cut(sc.Text(), "\t")
		if ok {
			table[k] = v
		}
	}
}

// Vendor returns the manufacturer for a colon-separated MAC address. It
// returns "" for unknown prefixes and for locally administered addresses
// (which carry no manufacturer).
func Vendor(mac string) string {
	if LocallyAdministered(mac) {
		return ""
	}
	key := strings.ToLower(strings.ReplaceAll(mac, ":", ""))
	if len(key) < 6 {
		return ""
	}
	once.Do(load)
	return table[key[:6]]
}

// LocallyAdministered reports whether the MAC's locally-administered bit
// (0x02 of the first octet) is set, as for randomised "private" addresses.
func LocallyAdministered(mac string) bool {
	if len(mac) < 2 {
		return false
	}
	var b byte
	for _, c := range []byte(strings.ToLower(mac[:2])) {
		b <<= 4
		switch {
		case c >= '0' && c <= '9':
			b |= c - '0'
		case c >= 'a' && c <= 'f':
			b |= c - 'a' + 10
		default:
			return false
		}
	}
	return b&0x02 != 0
}
