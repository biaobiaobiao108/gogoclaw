//go:build windows

package fileutil

import (
	"io"
	"os"

	"golang.org/x/sys/windows"
)

// ReadFileShared reads a file while allowing concurrent replacement.
// Windows readers must opt into FILE_SHARE_DELETE for atomic rename-based
// writers to replace the file while it is open.
func ReadFileShared(path string) ([]byte, error) {
	pathPtr, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, &os.PathError{Op: "open", Path: path, Err: err}
	}
	handle, err := windows.CreateFile(
		pathPtr,
		windows.GENERIC_READ,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil,
		windows.OPEN_EXISTING,
		windows.FILE_ATTRIBUTE_NORMAL,
		0,
	)
	if err != nil {
		return nil, &os.PathError{Op: "open", Path: path, Err: err}
	}
	file := os.NewFile(uintptr(handle), path)
	defer file.Close()
	return io.ReadAll(file)
}
