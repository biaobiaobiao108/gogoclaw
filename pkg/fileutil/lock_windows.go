//go:build windows

package fileutil

import (
	"os"

	"golang.org/x/sys/windows"
)

func lockFileHandle(f *os.File) error {
	return windows.LockFileEx(
		windows.Handle(f.Fd()),
		windows.LOCKFILE_EXCLUSIVE_LOCK,
		0,
		1,
		0,
		&windows.Overlapped{},
	)
}

func unlockFileHandle(f *os.File) error {
	return windows.UnlockFileEx(
		windows.Handle(f.Fd()),
		0,
		1,
		0,
		&windows.Overlapped{},
	)
}
