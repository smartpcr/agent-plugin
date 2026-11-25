//go:build linux

package subprocess

import (
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
)

// procStat is the fields of /proc/<pid>/stat after the command name: [0] is the
// state, [2] the process group, [19] the start tick.
func procStat(pid int) []string {
	b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return nil
	}
	s := string(b)
	i := strings.LastIndexByte(s, ')')
	if i < 0 {
		return nil
	}
	f := strings.Fields(s[i+1:])
	if len(f) < 20 {
		return nil
	}
	return f
}

// procIdentity reports whether pid runs (a zombie does not) and its start tick.
func procIdentity(pid int) (bool, string) {
	f := procStat(pid)
	if f == nil || f[0] == "Z" || f[0] == "X" {
		return false, ""
	}
	return true, f[19]
}

// survivors are the running members of a recorded tree: the direct child when
// it is the same process, the members of its control group while the group
// exists, otherwise the members of its process group (the child's pid is the
// group's id, and the kernel does not reuse it while a member is left).
func survivors(e ownerEntry) []int {
	var out []int
	if alive, start := procIdentity(e.Pid); alive && sameStart(e.PidStart, start) {
		out = append(out, e.Pid)
	}
	if e.Cgroup != "" {
		if b, err := os.ReadFile(filepath.Join(e.Cgroup, "cgroup.procs")); err == nil {
			for _, s := range strings.Fields(string(b)) {
				if n, err := strconv.Atoi(s); err == nil {
					out = append(out, n)
				}
			}
			return compact(out)
		}
	}
	dirs, err := os.ReadDir("/proc")
	if err != nil {
		return compact(out)
	}
	group := strconv.Itoa(e.Pid)
	for _, d := range dirs {
		n, err := strconv.Atoi(d.Name())
		if err != nil {
			continue
		}
		if f := procStat(n); f != nil && f[2] == group && f[0] != "Z" && f[0] != "X" {
			out = append(out, n)
		}
	}
	return compact(out)
}

func compact(pids []int) []int {
	slices.Sort(pids)
	return slices.Compact(pids)
}
