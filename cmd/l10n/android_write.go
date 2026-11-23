package main

import (
	"bytes"
	"cmp"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"regexp"
	"slices"
	"strings"
)

var (
	languageTag = regexp.MustCompile(`^([a-z]{2,3})(?:-([A-Z]{2}))?$`)
	anyTag      = regexp.MustCompile(`^[a-z]{2,3}(?:-[A-Za-z0-9]+)*$`)
)

// valuesDirFor is the folder a language's strings go in: values-hi,
// values-fil, values-pt-rBR, or values-b+zh+Hans for a tag Android's older
// qualifiers can't write. The older qualifiers take a two- or three-letter
// language, as aapt2 and valuesDirLanguage read them. It is valuesDirLanguage
// turned round, and "" for a tag that is not one.
func valuesDirFor(lang string) string {
	if m := languageTag.FindStringSubmatch(lang); m != nil {
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

// indentStep is how far a new file indents its entries, and a plural its
// items.
const indentStep = "    "

// stringsXML is a new strings.xml for a language: each English string the
// language has a translation of, in the English file's order. A string or a
// plural form with no translation is left out, so the app shows the English.
func stringsXML(english []androidString, trans map[string]map[string]string, lang string) []byte {
	var b strings.Builder
	b.WriteString("<?xml version=\"1.0\" encoding=\"utf-8\"?>\n<resources>\n")
	for _, s := range english {
		if forms := trans[s.key]; len(forms) > 0 {
			b.WriteString(indentStep + entryXML(s, forms, lang, indentStep) + "\n")
		}
	}
	b.WriteString("</resources>\n")
	return []byte(b.String())
}

// entryXML is a string's entry with forms as its translation: a <string>, or
// a <plurals> at indent with its items a step further in.
func entryXML(s androidString, forms map[string]string, lang, indent string) string {
	if isPlain(s) {
		return `<string name="` + s.key + `">` + escapeAndroid(forms[""]) + "</string>"
	}
	return `<plurals name="` + s.key + `">` + pluralItems(forms, lang, indent+indentStep, nil) + "\n" + indent + "</plurals>"
}

func isPlain(s androidString) bool {
	_, plain := s.version.forms[""]
	return plain
}

// pluralItems is a plural's items with forms, in CLDR's order, each on a line
// of its own at indent. kept holds, by quantity, each item to keep as the
// file has it.
func pluralItems(forms map[string]string, lang, indent string, kept map[string]string) string {
	var b strings.Builder
	for _, c := range pluralCategories(lang) {
		text, ok := forms["plural."+c]
		if !ok {
			continue
		}
		b.WriteString("\n" + indent)
		if item, ok := kept[c]; ok {
			b.WriteString(item)
		} else {
			b.WriteString(`<item quantity="` + c + `">` + escapeAndroid(text) + "</item>")
		}
	}
	return b.String()
}

// editStringsXML is a language's strings.xml, old, with trans written into
// it, so that a file kept by hand keeps its notes: an entry whose text is new
// gets the new text, an entry the file lacks goes in after the one before it
// in English, and everything else stays as it was, byte for byte. current is
// what old says now.
func editStringsXML(old []byte, english []androidString, current map[string]*version, trans map[string]map[string]string, lang string) ([]byte, error) {
	root, entries, err := scanStringsXML(old)
	if err != nil {
		return nil, err
	}
	indent, first := indentStep, len(old)
	for _, e := range entries {
		if e.start < first {
			first, indent = e.start, indentAt(old, e.start)
		}
	}
	var splices []splice
	after := root.inner // where an entry the file lacks goes
	for _, s := range english {
		forms := trans[s.key]
		e := entries[s.key]
		switch {
		case len(forms) == 0, e != nil && sameForms(forms, current[s.key]):
			// nothing new
		case e == nil:
			splices = append(splices, splice{after, after, "\n" + indent + entryXML(s, forms, lang, indent)})
		case isPlain(s) != (e.name == "string"): // the file has the other kind
			splices = append(splices, splice{e.start, e.end, entryXML(s, forms, lang, indentAt(old, e.start))})
		case isPlain(s):
			splices = append(splices, e.setText(old, escapeAndroid(forms[""])))
		default:
			splices = append(splices, e.setText(old, e.pluralText(old, forms, current[s.key], lang)))
		}
		if e != nil {
			after = e.end
		}
	}
	slices.SortStableFunc(splices, func(a, b splice) int { return cmp.Compare(a.from, b.from) })
	var b bytes.Buffer
	at := 0
	for _, sp := range splices {
		b.Write(old[at:sp.from])
		b.WriteString(sp.text)
		at = sp.to
	}
	b.Write(old[at:])
	return b.Bytes(), nil
}

// sameForms reports whether forms say just what v does.
func sameForms(forms map[string]string, v *version) bool {
	if v == nil || len(forms) != len(v.forms) {
		return false
	}
	for path, text := range forms {
		if f, ok := v.forms[path]; !ok || f.value != text {
			return false
		}
	}
	return true
}

// A splice puts text in place of the bytes from from to to.
type splice struct {
	from, to int
	text     string
}

// An element is where one sits in a strings.xml file: the whole of it from
// start to end, and what is between its tags from inner to innerEnd.
type element struct {
	name                        string
	start, inner, innerEnd, end int
	items                       map[string]*element // a <plurals>'s, by quantity
}

// scanStringsXML finds a strings.xml file's <resources>, and each <string>
// and <plurals> in it by name.
func scanStringsXML(body []byte) (*element, map[string]*element, error) {
	dec := xml.NewDecoder(bytes.NewReader(body))
	var root *element
	entries := map[string]*element{}
	var open []*element // the elements the decoder is in, nil for one not kept
	for {
		start := int(dec.InputOffset())
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, nil, err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			e := &element{name: t.Name.Local, start: start, inner: int(dec.InputOffset())}
			switch {
			case len(open) == 0:
				root = e
			case len(open) == 1 && (e.name == "string" || e.name == "plurals"):
				e.items = map[string]*element{}
				entries[attr(t, "name")] = e
			case len(open) == 2 && open[1] != nil && open[1].name == "plurals" && e.name == "item":
				open[1].items[attr(t, "quantity")] = e
			default:
				e = nil
			}
			open = append(open, e)
		case xml.EndElement:
			if e := open[len(open)-1]; e != nil {
				e.innerEnd, e.end = start, int(dec.InputOffset())
			}
			open = open[:len(open)-1]
		}
	}
	if root == nil {
		return nil, nil, errors.New("no <resources>")
	}
	if root.closesItself(body) {
		return nil, nil, errors.New("<resources/> has no room for a string: write it as <resources></resources>")
	}
	return root, entries, nil
}

func (e *element) closesItself(body []byte) bool {
	return bytes.HasSuffix(body[e.start:e.inner], []byte("/>"))
}

// setText is the splice that puts text between e's tags, which stay as they
// are, or that writes out a tag that closes itself to hold it.
func (e *element) setText(body []byte, text string) splice {
	if e.closesItself(body) {
		open := strings.TrimRight(strings.TrimSuffix(string(body[e.start:e.inner]), "/>"), " \t\n")
		return splice{e.start, e.end, open + ">" + text + "</" + e.name + ">"}
	}
	return splice{e.inner, e.innerEnd, text}
}

// pluralText is what goes between the tags of e, a <plurals>, for forms: an
// item for each, each item whose text is as it was kept as it is.
func (e *element) pluralText(body []byte, forms map[string]string, current *version, lang string) string {
	indent := indentAt(body, e.start)
	itemIndent := indent + indentStep
	kept := map[string]string{}
	first := len(body)
	for quantity, item := range e.items {
		if item.start < first {
			first, itemIndent = item.start, indentAt(body, item.start)
		}
		if current == nil {
			continue
		}
		if f, ok := current.forms["plural."+quantity]; ok && f.value == forms["plural."+quantity] {
			kept[quantity] = string(body[item.start:item.end])
		}
	}
	return pluralItems(forms, lang, itemIndent, kept) + "\n" + indent
}

// indentAt is the space before offset i on its line, or "" when there is more
// than space before it.
func indentAt(body []byte, i int) string {
	line := bytes.LastIndexByte(body[:i], '\n') + 1
	if indent := body[line:i]; len(bytes.TrimLeft(indent, " \t")) == 0 {
		return string(indent)
	}
	return ""
}

// isDirectionControl reports whether r is an invisible character that sets
// the direction of the text around it. Written raw, it hides in the file, and
// GitHub flags it as a possible "Trojan Source" attack.
func isDirectionControl(r rune) bool {
	return r == '\u061c' || r == '\u200e' || r == '\u200f' || (r >= '\u202a' && r <= '\u202e') || (r >= '\u2066' && r <= '\u2069')
}
