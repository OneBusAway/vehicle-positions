package main

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// matchIndex is what other OneBusAway apps say in one language for an
// English text, by the form's path and the text as comparable writes it:
// suggestions for whoever drafts or reviews the sheet, never written to the
// apps. They come from a folder of those apps' checkouts on the user's own
// disk; the tool fetches nothing.
type matchIndex map[string][]string

func matchKey(path, format, english string) string {
	return path + "\x00" + comparable(format, english)
}

func (m matchIndex) add(path, format, english, text string) {
	k := matchKey(path, format, english)
	if text != "" && !slices.Contains(m[k], text) {
		m[k] = append(m[k], text)
	}
}

// lookup is every match for a form's English, joined for one cell.
func (m matchIndex) lookup(path, format, english string) string {
	return strings.Join(m[matchKey(path, format, english)], " | ")
}

// readMatches reads the translations into lang under dir: each Android
// values/strings.xml beside a values-<lang>/strings.xml, and each String
// Catalog.
func readMatches(dir, lang string) (matchIndex, error) {
	m := matchIndex{}
	langDir := valuesDirFor(lang)
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		// A hidden folder, such as .git, holds no app's strings.
		if d.IsDir() {
			if path != dir && strings.HasPrefix(d.Name(), ".") {
				return filepath.SkipDir
			}
			return nil
		}
		switch {
		case d.Name() == "strings.xml" && filepath.Base(filepath.Dir(path)) == "values":
			return m.addAndroid(dir, path, filepath.Join(filepath.Dir(filepath.Dir(path)), langDir, "strings.xml"))
		case filepath.Ext(path) == ".xcstrings":
			units, err := readCatalog(dir, path)
			if err != nil {
				return err
			}
			for _, u := range units {
				if v, ok := u.trans[lang]; ok {
					for p, f := range v.forms {
						if english, ok := u.counterpart(p); ok {
							m.add(p, ios, english.value, f.value)
						}
					}
				}
			}
		}
		return nil
	})
	return m, err
}

// addAndroid adds an Android app's translations: the English in english, the
// language's in translated, if that app has the language.
func (m matchIndex) addAndroid(root, english, translated string) error {
	trans, err := readStringsXML(root, translated)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	source, err := readStringsXML(root, english)
	if err != nil {
		return err
	}
	byKey := map[string]*version{}
	for _, s := range source {
		byKey[s.key] = s.version
	}
	for _, s := range trans {
		v, ok := byKey[s.key]
		if !ok {
			continue
		}
		u := &unit{app: android, source: v}
		for p, f := range s.version.forms {
			if english, ok := u.counterpart(p); ok {
				m.add(p, android, english.value, f.value)
			}
		}
	}
	return nil
}
