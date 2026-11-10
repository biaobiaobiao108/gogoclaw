//go:build !windows

package fileutil

import (
	"os"

	"golang.org/x/sys/unix"
)

func lockFileHandle(f *os.File) error {
	return unix.Flock(int(f.Fd()), unix.LOCK_EX)
}

func unlockFileHandle(f *os.File) error {
	return unix.Flock(int(f.Fd()), unix.LOCK_UN)
}
