// Package tlscert serves a TLS certificate that lives in two files and may be
// replaced while the process runs (an ACME client renewing it every ~60 days).
//
// The files are checked on every handshake with two stat calls, and parsed
// again only when their size or modification time changed, so a renewal takes
// effect on the next connection with no restart and no signal. A renewal that
// writes the certificate before its key leaves a mismatched pair for a moment:
// that pair is rejected and the previous certificate keeps being served until
// a consistent pair appears.
package tlscert

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"log"
	"os"
	"sync"
	"time"
)

// Reloader provides tls.Config.GetCertificate backed by a certificate file and
// a key file.
type Reloader struct {
	certFile, keyFile string

	mu      sync.Mutex
	cert    *tls.Certificate // last pair that parsed and matched
	sigCert fileSig          // signature of the files that produced cert
	sigKey  fileSig
	lastErr string // last logged error, to log a failure once, not per handshake
}

// Info describes the certificate currently held.
type Info struct {
	Subject  string
	DNSNames []string
	NotAfter time.Time
}

type fileSig struct {
	size int64
	mod  time.Time
}

// New returns a Reloader for the given PEM files. Nothing is read until the
// first handshake or Check, so the files may not exist yet when the process
// starts (a boot-time ordering that a certificate deploy script cannot always
// win).
func New(certFile, keyFile string) *Reloader {
	return &Reloader{certFile: certFile, keyFile: keyFile}
}

// GetCertificate implements tls.Config.GetCertificate.
func (r *Reloader) GetCertificate(*tls.ClientHelloInfo) (*tls.Certificate, error) {
	return r.current()
}

// Check loads the certificate now and describes it, or returns why it cannot be
// served. It is for a startup log line; the handshake path needs no call to it.
func (r *Reloader) Check() (Info, error) {
	c, err := r.current()
	if err != nil {
		return Info{}, err
	}
	leaf, err := x509.ParseCertificate(c.Certificate[0])
	if err != nil {
		return Info{}, err
	}
	return Info{Subject: leaf.Subject.CommonName, DNSNames: leaf.DNSNames, NotAfter: leaf.NotAfter}, nil
}

func (r *Reloader) current() (*tls.Certificate, error) {
	sc, errC := stat(r.certFile)
	sk, errK := stat(r.keyFile)

	r.mu.Lock()
	defer r.mu.Unlock()

	if errC == nil && errK == nil && r.cert != nil && sc == r.sigCert && sk == r.sigKey {
		return r.cert, nil // unchanged since the last good load
	}
	if errC != nil || errK != nil {
		return r.fallback(firstErr(errC, errK))
	}

	pair, err := tls.LoadX509KeyPair(r.certFile, r.keyFile)
	if err != nil {
		return r.fallback(err)
	}
	if r.cert != nil {
		log.Printf("tls: loaded renewed certificate from %s", r.certFile)
	}
	r.cert, r.sigCert, r.sigKey, r.lastErr = &pair, sc, sk, ""
	return r.cert, nil
}

// fallback keeps serving the last good pair when a reload fails, and reports
// the failure once per distinct message. With no good pair yet, the handshake
// fails.
func (r *Reloader) fallback(err error) (*tls.Certificate, error) {
	if msg := err.Error(); msg != r.lastErr {
		r.lastErr = msg
		if r.cert != nil {
			log.Printf("tls: cannot load the new certificate, still serving the previous one: %v", err)
		} else {
			log.Printf("tls: no certificate to serve yet: %v", err)
		}
	}
	if r.cert != nil {
		return r.cert, nil
	}
	return nil, fmt.Errorf("tls certificate unavailable: %w", err)
}

func stat(path string) (fileSig, error) {
	fi, err := os.Stat(path)
	if err != nil {
		return fileSig{}, err
	}
	return fileSig{size: fi.Size(), mod: fi.ModTime()}, nil
}

func firstErr(errs ...error) error {
	for _, e := range errs {
		if e != nil {
			return e
		}
	}
	return nil
}
