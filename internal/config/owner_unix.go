//go:build unix

package config

import (
	"io/fs"
	"os/user"
	"strconv"
	"syscall"
)

// fileGroup returns the numeric group owning fi and its name, if the
// group database knows it (inside a container it often does not).
func fileGroup(fi fs.FileInfo) (int, string) {
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return -1, ""
	}
	gid := int(st.Gid)
	name := ""
	if g, err := user.LookupGroupId(strconv.Itoa(gid)); err == nil {
		name = g.Name
	}
	return gid, name
}
