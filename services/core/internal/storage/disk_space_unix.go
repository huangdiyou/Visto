//go:build !windows

package storage

import "golang.org/x/sys/unix"

func diskAvailableBytes(path string) (int64, error) {
	var stat unix.Statfs_t
	if err := unix.Statfs(path, &stat); err != nil {
		return 0, err
	}
	return int64(stat.Bavail) * int64(stat.Bsize), nil
}

// AvailableDiskBytes reports usable free space for the filesystem containing path.
// Callers use it before accepting files that must be staged locally.
func AvailableDiskBytes(path string) (int64, error) {
	return diskAvailableBytes(path)
}

// StagingDiskReservation returns the physical-volume key and currently usable
// bytes for a local staging directory.
func StagingDiskReservation(path string) (string, int64, error) {
	key, err := diskReservationKey(path)
	if err != nil {
		return "", 0, err
	}
	available, err := AvailableDiskBytes(path)
	if err != nil {
		return "", 0, err
	}
	return key, available, nil
}
