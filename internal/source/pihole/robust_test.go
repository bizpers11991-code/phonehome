package pihole

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"testing"

	"github.com/bizpers11991-code/phonehome/internal/source"
)

// TestDBReplaced covers a pihole-FTL.db that is deleted and recreated
// (the usual fix for a corrupted one) while phonehome holds it open: ids
// start again at 1, below the saved cursor.
func TestDBReplaced(t *testing.T) {
	path, w := fixture(t, v5Schema)
	for range 5 {
		exec(t, w, `INSERT INTO queries (timestamp, type, status, domain, client) VALUES (1727870281, 1, 2, 'old.example', '192.168.1.20')`)
	}
	d := openDB(t, path)
	ctx := context.Background()
	if _, cur, err := d.FetchDNS(ctx, "", 10); err != nil || cur != "5" {
		t.Fatalf("first read: cursor %q, %v", cur, err)
	}

	w.Close()
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	_, w2 := fixtureAt(t, path, v5Schema)
	exec(t, w2, `INSERT INTO queries (timestamp, type, status, domain, client) VALUES
		(1727880000, 1, 2, 'new1.example', '192.168.1.20'), (1727880001, 1, 2, 'new2.example', '192.168.1.20')`)

	got, cur, err := d.FetchDNS(ctx, "5", 10)
	if err != nil || len(got) != 2 || got[0].Domain != "new1.example" || cur != "2" {
		t.Fatalf("after replacement: got %+v, %q, %v", got, cur, err)
	}
	// An emptied (not replaced) database keeps the cursor.
	exec(t, w2, `DELETE FROM queries`)
	if got, cur, err := d.FetchDNS(ctx, "2", 10); err != nil || got != nil || cur != "2" {
		t.Fatalf("after delete: got %+v, %q, %v", got, cur, err)
	}
}

func TestDBBadCursor(t *testing.T) {
	path, _ := fixture(t, v5Schema)
	d := openDB(t, path)
	for _, c := range []string{"x", "-3", "12@1727870281"} {
		if _, _, err := d.FetchDNS(context.Background(), c, 10); !errors.Is(err, source.ErrBadCursor) {
			t.Errorf("cursor %q: err = %v, want ErrBadCursor", c, err)
		}
	}
}

// TestDBConcurrentWAL reads a WAL-mode database while another connection
// keeps committing to it, as FTL does every DBinterval.
func TestDBConcurrentWAL(t *testing.T) {
	path, w := fixture(t, v6Schema)
	exec(t, w, `PRAGMA journal_mode=WAL`)
	d := openDB(t, path)
	ctx := context.Background()

	const n = 2000
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := range n / 10 {
			tx, err := w.Begin()
			if err != nil {
				t.Error(err)
				return
			}
			for j := range 10 {
				if _, err := tx.Exec(`INSERT INTO query_storage (timestamp, type, status, domain, client) VALUES (?, 1, 2, 2, 1)`,
					1727870281+float64(i*10+j)/10); err != nil {
					t.Error(err)
				}
			}
			if err := tx.Commit(); err != nil {
				t.Error(err)
				return
			}
		}
	}()

	var (
		cur  string
		read int
	)
	for read < n && !t.Failed() {
		qs, next, err := d.FetchDNS(ctx, cur, 97)
		if err != nil {
			t.Fatal(err)
		}
		read += len(qs)
		if next != "" {
			if id, _ := strconv.Atoi(next); id != read {
				t.Fatalf("cursor %s after %d rows", next, read)
			}
		}
		cur = next
	}
	wg.Wait()
	if read != n {
		t.Fatalf("read %d rows, want %d", read, n)
	}
}

// TestDBReadOnlyFiles opens a WAL database phonehome may read but not
// write, nor create files next to, as with /etc/pihole owned by the pihole
// user and phonehome in its group.
func TestDBReadOnlyFiles(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("permissions do not apply to root")
	}
	path, w := fixture(t, v5Schema)
	exec(t, w, `PRAGMA journal_mode=WAL`)
	exec(t, w, `INSERT INTO queries (timestamp, type, status, domain, client) VALUES (1727870281, 1, 2, 'a.example', '192.168.1.20')`)
	dir := filepath.Dir(path)
	for _, name := range []string{path, path + "-wal", path + "-shm"} {
		if err := os.Chmod(name, 0o444); err != nil && !os.IsNotExist(err) {
			t.Fatal(err)
		}
	}
	if err := os.Chmod(dir, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(dir, 0o755) })

	got, cur, err := openDB(t, path).FetchDNS(context.Background(), "", 10)
	if err != nil || len(got) != 1 || cur != "1" {
		t.Fatalf("got %+v, %q, %v", got, cur, err)
	}
}
