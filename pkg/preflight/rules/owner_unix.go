//go:build !windows

package rules

import (
	"os"
	"syscall"
)

// owner returns the numeric owner of a file.
func owner(fi os.FileInfo) (int, bool) {
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, false
	}
	return int(st.Uid), true
}
