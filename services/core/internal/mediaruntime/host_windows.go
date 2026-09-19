//go:build windows

package mediaruntime

import (
	"os"

	"golang.org/x/sys/windows"

	"review-studio.local/core/internal/delivery"
)

func RequireAdministrator() error {
	if !windows.GetCurrentProcessToken().IsElevated() {
		return delivery.NewError(delivery.CodePermissionDenied, "run the runtime manager from an administrator terminal")
	}
	return nil
}
func secureRoot(root string) error {
	// Replace the DACL, rather than adding grants to unknown inherited/explicit ACEs.
	sd, err := windows.SecurityDescriptorFromString("D:P(A;OICI;FA;;;SY)(A;OICI;FA;;;BA)(A;OICI;GRGX;;;BU)")
	if err != nil {
		return err
	}
	dacl, _, err := sd.DACL()
	if err != nil {
		return err
	}
	return windows.SetNamedSecurityInfo(root, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, dacl, nil)
}

func lockFile(f *os.File) error {
	return windows.LockFileEx(windows.Handle(f.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, &windows.Overlapped{})
}
func unlockFile(f *os.File) {
	_ = windows.UnlockFileEx(windows.Handle(f.Fd()), 0, 1, 0, &windows.Overlapped{})
}
func replaceFile(from, to string) error {
	a, e := windows.UTF16PtrFromString(from)
	if e != nil {
		return e
	}
	b, e := windows.UTF16PtrFromString(to)
	if e != nil {
		return e
	}
	return windows.MoveFileEx(a, b, windows.MOVEFILE_REPLACE_EXISTING|windows.MOVEFILE_WRITE_THROUGH)
}
