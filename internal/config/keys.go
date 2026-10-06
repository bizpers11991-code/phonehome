package config

import (
	"errors"
	"fmt"
	"reflect"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

// unknownField matches yaml.v3's message for a key KnownFields rejects.
var unknownField = regexp.MustCompile(`^line (\d+): field (.+) not found in type config\.(\w+)$`)

// friendlyYAML rewrites yaml.v3's decoding errors for people editing the
// file: "line 3: unknown key "listne" (did you mean "listen"?)" instead of
// "yaml: unmarshal errors: line 3: field listne not found in type
// config.Config". Other errors are returned as they are, minus the "yaml: "
// prefix.
func friendlyYAML(err error) error {
	var te *yaml.TypeError
	if !errors.As(err, &te) {
		return errors.New(strings.TrimPrefix(err.Error(), "yaml: "))
	}
	lines := make([]string, 0, len(te.Errors))
	for _, e := range te.Errors {
		m := unknownField.FindStringSubmatch(e)
		if m == nil {
			lines = append(lines, e)
			continue
		}
		line, key, typ := m[1], m[2], m[3]
		msg := fmt.Sprintf("line %s: unknown key %q", line, key)
		if known := yamlKeys(typ); len(known) > 0 {
			if s := closest(key, known); s != "" {
				msg += fmt.Sprintf(" (did you mean %q?)", s)
			} else {
				msg += " (valid here: " + strings.Join(known, ", ") + ")"
			}
		}
		lines = append(lines, msg)
	}
	return errors.New(strings.Join(lines, "\n"))
}

// yamlKeys lists the keys a config struct type, named as in yaml.v3's
// messages, accepts.
func yamlKeys(typ string) []string {
	t := findType(reflect.TypeOf(Config{}), typ, map[reflect.Type]bool{})
	if t == nil {
		return nil
	}
	var keys []string
	for i := 0; i < t.NumField(); i++ {
		name, _, _ := strings.Cut(t.Field(i).Tag.Get("yaml"), ",")
		if name != "" && name != "-" {
			keys = append(keys, name)
		}
	}
	return keys
}

// findType finds the struct type called name in t or the types it holds.
func findType(t reflect.Type, name string, seen map[reflect.Type]bool) reflect.Type {
	for t.Kind() == reflect.Pointer || t.Kind() == reflect.Slice || t.Kind() == reflect.Map {
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct || seen[t] {
		return nil
	}
	seen[t] = true
	if t.Name() == name {
		return t
	}
	for i := 0; i < t.NumField(); i++ {
		if f := findType(t.Field(i).Type, name, seen); f != nil {
			return f
		}
	}
	return nil
}

// closest is the key in known nearest to key, if it is a likely typo: at
// most two edits away, or the same once "-" is read as "_".
func closest(key string, known []string) string {
	best, bestD := "", 3
	for _, k := range known {
		if strings.ReplaceAll(key, "-", "_") == k {
			return k
		}
		if d := editDistance(strings.ToLower(key), k); d < bestD {
			best, bestD = k, d
		}
	}
	return best
}

// editDistance is the Damerau–Levenshtein distance (with adjacent
// transpositions) between a and b.
func editDistance(a, b string) int {
	ra, rb := []rune(a), []rune(b)
	d := make([][]int, len(ra)+1)
	for i := range d {
		d[i] = make([]int, len(rb)+1)
		d[i][0] = i
	}
	for j := range d[0] {
		d[0][j] = j
	}
	for i := 1; i <= len(ra); i++ {
		for j := 1; j <= len(rb); j++ {
			cost := 1
			if ra[i-1] == rb[j-1] {
				cost = 0
			}
			d[i][j] = min(d[i-1][j]+1, d[i][j-1]+1, d[i-1][j-1]+cost)
			if i > 1 && j > 1 && ra[i-1] == rb[j-2] && ra[i-2] == rb[j-1] {
				d[i][j] = min(d[i][j], d[i-2][j-2]+1)
			}
		}
	}
	return d[len(ra)][len(rb)]
}
