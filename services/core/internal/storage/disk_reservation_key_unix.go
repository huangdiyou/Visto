//go:build !windows

package storage

import (
	"fmt"
	"os"
	"syscall"
)

func diskReservationKey(path string) (string, error) {
	info, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return "", fmt.Errorf("resolve filesystem identifier")
	}
	return fmt.Sprintf("device:%d", stat.Dev), nil
}
