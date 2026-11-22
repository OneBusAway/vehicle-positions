package main

import (
	"fmt"
	"regexp"
	"strings"
)

var (
	languageTag = regexp.MustCompile(`^([a-z]{2,3})(?:-([A-Z]{2}))?$`)
	anyTag      = regexp.MustCompile(`^[a-z]{2,3}(?:-[A-Za-z0-9]+)*$`)
)

// valuesDirFor is the folder a language's strings go in: values-hi,
// values-pt-rBR, or values-b+zh+Hans for a tag Android's older qualifiers
// can't write. It is valuesDirLanguage turned round, and "" for a tag that is
// not one.
func valuesDirFor(lang string) string {
	if m := languageTag.FindStringSubmatch(lang); m != nil && len(m[1]) == 2 {
		if m[2] != "" {
			return "values-" + m[1] + "-r" + m[2]
		}
		return "values-" + m[1]
	}
	if anyTag.MatchString(lang) {
		return "values-b+" + strings.ReplaceAll(lang, "-", "+")
	}
	return ""
}

// escapeAndroid writes s as a string resource's text, so that aapt, and
// unescapeAndroid, read it back as s: XML's special characters as entities,
// backslashes, quotes and apostrophes escaped, a leading @ or ? kept from
// reading as a reference, direction marks and isolates as \u escapes so the
// file shows them, and the whole quoted when its spaces would otherwise be
// collapsed.
func escapeAndroid(s string) string {
	var b strings.Builder
	for i, r := range s {
		switch {
		case isDirectionControl(r):
			fmt.Fprintf(&b, `\u%04x`, r)
		case r == '&':
			b.WriteString("&amp;")
		case r == '<':
			b.WriteString("&lt;")
		case r == '>':
			b.WriteString("&gt;")
		case r == '\\', r == '\'', r == '"':
			b.WriteString(`\` + string(r))
		case r == '\n':
			b.WriteString(`\n`)
		case r == '\t':
			b.WriteString(`\t`)
		case i == 0 && (r == '@' || r == '?'):
			b.WriteString(`\` + string(r))
		default:
			b.WriteRune(r)
		}
	}
	if strings.Join(strings.Fields(s), " ") != s {
		return `"` + b.String() + `"`
	}
	return b.String()
}

// stringsXML is a language's strings.xml: each English string the language
// has a translation of, in the English file's order, a plural's forms in
// CLDR's order. A string or a plural form with no translation is left out,
// so the app shows the English.
func stringsXML(english []androidString, trans map[string]map[string]string, lang string) []byte {
	var b strings.Builder
	b.WriteString("<?xml version=\"1.0\" encoding=\"utf-8\"?>\n<resources>\n")
	for _, s := range english {
		forms := trans[s.key]
		if len(forms) == 0 {
			continue
		}
		if _, plain := s.version.forms[""]; plain {
			b.WriteString(`    <string name="` + s.key + `">` + escapeAndroid(forms[""]) + "</string>\n")
			continue
		}
		b.WriteString(`    <plurals name="` + s.key + "\">\n")
		for _, c := range pluralCategories(lang) {
			if text, ok := forms["plural."+c]; ok {
				b.WriteString(`        <item quantity="` + c + `">` + escapeAndroid(text) + "</item>\n")
			}
		}
		b.WriteString("    </plurals>\n")
	}
	b.WriteString("</resources>\n")
	return []byte(b.String())
}

// isDirectionControl reports whether r is an invisible character that sets
// the direction of the text around it. Written raw, it hides in the file, and
// GitHub flags it as a possible "Trojan Source" attack.
func isDirectionControl(r rune) bool {
	return r == '\u061c' || r == '\u200e' || r == '\u200f' || (r >= '\u202a' && r <= '\u202e') || (r >= '\u2066' && r <= '\u2069')
}
