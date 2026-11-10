//go:build !windows

package fileutil

import "os"

// ReadFileShared reads a file that may be replaced concurrently.
func ReadFileShared(path string) ([]byte, error) {
	return os.ReadFile(path)
}
