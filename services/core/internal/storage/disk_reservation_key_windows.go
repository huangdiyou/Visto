//go:build windows

package storage

import (
	"path/filepath"
	"strings"
)

func diskReservationKey(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	return "volume:" + strings.ToUpper(filepath.VolumeName(abs)), nil
}
