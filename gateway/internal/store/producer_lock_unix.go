//go:build !windows

package store

import (
	"os"

	"golang.org/x/sys/unix"
)

func lockProducerFile(f *os.File) error { return unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB) }
func syncStateDirectory(path string) error {
	dir, err := os.Open(path) // #nosec G703 -- Sync the operator-configured state directory, never a request-supplied path.
	if err != nil {
		return err
	}
	defer func() { _ = dir.Close() }()
	return dir.Sync()
}
