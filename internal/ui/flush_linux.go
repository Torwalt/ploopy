package ui

import "golang.org/x/sys/unix"

// flushInput discards what the terminal received but nobody read.
func flushInput(fd int) error {
	return unix.IoctlSetInt(fd, unix.TCFLSH, unix.TCIFLUSH)
}
