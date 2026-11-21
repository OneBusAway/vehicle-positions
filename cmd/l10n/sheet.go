package main

import (
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
)

// A translation sheet is one language's strings from both apps as a CSV file,
// for drafting and review in any spreadsheet: one row for each string, or for
// each form of a plural, with what a translator needs beside it. A string
// both apps say alike in English, and translate alike, is one row.
var sheetColumns = []string{
	"android", "ios", "form", "screen", "english", "note", "placeholders", "glossary", "onebusaway", "current", "translation",
}

// A sheetRow is one row of a sheet.
type sheetRow struct {
	// android is the Android string's name and ios the iOS string's XLIFF
	// unit; a row for both apps has both. form is a plural's category, ""
	// for a string with no plural.
	android, ios, form string
	// What a translator reads: the screen it is on, from the Android name, the
	// English, its note, its placeholders, the glossary's words for the terms
	// it uses, and what other OneBusAway apps say.
	screen, english, note, placeholders, glossary, onebusaway string
	// current is the translation the apps have now; translation is for a
	// new or corrected one.
	current, translation string
	line                 int // its line in a sheet read back
}

func (r sheetRow) fields() []string {
	return []string{r.android, r.ios, r.form, r.screen, r.english, r.note, r.placeholders, r.glossary, r.onebusaway, r.current, r.translation}
}

// A project is one language's strings in both apps: the English and the
// language's Android strings, and the iOS strings in the XLIFF Xcode
// exported for the language, which is nil without one.
type project struct {
	root, lang string
	english    []androidString
	byName     map[string]androidString
	current    map[string]*version // the language's Android strings, by name
	xliff      *xliffDoc
	byID       map[string]*xliffUnit
	glossary   []term
}

func loadProject(root, lang, xliffPath string) (*project, error) {
	dir := valuesDirFor(lang)
	if dir == "" {
		return nil, fmt.Errorf("%q is not a language tag such as hi, pt-BR or zh-Hans", lang)
	}
	res := filepath.Join(root, androidRes)
	english, err := readStringsXML(root, filepath.Join(res, "values", "strings.xml"))
	if err != nil {
		return nil, err
	}
	p := &project{root: root, lang: lang, english: english, byName: map[string]androidString{}, current: map[string]*version{}, byID: map[string]*xliffUnit{}}
	for _, s := range english {
		p.byName[s.key] = s
	}
	translated, err := readStringsXML(root, filepath.Join(res, dir, "strings.xml"))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	for _, s := range translated {
		p.current[s.key] = s.version
	}
	if xliffPath != "" {
		if p.xliff, err = readXLIFF(xliffFile(xliffPath, lang)); err != nil {
			return nil, err
		}
		for _, u := range p.xliff.units {
			if _, dup := p.byID[u.id]; dup {
				return nil, fmt.Errorf("%s: two strings have the id %q", p.xliff.path, u.id)
			}
			p.byID[u.id] = u
		}
	}
	if p.glossary, err = readGlossary(root); err != nil {
		return nil, err
	}
	return p, nil
}

// An iosString is the XLIFF units of one iOS string, in file order: one for a
// plain string, one for each form of one with variations.
type iosString struct {
	key   string
	units []*xliffUnit
}

func (p *project) iosStrings() []*iosString {
	if p.xliff == nil {
		return nil
	}
	var out []*iosString
	byKey := map[string]*iosString{}
	for _, u := range p.xliff.units {
		k := u.file + "\x00" + u.key()
		s, ok := byKey[k]
		if !ok {
			s = &iosString{key: u.key()}
			byKey[k] = s
			out = append(out, s)
		}
		s.units = append(s.units, u)
	}
	return out
}

// androidPaths are the forms of s a translation has: the string itself, or
// each plural category the language needs.
func (p *project) androidPaths(s androidString) []string {
	if _, ok := s.version.forms[""]; ok {
		return []string{""}
	}
	var out []string
	for _, c := range pluralCategories(p.lang) {
		out = append(out, "plural."+c)
	}
	return out
}

func englishOf(s androidString, path string) string {
	f, _ := (&unit{app: android, source: s.version}).counterpart(path)
	return f.value
}

// shared pairs each Android string with the iOS string that says the same in
// English, with values of the same types, when only one Android string says
// it and the two apps translate it alike, or neither does: one row then
// writes both apps, and a sheet read back unchanged changes neither. A string
// built from a substitution, which Android writes as a whole plural, keeps a
// row of its own.
func (p *project) shared(groups []*iosString) map[string]*iosString {
	saying := map[string][]androidString{}
	for _, s := range p.english {
		k := comparable(android, englishOf(s, "plural.other"))
		if f, ok := s.version.forms[""]; ok {
			k = comparable(android, f.value)
		}
		saying[k] = append(saying[k], s)
	}
	out := map[string]*iosString{}
	for _, is := range groups {
		other := is.units[0]
		for _, u := range is.units {
			if u.path() == "plural.other" {
				other = u
			}
		}
		same := saying[comparable(ios, other.source)]
		if len(same) != 1 || !p.agree(same[0], is) {
			continue
		}
		out[same[0].key] = is
	}
	return out
}

// agree reports whether every form of is matches the Android string's, in
// English and in the language.
func (p *project) agree(s androidString, is *iosString) bool {
	_, plain := s.version.forms[""]
	for _, u := range is.units {
		path := u.path()
		if (plain && path != "") || (!plain && !strings.HasPrefix(path, "plural.")) {
			return false
		}
		english := englishOf(s, path)
		if comparable(android, english) != comparable(ios, u.source) || !sameTypes(placeholders(android, english), placeholders(ios, u.source)) {
			return false
		}
		var translated *string
		if v, ok := p.current[s.key]; ok {
			if f, ok := v.forms[path]; ok {
				translated = &f.value
			}
		}
		switch {
		case translated == nil && u.target == nil:
		case translated != nil && u.target != nil && toIOS(*translated, u.source) == *u.target:
		default:
			return false
		}
	}
	return true
}

func sameTypes(a, b []string) bool {
	a, b = slices.Clone(a), slices.Clone(b)
	slices.Sort(a)
	slices.Sort(b)
	return slices.Equal(a, b)
}

// toIOS writes text, a translation in Android's placeholders, with the iOS
// English's: each value's placeholder becomes the iOS English's for the same
// value, numbered when the iOS English numbers its own or the translation
// takes them in another order.
func toIOS(text, iosEnglish string) string {
	specs := map[int]string{}
	numbered := false
	next := 1
	for _, m := range iosPlaceholder.FindAllStringSubmatch(iosEnglish, -1) {
		if placeholderType(m[len(m)-1]) == "" {
			continue
		}
		arg, spec := next, m[0]
		if m[1] != "" {
			arg, _ = strconv.Atoi(m[1])
			spec = "%" + strings.TrimPrefix(spec, "%"+m[1]+"$")
			numbered = true
		} else {
			next++
		}
		specs[arg] = spec
	}
	var args []int
	for _, p := range placeholders(android, text) {
		n, _ := strconv.Atoi(strings.Split(p, ":")[0])
		args = append(args, n)
	}
	for i, n := range args {
		if n != i+1 {
			numbered = true
		}
	}
	next = 1
	return androidPlaceholder.ReplaceAllStringFunc(text, func(ph string) string {
		m := androidPlaceholder.FindStringSubmatch(ph)
		if placeholderType(m[len(m)-1]) == "" {
			return ph
		}
		arg := next
		if m[1] != "" {
			arg, _ = strconv.Atoi(m[1])
		} else {
			next++
		}
		spec, ok := specs[arg]
		if !ok {
			return ph
		}
		if numbered {
			return "%" + strconv.Itoa(arg) + "$" + strings.TrimPrefix(spec, "%")
		}
		return spec
	})
}

// formName is a form's path as a sheet's form column says it: "one" for a
// plural's, "reports: one" for a substitution's.
func formName(path string) string {
	if name, form, ok := strings.Cut(strings.TrimPrefix(path, "@"), "/"); ok && strings.HasPrefix(path, "@") {
		return name + ": " + strings.TrimPrefix(form, "plural.")
	}
	return strings.TrimPrefix(path, "plural.")
}

// screenOf is the screen an Android string is on, which its name starts with.
func screenOf(name string) string {
	screen, _, _ := strings.Cut(name, "_")
	return screen
}

// glossaryNotes is what the glossary says of the terms english uses, for a
// translation into lang.
func glossaryNotes(glossary []term, lang, english string) string {
	var notes []string
	for _, t := range glossary {
		if !t.inEnglish.MatchString(english) {
			continue
		}
		note := t.term
		if t.keep {
			notes = append(notes, note+": keep as it is")
			continue
		}
		var says []string
		if w := t.words[lang]; w != "" {
			says = append(says, w)
		}
		if f := t.forbidden[lang]; len(f) > 0 {
			says = append(says, "not "+strings.Join(f, ", "))
		}
		if len(says) > 0 {
			note += ": " + strings.Join(says, ", ")
		}
		notes = append(notes, note)
	}
	return strings.Join(notes, "; ")
}

func (p *project) describe(r *sheetRow, format, path string, matches matchIndex) {
	if ps := placeholders(format, r.english); len(ps) > 0 {
		r.placeholders = describe(ps)
	}
	r.glossary = glossaryNotes(p.glossary, p.lang, r.english)
	r.onebusaway = matches.lookup(path, format, r.english)
}

// rows is the language's sheet: the Android strings in their file's order,
// each with the iOS string it shares a row with, then the iOS strings of
// their own.
func (p *project) rows(matches matchIndex) []sheetRow {
	groups := p.iosStrings()
	shared := p.shared(groups)
	withAndroid := map[*iosString]bool{}
	for _, is := range shared {
		withAndroid[is] = true
	}
	var rows []sheetRow
	for _, s := range p.english {
		is := shared[s.key]
		for _, path := range p.androidPaths(s) {
			r := sheetRow{android: s.key, form: formName(path), screen: screenOf(s.key), english: englishOf(s, path), note: s.note}
			if v, ok := p.current[s.key]; ok {
				r.current = v.forms[path].value
			}
			if is != nil {
				for _, u := range is.units {
					if u.path() == path {
						r.ios = u.id
					}
				}
			}
			p.describe(&r, android, path, matches)
			rows = append(rows, r)
		}
	}
	for _, is := range groups {
		if withAndroid[is] {
			continue
		}
		for _, u := range is.units {
			r := sheetRow{ios: u.id, form: formName(u.path()), english: u.source, note: u.note, current: u.current()}
			p.describe(&r, ios, u.path(), matches)
			rows = append(rows, r)
		}
	}
	return rows
}

func writeSheet(w io.Writer, rows []sheetRow) error {
	cw := csv.NewWriter(w)
	if err := cw.Write(sheetColumns); err != nil {
		return err
	}
	for _, r := range rows {
		if err := cw.Write(r.fields()); err != nil {
			return err
		}
	}
	cw.Flush()
	return cw.Error()
}

// readSheet reads a sheet back, by its header's column names, so a
// spreadsheet that moved or added columns still reads.
func readSheet(path string) ([]sheetRow, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	cr := csv.NewReader(f)
	cr.FieldsPerRecord = -1
	header, err := cr.Read()
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	col := map[string]int{}
	for i, name := range header {
		// A spreadsheet saving "CSV UTF-8" starts the file with a byte order mark.
		col[strings.TrimSpace(strings.TrimPrefix(name, "\ufeff"))] = i
	}
	for _, name := range []string{"android", "ios", "form", "current", "translation"} {
		if _, ok := col[name]; !ok {
			return nil, fmt.Errorf("%s: no %q column", path, name)
		}
	}
	var rows []sheetRow
	for {
		rec, err := cr.Read()
		if errors.Is(err, io.EOF) {
			return rows, nil
		}
		if err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		get := func(name string) string {
			if i, ok := col[name]; ok && i < len(rec) {
				return rec[i]
			}
			return ""
		}
		line, _ := cr.FieldPos(0)
		rows = append(rows, sheetRow{
			android: strings.TrimSpace(get("android")), ios: get("ios"), form: strings.TrimSpace(get("form")),
			english: get("english"), current: get("current"), translation: get("translation"), line: line,
		})
	}
}

// exportSheet writes the language's sheet to out.
func exportSheet(root, xliffPath, matchesDir, lang string, out io.Writer) error {
	p, err := loadProject(root, lang, xliffPath)
	if err != nil {
		return err
	}
	matches := matchIndex{}
	if matchesDir != "" {
		if matches, err = readMatches(matchesDir, lang); err != nil {
			return err
		}
	}
	return writeSheet(out, p.rows(matches))
}

// importSheet writes a sheet's translations into the language's strings.xml
// and its XLIFF, and returns the files it changed. Each row's translation, or
// failing that its current one, goes to the strings it names. A sheet whose
// placeholders or plural forms fail the check is refused whole: the findings
// come back, and nothing is written.
func importSheet(root, xliffPath, lang, sheetPath string) (refused []finding, changed []string, err error) {
	p, err := loadProject(root, lang, xliffPath)
	if err != nil {
		return nil, nil, err
	}
	rows, err := readSheet(sheetPath)
	if err != nil {
		return nil, nil, err
	}
	trans := map[string]map[string]string{}
	for key, v := range p.current {
		trans[key] = map[string]string{}
		for path, f := range v.forms {
			trans[key][path] = f.value
		}
	}
	targets := map[*xliffUnit]string{}
	firstLine := map[string]int{} // by app and key
	for _, r := range rows {
		text := r.translation
		if strings.TrimSpace(text) == "" {
			text = r.current
		}
		if r.android != "" {
			s, ok := p.byName[r.android]
			if !ok {
				return nil, nil, fmt.Errorf("%s:%d: no Android string is named %q", sheetPath, r.line, r.android)
			}
			path, err := p.formPath(s, r.form)
			if err != nil {
				return nil, nil, fmt.Errorf("%s:%d: %s: %w", sheetPath, r.line, r.android, err)
			}
			if _, seen := firstLine[android+r.android]; !seen {
				firstLine[android+r.android] = r.line
			}
			if text != "" {
				if trans[s.key] == nil {
					trans[s.key] = map[string]string{}
				}
				trans[s.key][path] = text
			}
		}
		if r.ios != "" {
			u, ok := p.byID[r.ios]
			if !ok {
				if p.xliff == nil {
					return nil, nil, fmt.Errorf("%s:%d: an iOS string: give the XLIFF Xcode exported with -xliff", sheetPath, r.line)
				}
				return nil, nil, fmt.Errorf("%s:%d: %s has no string %q", sheetPath, r.line, p.xliff.path, r.ios)
			}
			if _, seen := firstLine[ios+u.file+u.key()]; !seen {
				firstLine[ios+u.file+u.key()] = r.line
			}
			if text != "" {
				if r.android != "" {
					text = toIOS(text, u.source)
				}
				targets[u] = text
			}
		}
	}

	if refused := p.refusals(trans, targets, sheetPath, firstLine); len(refused) > 0 {
		return refused, nil, nil
	}
	path, err := p.writeAndroid(trans)
	if err != nil {
		return nil, nil, err
	}
	if path != "" {
		changed = append(changed, path)
	}
	if p.xliff != nil {
		if body := p.xliff.withTargets(targets); string(body) != string(p.xliff.body) {
			if err := os.WriteFile(p.xliff.path, body, 0o644); err != nil {
				return nil, nil, err
			}
			changed = append(changed, p.xliff.path)
		}
	}
	return nil, changed, nil
}

// formPath is the form a row's form column names on Android.
func (p *project) formPath(s androidString, form string) (string, error) {
	if _, plain := s.version.forms[""]; plain {
		if form != "" {
			return "", fmt.Errorf("has no plural, but the row's form is %q", form)
		}
		return "", nil
	}
	if !slices.Contains(pluralCategories(p.lang), form) {
		return "", fmt.Errorf("form %q is not one of %s", form, strings.Join(pluralCategories(p.lang), ", "))
	}
	return "plural." + form, nil
}

// refusals is what the check finds wrong with the placeholders and plural
// forms of the translations about to be written, each at the sheet's first
// row for the string.
func (p *project) refusals(trans map[string]map[string]string, targets map[*xliffUnit]string, sheetPath string, firstLine map[string]int) []finding {
	r := &report{}
	at := func(key string, fallback *version) *version {
		if line, ok := firstLine[key]; ok {
			return &version{file: sheetPath, line: line, forms: map[string]form{}}
		}
		if fallback != nil {
			return &version{file: fallback.file, line: fallback.line, forms: map[string]form{}}
		}
		return &version{file: sheetPath, forms: map[string]form{}}
	}
	for _, s := range p.english {
		forms := trans[s.key]
		if len(forms) == 0 {
			continue
		}
		v := at(android+s.key, p.current[s.key])
		for path, text := range forms {
			v.forms[path] = form{value: text}
		}
		checkTranslation(r, &unit{app: android, key: s.key, note: s.note, source: s.version, trans: map[string]*version{p.lang: v}}, p.lang, p.glossary, "")
	}
	for _, is := range p.iosStrings() {
		source := &version{forms: map[string]form{}}
		v := at(ios+is.units[0].file+is.key, &version{file: relPath(p.root, p.xliff.path)})
		for _, u := range is.units {
			source.forms[u.path()] = form{value: u.source}
			text, ok := targets[u]
			if !ok {
				text = u.current()
			}
			if text != "" {
				v.forms[u.path()] = form{value: text}
			}
		}
		if len(v.forms) > 0 {
			checkTranslation(r, &unit{app: ios, key: is.key, source: source, trans: map[string]*version{p.lang: v}}, p.lang, p.glossary, "")
		}
	}
	var out []finding
	for _, f := range r.findings {
		if f.severity == errorLevel && (f.rule == rulePlaceholders || f.rule == rulePlurals) {
			out = append(out, f)
		}
	}
	return out
}

// writeAndroid writes the language's strings.xml and returns its path, or ""
// when it would say what it says now, or the language has no Android string
// at all yet.
func (p *project) writeAndroid(trans map[string]map[string]string) (string, error) {
	path := filepath.Join(p.root, androidRes, valuesDirFor(p.lang), "strings.xml")
	body := stringsXML(p.english, trans, p.lang)
	old, err := os.ReadFile(path)
	switch {
	case err == nil && string(old) == string(body):
		return "", nil
	case errors.Is(err, os.ErrNotExist):
		empty := true
		for _, forms := range trans {
			empty = empty && len(forms) == 0
		}
		if empty {
			return "", nil
		}
	case err != nil:
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", err
	}
	return path, os.WriteFile(path, body, 0o644)
}
