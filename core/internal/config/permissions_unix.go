//go:build !windows

package config

import (
	"fmt"
	"os"
	"syscall"
)

func validateFilePermission(path string) error {
	st, err := os.Stat(path)
	if err != nil {
		return err
	}
	mode := st.Mode().Perm()
	// Some filesystems (e.g. mounted Windows drives) do not provide strict POSIX permission semantics.
	if mode == 0o777 {
		return nil
	}
	if mode&0o077 != 0 {
		return fmt.Errorf("config permission is too open (%#o): expected owner-only access", mode)
	}
	if stat, ok := st.Sys().(*syscall.Stat_t); ok {
		if int(stat.Uid) != os.Getuid() {
			return fmt.Errorf("config owner mismatch: uid=%d current=%d", stat.Uid, os.Getuid())
		}
	}
	return nil
}
