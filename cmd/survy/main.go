// Command survy serves the router dashboard.
//
// Configuration is by environment:
//
//	DASH_LISTEN             comma-separated listen addresses (default "192.168.2.4:80,10.0.0.1:80")
//	DASH_ROOT               where the router's filesystem is mounted (default "/")
//	DASH_DATA               data directory (default "/data")
//	DASH_DNS                DNS server for reverse lookups (default "192.168.2.1:53")
//	DASH_STORAGE_CAP_BYTES  History database cap in bytes (default: app default)
//	DASH_INVENTORY, DASH_ALIASES, DASH_ASN  override file paths (default: in DASH_DATA)
//	DASH_DATA_HOST_PATH     where the router mounts DASH_DATA (default "/mnt/dash-data"; shown as the data disk's path)
//	DASH_DROP_PRIVS         "0" keeps the starting user (local development); default: drop root
//	DASH_UID, DASH_GID      who to become after opening privileged resources (default 65534)
//
// Startup order (story 67): while still root, open the conntrack sockets and
// the HTTP listeners, chown DASH_DATA, and learn the Kea lease file's group;
// then drop to DASH_UID/DASH_GID (keeping that group) and check that the
// host's root directory is no longer reachable through /proc/1/root.
package main

import (
	"context"
	"errors"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"survy/internal/adapters"
	"survy/internal/app"
	"survy/internal/privdrop"
)

func env(key, def string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return def
}

func main() {
	log.SetOutput(os.Stdout)
	log.SetFlags(log.LstdFlags | log.LUTC)

	root := env("DASH_ROOT", "/")
	data := env("DASH_DATA", "/data")
	listen := env("DASH_LISTEN", "192.168.2.4:80,10.0.0.1:80")

	settings := app.Settings{
		InventoryPath: os.Getenv("DASH_INVENTORY"),
		AliasPath:     os.Getenv("DASH_ALIASES"),
		ASNPath:       os.Getenv("DASH_ASN"),
	}
	if v := os.Getenv("DASH_STORAGE_CAP_BYTES"); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n <= 0 {
			log.Fatalf("DASH_STORAGE_CAP_BYTES: invalid value %q", v)
		}
		settings.StorageCapBytes = n
	}

	settings.DataHostPath = os.Getenv("DASH_DATA_HOST_PATH")

	// Privileged resources first: they keep working after the drop.
	kernel := adapters.NewKernel(adapters.KernelConfig{})
	_ = kernel.Open() // logged; Top Talkers then report errors
	defer kernel.Close()

	var listeners []net.Listener
	for _, addr := range strings.Split(listen, ",") {
		addr = strings.TrimSpace(addr)
		if addr == "" {
			continue
		}
		ln, err := net.Listen("tcp", addr)
		if err != nil {
			log.Printf("listen %s: %v (continuing)", addr, err)
			continue
		}
		listeners = append(listeners, ln)
	}
	if len(listeners) == 0 {
		log.Fatalf("no listen address could be bound (DASH_LISTEN=%q)", listen)
	}

	dropPrivileges(root, data)

	a, err := app.New(app.Config{
		Root:     root,
		DataDir:  data,
		Kernel:   kernel,
		Clock:    adapters.Clock{},
		Resolver: adapters.NewResolver(env("DASH_DNS", adapters.DefaultDNSServer), 2*time.Second),
		Settings: settings,
	})
	if err != nil {
		log.Fatalf("app: %v", err)
	}
	if err := a.Start(); err != nil {
		log.Fatalf("app start: %v", err)
	}

	// Cancelling the servers' base context ends the long-lived /api/live
	// streams, so Shutdown does not wait on them.
	baseCtx, cancelBase := context.WithCancel(context.Background())
	var servers []*http.Server
	var wg sync.WaitGroup
	for _, ln := range listeners {
		srv := &http.Server{Handler: a.Handler(), ReadHeaderTimeout: 10 * time.Second,
			BaseContext: func(net.Listener) context.Context { return baseCtx }}
		servers = append(servers, srv)
		wg.Add(1)
		go func() {
			defer wg.Done()
			log.Printf("serving on %s", ln.Addr())
			if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
				log.Printf("serve %s: %v", ln.Addr(), err)
			}
		}()
	}

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGTERM, os.Interrupt)
	s := <-sig
	log.Printf("received %v, shutting down", s)

	// Stop serving first, then close the app: no request is using the store
	// when it closes. Close waits for an in-flight collection tick.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cancelBase()
	for _, srv := range servers {
		_ = srv.Shutdown(ctx)
	}
	wg.Wait()
	a.Close()
	log.Printf("bye")
}

// dropPrivileges becomes DASH_UID/DASH_GID when running as root, keeping
// the Kea lease file's group. It exits on any failure (fail closed). It is a
// no-op, with a log line, when DASH_DROP_PRIVS=0 or when not running as root.
func dropPrivileges(root, data string) {
	if os.Getenv("DASH_DROP_PRIVS") == "0" {
		log.Printf("privileges: DASH_DROP_PRIVS=0, keeping uid %d", os.Geteuid())
		return
	}
	if !privdrop.IsRoot() {
		log.Printf("privileges: not running as root (uid %d), nothing to drop", os.Geteuid())
		return
	}
	t := privdrop.Target{UID: envID("DASH_UID"), GID: envID("DASH_GID"), Keep: []uintptr{privdrop.CapNetAdmin}}
	if err := privdrop.ChownTree(data, t.UID, t.GID); err != nil {
		log.Fatalf("privileges: chown %s: %v", data, err)
	}
	lease := filepath.Join(root, "config/dhcp/dhcp4-leases.csv")
	if gid, err := privdrop.FileGID(lease); err != nil {
		log.Printf("privileges: %v (device names from DHCP may be unavailable)", err)
	} else if gid != 0 {
		t.Groups = []int{gid}
	}
	if err := privdrop.Drop(t); err != nil {
		log.Fatalf("%v", err)
	}
	log.Printf("privileges: dropped to uid %d gid %d groups %v", t.UID, t.GID, t.Groups)
	checked, err := privdrop.CheckHostRootSealed(root)
	if err != nil {
		log.Fatalf("self-check: %v", err)
	}
	if checked {
		log.Printf("self-check: %s is not reachable", filepath.Join(root, "proc/1/root"))
	}
}

// envID reads a uid/gid variable (default privdrop.Nobody).
func envID(key string) int {
	v := env(key, strconv.Itoa(privdrop.Nobody))
	n, err := strconv.Atoi(v)
	if err != nil || n <= 0 {
		log.Fatalf("%s: invalid value %q (want a non-zero id)", key, v)
	}
	return n
}
