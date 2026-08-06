//go:build !windows

package daemon

import "os"

func replaceUpdateObservationFile(from, to string) error {
	return os.Rename(from, to)
}

func syncUpdateObservationDir(path string) error {
	dir, err := os.Open(path)
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}
