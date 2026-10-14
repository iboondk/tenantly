//go:build linux

package pgtest

import "syscall"

// filesystemType returns "exfat", "msdos" or "" for other filesystems.
func filesystemType(path string) string {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return ""
	}
	switch int64(st.Type) {
	case 0x2011BAB0: // EXFAT_SUPER_MAGIC
		return "exfat"
	case 0x4D44: // MSDOS_SUPER_MAGIC (FAT and VFAT)
		return "msdos"
	}
	return ""
}
