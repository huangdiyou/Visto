//go:build !windows

package mediaruntime

import (
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"

	"review-studio.local/core/internal/delivery"
)

func RequireAdministrator() error {
	if os.Geteuid() != 0 {
		return delivery.NewError(delivery.CodePermissionDenied, "run the runtime manager as root")
	}
	return nil
}
func secureRoot(root string) error {
	// A service-writable parent could replace the runtime directory after install.
	for p := root; ; p = filepath.Dir(p) {
		var st unix.Stat_t
		if err := unix.Lstat(p, &st); err != nil {
			return err
		}
		if st.Uid != 0 || (st.Mode&0022 != 0 && st.Mode&unix.S_ISVTX == 0) {
			return delivery.NewError(delivery.CodePermissionDenied, "runtime root and parents must be owned by root and not writable by the service account")
		}
		if filepath.Dir(p) == p {
			break
		}
	}
	return os.Chmod(root, 0755)
}
func lockFile(f *os.File) error { return unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB) }
func unlockFile(f *os.File)     { _ = unix.Flock(int(f.Fd()), unix.LOCK_UN) }
func replaceFile(from, to string) error {
	if e := os.Rename(from, to); e != nil {
		return e
	}
	d, e := os.Open(filepath.Dir(to))
	if e != nil {
		return e
	}
	defer d.Close()
	return d.Sync()
}
