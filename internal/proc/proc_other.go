//go:build !darwin && !linux

package proc

func Lookup(int) (Process, bool) { return Process{}, false }

func Named(...string) []Process { return nil }

func Args(int) []string { return nil }

func TerminalOf(string) (TTY, bool) { return TTY{}, false }
