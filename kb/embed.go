// Package kbdata embeds phonehome's community-maintained knowledge base: who
// operates which domains, what that traffic is for, and how to turn it off.
// The data is plain YAML so anyone can contribute; see README.md in this
// directory. Parsing and validation live in internal/kb.
package kbdata

import "embed"

// FS holds the companies/, domains/ and fixes/ directories. Only .yaml and
// .yml files are read; anything else (such as a README) is ignored.
//
//go:embed companies domains fixes
var FS embed.FS
