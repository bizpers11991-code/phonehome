package web

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"io/fs"
	"mime"
	"net/http"
	"path"
	"strings"
	"time"
)

//go:embed static
var staticFS embed.FS

type asset struct {
	ctype string
	etag  string
	raw   []byte
	gz    []byte // nil when compression does not help
}

type assets map[string]asset

// loadAssets reads the embedded files once, computing ETags and gzipped
// copies up front so serving them costs nothing on a Raspberry Pi.
func loadAssets() assets {
	a := assets{}
	files, err := fs.ReadDir(staticFS, "static")
	if err != nil {
		panic(err)
	}
	for _, f := range files {
		raw, err := staticFS.ReadFile("static/" + f.Name())
		if err != nil {
			panic(err)
		}
		sum := sha256.Sum256(raw)
		as := asset{
			ctype: mime.TypeByExtension(path.Ext(f.Name())),
			etag:  `"` + hex.EncodeToString(sum[:8]) + `"`,
			raw:   raw,
		}
		if as.ctype == "" {
			as.ctype = "application/octet-stream"
		}
		var buf bytes.Buffer
		zw, _ := gzip.NewWriterLevel(&buf, gzip.BestCompression)
		zw.Write(raw)
		zw.Close()
		if buf.Len() < len(raw)*9/10 {
			as.gz = buf.Bytes()
		}
		a[f.Name()] = as
	}
	return a
}

// serve returns a handler for one asset. Assets are revalidated on every
// load (no-cache + ETag), which is cheap and never serves a stale UI after
// an upgrade.
func (a assets) serve(name string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		as, ok := a[name]
		if !ok {
			http.NotFound(w, r)
			return
		}
		h := w.Header()
		h.Set("Content-Type", as.ctype)
		h.Set("Cache-Control", "no-cache")
		h.Set("ETag", as.etag)
		body := as.raw
		if as.gz != nil {
			h.Add("Vary", "Accept-Encoding")
			if acceptsGzip(r) {
				h.Set("Content-Encoding", "gzip")
				h.Set("ETag", strings.TrimSuffix(as.etag, `"`)+`-gz"`)
				body = as.gz
			}
		}
		http.ServeContent(w, r, "", time.Time{}, bytes.NewReader(body))
	}
}

func acceptsGzip(r *http.Request) bool {
	for _, part := range strings.Split(r.Header.Get("Accept-Encoding"), ",") {
		enc, q, _ := strings.Cut(strings.TrimSpace(part), ";")
		if strings.TrimSpace(enc) == "gzip" && strings.ReplaceAll(q, " ", "") != "q=0" {
			return true
		}
	}
	return false
}
