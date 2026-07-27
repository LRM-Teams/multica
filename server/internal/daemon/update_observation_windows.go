//go:build windows

package daemon

import "golang.org/x/sys/windows"

func replaceUpdateObservationFile(from, to string) error {
	fromPtr, err := windows.UTF16PtrFromString(from)
	if err != nil {
		return err
	}
	toPtr, err := windows.UTF16PtrFromString(to)
	if err != nil {
		return err
	}
	return windows.MoveFileEx(
		fromPtr,
		toPtr,
		windows.MOVEFILE_REPLACE_EXISTING|windows.MOVEFILE_WRITE_THROUGH,
	)
}

// MoveFileEx(..., MOVEFILE_WRITE_THROUGH) is the durable metadata boundary on
// Windows. Directory handles cannot be fsynced portably through os.File.
func syncUpdateObservationDir(string) error {
	return nil
}
