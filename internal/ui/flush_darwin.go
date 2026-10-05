package ui

import "golang.org/x/sys/unix"

// fread is FREAD from <sys/fcntl.h>: flush the input queue only.
const fread = 1

// flushInput discards what the terminal received but nobody read.
func flushInput(fd int) error {
	return unix.IoctlSetPointerInt(fd, unix.TIOCFLUSH, fread)
}
