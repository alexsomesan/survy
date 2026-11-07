// Package privdrop drops the dashboard's root privileges once it has opened
// what needs them (story 67: a compromised dashboard must not reach the host
// filesystem through /host/proc/1/root).
//
// The container runs as host root with the host's /proc mounted. After Drop,
// the process is an ordinary uid holding at most the capabilities in
// Target.Keep (the dashboard keeps only CAP_NET_ADMIN), so the kernel's
// ptrace-access check (which needs a matching uid or CAP_SYS_PTRACE) refuses
// /proc/<pid>/root of host processes.
//
// CAP_NET_ADMIN has to survive the uid change: conntrack netlink requests are
// checked against both the socket opener's credentials and the current task
// (__netlink_ns_capable), so a socket opened as root stops working once the
// task loses the capability. A bound :80 listener needs nothing further.
//
// Capabilities, keepcaps and the bounding set are per-thread, so every change
// goes through syscall.AllThreadsSyscall (the binary is built without cgo).
//
// Nothing here is covered by the seam tests (it needs root); the router
// smoke run verifies it.
package privdrop

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"syscall"
	"unsafe"

	"golang.org/x/sys/unix"
)

// Nobody is the default target uid and gid.
const Nobody = 65534

// Target is who the process becomes.
type Target struct {
	UID, GID int
	// Groups are the supplementary groups kept (e.g. the Kea lease file's
	// group, so the lease file stays readable).
	Groups []int
	// Keep are the capabilities retained (effective and permitted) after the
	// uid change; every other capability is also dropped from the bounding set.
	Keep []uintptr
}

// CapNetAdmin is CAP_NET_ADMIN.
const CapNetAdmin uintptr = 12

// IsRoot reports whether the process runs with euid 0.
func IsRoot() bool { return os.Geteuid() == 0 }

// ChownTree gives dir and everything below it to uid:gid, without following
// symlinks.
func ChownTree(dir string, uid, gid int) error {
	return filepath.WalkDir(dir, func(p string, _ fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		return os.Lchown(p, uid, gid)
	})
}

// FileGID is the group owning path.
func FileGID(path string) (int, error) {
	fi, err := os.Stat(path)
	if err != nil {
		return 0, err
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, fmt.Errorf("%s: no unix stat", path)
	}
	return int(st.Gid), nil
}

// Drop limits the bounding set to t.Keep, sets the supplementary groups,
// gid and uid of every thread to t, keeps only t.Keep as effective and
// permitted capabilities, clears ambient capabilities and sets no_new_privs.
// It fails closed: it returns an error unless the process ends up with exactly
// t's ids and capabilities and cannot regain root.
func Drop(t Target) error {
	if t.UID == 0 || t.GID == 0 {
		return errors.New("privdrop: refusing to drop to uid or gid 0")
	}
	keep := map[uintptr]bool{}
	var mask uint64
	for _, c := range t.Keep {
		keep[c] = true
		mask |= 1 << c
	}
	// 1. Bounding set: drop everything not kept (needs CAP_SETPCAP, so before
	// the uid change). EINVAL marks a capability this kernel doesn't know.
	for c := uintptr(0); c <= lastCap(); c++ {
		if keep[c] {
			continue
		}
		if err := allThreads(syscall.SYS_PRCTL, unix.PR_CAPBSET_DROP, c, 0); err != nil && !errors.Is(err, syscall.EINVAL) {
			return fmt.Errorf("privdrop: drop cap %d from bounding set: %w", c, err)
		}
	}
	// 2. Keep permitted capabilities across the uid change.
	if len(t.Keep) > 0 {
		if err := allThreads(syscall.SYS_PRCTL, unix.PR_SET_KEEPCAPS, 1, 0); err != nil {
			return fmt.Errorf("privdrop: prctl(PR_SET_KEEPCAPS): %w", err)
		}
	}
	// 3. Ids. syscall's setters apply to all threads.
	groups := t.Groups
	if groups == nil {
		groups = []int{}
	}
	if err := syscall.Setgroups(groups); err != nil {
		return fmt.Errorf("privdrop: setgroups %v: %w", groups, err)
	}
	if err := syscall.Setresgid(t.GID, t.GID, t.GID); err != nil {
		return fmt.Errorf("privdrop: setresgid %d: %w", t.GID, err)
	}
	if err := syscall.Setresuid(t.UID, t.UID, t.UID); err != nil {
		return fmt.Errorf("privdrop: setresuid %d: %w", t.UID, err)
	}
	// 4. Effective = permitted = Keep, inheritable and ambient empty.
	if err := capsetAll(mask); err != nil {
		return err
	}
	if err := allThreads(syscall.SYS_PRCTL, unix.PR_CAP_AMBIENT, unix.PR_CAP_AMBIENT_CLEAR_ALL, 0); err != nil && !errors.Is(err, syscall.EINVAL) {
		return fmt.Errorf("privdrop: clear ambient capabilities: %w", err)
	}
	if err := allThreads(syscall.SYS_PRCTL, unix.PR_SET_KEEPCAPS, 0, 0); err != nil {
		return fmt.Errorf("privdrop: prctl(PR_SET_KEEPCAPS, 0): %w", err)
	}
	if err := noNewPrivs(); err != nil {
		return err
	}
	if err := Verify(t); err != nil {
		return err
	}
	return verifyCaps(mask)
}

// allThreads runs a 3-argument syscall on every OS thread of the process.
func allThreads(trap, a1, a2, a3 uintptr) error {
	_, _, errno := syscall.AllThreadsSyscall(trap, a1, a2, a3)
	if errno != 0 {
		return errno
	}
	return nil
}

// lastCap is the highest capability number this kernel knows.
func lastCap() uintptr {
	b, err := os.ReadFile("/proc/sys/kernel/cap_last_cap")
	if err == nil {
		var n int
		if _, err := fmt.Sscanf(string(b), "%d", &n); err == nil && n > 0 {
			return uintptr(n)
		}
	}
	return 40
}

// capsetAll sets effective and permitted to mask (inheritable empty) on every
// thread.
func capsetAll(mask uint64) error {
	hdr := unix.CapUserHeader{Version: unix.LINUX_CAPABILITY_VERSION_3}
	var data [2]unix.CapUserData
	data[0].Effective, data[0].Permitted = uint32(mask), uint32(mask)
	data[1].Effective, data[1].Permitted = uint32(mask>>32), uint32(mask>>32)
	_, _, errno := syscall.AllThreadsSyscall(syscall.SYS_CAPSET,
		uintptr(unsafe.Pointer(&hdr)), uintptr(unsafe.Pointer(&data[0])), 0)
	runtime.KeepAlive(&hdr)
	runtime.KeepAlive(&data)
	if errno != 0 {
		return fmt.Errorf("privdrop: capset: %w", errno)
	}
	return nil
}

// verifyCaps checks the calling thread's effective and permitted sets.
func verifyCaps(mask uint64) error {
	hdr := unix.CapUserHeader{Version: unix.LINUX_CAPABILITY_VERSION_3}
	var data [2]unix.CapUserData
	if err := unix.Capget(&hdr, &data[0]); err != nil {
		return fmt.Errorf("privdrop: capget: %w", err)
	}
	eff := uint64(data[0].Effective) | uint64(data[1].Effective)<<32
	perm := uint64(data[0].Permitted) | uint64(data[1].Permitted)<<32
	if eff != mask || perm != mask {
		return fmt.Errorf("privdrop: capabilities eff=%#x perm=%#x, want %#x", eff, perm, mask)
	}
	return nil
}

// noNewPrivs sets PR_SET_NO_NEW_PRIVS on every thread. The dashboard never
// executes anything, so this is defence in depth. AllThreadsSyscall is
// unavailable when cgo is linked in; the current thread is then covered.
func noNewPrivs() error {
	_, _, errno := syscall.AllThreadsSyscall(syscall.SYS_PRCTL, unix.PR_SET_NO_NEW_PRIVS, 1, 0)
	if errno == 0 {
		return nil
	}
	if errno != syscall.ENOTSUP {
		return fmt.Errorf("privdrop: prctl(PR_SET_NO_NEW_PRIVS): %w", errno)
	}
	if err := unix.Prctl(unix.PR_SET_NO_NEW_PRIVS, 1, 0, 0, 0); err != nil {
		return fmt.Errorf("privdrop: prctl(PR_SET_NO_NEW_PRIVS): %w", err)
	}
	return nil
}

// Verify checks that the real, effective and saved ids are t's and that
// root cannot be regained.
func Verify(t Target) error {
	ruid, euid, suid := unix.Getresuid()
	rgid, egid, sgid := unix.Getresgid()
	if ruid != t.UID || euid != t.UID || suid != t.UID {
		return fmt.Errorf("privdrop: uids are %d/%d/%d, want %d", ruid, euid, suid, t.UID)
	}
	if rgid != t.GID || egid != t.GID || sgid != t.GID {
		return fmt.Errorf("privdrop: gids are %d/%d/%d, want %d", rgid, egid, sgid, t.GID)
	}
	if os.Getuid() == 0 || os.Geteuid() == 0 {
		return errors.New("privdrop: still uid 0")
	}
	if err := unix.Setresuid(0, 0, 0); err == nil {
		return errors.New("privdrop: could regain uid 0")
	}
	return nil
}

// CheckHostRootSealed fails if root+"/proc/1/root" (the host's root
// directory, through the host's /proc) can be opened. It is skipped (nil,
// false) when root+"/proc/1" does not exist.
func CheckHostRootSealed(root string) (checked bool, err error) {
	proc1 := filepath.Join(root, "proc", "1")
	if _, err := os.Stat(proc1); err != nil {
		return false, nil
	}
	f, err := os.Open(filepath.Join(proc1, "root"))
	if err != nil {
		return true, nil
	}
	f.Close()
	return true, fmt.Errorf("privdrop: %s is readable: the host filesystem is reachable", filepath.Join(proc1, "root"))
}
