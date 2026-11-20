package main

import (
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// androidRes is where the Android app keeps its resources.
const androidRes = "android/app/src/main/res"

// languageQualifier is the language a values folder is for: "hi", "pt-rBR",
// or a BCP 47 tag written as "b+zh+Hans".
var languageQualifier = regexp.MustCompile(`^values-(?:([a-z]{2,3})(?:-r([A-Z]{2}))?|b\+([a-z]{2,3}(?:\+[A-Za-z0-9]+)*))$`)

// valuesDirLanguage is the language a values folder's strings are in, as a
// BCP 47 tag, or "" for the default folder and any other qualifier: a night
// theme, or a smallest width such as values-sw600dp, whose "sw" is not
// Swahili.
func valuesDirLanguage(dir string) string {
	m := languageQualifier.FindStringSubmatch(dir)
	switch {
	case m == nil:
		return ""
	case m[3] != "":
		return strings.ReplaceAll(m[3], "+", "-")
	case m[2] != "":
		return m[1] + "-" + m[2]
	}
	return m[1]
}

// readAndroid reads the app's strings: the English in values/strings.xml,
// with each language's from values-<language>/strings.xml.
func readAndroid(root string) ([]*unit, error) {
	res := filepath.Join(root, androidRes)
	source, err := readStringsXML(root, filepath.Join(res, "values", "strings.xml"))
	if err != nil {
		return nil, err
	}
	dirs, err := os.ReadDir(res)
	if err != nil {
		return nil, err
	}
	units := make([]*unit, 0, len(source))
	byKey := make(map[string]*unit, len(source))
	for _, s := range source {
		u := &unit{app: android, key: s.key, note: s.note, source: s.version, trans: map[string]*version{}}
		units = append(units, u)
		byKey[s.key] = u
	}
	for _, d := range dirs {
		lang := valuesDirLanguage(d.Name())
		if !d.IsDir() || lang == "" {
			continue
		}
		path := filepath.Join(res, d.Name(), "strings.xml")
		translated, err := readStringsXML(root, path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		for _, s := range translated {
			// A string with no English is lint's ExtraTranslation.
			if u, ok := byKey[s.key]; ok {
				u.trans[lang] = s.version
			}
		}
	}
	return units, nil
}

// androidString is one <string> or <plurals> of a strings.xml file.
type androidString struct {
	key     string
	note    string
	version *version
}

// readStringsXML reads a strings.xml file's translatable strings. A string's
// note is the comment just before it, with nothing but space between.
func readStringsXML(root, path string) ([]androidString, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	rel := relPath(root, path)

	var out []androidString
	dec := xml.NewDecoder(f)
	note := ""
	depth := 0
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			return out, nil
		}
		if err != nil {
			return nil, fmt.Errorf("%s: %w", rel, err)
		}
		switch t := tok.(type) {
		case xml.Comment:
			if depth == 1 {
				note = strings.TrimSpace(string(t))
			}
		case xml.CharData:
			if strings.TrimSpace(string(t)) != "" {
				note = ""
			}
		case xml.EndElement:
			depth--
		case xml.StartElement:
			depth++
			if depth != 2 || (t.Name.Local != "string" && t.Name.Local != "plurals") {
				note = ""
				continue
			}
			line, _ := dec.InputPos()
			s := androidString{key: attr(t, "name"), note: note, version: &version{file: rel, line: line, forms: map[string]form{}}}
			note = ""
			translatable := attr(t, "translatable") != "false"
			if t.Name.Local == "string" {
				text, err := innerText(dec)
				if err != nil {
					return nil, fmt.Errorf("%s:%d: %w", rel, line, err)
				}
				s.version.forms[""] = form{value: unescapeAndroid(text)}
			} else if err := readPluralItems(dec, s.version); err != nil {
				return nil, fmt.Errorf("%s:%d: %w", rel, line, err)
			}
			depth--
			if translatable {
				out = append(out, s)
			}
		}
	}
}

// readPluralItems reads a <plurals> element's items, up to its end.
func readPluralItems(dec *xml.Decoder, v *version) error {
	for {
		tok, err := dec.Token()
		if err != nil {
			return err
		}
		switch t := tok.(type) {
		case xml.EndElement:
			return nil
		case xml.StartElement:
			if t.Name.Local != "item" {
				if err := dec.Skip(); err != nil {
					return err
				}
				continue
			}
			text, err := innerText(dec)
			if err != nil {
				return err
			}
			v.forms["plural."+attr(t, "quantity")] = form{value: unescapeAndroid(text)}
		}
	}
}

// innerText is the text of the element just started, inner elements such as
// <xliff:g> included, up to its end.
func innerText(dec *xml.Decoder) (string, error) {
	var b strings.Builder
	for depth := 1; depth > 0; {
		tok, err := dec.Token()
		if err != nil {
			return "", err
		}
		switch t := tok.(type) {
		case xml.CharData:
			b.Write(t)
		case xml.StartElement:
			depth++
		case xml.EndElement:
			depth--
		}
	}
	return b.String(), nil
}

func attr(t xml.StartElement, name string) string {
	for _, a := range t.Attr {
		if a.Name.Local == name {
			return a.Value
		}
	}
	return ""
}

// unescapeAndroid is a string resource's text as the app shows it: its
// backslash escapes undone, a double-quoted string unquoted, and otherwise
// runs of space collapsed, as aapt does.
func unescapeAndroid(s string) string {
	s = strings.TrimSpace(s)
	quoted := len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"'
	if quoted {
		s = s[1 : len(s)-1]
	} else {
		s = strings.Join(strings.Fields(s), " ")
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] != '\\' || i+1 == len(s) {
			b.WriteByte(s[i])
			continue
		}
		i++
		switch s[i] {
		case 'n':
			b.WriteByte('\n')
		case 't':
			b.WriteByte('\t')
		default: // \' \" \\ \@ \?
			b.WriteByte(s[i])
		}
	}
	return b.String()
}

// relPath is path relative to root with forward slashes, as GitHub names a
// file in an annotation.
func relPath(root, path string) string {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return filepath.ToSlash(path)
	}
	return filepath.ToSlash(rel)
}
