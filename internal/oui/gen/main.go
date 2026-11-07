//go:build ignore

// Command gen turns the IEEE MA-L registry CSV
// (https://standards-oui.ieee.org/oui/oui.csv) into the trimmed, gzipped
// vendor table embedded by package oui:
//
//	go run ./internal/oui/gen/main.go oui.csv internal/oui/oui.tsv.gz
//
// Each output line is "<6 lower-case hex digits>\t<short vendor name>", sorted.
package main

import (
	"compress/gzip"
	"encoding/csv"
	"fmt"
	"os"
	"sort"
	"strings"
)

func main() {
	if len(os.Args) != 3 {
		fmt.Fprintln(os.Stderr, "usage: gen <oui.csv> <out.tsv.gz>")
		os.Exit(2)
	}
	in, err := os.Open(os.Args[1])
	check(err)
	defer in.Close()
	r := csv.NewReader(in)
	r.FieldsPerRecord = -1
	rows, err := r.ReadAll()
	check(err)
	var lines []string
	for _, row := range rows[1:] {
		if len(row) < 3 || row[0] != "MA-L" || len(row[1]) != 6 {
			continue
		}
		name := shorten(row[2])
		if name == "" {
			continue
		}
		lines = append(lines, strings.ToLower(row[1])+"\t"+name)
	}
	sort.Strings(lines)
	out, err := os.Create(os.Args[2])
	check(err)
	zw, _ := gzip.NewWriterLevel(out, gzip.BestCompression)
	_, err = zw.Write([]byte(strings.Join(lines, "\n") + "\n"))
	check(err)
	check(zw.Close())
	check(out.Close())
}

// stop are words dropped from the end of a vendor name.
var stop = map[string]bool{
	"inc": true, "incorporated": true, "ltd": true, "limited": true, "co": true, "corp": true,
	"corporation": true, "company": true, "gmbh": true, "llc": true, "ag": true, "sa": true,
	"sas": true, "srl": true, "spa": true, "bv": true, "b.v": true, "nv": true, "ab": true, "oy": true,
	"as": true, "a/s": true, "kg": true, "plc": true, "pty": true, "pte": true, "kk": true,
	"technologies": true, "technology": true, "tech": true, "electronics": true, "electronic": true,
	"communications": true, "communication": true, "international": true, "industries": true,
	"industrial": true, "holdings": true, "group": true, "systems": true, "networks": true,
	"network": true, "solutions": true, "devices": true, "products": true, "enterprise": true,
	"manufacturing": true, "mfg": true, "the": true, "and": true, "&": true, "of": true,
	"ind": true, "corporate": true, "semiconductor": true, "semiconductors": true,
}

func isStop(w string) bool { return stop[strings.ToLower(strings.Trim(w, ".,"))] }

func shorten(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexAny(s, ",("); i > 0 {
		s = s[:i]
	}
	words := strings.Fields(s)
	trim := func() {
		for len(words) > 1 && isStop(words[len(words)-1]) {
			words = words[:len(words)-1]
		}
	}
	trim()
	if len(words) > 2 {
		words = words[:2]
		trim()
	}
	out := strings.Trim(strings.Join(words, " "), " .,")
	// Shouting names (e.g. "SAMSUNG") read better in title case.
	if out == strings.ToUpper(out) {
		ws := strings.Fields(out)
		for i, w := range ws {
			if len(w) > 3 && !strings.ContainsAny(w, "-0123456789&") {
				ws[i] = w[:1] + strings.ToLower(w[1:])
			}
		}
		out = strings.Join(ws, " ")
	}
	return out
}

func check(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
