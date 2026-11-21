package main

import (
	"fmt"
	"slices"
	"sort"
	"strings"
)

type options struct {
	// release also fails a language that ships at under releaseCoverage in
	// either app.
	release bool
}

// releaseCoverage is the share of an app's strings a language must have for a
// release to ship it.
const releaseCoverage = 95.0

// The rules, as findings name them. docs/translations/README.md says what each
// means. Font coverage, that a language drawn in the Android app's bundled
// font has every letter in it, is the Android app's FontCoverageTest, beside
// the app's list of languages set in the system font.
const (
	rulePlaceholders = "placeholders"
	rulePlurals      = "plurals"
	ruleNumbers      = "numbers"
	ruleURLs         = "urls"
	ruleKeep         = "do-not-translate"
	ruleForbidden    = "forbidden"
	ruleIOSState     = "ios-state"
	ruleStatus       = "status"
	ruleRelease      = "release-coverage"
	ruleConsistency  = "consistency"
	ruleNote         = "note"
)

// runCheck checks the translations of the repo at root.
func runCheck(root string, opts options) (*report, error) {
	androidUnits, err := readAndroid(root)
	if err != nil {
		return nil, err
	}
	iosUnits, err := readCatalogs(root)
	if err != nil {
		return nil, err
	}
	glossary, err := readGlossary(root)
	if err != nil {
		return nil, err
	}
	languages, err := readStatus(root)
	if err != nil {
		return nil, err
	}
	statusOf := map[string]string{}
	for _, l := range languages {
		statusOf[l.lang] = l.status
	}

	r := &report{}
	units := slices.Concat(androidUnits, iosUnits)
	for _, u := range units {
		if u.stale {
			r.add(u.finding(errorLevel, ruleIOSState, "en", u.source, "Xcode no longer finds this string in the code: delete it from the catalog"))
			continue
		}
		checkSource(r, u, glossary)
		for _, lang := range sortedLangs(u.trans) {
			checkTranslation(r, u, lang, glossary, statusOf[lang])
		}
	}
	checkConsistency(r, androidUnits, iosUnits)
	r.coverage = coverageOf(androidUnits, iosUnits, languages)
	checkStatus(r, languages, r.coverage, opts)
	r.sort()
	return r, nil
}

func (u *unit) finding(sev severity, rule, lang string, v *version, msg string) finding {
	return finding{severity: sev, rule: rule, file: v.file, line: v.line, key: u.key, lang: lang, message: msg}
}

func (u *unit) format() string { return u.app }

func sortedLangs(m map[string]*version) []string {
	langs := make([]string, 0, len(m))
	for l := range m {
		langs = append(langs, l)
	}
	sort.Strings(langs)
	return langs
}

func sortedPaths(forms map[string]form) []string {
	paths := make([]string, 0, len(forms))
	for p := range forms {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	return paths
}

// checkSource checks a unit's English: its plural forms, the words the house
// rule keeps out of it, and that a string taking values has a note saying
// what they are.
func checkSource(r *report, u *unit, glossary []term) {
	checkPlurals(r, u, "en", u.source)
	takesValues := false
	for _, path := range sortedPaths(u.source.forms) {
		text := u.source.forms[path].value
		takesValues = takesValues || len(placeholders(u.format(), text)) > 0
		for _, t := range glossary {
			for _, w := range t.forbidden["en"] {
				if containsWord(text, w) {
					r.add(u.finding(errorLevel, ruleForbidden, "en", u.source, fmt.Sprintf("the English says %q; the apps say %q (glossary)", w, t.term)))
				}
			}
		}
	}
	if takesValues && strings.TrimSpace(u.note) == "" {
		r.add(u.finding(warning, ruleNote, "en", u.source, "takes a value but has no note saying what it is"))
	}
}

// checkTranslation checks one language's text of a unit against its English.
func checkTranslation(r *report, u *unit, lang string, glossary []term, status string) {
	v := u.trans[lang]
	checkPlurals(r, u, lang, v)
	for _, path := range sortedPaths(v.forms) {
		f := v.forms[path]
		english, ok := u.counterpart(path)
		if !ok {
			continue
		}
		_, _, plural := pluralGroup(path)
		at := ""
		if path != "" {
			at = path + ": "
		}
		missing, extra := sameValues(placeholders(u.format(), english.value), placeholders(u.format(), f.value), plural)
		if len(missing) > 0 || len(extra) > 0 {
			r.add(u.finding(errorLevel, rulePlaceholders, lang, v, fmt.Sprintf("%splaceholders %s missing, %s not in the English", at, describe(missing), describe(extra))))
		}
		if u.format() == ios {
			for _, msg := range otherSpecifiers(english.value, f.value) {
				r.add(u.finding(errorLevel, rulePlaceholders, lang, v, at+msg))
			}
		}
		have := numbers(u.format(), f.value)
		for _, n := range numbers(u.format(), english.value) {
			if !slices.Contains(have, n) {
				r.add(u.finding(errorLevel, ruleNumbers, lang, v, fmt.Sprintf("%sthe English has %s; write it in digits", at, n)))
			}
		}
		for _, link := range urls(english.value) {
			if !strings.Contains(f.value, link) {
				r.add(u.finding(errorLevel, ruleURLs, lang, v, fmt.Sprintf("%s%s must stay as it is", at, link)))
			}
		}
		for _, t := range glossary {
			if t.keep && strings.Contains(english.value, t.term) && !strings.Contains(f.value, t.term) {
				r.add(u.finding(errorLevel, ruleKeep, lang, v, fmt.Sprintf("%s%q stays as it is (glossary)", at, t.term)))
			}
			if !t.inEnglish.MatchString(english.value) {
				continue
			}
			for _, w := range t.forbidden[lang] {
				if containsWord(f.value, w) {
					r.add(u.finding(errorLevel, ruleForbidden, lang, v, fmt.Sprintf("%s%q is not the word for %q (glossary)", at, w, t.term)))
				}
			}
		}
		switch {
		case f.state == "needs_review":
			r.add(u.finding(errorLevel, ruleIOSState, lang, v, fmt.Sprintf("%sneeds review since its English changed", at)))
		case f.state == "machine_translated" && status == statusReviewed:
			r.add(u.finding(errorLevel, ruleIOSState, lang, v, fmt.Sprintf("%sa machine translation in a reviewed language", at)))
		}
	}
}

// otherSpecifiers says which of an iOS translation's values are written with
// a specifier other than its English's for the same type: Android's %s where
// iOS takes %@, or %d where it takes %lld. Each type name covers both, but a
// C string or a 32-bit number read in place of an object or a 64-bit one
// shows garbage or crashes.
func otherSpecifiers(english, translated string) []string {
	want := map[int]iosValue{}
	for _, v := range iosValues(english) {
		want[v.arg] = v
	}
	var out []string
	for _, v := range iosValues(translated) {
		// A value of another type is already a placeholders finding.
		if w, ok := want[v.arg]; ok && w.kind == v.kind && w.specifier != v.specifier {
			out = append(out, fmt.Sprintf("%s is %s: write it as the English does, %s", describe([]string{fmt.Sprintf("%d:%s", v.arg, v.kind)}), v.written, w.written))
		}
	}
	return out
}

// checkPlurals checks that each of v's plural groups has every category the
// language needs, and that a translation has every plural group its English
// has. A single string in place of one reads the same for every count, which
// only an iOS translation into a language whose one category is "other" can
// do: Android never reads a <string> in place of <plurals>.
func checkPlurals(r *report, u *unit, lang string, v *version) {
	groups := map[string][]string{}
	for path := range v.forms {
		if prefix, category, ok := pluralGroup(path); ok {
			groups[prefix] = append(groups[prefix], category)
		}
	}
	if v != u.source {
		single := u.app == ios && slices.Equal(pluralCategories(lang), []string{"other"})
		for path := range u.source.forms {
			prefix, _, ok := pluralGroup(path)
			if !ok {
				continue
			}
			if _, has := groups[prefix]; has {
				continue
			}
			if _, plain := v.forms[strings.TrimSuffix(prefix, "/")]; single && plain {
				continue
			}
			groups[prefix] = nil
		}
	}
	prefixes := make([]string, 0, len(groups))
	for p := range groups {
		prefixes = append(prefixes, p)
	}
	sort.Strings(prefixes)
	for _, prefix := range prefixes {
		var missing []string
		for _, c := range pluralCategories(lang) {
			if !slices.Contains(groups[prefix], c) {
				missing = append(missing, c)
			}
		}
		if len(missing) > 0 {
			where := ""
			if prefix != "" {
				where = strings.TrimSuffix(prefix, "/") + ": "
			}
			r.add(u.finding(errorLevel, rulePlurals, lang, v, fmt.Sprintf("%splural forms %s missing", where, list(missing))))
		}
	}
}

// checkConsistency warns where the two apps translate the same English
// differently. Both say the same thing, so they should say it the same way.
func checkConsistency(r *report, androidUnits, iosUnits []*unit) {
	type side struct {
		u    *unit
		path string
	}
	shared := func(u *unit) []side {
		var out []side
		for _, path := range []string{"", "plural.other"} {
			if _, ok := u.source.forms[path]; ok {
				out = append(out, side{u, path})
			}
		}
		return out
	}
	byEnglish := map[string]side{}
	for _, u := range androidUnits {
		for _, s := range shared(u) {
			byEnglish[s.path+"\x00"+comparable(android, u.source.forms[s.path].value)] = s
		}
	}
	for _, u := range iosUnits {
		if u.stale {
			continue
		}
		for _, s := range shared(u) {
			a, ok := byEnglish[s.path+"\x00"+comparable(ios, u.source.forms[s.path].value)]
			if !ok {
				continue
			}
			for _, lang := range sortedLangs(u.trans) {
				av, ok := a.u.trans[lang]
				if !ok {
					continue
				}
				af, aok := av.forms[a.path]
				ifm, iok := u.trans[lang].forms[s.path]
				if aok && iok && comparable(android, af.value) != comparable(ios, ifm.value) {
					r.add(u.finding(warning, ruleConsistency, lang, u.trans[lang],
						fmt.Sprintf("Android's %s says %q, iOS says %q", a.u.key, af.value, ifm.value)))
				}
			}
		}
	}
}

// coverageOf counts, for each language, how many of each app's strings it
// has: the status table's languages in its order, then any it lacks.
func coverageOf(androidUnits, iosUnits []*unit, languages []language) []coverage {
	var out []coverage
	seen := map[string]bool{}
	add := func(lang, name, status string) {
		if seen[lang] {
			return
		}
		seen[lang] = true
		out = append(out, coverage{lang: lang, name: name, status: status,
			android: countFor(androidUnits, lang), ios: countFor(iosUnits, lang)})
	}
	for _, l := range languages {
		add(l.lang, l.name, l.status)
	}
	var found []string
	for _, u := range slices.Concat(androidUnits, iosUnits) {
		for lang := range u.trans {
			if !seen[lang] && !slices.Contains(found, lang) {
				found = append(found, lang)
			}
		}
	}
	sort.Strings(found)
	for _, lang := range found {
		add(lang, lang, "not listed")
	}
	return out
}

// countFor is how many of units lang has, every form in a state that counts
// for a String Catalog: English has them all.
func countFor(units []*unit, lang string) count {
	var c count
	for _, u := range units {
		if u.stale {
			continue
		}
		c.total++
		if lang == "en" {
			c.done++
			continue
		}
		v, ok := u.trans[lang]
		if !ok {
			continue
		}
		done := true
		for _, f := range v.forms {
			if f.state != "" && f.state != "translated" && f.state != "machine_translated" {
				done = false
			}
		}
		if done {
			c.done++
		}
	}
	return c
}

// checkStatus checks the status table, and for a release, that each language
// that ships has enough of both apps.
func checkStatus(r *report, languages []language, cov []coverage, opts options) {
	at := func(l language, msg string) {
		r.add(finding{severity: errorLevel, rule: ruleStatus, file: statusPath, line: l.line, lang: l.lang, message: msg})
	}
	for _, l := range languages {
		switch {
		case l.status != statusSource && l.status != statusReviewed && l.status != statusDraft:
			at(l, fmt.Sprintf("status %q is not source, reviewed or draft", l.status))
		case (l.lang == "en") != (l.status == statusSource):
			at(l, "English is the source, and the only language that is")
		case l.status == statusReviewed && (l.reviewer == "" || l.reviewedOn == ""):
			at(l, "a reviewed language names who reviewed it and when")
		}
	}
	for _, c := range cov {
		if c.status == "not listed" {
			r.add(finding{severity: errorLevel, rule: ruleStatus, file: statusPath, line: 1, lang: c.lang,
				message: "has translations but no row in status.csv"})
			continue
		}
		if opts.release && c.status != statusSource && (c.android.percent() < releaseCoverage || c.ios.percent() < releaseCoverage) {
			line := 1
			for _, l := range languages {
				if l.lang == c.lang {
					line = l.line
				}
			}
			r.add(finding{severity: errorLevel, rule: ruleRelease, file: statusPath, line: line, lang: c.lang,
				message: fmt.Sprintf("Android has %s, iOS %s: a release needs %.0f%% of each", c.android, c.ios, releaseCoverage)})
		}
	}
}

// describe lists placeholders as a translator reads them: "#1 (text)", or
// "%#@reports@" for a String Catalog substitution.
func describe(placeholders []string) string {
	words := map[string]string{"string": "text", "int": "whole number", "float": "number", "char": "character"}
	out := make([]string, len(placeholders))
	for i, p := range placeholders {
		arg, kind, _ := strings.Cut(p, ":")
		if arg == "plural" {
			out[i] = "%#@" + kind + "@"
			continue
		}
		if w, ok := words[kind]; ok {
			kind = w
		}
		out[i] = "#" + arg + " (" + kind + ")"
	}
	return list(out)
}

func list(items []string) string {
	if len(items) == 0 {
		return "none"
	}
	return strings.Join(items, ", ")
}
