//go:build !unix

package config

import "io/fs"

// fileGroup is unknown on systems without Unix ownership.
func fileGroup(fs.FileInfo) (int, string) { return -1, "" }
