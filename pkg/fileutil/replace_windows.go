//go:build windows

package fileutil

import (
	"errors"
	"os"
	"time"

	"golang.org/x/sys/windows"
)

func replaceFile(src, dst string) error {
	delay := 5 * time.Millisecond
	for attempt := 0; ; attempt++ {
		err := os.Rename(src, dst)
		if err == nil {
			return nil
		}
		if attempt >= 8 || !retryableReplaceError(err) {
			return err
		}
		time.Sleep(delay)
		if delay < 100*time.Millisecond {
			delay *= 2
		}
	}
}

func retryableReplaceError(err error) bool {
	return errors.Is(err, windows.ERROR_ACCESS_DENIED) ||
		errors.Is(err, windows.ERROR_SHARING_VIOLATION) ||
		errors.Is(err, windows.ERROR_LOCK_VIOLATION)
}
