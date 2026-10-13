package bench

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// Machine describes where a benchmark ran.
type Machine struct {
	CPU, Memory, Kernel, Go, Filesystem string
	Cores                               int
}

// DescribeMachine reads the CPU model, memory, kernel and the filesystem
// holding dir from /proc (Linux, like lit-server).
func DescribeMachine(dir string) Machine {
	m := Machine{Cores: runtime.NumCPU(), Go: runtime.Version(), CPU: "unknown", Memory: "unknown", Kernel: "unknown", Filesystem: "unknown"}
	if b, err := os.ReadFile("/proc/cpuinfo"); err == nil {
		for _, line := range strings.Split(string(b), "\n") {
			if k, v, ok := strings.Cut(line, ":"); ok && strings.TrimSpace(k) == "model name" {
				m.CPU = strings.TrimSpace(v)
				break
			}
		}
	}
	if b, err := os.ReadFile("/proc/meminfo"); err == nil {
		for _, line := range strings.Split(string(b), "\n") {
			var kb int64
			if _, err := fmt.Sscanf(line, "MemTotal: %d kB", &kb); err == nil {
				m.Memory = fmt.Sprintf("%.1f GiB", float64(kb)/(1<<20))
			}
		}
	}
	if b, err := os.ReadFile("/proc/sys/kernel/osrelease"); err == nil {
		m.Kernel = strings.TrimSpace(string(b))
	}
	m.Filesystem = filesystemOf(dir)
	return m
}

// filesystemOf names the mount holding dir: type, mount point and device.
func filesystemOf(dir string) string {
	abs, err := filepath.EvalSymlinks(dir)
	if err != nil {
		abs = dir
	}
	b, err := os.ReadFile("/proc/self/mounts")
	if err != nil {
		return "unknown"
	}
	best, out := "", "unknown"
	for _, line := range strings.Split(string(b), "\n") {
		f := strings.Fields(line)
		if len(f) < 3 {
			continue
		}
		point := f[1]
		if (abs == point || strings.HasPrefix(abs, strings.TrimSuffix(point, "/")+"/")) && len(point) > len(best) {
			best, out = point, fmt.Sprintf("%s on %s (%s)", f[2], point, f[0])
		}
	}
	return out
}

// LoadAverage is /proc/loadavg's 1, 5 and 15 minute load averages.
func LoadAverage() string {
	b, err := os.ReadFile("/proc/loadavg")
	if err != nil {
		return "unknown"
	}
	f := strings.Fields(string(b))
	if len(f) < 3 {
		return "unknown"
	}
	return strings.Join(f[:3], " ")
}
