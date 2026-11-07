// mkimage builds the survy container image without a container daemon: it
// pulls the busybox base, appends one layer holding the survy binary, sets the
// same config as the Dockerfile's runtime stage and writes a docker-archive
// tarball that `podman load` accepts.
//
//	go run ./tools/mkimage -bin bin/survy -o dist/survy.tar -t localhost/dash:abc123 -t localhost/dash:latest
package main

import (
	"archive/tar"
	"bytes"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"strings"

	"github.com/google/go-containerregistry/pkg/name"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/mutate"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/google/go-containerregistry/pkg/v1/tarball"
)

type tags []string

func (t *tags) String() string     { return strings.Join(*t, ",") }
func (t *tags) Set(s string) error { *t = append(*t, s); return nil }

func main() {
	var ts tags
	base := flag.String("base", "busybox:1.37-musl", "base image")
	bin := flag.String("bin", "bin/survy", "static survy binary")
	out := flag.String("o", "dist/survy.tar", "output docker-archive")
	flag.Var(&ts, "t", "tag (repeatable)")
	flag.Parse()
	if len(ts) == 0 {
		log.Fatal("at least one -t tag is required")
	}

	ref, err := name.ParseReference(*base)
	check(err)
	img, err := remote.Image(ref, remote.WithPlatform(v1.Platform{OS: "linux", Architecture: "amd64"}))
	check(err)

	layer, err := binaryLayer(*bin, "usr/local/bin/survy")
	check(err)
	img, err = mutate.AppendLayers(img, layer)
	check(err)

	cf, err := img.ConfigFile()
	check(err)
	cfg := cf.Config
	// Keep in sync with the Dockerfile's runtime stage.
	cfg.Entrypoint = []string{"nice", "-n", "19", "ionice", "-c", "3", "/usr/local/bin/survy"}
	cfg.Cmd = nil
	cfg.Env = append(cfg.Env, "DASH_LISTEN=192.168.2.4:80,10.0.0.1:80", "DASH_ROOT=/host", "DASH_DATA=/data")
	cfg.Volumes = map[string]struct{}{"/data": {}}
	img, err = mutate.Config(img, cfg)
	check(err)

	refs := map[name.Reference]v1.Image{}
	for _, t := range ts {
		tag, err := name.NewTag(t)
		check(err)
		refs[tag] = img
	}
	f, err := os.Create(*out)
	check(err)
	check(tarball.MultiRefWrite(refs, f))
	check(f.Close())
	d, _ := img.Digest()
	fmt.Printf("wrote %s (%s) tags=%v\n", *out, d, []string(ts))
}

func binaryLayer(src, dst string) (v1.Layer, error) {
	b, err := os.ReadFile(src)
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	for _, dir := range []string{"usr/", "usr/local/", "usr/local/bin/"} {
		if err := tw.WriteHeader(&tar.Header{Name: dir, Typeflag: tar.TypeDir, Mode: 0o755}); err != nil {
			return nil, err
		}
	}
	if err := tw.WriteHeader(&tar.Header{Name: dst, Mode: 0o755, Size: int64(len(b)), Typeflag: tar.TypeReg}); err != nil {
		return nil, err
	}
	if _, err := tw.Write(b); err != nil {
		return nil, err
	}
	if err := tw.Close(); err != nil {
		return nil, err
	}
	data := buf.Bytes()
	return tarball.LayerFromOpener(func() (io.ReadCloser, error) { return io.NopCloser(bytes.NewReader(data)), nil })
}

func check(err error) {
	if err != nil {
		log.Fatal(err)
	}
}
