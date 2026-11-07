package app

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// userHZ is the kernel's clock tick rate for /proc times (100 on x86 Linux).
const userHZ = 100

// procFS reads the router's /proc and /sys files under a root directory.
// Every reader reports missing or malformed input as "not ok".
type procFS struct{ root string }

func (f procFS) readFile(p string) (string, bool) {
	b, err := os.ReadFile(filepath.Join(f.root, p))
	if err != nil {
		return "", false
	}
	return string(b), true
}

func (f procFS) readUint(p string) (uint64, bool) {
	s, ok := f.readFile(p)
	if !ok {
		return 0, false
	}
	n, err := strconv.ParseUint(strings.TrimSpace(s), 10, 64)
	return n, err == nil
}

func (f procFS) readInt(p string) (int64, bool) {
	s, ok := f.readFile(p)
	if !ok {
		return 0, false
	}
	n, err := strconv.ParseInt(strings.TrimSpace(s), 10, 64)
	return n, err == nil
}

// ifaceBytes returns an interface's cumulative rx and tx byte counters.
func (f procFS) ifaceBytes(iface string) (rx, tx uint64, ok bool) {
	rx, okR := f.readUint("/sys/class/net/" + iface + "/statistics/rx_bytes")
	tx, okT := f.readUint("/sys/class/net/" + iface + "/statistics/tx_bytes")
	return rx, tx, okR && okT
}

// cpuJiffies returns busy and total jiffies from the aggregate cpu line of
// /proc/stat. Idle is idle+iowait; guest time is already inside user/nice.
func (f procFS) cpuJiffies() (busy, total uint64, ok bool) {
	s, ok := f.readFile("/proc/stat")
	if !ok {
		return 0, 0, false
	}
	line, _, _ := strings.Cut(s, "\n")
	fields := strings.Fields(line)
	if len(fields) < 5 || fields[0] != "cpu" {
		return 0, 0, false
	}
	var idle uint64
	for i, v := range fields[1:] {
		if i >= 8 { // user nice system idle iowait irq softirq steal
			break
		}
		n, err := strconv.ParseUint(v, 10, 64)
		if err != nil {
			return 0, 0, false
		}
		total += n
		if i == 3 || i == 4 {
			idle += n
		}
	}
	return total - idle, total, true
}

// meminfo returns MemTotal and MemAvailable in bytes.
func (f procFS) meminfo() (total, avail int64, ok bool) {
	s, ok := f.readFile("/proc/meminfo")
	if !ok {
		return 0, 0, false
	}
	var haveT, haveA bool
	for _, line := range strings.Split(s, "\n") {
		k, v, found := strings.Cut(line, ":")
		if !found {
			continue
		}
		fields := strings.Fields(v)
		if len(fields) == 0 {
			continue
		}
		n, err := strconv.ParseInt(fields[0], 10, 64)
		if err != nil {
			continue
		}
		switch k {
		case "MemTotal":
			total, haveT = n*1024, true
		case "MemAvailable":
			avail, haveA = n*1024, true
		}
	}
	return total, avail, haveT && haveA
}

// coretempC is the CPU package temperature from the hwmon device named
// coretemp: the "Package id" sensor, else its first sensor.
func (f procFS) coretempC() *float64 {
	const hwmon = "/sys/class/hwmon/"
	devs, _ := os.ReadDir(filepath.Join(f.root, hwmon))
	for _, d := range devs {
		dir := hwmon + d.Name() + "/"
		if name, _ := f.readFile(dir + "name"); strings.TrimSpace(name) != "coretemp" {
			continue
		}
		chosen := dir + "temp1_input"
		inputs, _ := filepath.Glob(filepath.Join(f.root, dir, "temp*_input"))
		for _, in := range inputs {
			sensor := strings.TrimSuffix(filepath.Base(in), "_input")
			if label, _ := f.readFile(dir + sensor + "_label"); strings.HasPrefix(label, "Package id") {
				chosen = dir + sensor + "_input"
				break
			}
		}
		if milli, ok := f.readInt(chosen); ok {
			v := float64(milli) / 1000
			return &v
		}
	}
	return nil
}

// loadavg returns the 1, 5 and 15 minute load averages, or nil.
func (f procFS) loadavg() []float64 {
	s, ok := f.readFile("/proc/loadavg")
	if !ok {
		return nil
	}
	fields := strings.Fields(s)
	if len(fields) < 3 {
		return nil
	}
	out := make([]float64, 3)
	for i := range out {
		v, err := strconv.ParseFloat(fields[i], 64)
		if err != nil {
			return nil
		}
		out[i] = v
	}
	return out
}

// procStatFields returns the fields of a /proc/<pid>/stat file after the
// command name, so index 0 is field 3 (state).
func (f procFS) procStatFields(p string) ([]string, bool) {
	s, ok := f.readFile(p)
	if !ok {
		return nil, false
	}
	i := strings.LastIndexByte(s, ')')
	if i < 0 {
		return nil, false
	}
	return strings.Fields(s[i+1:]), true
}

func (f procFS) uptimeSeconds() (float64, bool) {
	s, ok := f.readFile("/proc/uptime")
	if !ok {
		return 0, false
	}
	fields := strings.Fields(s)
	if len(fields) == 0 {
		return 0, false
	}
	v, err := strconv.ParseFloat(fields[0], 64)
	return v, err == nil
}

// pidfileUptime is how long the process named in a pid file has been
// running, in whole seconds, or nil.
func (f procFS) pidfileUptime(pidfile string) *int64 {
	pid, ok := f.readUint(pidfile)
	if !ok {
		return nil
	}
	return f.pidUptime(pid)
}

// pidUptime is how long a process has been running, in whole seconds, or nil.
func (f procFS) pidUptime(pid uint64) *int64 {
	fields, ok := f.procStatFields("/proc/" + strconv.FormatUint(pid, 10) + "/stat")
	if !ok || len(fields) < 20 {
		return nil
	}
	ticks, err := strconv.ParseUint(fields[19], 10, 64) // field 22: starttime
	if err != nil {
		return nil
	}
	up, ok := f.uptimeSeconds()
	start := float64(ticks) / userHZ
	if !ok || up < start {
		return nil
	}
	v := int64(up - start)
	return &v
}

// isPPPD reports whether pid is `pppd call <iface>`.
func (f procFS) isPPPD(pid uint64, iface string) bool {
	s, ok := f.readFile("/proc/" + strconv.FormatUint(pid, 10) + "/cmdline")
	if !ok {
		return false
	}
	args := strings.Split(strings.TrimRight(s, "\x00"), "\x00")
	if len(args) == 0 || filepath.Base(args[0]) != "pppd" {
		return false
	}
	for i := 1; i+1 < len(args); i++ {
		if args[i] == "call" && args[i+1] == iface {
			return true
		}
	}
	return false
}

// findPPPD scans /proc for the pppd process serving iface. It is used when
// /run (and with it the pid file) is not visible, as inside the container.
func (f procFS) findPPPD(iface string) (uint64, bool) {
	entries, err := os.ReadDir(filepath.Join(f.root, "/proc"))
	if err != nil {
		return 0, false
	}
	for _, e := range entries {
		pid, err := strconv.ParseUint(e.Name(), 10, 64)
		if err != nil || !e.IsDir() {
			continue
		}
		if f.isPPPD(pid, iface) {
			return pid, true
		}
	}
	return 0, false
}

// cgroupContainer is one container's cgroup v2 readings.
type cgroupContainer struct {
	name          string
	usageUsec     uint64
	usageOK       bool
	memBytes      int64
	memLimitBytes int64 // 0 when unlimited
}

const containerSlice = "/sys/fs/cgroup/system.slice/"

// containers reads every vyos-container-<name>.service cgroup, sorted by name.
func (f procFS) containers() []cgroupContainer {
	ents, _ := os.ReadDir(filepath.Join(f.root, containerSlice))
	var out []cgroupContainer
	for _, e := range ents {
		name, ok := strings.CutPrefix(e.Name(), "vyos-container-")
		if !ok || !e.IsDir() {
			continue
		}
		name, ok = strings.CutSuffix(name, ".service")
		if !ok {
			continue
		}
		dir := containerSlice + e.Name() + "/"
		c := cgroupContainer{name: name}
		if s, ok := f.readFile(dir + "cpu.stat"); ok {
			for _, line := range strings.Split(s, "\n") {
				if v, ok := strings.CutPrefix(line, "usage_usec "); ok {
					c.usageUsec, c.usageOK = parseUint(v)
				}
			}
		}
		if n, ok := f.readInt(dir + "memory.current"); ok {
			c.memBytes = n
		}
		c.memLimitBytes = f.containerMemLimit(dir)
		out = append(out, c)
	}
	return out
}

func parseUint(s string) (uint64, bool) {
	n, err := strconv.ParseUint(strings.TrimSpace(s), 10, 64)
	return n, err == nil
}

// selfCPUTicks is this process's user+system CPU time in clock ticks.
func (f procFS) selfCPUTicks() (uint64, bool) {
	fields, ok := f.procStatFields("/proc/self/stat")
	if !ok || len(fields) < 13 {
		return 0, false
	}
	u, okU := parseUint(fields[11]) // field 14: utime
	s, okS := parseUint(fields[12]) // field 15: stime
	return u + s, okU && okS
}

// selfRSS is this process's resident memory in bytes.
func (f procFS) selfRSS() *int64 {
	s, ok := f.readFile("/proc/self/status")
	if !ok {
		return nil
	}
	for _, line := range strings.Split(s, "\n") {
		v, ok := strings.CutPrefix(line, "VmRSS:")
		if !ok {
			continue
		}
		fields := strings.Fields(v)
		if len(fields) == 0 {
			return nil
		}
		n, err := strconv.ParseInt(fields[0], 10, 64)
		if err != nil {
			return nil
		}
		n *= 1024
		return &n
	}
	return nil
}

// containerMemLimit is a container's memory limit in bytes, 0 when unlimited.
// Podman sets the limit on the libpod-payload-<id> child cgroup, so when the
// service cgroup says "max" (which fails to parse) the payload's is used.
func (f procFS) containerMemLimit(dir string) int64 {
	if n, ok := f.readInt(dir + "memory.max"); ok {
		return n
	}
	payloads, _ := filepath.Glob(filepath.Join(f.root, dir, "libpod-payload-*", "memory.max"))
	for _, p := range payloads {
		rel, err := filepath.Rel(f.root, p)
		if err != nil {
			continue
		}
		if n, ok := f.readInt("/" + rel); ok {
			return n
		}
	}
	return 0
}
