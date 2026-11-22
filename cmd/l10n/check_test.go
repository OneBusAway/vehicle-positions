package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The fixture's files, by their place in the repo.
const (
	androidSource = "android/app/src/main/res/values/strings.xml"
	androidHindi  = "android/app/src/main/res/values-hi/strings.xml"
	iosCatalog    = "ios/App/Localizable.xcstrings"
	glossaryFile  = "docs/translations/glossary.csv"
	statusFile    = "docs/translations/status.csv"
)

// An edit changes the fixture copy rooted at root.
type edit func(t *testing.T, root string)

// replace swaps old, which must occur once in file, for new.
func replace(file, old, new string) edit {
	return func(t *testing.T, root string) {
		t.Helper()
		path := filepath.Join(root, file)
		body, err := os.ReadFile(path)
		require.NoError(t, err)
		require.Equal(t, 1, strings.Count(string(body), old), "%q in %s", old, file)
		require.NoError(t, os.WriteFile(path, []byte(strings.Replace(string(body), old, new, 1)), 0o644))
	}
}

// editCatalog changes the fixture's String Catalog as JSON: fn gets its
// "strings" object.
func editCatalog(fn func(t *testing.T, entries map[string]any)) edit {
	return func(t *testing.T, root string) {
		t.Helper()
		path := filepath.Join(root, iosCatalog)
		body, err := os.ReadFile(path)
		require.NoError(t, err)
		var catalog map[string]any
		require.NoError(t, json.Unmarshal(body, &catalog))
		fn(t, catalog["strings"].(map[string]any))
		out, err := json.MarshalIndent(catalog, "", "  ")
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(path, out, 0o644))
	}
}

// object is m[key] as a JSON object.
func object(t *testing.T, m map[string]any, keys ...string) map[string]any {
	t.Helper()
	for _, k := range keys {
		next, ok := m[k].(map[string]any)
		require.True(t, ok, "no object %q", k)
		m = next
	}
	return m
}

// setState sets the state of key's Hindi string in the catalog.
func setState(key, state string) edit {
	return editCatalog(func(t *testing.T, entries map[string]any) {
		object(t, entries, key, "localizations", "hi", "stringUnit")["state"] = state
	})
}

// vietnamese adds Vietnamese, whose only plural form is "other", to the
// status table.
var vietnamese = replace(statusFile, "hi,Hindi,draft,,,\n", "hi,Hindi,draft,,,\nvi,Vietnamese,draft,,,\n")

// french adds French, whose plural forms are one, many and other on Android
// and one and other on iOS, to the fixture.
var french = replace(statusFile, "hi,Hindi,draft,,,\n", "hi,Hindi,draft,,,\nfr,French,draft,,,\n")

// fixture copies testdata/good to a temporary directory, applies edits and
// returns its root.
func fixture(t *testing.T, edits ...edit) string {
	t.Helper()
	root := t.TempDir()
	err := filepath.WalkDir("testdata/good", func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel("testdata/good", path)
		if d.IsDir() {
			return os.MkdirAll(filepath.Join(root, rel), 0o755)
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(root, rel), body, 0o644)
	})
	require.NoError(t, err)
	for _, e := range edits {
		e(t, root)
	}
	return root
}

func checked(t *testing.T, root string, opts options) *report {
	t.Helper()
	r, err := runCheck(root, opts)
	require.NoError(t, err)
	return r
}

// matching is every finding of r with that severity, rule, key and language.
func matching(r *report, sev severity, rule, key, lang string) []finding {
	var out []finding
	for _, f := range r.findings {
		if f.severity == sev && f.rule == rule && f.key == key && f.lang == lang {
			out = append(out, f)
		}
	}
	return out
}

func allFindings(r *report) string {
	var b strings.Builder
	for _, f := range r.findings {
		b.WriteString(f.String() + "\n")
	}
	return b.String()
}

func TestCheck_GoodFixturePasses(t *testing.T) {
	r := checked(t, fixture(t), options{})
	for _, f := range r.findings {
		assert.Equal(t, warning, f.severity, "only warnings: %s", f)
	}
	// The one string with a placeholder and no note in each app.
	require.Len(t, r.findings, 2, allFindings(r))
	assert.Len(t, matching(r, warning, "note", "started_ago", "en"), 1)
	assert.Len(t, matching(r, warning, "note", "Started %lld min ago", "en"), 1)
}

func TestCheck_Rules(t *testing.T) {
	tests := []struct {
		name  string
		edits []edit
		opts  options
		sev   severity
		rule  string
		key   string
		lang  string
		file  string
	}{
		{"a placeholder of another type (Android)",
			[]edit{replace(androidHindi, "%1$s तक", "%1$d तक")}, options{},
			errorLevel, "placeholders", "login_error_unreachable", "hi", androidHindi},
		{"a placeholder left out (iOS)",
			[]edit{replace(iosCatalog, `"%@ तक नहीं`, `"तक नहीं`)}, options{},
			errorLevel, "placeholders", "Can't reach %@. Check your connection.", "hi", iosCatalog},
		{"a placeholder of another type (iOS)",
			[]edit{replace(iosCatalog, `"%@ तक नहीं`, `"%lld तक नहीं`)}, options{},
			errorLevel, "placeholders", "Can't reach %@. Check your connection.", "hi", iosCatalog},
		{"Android's %s where the iOS English has %@",
			[]edit{replace(iosCatalog, `"%@ तक नहीं`, `"%s तक नहीं`)}, options{},
			errorLevel, "placeholders", "Can't reach %@. Check your connection.", "hi", iosCatalog},
		{"Android's %d where the iOS English has %lld, in a plural form",
			[]edit{replace(iosCatalog, `"%lld रिपोर्ट भेजी गई"`, `"%d रिपोर्ट भेजी गई"`)}, options{},
			errorLevel, "placeholders", "%lld reports sent", "hi", iosCatalog},
		{"a placeholder the English lacks, in a plural form",
			[]edit{replace(androidHindi, "%1$d रिपोर्ट भेजी गई<", "%1$d रिपोर्ट %2$s भेजी गई<")}, options{},
			errorLevel, "placeholders", "reports_sent", "hi", androidHindi},
		{"a plural form the language needs, missing (Android)",
			[]edit{replace(androidHindi, "<item quantity=\"other\">%1$d रिपोर्ट भेजी गईं</item>", "")}, options{},
			errorLevel, "plurals", "reports_sent", "hi", androidHindi},
		{"a plural form the language needs, missing (iOS)",
			[]edit{editCatalog(func(t *testing.T, entries map[string]any) {
				delete(object(t, entries, "%lld reports sent", "localizations", "hi", "variations", "plural"), "one")
			})}, options{},
			errorLevel, "plurals", "%lld reports sent", "hi", iosCatalog},
		{"an iOS translation written as a single string where the English varies by plural",
			[]edit{editCatalog(func(t *testing.T, entries map[string]any) {
				object(t, entries, "%lld reports sent", "localizations")["hi"] = map[string]any{
					"stringUnit": map[string]any{"state": "translated", "value": "%lld रिपोर्ट भेजी गईं"},
				}
			})}, options{},
			errorLevel, "plurals", "%lld reports sent", "hi", iosCatalog},
		{"an Android translation written as a <string> where the English is <plurals>",
			[]edit{replace(androidHindi, `<plurals name="reports_sent">
        <item quantity="one">%1$d रिपोर्ट भेजी गई</item>
        <item quantity="other">%1$d रिपोर्ट भेजी गईं</item>
    </plurals>`, `<string name="reports_sent">%1$d रिपोर्ट भेजी गईं</string>`)}, options{},
			errorLevel, "plurals", "reports_sent", "hi", androidHindi},
		{"an Android <string> in place of <plurals>, even in a language with no plural forms but other",
			[]edit{vietnamese, func(t *testing.T, root string) {
				dir := filepath.Join(root, androidRes, "values-vi")
				require.NoError(t, os.MkdirAll(dir, 0o755))
				require.NoError(t, os.WriteFile(filepath.Join(dir, "strings.xml"), []byte(`<?xml version="1.0" encoding="utf-8"?>
<resources>
    <string name="reports_sent">Đã gửi %1$d báo cáo</string>
</resources>
`), 0o644))
			}}, options{},
			errorLevel, "plurals", "reports_sent", "vi", "android/app/src/main/res/values-vi/strings.xml"},
		{"many missing from an Android plural in a language whose iOS strings need one and other alone",
			[]edit{french, func(t *testing.T, root string) {
				dir := filepath.Join(root, androidRes, "values-fr")
				require.NoError(t, os.MkdirAll(dir, 0o755))
				require.NoError(t, os.WriteFile(filepath.Join(dir, "strings.xml"), []byte(`<?xml version="1.0" encoding="utf-8"?>
<resources>
    <plurals name="reports_sent">
        <item quantity="one">%1$d rapport envoyé</item>
        <item quantity="other">%1$d rapports envoyés</item>
    </plurals>
</resources>
`), 0o644))
			}}, options{},
			errorLevel, "plurals", "reports_sent", "fr", "android/app/src/main/res/values-fr/strings.xml"},
		{"a single string without the count, in a language with no plural forms but other",
			[]edit{vietnamese, editCatalog(func(t *testing.T, entries map[string]any) {
				object(t, entries, "%lld reports sent", "localizations")["vi"] = map[string]any{
					"stringUnit": map[string]any{"state": "translated", "value": "Đã gửi báo cáo"},
				}
			})}, options{},
			errorLevel, "placeholders", "%lld reports sent", "vi", iosCatalog},
		{"a plural form missing in a substitution",
			[]edit{editCatalog(func(t *testing.T, entries map[string]any) {
				delete(object(t, entries, "Route %@, %lld reports sent", "localizations", "hi", "substitutions", "reports", "variations", "plural"), "other")
			})}, options{},
			errorLevel, "plurals", "Route %@, %lld reports sent", "hi", iosCatalog},
		{"a number written as a word",
			[]edit{replace(androidHindi, "24 घंटे", "चौबीस घंटे")}, options{},
			errorLevel, "numbers", "help_link", "hi", androidHindi},
		{"a URL changed",
			[]edit{replace(androidHindi, "https://onebusaway.org", "https://onebusaway.example")}, options{},
			errorLevel, "urls", "help_link", "hi", androidHindi},
		{"a do-not-translate term translated",
			[]edit{replace(androidHindi, "GPS बंद है", "जीपीएस बंद है")}, options{},
			errorLevel, "do-not-translate", "tracking_gps", "hi", androidHindi},
		{"bus in the English source (Android)",
			[]edit{replace(androidSource, ">Sign in<", ">Board the bus<")}, options{},
			errorLevel, "forbidden", "login_title", "en", androidSource},
		{"bus in the English source (iOS)",
			[]edit{editCatalog(func(t *testing.T, entries map[string]any) {
				entries["Wait for the next bus"] = map[string]any{"comment": "The stop's board."}
			})}, options{},
			errorLevel, "forbidden", "Wait for the next bus", "en", iosCatalog},
		{"a forbidden glossary word in a translation",
			[]edit{replace(androidHindi, "गाड़ी रूट", "बस रूट")}, options{},
			errorLevel, "forbidden", "vehicle_route_stop", "hi", androidHindi},
		{"needs review",
			[]edit{setState("Sign in", "needs_review")}, options{},
			errorLevel, "ios-state", "Sign in", "hi", iosCatalog},
		{"stale",
			[]edit{editCatalog(func(t *testing.T, entries map[string]any) {
				object(t, entries, "Sign in")["extractionState"] = "stale"
			})}, options{},
			errorLevel, "ios-state", "Sign in", "en", iosCatalog},
		{"a machine translation in a reviewed language",
			[]edit{setState("Sign in", "machine_translated"), replace(statusFile, "hi,Hindi,draft,,,", "hi,Hindi,reviewed,A. Reader,2026-11-20,")}, options{},
			errorLevel, "ios-state", "Sign in", "hi", iosCatalog},
		{"a language with no status row",
			[]edit{replace(statusFile, "hi,Hindi,draft,,,\n", "")}, options{},
			errorLevel, "status", "", "hi", statusFile},
		{"a status that isn't one of source, reviewed or draft",
			[]edit{replace(statusFile, "hi,Hindi,draft,,,", "hi,Hindi,done,,,")}, options{},
			errorLevel, "status", "", "hi", statusFile},
		{"reviewed with no reviewer",
			[]edit{replace(statusFile, "hi,Hindi,draft,,,", "hi,Hindi,reviewed,,2026-11-20,")}, options{},
			errorLevel, "status", "", "hi", statusFile},
		{"a release with a language under 95%",
			nil, options{release: true},
			errorLevel, "release-coverage", "", "hi", statusFile},
		{"the two apps translating the same English differently",
			[]edit{replace(iosCatalog, `"value" : "साइन इन करें"`, `"value" : "लॉग इन करें"`)}, options{},
			warning, "consistency", "Sign in", "hi", iosCatalog},
		{"an iOS translation none of the Android strings with its English says",
			[]edit{
				replace(androidSource, `<string name="login_title">Sign in</string>`, "<string name=\"login_title\">Sign in</string>\n    <string name=\"account_sign_in\">Sign in</string>"),
				replace(androidHindi, `<string name="login_title">साइन इन करें</string>`, "<string name=\"login_title\">साइन इन करें</string>\n    <string name=\"account_sign_in\">लॉग इन करें</string>"),
				replace(iosCatalog, `"value" : "साइन इन करें"`, `"value" : "लॉगिन करें"`),
			}, options{},
			warning, "consistency", "Sign in", "hi", iosCatalog},
		{"the same English across a \\u escape, translated differently",
			[]edit{
				replace(androidSource, ">Sign in<", `>Sign in\u2026<`),
				replace(iosCatalog, `"value" : "साइन इन करें"`, `"value" : "लॉग इन करें"`),
				editCatalog(func(t *testing.T, entries map[string]any) {
					entries["Sign in…"] = object(t, entries, "Sign in")
					delete(entries, "Sign in")
				}),
			}, options{},
			warning, "consistency", "Sign in…", "hi", iosCatalog},
		{"the same English across an escaped apostrophe and a placeholder, translated differently",
			[]edit{replace(iosCatalog, "%@ तक नहीं पहुँच सके", "%@ तक नहीं पहुंच पाए")}, options{},
			warning, "consistency", "Can't reach %@. Check your connection.", "hi", iosCatalog},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			root := fixture(t, tc.edits...)
			r := checked(t, root, tc.opts)
			got := matching(r, tc.sev, tc.rule, tc.key, tc.lang)
			require.Len(t, got, 1, allFindings(r))
			assert.Equal(t, tc.file, got[0].file)
			switch tc.file {
			case androidSource, androidHindi:
				assert.Contains(t, lineOf(t, root, got[0]), `name="`+tc.key+`"`)
			case iosCatalog:
				key, _ := json.Marshal(tc.key)
				assert.Contains(t, lineOf(t, root, got[0]), string(key))
			case statusFile:
				if tc.rule != "status" || tc.name != "a language with no status row" {
					assert.Contains(t, lineOf(t, root, got[0]), tc.lang+",")
				}
			}
		})
	}
}

// TestCheck_AnIOSKeyMayTakeAnyAndroidWordingOfItsEnglish: where Android
// words one English apart on different screens, an iOS key for one of those
// screens may take that screen's wording, not only the last one.
func TestCheck_AnIOSKeyMayTakeAnyAndroidWordingOfItsEnglish(t *testing.T) {
	r := checked(t, fixture(t,
		replace(androidSource, `<string name="login_title">Sign in</string>`, "<string name=\"login_title\">Sign in</string>\n    <string name=\"account_sign_in\">Sign in</string>"),
		replace(androidHindi, `<string name="login_title">साइन इन करें</string>`, "<string name=\"login_title\">साइन इन करें</string>\n    <string name=\"account_sign_in\">लॉग इन करें</string>"),
	), options{})
	assert.Empty(t, matching(r, warning, "consistency", "Sign in", "hi"), allFindings(r))
}

// lineOf is the line of the fixture file f points at.
func lineOf(t *testing.T, root string, f finding) string {
	t.Helper()
	body, err := os.ReadFile(filepath.Join(root, f.file))
	require.NoError(t, err)
	lines := strings.Split(string(body), "\n")
	require.True(t, f.line >= 1 && f.line <= len(lines), "line %d of %s", f.line, f.file)
	return lines[f.line-1]
}

// TestCheck_RulesDoNotOverreach: what each rule must let through.
func TestCheck_RulesDoNotOverreach(t *testing.T) {
	tests := []struct {
		name  string
		edits []edit
	}{
		{"a number in the language's own digits", []edit{replace(androidHindi, "24 घंटे", "२४ घंटे")}},
		{"a forbidden word in a string whose English doesn't use the term",
			[]edit{replace(androidHindi, ">साइन इन करें<", ">बस साइन इन करें<"), replace(iosCatalog, `"value" : "साइन इन करें"`, `"value" : "बस साइन इन करें"`)}},
		{"a machine translation in a draft language", []edit{setState("Sign in", "machine_translated")}},
		{"placeholders reordered by position",
			[]edit{replace(androidHindi, "गाड़ी रूट %1$s पर, %2$s की ओर", "%2$s की ओर, गाड़ी रूट %1$s पर")}},
		{"a real character where the English writes it as a \\u escape", []edit{
			replace(androidSource, ">Sign in<", `>Starting\u2026 \u2014 %1$s \u2192 %2$s<`),
			replace(androidHindi, ">साइन इन करें<", ">शुरू हो रहा है… — %1$s → %2$s<"),
		}},
		{"a single string in a language with no plural forms but other (iOS)",
			[]edit{vietnamese, editCatalog(func(t *testing.T, entries map[string]any) {
				object(t, entries, "%lld reports sent", "localizations")["vi"] = map[string]any{
					"stringUnit": map[string]any{"state": "translated", "value": "Đã gửi %lld báo cáo"},
				}
			})}},
		{"an iOS plural with one and other alone, as Xcode offers French",
			[]edit{french, editCatalog(func(t *testing.T, entries map[string]any) {
				object(t, entries, "%lld reports sent", "localizations")["fr"] = map[string]any{
					"variations": map[string]any{"plural": map[string]any{
						"one":   map[string]any{"stringUnit": map[string]any{"state": "translated", "value": "%lld rapport envoyé"}},
						"other": map[string]any{"stringUnit": map[string]any{"state": "translated", "value": "%lld rapports envoyés"}},
					}},
				}
			})}},
		{"an iOS placeholder given a position the English doesn't write",
			[]edit{replace(iosCatalog, `"%@ तक नहीं`, `"%1$@ तक नहीं`)}},
		{"a plural form without its count", []edit{replace(androidHindi, ">%1$d रिपोर्ट भेजी गई<", ">एक रिपोर्ट भेजी गई<")}},
		{"a word with a forbidden word inside it", []edit{replace(androidSource, ">Sign in<", ">The server is busy<")}},
		{"a catalog in a build folder", []edit{func(t *testing.T, root string) {
			dir := filepath.Join(root, "ios", "Kit", ".build", "checkouts")
			require.NoError(t, os.MkdirAll(dir, 0o755))
			require.NoError(t, os.WriteFile(filepath.Join(dir, "Other.xcstrings"),
				[]byte(`{"sourceLanguage":"en","strings":{"Wait for the bus":{}},"version":"1.0"}`), 0o644))
		}}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := checked(t, fixture(t, tc.edits...), options{})
			for _, f := range r.findings {
				assert.NotEqual(t, errorLevel, f.severity, "%s", f)
			}
		})
	}
}

func TestCheck_Coverage(t *testing.T) {
	r := checked(t, fixture(t), options{})
	byLang := map[string]coverage{}
	for _, c := range r.coverage {
		byLang[c.lang] = c
	}
	require.Contains(t, byLang, "hi")
	assert.Equal(t, coverage{lang: "hi", name: "Hindi", status: "draft", android: count{6, 7}, ios: count{4, 5}}, byLang["hi"])
	assert.Equal(t, count{7, 7}, byLang["en"].android)
	assert.Equal(t, count{5, 5}, byLang["en"].ios)
}

// TestCheck_CoverageCountsFinishedStrings: a String Catalog string counts
// once it is translated, by a person or as a machine draft, and not while it
// needs review.
func TestCheck_CoverageCountsFinishedStrings(t *testing.T) {
	for state, want := range map[string]count{"needs_review": {3, 5}, "new": {3, 5}, "machine_translated": {4, 5}} {
		r := checked(t, fixture(t, setState("Sign in", state)), options{})
		for _, c := range r.coverage {
			if c.lang == "hi" {
				assert.Equal(t, want, c.ios, state)
			}
		}
	}
}

func TestAnnotationEscape(t *testing.T) {
	assert.Equal(t, "says %251$d%0Anext", annotationEscape("says %1$d\nnext"))
}

// TestReportWrite_OneCommandPerFinding: a key, file or message holding a
// newline, a colon or a comma cannot start a workflow command of its own or
// end the annotation's properties early.
func TestReportWrite_OneCommandPerFinding(t *testing.T) {
	r := &report{
		findings: []finding{{
			severity: errorLevel, rule: rulePlurals, file: "ios/a,b:c.xcstrings", line: 3,
			key: "Line one\r\n::error::from the key", lang: "hi", message: "50% done\n::warning::from the message",
		}},
		coverage: []coverage{{lang: "hi", name: "Hindi\n::error::from the status table", status: "draft", android: count{1, 2}}},
	}
	var out bytes.Buffer
	r.write(&out, true)
	lines := strings.Split(strings.TrimSuffix(out.String(), "\n"), "\n")
	require.Len(t, lines, 4, out.String())
	assert.Equal(t, `ios/a,b:c.xcstrings:3: error [plurals] Line one\r\n::error::from the key (hi): 50% done\n::warning::from the message`, lines[0])
	assert.Equal(t, "::error file=ios/a%2Cb%3Ac.xcstrings,line=3,title=l10n plurals::50%25 done%0A::warning::from the message", lines[1])
	assert.Equal(t, `Hindi\n::error::from the status table (hi) draft: Android 1 of 2, iOS 0 of 0`, lines[2])
	assert.Equal(t, "1 errors, 0 warnings", lines[3])
}

// TestRun: the command's exit code, its lines, and what it writes for GitHub
// Actions.
func TestRun(t *testing.T) {
	summary := filepath.Join(t.TempDir(), "summary.md")

	var out, errOut bytes.Buffer
	assert.Equal(t, 0, run([]string{"check", "-root", fixture(t), "-annotate", "-summary", summary}, &out, &errOut), errOut.String())
	assert.Contains(t, out.String(), androidSource+":")
	assert.Contains(t, out.String(), "warning [note] started_ago")
	assert.Contains(t, out.String(), "::warning file="+androidSource+",line=")
	body, err := os.ReadFile(summary)
	require.NoError(t, err)
	assert.Contains(t, string(body), "| Hindi (hi) | draft | 6 of 7 | 4 of 5 |")

	out.Reset()
	bad := fixture(t, replace(androidHindi, "%1$s तक", "%1$d तक"))
	assert.Equal(t, 1, run([]string{"check", "-root", bad, "-annotate"}, &out, &errOut))
	assert.Contains(t, out.String(), "error [placeholders] login_error_unreachable (hi)")
	assert.Contains(t, out.String(), "::error file="+androidHindi+",line=")

	out.Reset()
	assert.Equal(t, 1, run([]string{"check", "-root", fixture(t), "-release"}, &out, &errOut), "Hindi is under 95%")
	assert.NotContains(t, out.String(), "::", "annotations only with -annotate")
	assert.Equal(t, 2, run([]string{"lint"}, &out, &errOut), "an unknown command")
}

// TestReadAndroid_RepoEscapesUndone: each of the app's own strings.xml files
// reads the same as it would with every \\u escape written as its character,
// and no \\u is left in any string.
func TestReadAndroid_RepoEscapesUndone(t *testing.T) {
	files, err := filepath.Glob(filepath.Join("../..", androidRes, "values*", "strings.xml"))
	require.NoError(t, err)
	require.NotEmpty(t, files)
	escape := regexp.MustCompile(`\\u([0-9a-fA-F]{4})`)
	for _, file := range files {
		body, err := os.ReadFile(file)
		require.NoError(t, err)
		written := escape.ReplaceAllStringFunc(string(body), func(m string) string {
			code, err := strconv.ParseUint(m[2:], 16, 16)
			require.NoError(t, err)
			return string(rune(code))
		})
		plain := filepath.Join(t.TempDir(), "strings.xml")
		require.NoError(t, os.WriteFile(plain, []byte(written), 0o644))

		got, err := readStringsXML("../..", file)
		require.NoError(t, err)
		want, err := readStringsXML("../..", plain)
		require.NoError(t, err)
		require.Len(t, got, len(want), file)
		for i := range got {
			for path, f := range got[i].version.forms {
				assert.NotContains(t, f.value, `\u`, "%s %s %s", file, got[i].key, path)
				assert.Equal(t, want[i].version.forms[path].value, f.value, "%s %s %s", file, got[i].key, path)
			}
		}
	}
}

// TestCheck_Repo: the repo's own strings pass, so the check can run on every
// pull request from the day it lands.
func TestCheck_Repo(t *testing.T) {
	r := checked(t, "../..", options{})
	for _, f := range r.findings {
		assert.NotEqual(t, errorLevel, f.severity, "%s", f)
	}
	assert.NotEmpty(t, r.coverage)
}
