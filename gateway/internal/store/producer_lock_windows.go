package store

import (
	"os"

	"golang.org/x/sys/windows"
)

func lockProducerFile(f *os.File) error {
	var overlapped windows.Overlapped
	return windows.LockFileEx(windows.Handle(f.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, &overlapped)
}

// Windows flushes the state file before replacement; directory handles cannot Sync.
func syncStateDirectory(string) error { return nil }
