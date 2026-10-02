package adguard

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// FuzzFetchDNS reads arbitrary querylog.json.1 and querylog.json contents
// and checks that the cursor always settles, no batch exceeds its limit,
// and batches neither lose nor repeat entries compared with one big read.
func FuzzFetchDNS(f *testing.F) {
	cur, err := os.ReadFile(filepath.Join("testdata", "querylog.json"))
	if err != nil {
		f.Fatal(err)
	}
	old, err := os.ReadFile(filepath.Join("testdata", "querylog.json.1"))
	if err != nil {
		f.Fatal(err)
	}
	f.Add(old, cur, uint8(2))
	f.Add([]byte(nil), cur, uint8(0))
	f.Add(old, []byte(`{"T":"2026-10-02T00:00:00Z","QH":"a.","IP":"::ffff:10.0.0.1","Result":{"IsFiltered":true,"Reason":"FilteredBlockedService"}}`+"\n"), uint8(5))
	f.Fuzz(func(t *testing.T, old, cur []byte, limit uint8) {
		path := filepath.Join(t.TempDir(), "querylog.json")
		if err := os.WriteFile(path, cur, 0o644); err != nil {
			t.Fatal(err)
		}
		if len(old) > 0 {
			if err := os.WriteFile(path+".1", old, 0o644); err != nil {
				t.Fatal(err)
			}
		}
		l := NewQueryLog(path)
		all, _ := drain(t, l, "", 1<<20)
		some, _ := drain(t, l, "", int(limit%8)+1)
		if len(all) != len(some) {
			t.Fatalf("read %d lookups at once but %d in batches", len(all), len(some))
		}
	})
}

// FuzzSeek checks that the binary search over a log of arbitrary lines
// terminates and stays inside the file.
func FuzzSeek(f *testing.F) {
	b, err := os.ReadFile(filepath.Join("testdata", "querylog.json"))
	if err != nil {
		f.Fatal(err)
	}
	f.Add(b, int64(0))
	f.Fuzz(func(t *testing.T, data []byte, target int64) {
		// Inflate the input past seek's window so the search actually runs.
		var buf bytes.Buffer
		for buf.Len() < 3*(64<<10) && len(data) > 0 {
			buf.Write(data)
		}
		r := bytes.NewReader(buf.Bytes())
		off := seek(r, int64(buf.Len()), time.Unix(target%(1<<34), 0))
		if off < 0 || off > int64(buf.Len()) {
			t.Fatalf("seek returned %d for a %d-byte file", off, buf.Len())
		}
	})
}
