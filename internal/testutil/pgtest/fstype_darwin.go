//go:build darwin

package pgtest

import (
	"strings"
	"syscall"
)

// filesystemType returns the lowercase filesystem name of the volume holding
// path, such as "apfs", "hfs", "exfat" or "msdos", or "" when unknown.
func filesystemType(path string) string {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return ""
	}
	var name []byte
	for _, c := range st.Fstypename {
		if c == 0 {
			break
		}
		name = append(name, byte(c))
	}
	return strings.ToLower(string(name))
}
