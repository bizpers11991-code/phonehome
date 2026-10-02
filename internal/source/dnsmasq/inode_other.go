//go:build !unix

package dnsmasq

import "os"

// inode is unknown off Unix; rotation is then only noticed when the file
// shrinks.
func inode(os.FileInfo) uint64 { return 0 }
