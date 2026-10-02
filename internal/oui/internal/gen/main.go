// Command gen builds internal/oui/vendors.txt from the IEEE MAC address
// registries, keeping only the organisations listed in brands.go and
// renaming them to the short names people recognise.
//
// Run it via `go generate ./internal/oui`. By default it downloads the three
// registries (MA-L, MA-M, MA-S) from standards-oui.ieee.org; pass -dir to use
// copies you downloaded earlier (oui.csv, mam.csv, oui36.csv).
package main

import (
	"bufio"
	"encoding/csv"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"maps"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
	"unicode"
)

// registry is one IEEE assignment list. Prefix lengths are in hex digits.
type registry struct {
	file   string
	url    string
	digits int
}

var registries = []registry{
	{"oui.csv", "https://standards-oui.ieee.org/oui/oui.csv", 6},
	{"mam.csv", "https://standards-oui.ieee.org/oui28/mam.csv", 7},
	{"oui36.csv", "https://standards-oui.ieee.org/oui36/oui36.csv", 9},
}

func main() {
	dir := flag.String("dir", "", "read registries from this directory instead of downloading them")
	out := flag.String("out", "vendors.txt", "output file")
	date := flag.String("date", "", "registry download date for the header (default: today, or the file date with -dir)")
	report := flag.Bool("report", false, "print which registry names each brand matched, for reviewing brands.go")
	flag.Parse()
	log.SetFlags(0)
	log.SetPrefix("oui/gen: ")

	entries := map[string]string{} // hex prefix -> friendly name
	matched := map[string]map[string]bool{}
	downloaded := *date
	for _, reg := range registries {
		rc, mod, err := open(*dir, reg)
		if err != nil {
			log.Fatal(err)
		}
		if downloaded == "" {
			downloaded = mod.UTC().Format(time.DateOnly)
		}
		err = parse(rc, reg.digits, func(prefix, org string) {
			name := brandFor(org)
			if name == "" {
				return
			}
			entries[prefix] = name
			if matched[name] == nil {
				matched[name] = map[string]bool{}
			}
			matched[name][org] = true
		})
		rc.Close()
		if err != nil {
			log.Fatalf("%s: %v", reg.file, err)
		}
	}
	if len(entries) == 0 {
		log.Fatal("no entries matched; is the registry download an error page?")
	}
	if *report {
		for _, name := range slices.Sorted(maps.Keys(matched)) {
			fmt.Printf("%s\n", name)
			for _, org := range slices.Sorted(maps.Keys(matched[name])) {
				fmt.Printf("\t%s\n", org)
			}
		}
	}
	if err := write(*out, downloaded, entries); err != nil {
		log.Fatal(err)
	}
	log.Printf("wrote %d prefixes to %s", len(entries), *out)
}

// open returns the registry contents and its modification (or download) time.
func open(dir string, reg registry) (io.ReadCloser, time.Time, error) {
	if dir != "" {
		f, err := os.Open(filepath.Join(dir, reg.file))
		if err != nil {
			return nil, time.Time{}, err
		}
		st, err := f.Stat()
		if err != nil {
			f.Close()
			return nil, time.Time{}, err
		}
		return f, st.ModTime(), nil
	}
	req, err := http.NewRequest(http.MethodGet, reg.url, nil)
	if err != nil {
		return nil, time.Time{}, err
	}
	// The IEEE web firewall rejects requests without a browser-like agent.
	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; phonehome-oui-gen)")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, time.Time{}, err
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, time.Time{}, fmt.Errorf("%s: %s", reg.url, resp.Status)
	}
	return resp.Body, time.Now(), nil
}

// parse calls fn for every assignment in an IEEE registry CSV
// (Registry,Assignment,Organization Name,Organization Address).
func parse(r io.Reader, digits int, fn func(prefix, org string)) error {
	cr := csv.NewReader(r)
	cr.FieldsPerRecord = -1
	header, err := cr.Read()
	if err != nil {
		return err
	}
	if len(header) < 3 || header[1] != "Assignment" {
		return errors.New("unexpected header; is this an IEEE registry CSV?")
	}
	for {
		rec, err := cr.Read()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		if len(rec) < 3 {
			continue
		}
		prefix := strings.ToLower(strings.TrimSpace(rec[1]))
		if len(prefix) != digits {
			return fmt.Errorf("assignment %q: want %d hex digits", rec[1], digits)
		}
		fn(prefix, strings.TrimSpace(rec[2]))
	}
}

// normalize upper-cases s and turns every run of non-alphanumerics into a
// single space, padded at both ends so word sequences can be matched with
// strings.Contains.
func normalize(s string) string {
	var b strings.Builder
	b.WriteByte(' ')
	space := true
	for _, r := range strings.ToUpper(s) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
			space = false
		} else if !space {
			b.WriteByte(' ')
			space = true
		}
	}
	if !space {
		b.WriteByte(' ')
	}
	return b.String()
}

// normBrands is brands with every match normalized; exact matches keep
// their leading "=".
var normBrands = func() []brand {
	nb := make([]brand, len(brands))
	for i, b := range brands {
		nb[i].name = b.name
		for _, m := range b.match {
			if exact, ok := strings.CutPrefix(m, "="); ok {
				nb[i].match = append(nb[i].match, "="+normalize(exact))
			} else {
				nb[i].match = append(nb[i].match, normalize(m))
			}
		}
	}
	return nb
}()

// brandFor returns the friendly name for a registry organisation, or "".
// The first brand with a matching entry wins.
func brandFor(org string) string {
	n := normalize(org)
	for _, b := range normBrands {
		for _, m := range b.match {
			if exact, ok := strings.CutPrefix(m, "="); ok {
				if n == exact {
					return b.name
				}
			} else if strings.Contains(n, m) {
				return b.name
			}
		}
	}
	return ""
}

func write(path, date string, entries map[string]string) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	w := bufio.NewWriter(f)
	fmt.Fprintf(w, "# Code generated by internal/oui/internal/gen; DO NOT EDIT.\n")
	fmt.Fprintf(w, "# Source: IEEE MA-L, MA-M and MA-S registries, downloaded %s.\n", date)
	fmt.Fprintf(w, "# Format: <hex prefix (6, 7 or 9 digits)> TAB <vendor>\n")
	for _, p := range slices.Sorted(maps.Keys(entries)) {
		fmt.Fprintf(w, "%s\t%s\n", p, entries[p])
	}
	if err := w.Flush(); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}
