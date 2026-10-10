//go:build darwin

package proc

import (
	"bytes"
	"strings"
	"time"

	"golang.org/x/sys/unix"
)

func Lookup(pid int) (Process, bool) {
	if pid <= 0 {
		return Process{}, false
	}
	k, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	if err != nil || int(k.Proc.P_pid) != pid {
		return Process{}, false
	}
	return processOf(k), true
}

func Named(prefix string) []Process {
	all, err := unix.SysctlKinfoProcSlice("kern.proc.all")
	if err != nil {
		return nil
	}
	var out []Process
	for i := range all {
		if p := processOf(&all[i]); p.PID > 0 && strings.HasPrefix(p.Name, prefix) {
			out = append(out, p)
		}
	}
	return out
}

func Args(pid int) []string {
	raw, err := unix.SysctlRaw("kern.procargs2", pid)
	if err != nil {
		return nil
	}
	return procArgs(raw)
}

func TerminalOf(path string) (TTY, bool) {
	var st unix.Stat_t
	if unix.Stat(path, &st) != nil || st.Mode&unix.S_IFMT != unix.S_IFCHR {
		return TTY{}, false
	}
	dev := uint64(uint32(st.Rdev))
	return TTY{unix.Major(dev), unix.Minor(dev)}, true
}

func processOf(k *unix.KinfoProc) Process {
	name, _, _ := bytes.Cut(k.Proc.P_comm[:], []byte{0})
	started := time.Unix(k.Proc.P_starttime.Sec, int64(k.Proc.P_starttime.Usec)*int64(time.Microsecond))
	return Process{PID: int(k.Proc.P_pid), Name: string(name), Start: started, TTY: fromTdev(k.Eproc.Tdev)}
}
