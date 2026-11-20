package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// The JSON of an Xcode String Catalog (.xcstrings). Apple has not published
// the format; these are the fields Xcode writes and this check reads.
type xcCatalog struct {
	SourceLanguage string             `json:"sourceLanguage"`
	Strings        map[string]xcEntry `json:"strings"`
}

type xcEntry struct {
	Comment         string                    `json:"comment"`
	ExtractionState string                    `json:"extractionState"`
	ShouldTranslate *bool                     `json:"shouldTranslate"`
	Localizations   map[string]xcLocalization `json:"localizations"`
}

// An xcLocalization is a language's text of an entry, and also each node of
// its variations: a string unit, or variations by plural or device, or both
// nested.
type xcLocalization struct {
	StringUnit    *xcStringUnit                        `json:"stringUnit"`
	Variations    map[string]map[string]xcLocalization `json:"variations"`
	Substitutions map[string]xcSubstitution            `json:"substitutions"`
}

type xcStringUnit struct {
	State string `json:"state"`
	Value string `json:"value"`
}

type xcSubstitution struct {
	FormatSpecifier string                               `json:"formatSpecifier"`
	Variations      map[string]map[string]xcLocalization `json:"variations"`
}

// skippedDirs are build output and dependencies, never the app's catalogs.
var skippedDirs = map[string]bool{"DerivedData": true, "build": true, "Pods": true}

// readCatalogs reads every String Catalog under root/ios.
func readCatalogs(root string) ([]*unit, error) {
	var units []*unit
	err := filepath.WalkDir(filepath.Join(root, "ios"), func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && (skippedDirs[d.Name()] || strings.HasPrefix(d.Name(), ".")) {
			return filepath.SkipDir
		}
		if d.IsDir() || filepath.Ext(path) != ".xcstrings" {
			return nil
		}
		read, err := readCatalog(root, path)
		units = append(units, read...)
		return err
	})
	return units, err
}

// readCatalog reads one String Catalog's entries, skipping those marked not to
// be translated. An entry with no text in the source language has its key as
// its English, as Xcode extracts it from the code.
func readCatalog(root, path string) ([]*unit, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	rel := relPath(root, path)
	var catalog xcCatalog
	if err := json.Unmarshal(body, &catalog); err != nil {
		return nil, fmt.Errorf("%s: %w", rel, err)
	}
	if catalog.SourceLanguage == "" {
		catalog.SourceLanguage = "en"
	}
	lines := keyLines(body)

	keys := make([]string, 0, len(catalog.Strings))
	for k := range catalog.Strings {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var units []*unit
	for _, key := range keys {
		e := catalog.Strings[key]
		if e.ShouldTranslate != nil && !*e.ShouldTranslate {
			continue
		}
		line := lines[key]
		u := &unit{app: ios, key: key, note: e.Comment, stale: e.ExtractionState == "stale", trans: map[string]*version{}}
		for lang, loc := range e.Localizations {
			v := &version{file: rel, line: line, forms: map[string]form{}}
			collectForms(loc, "", v.forms)
			if lang == catalog.SourceLanguage {
				u.source = v
			} else {
				u.trans[lang] = v
			}
		}
		if u.source == nil {
			u.source = &version{file: rel, line: line, forms: map[string]form{"": {value: key, state: "new"}}}
		}
		units = append(units, u)
	}
	return units, nil
}

// collectForms adds node's string units to forms by path.
func collectForms(node xcLocalization, path string, forms map[string]form) {
	if node.StringUnit != nil {
		forms[path] = form{value: node.StringUnit.Value, state: node.StringUnit.State}
	}
	collectVariations(node.Variations, path, "", forms)
	for name, sub := range node.Substitutions {
		collectVariations(sub.Variations, "@"+name+"/", "%"+sub.FormatSpecifier, forms)
	}
}

// collectVariations adds each variation's forms. In a substitution, whose
// argument its forms write as %arg, arg is that argument's format.
func collectVariations(variations map[string]map[string]xcLocalization, prefix, arg string, forms map[string]form) {
	for kind, cases := range variations {
		for name, node := range cases {
			path := prefix + kind + "." + name
			if node.StringUnit != nil {
				value := node.StringUnit.Value
				if arg != "" {
					value = strings.ReplaceAll(value, "%arg", arg)
				}
				forms[path] = form{value: value, state: node.StringUnit.State}
			}
			collectVariations(node.Variations, path+"/", arg, forms)
		}
	}
}

// keyLines is the line each entry's key is on: the catalog's "strings" object
// is the only one whose keys are indented four spaces.
func keyLines(body []byte) map[string]int {
	lines := map[string]int{}
	for i, line := range bytes.Split(body, []byte("\n")) {
		if !bytes.HasPrefix(line, []byte(`    "`)) || bytes.HasPrefix(line, []byte(`     `)) {
			continue
		}
		dec := json.NewDecoder(bytes.NewReader(bytes.TrimSpace(line)))
		var key string
		if dec.Decode(&key) == nil {
			if _, seen := lines[key]; !seen {
				lines[key] = i + 1
			}
		}
	}
	return lines
}
