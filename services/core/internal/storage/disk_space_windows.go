//go:build windows

package storage

import "golang.org/x/sys/windows"

func diskAvailableBytes(path string) (int64, error) {
	pathName, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return 0, err
	}
	var freeBytes uint64
	if err := windows.GetDiskFreeSpaceEx(pathName, &freeBytes, nil, nil); err != nil {
		return 0, err
	}
	if freeBytes > uint64(^uint64(0)>>1) {
		return int64(^uint64(0) >> 1), nil
	}
	return int64(freeBytes), nil
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
