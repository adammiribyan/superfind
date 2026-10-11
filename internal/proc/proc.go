package proc

import (
	"bytes"
	"encoding/binary"
	"strconv"
	"strings"
	"time"
)

type TTY struct{ Major, Minor uint32 }

type Process struct {
	PID   int
	PPID  int
	Name  string
	Start time.Time
	TTY   TTY
}

func procArgs(raw []byte) []string {
	if len(raw) < 4 {
		return nil
	}
	argc := int(int32(binary.LittleEndian.Uint32(raw)))
	_, rest, found := bytes.Cut(raw[4:], []byte{0})
	if argc <= 0 || !found {
		return nil
	}
	rest = bytes.TrimLeft(rest, "\x00")
	args := make([]string, 0, argc)
	for len(args) < argc && len(rest) > 0 {
		arg, after, _ := bytes.Cut(rest, []byte{0})
		args = append(args, string(arg))
		rest = after
	}
	return args
}

func fromTdev(dev int32) TTY {
	if dev == -1 {
		return TTY{}
	}
	return TTY{uint32(dev>>24) & 0xff, uint32(dev) & 0xffffff}
}

type stat struct {
	name  string
	ppid  int
	tty   TTY
	ticks uint64
}

func parseStat(line string) (stat, bool) {
	open, end := strings.IndexByte(line, '('), strings.LastIndexByte(line, ')')
	if open < 0 || end < open {
		return stat{}, false
	}
	fields := strings.Fields(line[end+1:])
	if len(fields) < 20 || fields[0] == "Z" || fields[0] == "X" {
		return stat{}, false
	}
	ppid, err1 := strconv.Atoi(fields[1])
	nr, err2 := strconv.ParseInt(fields[4], 10, 64)
	ticks, err3 := strconv.ParseUint(fields[19], 10, 64)
	if err1 != nil || err2 != nil || err3 != nil {
		return stat{}, false
	}
	return stat{line[open+1 : end], ppid, fromTTYNr(nr), ticks}, true
}

func fromTTYNr(n int64) TTY {
	return TTY{uint32(n>>8) & 0xfff, uint32(n&0xff) | uint32(n>>12)&0xfff00}
}

const ticksPerSecond = 100

func startedAt(boot time.Time, ticks uint64) time.Time {
	return boot.Add(time.Duration(ticks) * (time.Second / ticksPerSecond))
}
