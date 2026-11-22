package main

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// The iOS app's strings for one language come as the XLIFF file that
// `xcodebuild -exportLocalizations` writes, <lang>.xcloc/Localized
// Contents/<lang>.xliff, and go back through `xcodebuild -importLocalizations`,
// which writes the String Catalogs. The tool reads the file and fills in its
// <target> elements, leaving every other byte as Xcode wrote it. It never
// writes a String Catalog itself: Apple keeps the right to change that format.

// An xliffUnit is one <trans-unit>: a string, or one form of a string with
// variations, whose id is the string's key, "|==|", then the form's path, as
// in "%lld sent|==|plural.one" or "Route %@|==|substitutions.reports.plural.one".
type xliffUnit struct {
	file   string // the <file> it is in, by its original, such as Resources/Localizable.xcstrings
	id     string
	source string
	note   string
	target *string // nil when the unit has none
	// sourceEnd is the byte just after </source>, where a new <target> goes,
	// and indent the space before <source>, which a new <target> repeats.
	// targetStart and targetEnd are an existing <target>'s bytes.
	sourceEnd, targetStart, targetEnd int
	indent                            string
}

// key is the string the unit belongs to, and path its form within the
// string's variations, written as a String Catalog's forms are (version):
// "" for the string itself, "plural.one", or "@reports/plural.one" for a
// form of the substitution named reports.
func (u *xliffUnit) key() string {
	key, _, _ := strings.Cut(u.id, "|==|")
	return key
}

func (u *xliffUnit) path() string {
	_, path, _ := strings.Cut(u.id, "|==|")
	if rest, ok := strings.CutPrefix(path, "substitutions."); ok {
		if name, form, ok := strings.Cut(rest, "."); ok {
			return "@" + name + "/" + form
		}
	}
	return path
}

func (u *xliffUnit) current() string {
	if u.target == nil {
		return ""
	}
	return *u.target
}

type xliffDoc struct {
	path  string
	body  []byte
	units []*xliffUnit
}

// xliffFile is the XLIFF file in an .xcloc folder, or path itself when it
// names the file.
func xliffFile(path, lang string) string {
	if strings.HasSuffix(path, ".xcloc") {
		return filepath.Join(path, "Localized Contents", lang+".xliff")
	}
	return path
}

func readXLIFF(path string) (*xliffDoc, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	doc := &xliffDoc{path: path, body: body}
	dec := xml.NewDecoder(bytes.NewReader(body))
	file, space := "", ""
	var unit *xliffUnit
	for {
		start := int(dec.InputOffset())
		tok, err := dec.Token()
		if err == io.EOF {
			return doc, nil
		}
		if err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		switch t := tok.(type) {
		case xml.CharData:
			space = string(t)
		case xml.EndElement:
			if t.Name.Local == "trans-unit" && unit != nil {
				doc.units = append(doc.units, unit)
				unit = nil
			}
		case xml.StartElement:
			switch t.Name.Local {
			case "file":
				file = attr(t, "original")
			case "trans-unit":
				unit = &xliffUnit{file: file, id: attr(t, "id")}
			case "source", "target", "note":
				if unit == nil {
					continue
				}
				text, err := innerText(dec)
				if err != nil {
					return nil, fmt.Errorf("%s: %s: %w", path, unit.id, err)
				}
				switch t.Name.Local {
				case "source":
					unit.source, unit.sourceEnd, unit.indent = text, int(dec.InputOffset()), space
				case "target":
					unit.target, unit.targetStart, unit.targetEnd = &text, start, int(dec.InputOffset())
				default:
					unit.note = text
				}
			}
		}
	}
}

// withTargets is the file with each unit in targets given that text as its
// target, in place of the one it has or just after its source. The target is
// translated, and with draft also qualified leveraged-mt, which Xcode imports
// as machine_translated and exports again the same way. A unit whose target
// already says that text keeps it, state and all, unless the sheet filled it
// in: then it takes this import's state, so copying a draft into the
// translation column marks it read.
func (d *xliffDoc) withTargets(targets map[*xliffUnit]string, filled map[*xliffUnit]bool, draft bool) []byte {
	open := `<target state="translated">`
	if draft {
		open = `<target state="translated" state-qualifier="leveraged-mt">`
	}
	var out bytes.Buffer
	from := 0
	for _, u := range d.units {
		text, ok := targets[u]
		if !ok {
			continue
		}
		element := open + escapeXMLText(text) + `</target>`
		if u.target != nil && *u.target == text && (!filled[u] || string(d.body[u.targetStart:u.targetEnd]) == element) {
			continue
		}
		if u.target != nil {
			out.Write(d.body[from:u.targetStart])
			out.WriteString(element)
			from = u.targetEnd
		} else {
			out.Write(d.body[from:u.sourceEnd])
			out.WriteString(u.indent + element)
			from = u.sourceEnd
		}
	}
	out.Write(d.body[from:])
	return out.Bytes()
}

// escapeXMLText escapes text for an element's content as Xcode writes it:
// quotes and apostrophes stay as they are.
func escapeXMLText(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;").Replace(s)
}
