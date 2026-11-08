package tlscert_test

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"

	"survy/internal/tlscert"
)

// pair is a self-signed certificate and its key, PEM encoded.
type pair struct{ cert, key []byte }

func newPair(t *testing.T, cn string) pair {
	t.Helper()
	k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tpl := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()),
		Subject:      pkix.Name{CommonName: cn},
		DNSNames:     []string{cn},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &k.PublicKey, k)
	if err != nil {
		t.Fatal(err)
	}
	kb, err := x509.MarshalPKCS8PrivateKey(k)
	if err != nil {
		t.Fatal(err)
	}
	return pair{
		cert: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
		key:  pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: kb}),
	}
}

// files writes p to dir and returns the two paths. bump moves both mtimes
// forward so a rewrite is visible even on a coarse filesystem clock.
type files struct{ cert, key string }

func (f files) write(t *testing.T, cert, key []byte, bump time.Duration) {
	t.Helper()
	for path, b := range map[string][]byte{f.cert: cert, f.key: key} {
		if err := os.WriteFile(path, b, 0o600); err != nil {
			t.Fatal(err)
		}
		when := time.Now().Add(bump)
		if err := os.Chtimes(path, when, when); err != nil {
			t.Fatal(err)
		}
	}
}

func newFiles(t *testing.T) files {
	dir := t.TempDir()
	return files{cert: filepath.Join(dir, "cert.pem"), key: filepath.Join(dir, "key.pem")}
}

// serve starts a TLS listener whose certificate comes from r and returns a
// function that completes a handshake and reports the served leaf's CN.
func serve(t *testing.T, r *tlscert.Reloader) func() (string, error) {
	t.Helper()
	ln, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{
		MinVersion:     tls.VersionTLS12,
		GetCertificate: r.GetCertificate,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer c.Close()
				_ = c.(*tls.Conn).Handshake()
			}()
		}
	}()
	return func() (string, error) {
		c, err := tls.Dial("tcp", ln.Addr().String(), &tls.Config{InsecureSkipVerify: true})
		if err != nil {
			return "", err
		}
		defer c.Close()
		return c.ConnectionState().PeerCertificates[0].Subject.CommonName, nil
	}
}

func TestServesTheCertificateFromDisk(t *testing.T) {
	f := newFiles(t)
	p := newPair(t, "one.example")
	f.write(t, p.cert, p.key, 0)

	handshake := serve(t, tlscert.New(f.cert, f.key))

	if cn, err := handshake(); err != nil || cn != "one.example" {
		t.Fatalf("got %q, %v; want one.example", cn, err)
	}
}

func TestPicksUpARenewedCertificateWithoutARestart(t *testing.T) {
	f := newFiles(t)
	one, two := newPair(t, "one.example"), newPair(t, "two.example")
	f.write(t, one.cert, one.key, 0)
	handshake := serve(t, tlscert.New(f.cert, f.key))
	if cn, _ := handshake(); cn != "one.example" {
		t.Fatalf("before renewal: got %q", cn)
	}

	f.write(t, two.cert, two.key, time.Minute)

	if cn, err := handshake(); err != nil || cn != "two.example" {
		t.Fatalf("after renewal: got %q, %v; want two.example", cn, err)
	}
}

func TestKeepsServingTheOldCertificateWhileTheNewPairIsHalfWritten(t *testing.T) {
	f := newFiles(t)
	one, two := newPair(t, "one.example"), newPair(t, "two.example")
	f.write(t, one.cert, one.key, 0)
	handshake := serve(t, tlscert.New(f.cert, f.key))
	if cn, _ := handshake(); cn != "one.example" {
		t.Fatalf("before renewal: got %q", cn)
	}

	// A renewal writes the certificate first; its key still belongs to the old one.
	if err := os.WriteFile(f.cert, two.cert, 0o600); err != nil {
		t.Fatal(err)
	}
	later := time.Now().Add(time.Minute)
	_ = os.Chtimes(f.cert, later, later)

	if cn, err := handshake(); err != nil || cn != "one.example" {
		t.Fatalf("mid-renewal: got %q, %v; want the old certificate", cn, err)
	}

	// The key lands: the pair is consistent again and the new certificate is served.
	f.write(t, two.cert, two.key, 2*time.Minute)
	if cn, err := handshake(); err != nil || cn != "two.example" {
		t.Fatalf("after the key lands: got %q, %v; want two.example", cn, err)
	}
}

func TestHandshakeFailsUntilTheFilesAppear(t *testing.T) {
	f := newFiles(t)
	handshake := serve(t, tlscert.New(f.cert, f.key))

	if cn, err := handshake(); err == nil {
		t.Fatalf("handshake succeeded with no certificate on disk (served %q)", cn)
	}

	p := newPair(t, "late.example")
	f.write(t, p.cert, p.key, 0)
	if cn, err := handshake(); err != nil || cn != "late.example" {
		t.Fatalf("after the files appear: got %q, %v", cn, err)
	}
}

func TestCheckReportsExpiryAndErrors(t *testing.T) {
	f := newFiles(t)
	r := tlscert.New(f.cert, f.key)
	if _, err := r.Check(); err == nil {
		t.Fatal("Check succeeded with no files")
	}
	p := newPair(t, "x.example")
	f.write(t, p.cert, p.key, 0)
	info, err := r.Check()
	if err != nil {
		t.Fatal(err)
	}
	if info.Subject != "x.example" || info.NotAfter.Before(time.Now()) {
		t.Fatalf("unexpected info %+v", info)
	}
}
