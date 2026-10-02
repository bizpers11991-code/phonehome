//go:build tools

// Pins the only third-party modules phonehome may use. See docs/ARCHITECTURE.md.
package tools

import (
	_ "golang.org/x/image/font/gofont/gomono"
	_ "golang.org/x/image/font/opentype"
	_ "gopkg.in/yaml.v3"
	_ "modernc.org/sqlite"
)
