package main

import (
	"bytes"
	"encoding/csv"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The XLIFF Xcode exports for Hindi from the fixture's String Catalog, the
// matches folder, and the expected files.
const (
	xliffFixture  = "testdata/xliff/hi.xliff"
	matchesFolder = "testdata/onebusaway"
	wantSheet     = "testdata/sheet/hi.csv"
	wantStrings   = "testdata/sheet/values-hi-strings.xml"
	wantXLIFF     = "testdata/sheet/hi-imported.xliff"
)

// A sheetProject is a copy of the fixture app with a copy of its Hindi XLIFF,
// for export and import to change.
type sheetProject struct {
	root, xliff, dir string
}

func newSheetProject(t *testing.T, edits ...edit) sheetProject {
	t.Helper()
	p := sheetProject{root: fixture(t, edits...), dir: t.TempDir()}
	p.xliff = filepath.Join(p.dir, "hi.xliff")
	body, err := os.ReadFile(xliffFixture)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(p.xliff, body, 0o644))
	return p
}

func (p sheetProject) read(t *testing.T, rel string) string {
	t.Helper()
	body, err := os.ReadFile(filepath.Join(p.root, rel))
	require.NoError(t, err)
	return string(body)
}

func (p sheetProject) xliffText(t *testing.T) string {
	t.Helper()
	body, err := os.ReadFile(p.xliff)
	require.NoError(t, err)
	return string(body)
}

// export runs the export command and returns the sheet's path.
func (p sheetProject) export(t *testing.T, lang string, extra ...string) string {
	t.Helper()
	out := filepath.Join(p.dir, lang+".csv")
	args := append([]string{"export", "-root", p.root, "-o", out}, extra...)
	var stdout, stderr bytes.Buffer
	require.Equal(t, 0, run(append(args, lang), &stdout, &stderr), stderr.String())
	return out
}

// importSheet runs the import command and returns its exit code and output.
func (p sheetProject) importSheet(t *testing.T, lang, sheet string, extra ...string) (int, string) {
	t.Helper()
	var out bytes.Buffer
	code := run(append(append([]string{"import", "-root", p.root}, extra...), lang, sheet), &out, &out)
	return code, out.String()
}

func readRows(t *testing.T, sheet string) [][]string {
	t.Helper()
	f, err := os.Open(sheet)
	require.NoError(t, err)
	defer f.Close()
	rows, err := csv.NewReader(f).ReadAll()
	require.NoError(t, err)
	rows[0][0] = strings.TrimPrefix(rows[0][0], "\ufeff")
	require.Equal(t, sheetColumns, rows[0])
	return rows[1:]
}

func cell(row []string, column string) string {
	for i, c := range sheetColumns {
		if c == column {
			return row[i]
		}
	}
	panic("no column " + column)
}

// translate fills the translation column of each row whose English is a key
// of texts, and fails the test for an English no row has.
func translate(t *testing.T, sheet string, texts map[string]string) {
	t.Helper()
	f, err := os.Open(sheet)
	require.NoError(t, err)
	rows, err := csv.NewReader(f).ReadAll()
	f.Close()
	require.NoError(t, err)
	used := map[string]bool{}
	for _, row := range rows[1:] {
		if text, ok := texts[cell(row, "english")]; ok {
			row[len(sheetColumns)-1] = text
			used[cell(row, "english")] = true
		}
	}
	for english := range texts {
		require.True(t, used[english], "no row says %q", english)
	}
	var b bytes.Buffer
	w := csv.NewWriter(&b)
	require.NoError(t, w.WriteAll(rows))
	require.NoError(t, os.WriteFile(sheet, b.Bytes(), 0o644))
}

func TestExport_FixtureAppGivesTheExpectedSheet(t *testing.T) {
	p := newSheetProject(t)
	sheet := p.export(t, "hi", "-xliff", p.xliff, "-matches", matchesFolder)

	got, err := os.ReadFile(sheet)
	require.NoError(t, err)
	want, err := os.ReadFile(wantSheet)
	require.NoError(t, err)
	assert.Equal(t, string(want), string(got))
}

func TestExport_StringsBothAppsShareAppearOnce(t *testing.T) {
	p := newSheetProject(t)
	rows := readRows(t, p.export(t, "hi", "-xliff", p.xliff))

	byEnglish := map[string][][]string{}
	for _, row := range rows {
		byEnglish[cell(row, "english")] = append(byEnglish[cell(row, "english")], row)
	}
	for english, want := range map[string][2]string{
		"Sign in": {"login_title", "Sign in"},
		"Can't reach %1$s. Check your connection.": {"login_error_unreachable", "Can't reach %@. Check your connection."},
		"%1$d report sent":                         {"reports_sent", "%lld reports sent|==|plural.one"},
		"%1$d reports sent":                        {"reports_sent", "%lld reports sent|==|plural.other"},
		"Started %1$d min ago":                     {"started_ago", "Started %lld min ago"},
	} {
		require.Len(t, byEnglish[english], 1, english)
		assert.Equal(t, want[0], cell(byEnglish[english][0], "android"), english)
		assert.Equal(t, want[1], cell(byEnglish[english][0], "ios"), english)
	}
	// One app alone has these.
	assert.Empty(t, cell(byEnglish["GPS is off"][0], "ios"))
	assert.Empty(t, cell(byEnglish["Route %1$@, %2$#@reports@"][0], "android"), "Android writes no string from a substitution")
	assert.Empty(t, cell(byEnglish["OBA Vehicle Tracker"][0], "android"))
	for _, row := range rows {
		assert.Empty(t, cell(row, "translation"), "the translation column starts empty")
	}
}

// editXLIFF swaps old, which must occur once in the project's XLIFF, for new.
func (p sheetProject) editXLIFF(t *testing.T, old, new string) {
	t.Helper()
	body := p.xliffText(t)
	require.Equal(t, 1, strings.Count(body, old), old)
	require.NoError(t, os.WriteFile(p.xliff, []byte(strings.Replace(body, old, new, 1)), 0o644))
}

func TestExport_AnIOSStringKeepsItsOwnRowUnlessItMatchesAndroidsWhole(t *testing.T) {
	startedUnit := `<trans-unit id="Started %lld min ago" xml:space="preserve">
        <source>Started %lld min ago</source>
        <note/>
      </trans-unit>`
	tests := []struct {
		name, android, ios string
		edit               func(t *testing.T, p sheetProject)
		edits              []edit
	}{
		{"the apps translate it differently", "login_title", "Sign in",
			func(t *testing.T, p sheetProject) {
				p.editXLIFF(t, `<target state="translated">साइन इन करें</target>`, `<target state="translated">लॉग इन करें</target>`)
			}, nil},
		{"its values are of other types", "started_ago", "Started %lld min ago",
			func(t *testing.T, p sheetProject) {
				p.editXLIFF(t, "<source>Started %lld min ago</source>", "<source>Started %@ min ago</source>")
			}, nil},
		{"one app has a plural and the other doesn't", "started_ago", "Started %lld min ago|==|plural.other",
			func(t *testing.T, p sheetProject) {
				p.editXLIFF(t, startedUnit, strings.ReplaceAll(startedUnit, `id="Started %lld min ago"`, `id="Started %lld min ago|==|plural.one"`)+
					"\n      "+strings.ReplaceAll(startedUnit, `id="Started %lld min ago"`, `id="Started %lld min ago|==|plural.other"`))
			}, nil},
		{"two Android strings say it", "login_title", "Sign in",
			func(t *testing.T, p sheetProject) {},
			[]edit{replace(androidSource, `<string name="login_title">Sign in</string>`, `<string name="login_title">Sign in</string>
    <string name="login_button">Sign in</string>`)}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			p := newSheetProject(t, tc.edits...)
			tc.edit(t, p)

			rows := readRows(t, p.export(t, "hi", "-xliff", p.xliff))

			ownRow := false
			for _, row := range rows {
				if cell(row, "android") == tc.android {
					assert.Empty(t, cell(row, "ios"), "%s shares no row", tc.android)
				}
				ownRow = ownRow || (cell(row, "android") == "" && cell(row, "ios") == tc.ios)
			}
			assert.True(t, ownRow, "%q has a row of its own", tc.ios)
		})
	}
}

func TestExport_APluralHasARowForEachFormTheLanguageNeeds(t *testing.T) {
	p := newSheetProject(t)
	var forms []string
	for _, row := range readRows(t, p.export(t, "pl")) {
		if cell(row, "android") == "reports_sent" {
			forms = append(forms, cell(row, "form")+"="+cell(row, "english"))
		}
	}
	// English has no few or many: their rows show its "other".
	assert.Equal(t, []string{"one=%1$d report sent", "few=%1$d reports sent", "many=%1$d reports sent", "other=%1$d reports sent"}, forms)
}

func TestImport_WritesTheExpectedStringsXMLAndXLIFF(t *testing.T) {
	p := newSheetProject(t)
	sheet := p.export(t, "hi", "-xliff", p.xliff)
	translate(t, sheet, map[string]string{
		"Started %1$d min ago":                     "%1$d मिनट पहले शुरू",     // both apps, new to each
		"Can't reach %1$s. Check your connection.": "%1$s तक नहीं पहुँच सके।", // both apps, corrected
		"GPS is off":          "GPS बंद",             // Android alone
		"OBA Vehicle Tracker": "OBA Vehicle Tracker", // iOS alone
	})

	code, out := p.importSheet(t, "hi", sheet, "-xliff", p.xliff)
	require.Equal(t, 0, code, out)

	want, err := os.ReadFile(wantStrings)
	require.NoError(t, err)
	assert.Equal(t, string(want), p.read(t, androidHindi))
	want, err = os.ReadFile(wantXLIFF)
	require.NoError(t, err)
	assert.Equal(t, string(want), p.xliffText(t))
}

func TestImport_ASheetReadBackUnchangedChangesNothing(t *testing.T) {
	p := newSheetProject(t)
	p.editXLIFF(t, `<target state="translated">रूट %1$@, %2$#@reports@</target>`, `<target state="needs-review-l10n">रूट %1$@, %2$#@reports@</target>`)
	strings0, xliff0 := p.read(t, androidHindi), p.xliffText(t)

	code, out := p.importSheet(t, "hi", p.export(t, "hi", "-xliff", p.xliff, "-matches", matchesFolder), "-xliff", p.xliff)

	require.Equal(t, 0, code, out)
	assert.Contains(t, out, "nothing to change")
	assert.Equal(t, strings0, p.read(t, androidHindi))
	assert.Equal(t, xliff0, p.xliffText(t), "a translation left as it is keeps its state too")

	// A language with nothing translated yet gets no file, though another
	// OneBusAway app has none of it either.
	code, out = p.importSheet(t, "sw", p.export(t, "sw", "-matches", matchesFolder))
	require.Equal(t, 0, code, out)
	assert.NoFileExists(t, filepath.Join(p.root, "android/app/src/main/res/values-sw/strings.xml"))
}

func TestImport_RefusesASheetThatBreaksTheCheck(t *testing.T) {
	tests := []struct {
		name, lang string
		texts      map[string]string
		want       []string
	}{
		{"a placeholder left out, in a row for both apps", "hi",
			map[string]string{"Can't reach %1$s. Check your connection.": "सर्वर तक नहीं पहुँच सके।"},
			[]string{"[placeholders] login_error_unreachable (hi): placeholders #1 (text) missing",
				"[placeholders] Can't reach %@. Check your connection. (hi): placeholders #1 (text) missing"}},
		{"iOS's placeholder in a row Android writes too", "hi",
			map[string]string{"Started %1$d min ago": "%@ मिनट पहले"},
			[]string{"[placeholders] started_ago (hi): placeholders #1 (whole number) missing"}},
		{"a substitution left out", "hi",
			map[string]string{"Route %1$@, %2$#@reports@": "रूट %1$@"},
			[]string{"[placeholders] Route %@, %lld reports sent (hi): placeholders %#@reports@ missing"}},
		{"plural forms the language needs left empty", "pl",
			map[string]string{"%1$d report sent": "%1$d raport wysłany"},
			[]string{"[plurals] reports_sent (pl): plural forms few, many, other missing"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			p := newSheetProject(t)
			strings0, xliff0 := p.read(t, androidHindi), p.xliffText(t)
			var extra []string
			if tc.lang == "hi" {
				extra = []string{"-xliff", p.xliff}
			}
			sheet := p.export(t, tc.lang, extra...)
			translate(t, sheet, tc.texts)

			code, out := p.importSheet(t, tc.lang, sheet, extra...)

			assert.Equal(t, 1, code, out)
			for _, w := range tc.want {
				assert.Contains(t, out, w)
			}
			assert.Contains(t, out, filepath.Base(sheet)+":", "it names the sheet's row")
			assert.Contains(t, out, "Nothing was written.")
			assert.Equal(t, strings0, p.read(t, androidHindi))
			assert.Equal(t, xliff0, p.xliffText(t))
			assert.NoFileExists(t, filepath.Join(p.root, "android/app/src/main/res/values-pl/strings.xml"))
		})
	}
}

func TestImport_ARowThatNamesNoStringIsAnError(t *testing.T) {
	tests := []struct{ row, want string }{
		{"login_titel,,,,साइन इन", `hi.csv:2: no Android string is named "login_titel"`},
		{"tracking_gps,,one,,GPS बंद", `hi.csv:2: tracking_gps: has no plural, but the row's form is "one"`},
		{"reports_sent,,few,,%1$d रिपोर्ट", `hi.csv:2: reports_sent: form "few" is not one of one, other`},
		{",Sgin in,,,साइन इन", `has no string "Sgin in"`},
	}
	for _, tc := range tests {
		t.Run(tc.want, func(t *testing.T) {
			p := newSheetProject(t)
			sheet := filepath.Join(p.dir, "hi.csv")
			require.NoError(t, os.WriteFile(sheet, []byte("android,ios,form,current,translation\n"+tc.row+"\n"), 0o644))

			code, out := p.importSheet(t, "hi", sheet, "-xliff", p.xliff)

			assert.Equal(t, 2, code)
			assert.Contains(t, out, tc.want)
		})
	}
}

func TestExport_AnXLIFFWithTheSameIDTwiceIsAnError(t *testing.T) {
	p := newSheetProject(t)
	// Which of the two a row writes would be a guess.
	p.editXLIFF(t, `<trans-unit id="CFBundleDisplayName"`, `<trans-unit id="Sign in"`)
	var stdout, stderr bytes.Buffer

	code := run([]string{"export", "-root", p.root, "-xliff", p.xliff, "-o", filepath.Join(p.dir, "hi.csv"), "hi"}, &stdout, &stderr)

	assert.Equal(t, 2, code)
	assert.Contains(t, stderr.String(), `two strings have the id "Sign in"`)
}

func TestImport_AnEditToTheCurrentCellIsWrittenToo(t *testing.T) {
	p := newSheetProject(t)
	sheet := p.export(t, "hi", "-xliff", p.xliff)
	body, err := os.ReadFile(sheet)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(sheet, []byte(strings.Replace(string(body), "GPS बंद है", "GPS बंद", 1)), 0o644))

	code, out := p.importSheet(t, "hi", sheet, "-xliff", p.xliff)

	require.Equal(t, 0, code, out)
	assert.Contains(t, p.read(t, androidHindi), `<string name="tracking_gps">GPS बंद</string>`)
}

// TestImport_ADraftMarksWhatTheSheetFillsIn: with -draft, the iOS strings a
// sheet fills in get the qualifier Xcode imports as machine_translated, and
// nothing else in the XLIFF changes.
func TestImport_ADraftMarksWhatTheSheetFillsIn(t *testing.T) {
	p := newSheetProject(t)
	xliff0 := p.xliffText(t)
	sheet := p.export(t, "hi", "-xliff", p.xliff)
	translate(t, sheet, map[string]string{"Sign in": "साइन इन करो"})

	code, out := p.importSheet(t, "hi", sheet, "-xliff", p.xliff, "-draft")

	require.Equal(t, 0, code, out)
	want := strings.Replace(xliff0, `<target state="translated">साइन इन करें</target>`,
		`<target state="translated" state-qualifier="leveraged-mt">साइन इन करो</target>`, 1)
	assert.Equal(t, want, p.xliffText(t))
}

// TestImport_ADraftCopiedIntoTheTranslationIsRead: a reviewer marks a draft
// read by copying it into the translation column, and a draft the sheet
// leaves empty stays a draft.
func TestImport_ADraftCopiedIntoTheTranslationIsRead(t *testing.T) {
	p := newSheetProject(t)
	signIn, sent := "साइन इन करें", "%lld रिपोर्ट भेजी गईं"
	p.editXLIFF(t, `<target state="translated">`+signIn+`</target>`, `<target state="translated" state-qualifier="leveraged-mt">`+signIn+`</target>`)
	p.editXLIFF(t, `<target state="translated">`+sent+`</target>`, `<target state="translated" state-qualifier="leveraged-mt">`+sent+`</target>`)
	sheet := p.export(t, "hi", "-xliff", p.xliff)
	translate(t, sheet, map[string]string{"Sign in": signIn})

	code, out := p.importSheet(t, "hi", sheet, "-xliff", p.xliff)

	require.Equal(t, 0, code, out)
	xliff := p.xliffText(t)
	assert.Contains(t, xliff, `<target state="translated">`+signIn+`</target>`)
	assert.Contains(t, xliff, `<target state="translated" state-qualifier="leveraged-mt">`+sent+`</target>`)
}

// TestImport_AnUnfilledSharedRowLeavesTheIOSDraft: a row both apps share
// shows Android's translation as its current text. Left empty, it doesn't
// change the iOS string, even a draft since worded apart from Android: that
// keeps its text and its state (#552).
func TestImport_AnUnfilledSharedRowLeavesTheIOSDraft(t *testing.T) {
	p := newSheetProject(t)
	sheet := p.export(t, "hi", "-xliff", p.xliff)
	shared := false
	for _, row := range readRows(t, sheet) {
		shared = shared || (cell(row, "android") == "login_title" && cell(row, "ios") == "Sign in")
	}
	require.True(t, shared, "Sign in has one row for both apps")
	draft := `<target state="translated" state-qualifier="leveraged-mt">साइन इन करो</target>`
	p.editXLIFF(t, `<target state="translated">साइन इन करें</target>`, draft)

	code, out := p.importSheet(t, "hi", sheet, "-xliff", p.xliff)

	require.Equal(t, 0, code, out)
	assert.Contains(t, p.xliffText(t), draft)
}

func TestImport_ReadsASheetASpreadsheetSaved(t *testing.T) {
	p := newSheetProject(t)
	sheet := filepath.Join(p.dir, "hi.csv")
	// Excel's "CSV UTF-8" starts with a byte order mark, and a spreadsheet may
	// drop the columns no one filled in.
	require.NoError(t, os.WriteFile(sheet, []byte("\ufefftranslation,android,ios,form,current\nGPS बंद,tracking_gps,,,GPS बंद है\n"), 0o644))

	code, out := p.importSheet(t, "hi", sheet)

	require.Equal(t, 0, code, out)
	assert.Contains(t, p.read(t, androidHindi), `<string name="tracking_gps">GPS बंद</string>`)

	require.NoError(t, os.WriteFile(sheet, []byte("android,ios,form,current\ntracking_gps,,,GPS बंद है\n"), 0o644))
	code, out = p.importSheet(t, "hi", sheet)
	assert.Equal(t, 2, code)
	assert.Contains(t, out, `no "translation" column`)
}

func TestExportAndImport_TakeTheXclocFolderXcodeWrites(t *testing.T) {
	p := newSheetProject(t)
	xcloc := filepath.Join(p.dir, "hi.xcloc")
	inside := filepath.Join(xcloc, "Localized Contents", "hi.xliff")
	require.NoError(t, os.MkdirAll(filepath.Dir(inside), 0o755))
	require.NoError(t, os.Rename(p.xliff, inside))

	sheet := p.export(t, "hi", "-xliff", xcloc)
	translate(t, sheet, map[string]string{"OBA Vehicle Tracker": "OBA Vehicle Tracker"})
	code, out := p.importSheet(t, "hi", sheet, "-xliff", xcloc)

	require.Equal(t, 0, code, out)
	body, err := os.ReadFile(inside)
	require.NoError(t, err)
	assert.Contains(t, string(body), `<target state="translated">OBA Vehicle Tracker</target>`)
}

func TestExport_WritesTheLanguagesSheetByDefault(t *testing.T) {
	root := fixture(t)
	t.Chdir(t.TempDir())
	var stdout, stderr bytes.Buffer

	require.Equal(t, 0, run([]string{"export", "-root", root, "sw"}, &stdout, &stderr), stderr.String())

	assert.FileExists(t, "sw.csv")
	assert.Contains(t, stdout.String(), "Android's strings alone")
}

func TestXLIFF_TargetsReadBackAsWritten(t *testing.T) {
	path := filepath.Join(t.TempDir(), "hi.xliff")
	body, err := os.ReadFile(xliffFixture)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, body, 0o644))
	doc, err := readXLIFF(path)
	require.NoError(t, err)
	texts := map[string]string{"CFBundleDisplayName": `<b> & "quoted" 'too'</b>`, "Sign in": "R&D <साइन इन>"}
	targets := map[*xliffUnit]string{}
	for _, u := range doc.units {
		if text, ok := texts[u.id]; ok {
			targets[u] = text
		}
	}
	require.NoError(t, os.WriteFile(path, doc.withTargets(targets, nil, false), 0o644))

	read, err := readXLIFF(path)

	require.NoError(t, err)
	for _, u := range read.units {
		if text, ok := texts[u.id]; ok {
			assert.Equal(t, text, u.current(), u.id)
		}
	}
}

func TestToIOS(t *testing.T) {
	tests := []struct{ text, iosEnglish, want string }{
		{"%1$s तक नहीं", "Can't reach %@.", "%@ तक नहीं"},
		{"%1$d रिपोर्ट", "%lld report sent", "%lld रिपोर्ट"},
		{"%2$s से %1$s", "Route %@ to %@", "%2$@ से %1$@"},
		{"%1$s, %2$s", "Route %1$@ to %2$@", "%1$@, %2$@"},
		{"%1$.1f किमी, 100%%", "%.1f km, 100%%", "%.1f किमी, 100%%"},
		{"एक रिपोर्ट", "%lld report sent", "एक रिपोर्ट"},
	}
	for _, tc := range tests {
		assert.Equal(t, tc.want, toIOS(tc.text, tc.iosEnglish), "%q for %q", tc.text, tc.iosEnglish)
	}
}

func TestStringsXML_ReadsBackAsWritten(t *testing.T) {
	texts := []string{
		"Can't reach it", `Say "go"`, `C:\path`, "@home", "?why", "two  spaces", " padded ",
		"line\nbreak", "tab\there", "<b> & </b>", "%1$s तक", "", "100%",
	}
	english := make([]androidString, len(texts))
	trans := map[string]map[string]string{}
	for i, text := range texts {
		key := "s" + string(rune('a'+i))
		english[i] = androidString{key: key, version: &version{forms: map[string]form{"": {value: "x"}}}}
		trans[key] = map[string]string{"": text}
	}
	path := filepath.Join(t.TempDir(), "strings.xml")
	body := stringsXML(english, trans, "hi")
	require.NoError(t, os.WriteFile(path, body, 0o644))
	// aapt refuses an apostrophe or a quote that isn't escaped, and reads a
	// leading @ or ? as a reference to another resource.
	assert.Contains(t, string(body), `>Can\'t reach it<`)
	assert.Contains(t, string(body), `>Say \"go\"<`)
	assert.Contains(t, string(body), `>\@home<`)
	assert.Contains(t, string(body), `>\?why<`)

	read, err := readStringsXML(filepath.Dir(path), path)

	require.NoError(t, err)
	require.Len(t, read, len(texts))
	for i, s := range read {
		assert.Equal(t, texts[i], s.version.forms[""].value, s.key)
	}
}

func TestValuesDirFor(t *testing.T) {
	for _, lang := range []string{"hi", "sw", "pt-BR", "zh-Hans", "zh-Hant", "fil", "fil-PH", "es-419"} {
		dir := valuesDirFor(lang)
		require.NotEmpty(t, dir, lang)
		assert.Equal(t, lang, valuesDirLanguage(dir), "%s goes in %s and reads back", lang, dir)
	}
	assert.Equal(t, "values-pt-rBR", valuesDirFor("pt-BR"))
	assert.Equal(t, "values-b+zh+Hans", valuesDirFor("zh-Hans"))
	assert.Equal(t, "values-fil", valuesDirFor("fil"), "the older qualifier takes a three-letter language too, as aapt2 reads it")
	assert.Empty(t, valuesDirFor("../hi"), "not a language")
}

// TestValuesDirFor_NamesTheAppsOwnFolders: each language the app has is
// written to the folder it is read from, so an import never starts a second
// folder beside it, such as values-b+fil beside values-fil.
func TestValuesDirFor_NamesTheAppsOwnFolders(t *testing.T) {
	dirs, err := os.ReadDir(filepath.Join("../..", androidRes))
	require.NoError(t, err)
	langs := 0
	for _, d := range dirs {
		if lang := valuesDirLanguage(d.Name()); lang != "" {
			langs++
			assert.Equal(t, d.Name(), valuesDirFor(lang), lang)
		}
	}
	assert.Greater(t, langs, 1)
}

// TestExportAndImport_TheAppsLanguagesReadBackUnchanged: each of the app's
// languages exports a sheet with its strings in, and that sheet imported
// with no edits leaves its strings.xml as it is, comments and all.
func TestExportAndImport_TheAppsLanguagesReadBackUnchanged(t *testing.T) {
	p := sheetProject{root: t.TempDir(), dir: t.TempDir()}
	files, err := filepath.Glob(filepath.Join("../..", androidRes, "values*", "strings.xml"))
	require.NoError(t, err)
	for _, file := range append(files, filepath.Join("../..", glossaryPath)) {
		body, err := os.ReadFile(file)
		require.NoError(t, err)
		rel, err := filepath.Rel("../..", file)
		require.NoError(t, err)
		require.NoError(t, os.MkdirAll(filepath.Join(p.root, filepath.Dir(rel)), 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(p.root, rel), body, 0o644))
	}
	langs := 0
	for _, file := range files {
		lang := valuesDirLanguage(filepath.Base(filepath.Dir(file)))
		if lang == "" {
			continue
		}
		langs++
		sheet := p.export(t, lang)
		shown := 0
		for _, row := range readRows(t, sheet) {
			if cell(row, "current") != "" {
				shown++
			}
		}
		assert.NotZero(t, shown, "%s's sheet shows its strings", lang)

		code, out := p.importSheet(t, lang, sheet)

		require.Equal(t, 0, code, out)
		assert.Equal(t, "nothing to change\n", out, lang)
	}
	assert.Greater(t, langs, 1)
}

// TestImport_KeepsWhatItDoesNotChange: an import writes the strings a sheet
// changes or adds, each new one after the one before it in English, and
// leaves the rest of the file as it was: its comments, blank lines and
// attributes, how each other string is written, and the forms of a plural it
// doesn't change.
func TestImport_KeepsWhatItDoesNotChange(t *testing.T) {
	nbsp := string(rune(0xa0)) // a no-break space, which aapt keeps
	p := newSheetProject(t)
	require.NoError(t, os.WriteFile(filepath.Join(p.root, androidHindi), []byte(`<?xml version="1.0" encoding="utf-8"?>
<!--
    Hindi: notes for the next translator.
-->
<resources xmlns:tools="http://schemas.android.com/tools">
    <!-- Sign-in -->
    <string name="login_title">साइन`+nbsp+`इन करें</string>
    <string name="login_error_unreachable">%1$s तक नहीं पहुँच सके। अपना कनेक्शन जाँचें।</string>

    <!-- Help -->
    <string name="help_link">"24 घंटे में https://onebusaway.org पर और पढ़ें।"</string>
    <string name="tracking_gps">GPS बंद है</string>
    <plurals name="reports_sent" tools:ignore="ImpliedQuantity">
        <item quantity="one">%1$d`+nbsp+`रिपोर्ट भेजी गई</item>
        <item quantity="other">%1$d रिपोर्ट भेजी गईं</item>
    </plurals>
</resources>
`), 0o644))
	sheet := p.export(t, "hi")
	translate(t, sheet, map[string]string{
		"GPS is off":                             "GPS बंद",
		"Vehicle on route %1$s, heading to %2$s": "गाड़ी रूट %1$s पर, %2$s की ओर",
		"%1$d reports sent":                      "%1$d रिपोर्टें भेजी गईं",
		"Started %1$d min ago":                   "%1$d मिनट पहले शुरू",
	})

	code, out := p.importSheet(t, "hi", sheet)

	require.Equal(t, 0, code, out)
	assert.Equal(t, `<?xml version="1.0" encoding="utf-8"?>
<!--
    Hindi: notes for the next translator.
-->
<resources xmlns:tools="http://schemas.android.com/tools">
    <!-- Sign-in -->
    <string name="login_title">साइन`+nbsp+`इन करें</string>
    <string name="login_error_unreachable">%1$s तक नहीं पहुँच सके। अपना कनेक्शन जाँचें।</string>

    <!-- Help -->
    <string name="help_link">"24 घंटे में https://onebusaway.org पर और पढ़ें।"</string>
    <string name="tracking_gps">GPS बंद</string>
    <string name="vehicle_route_stop">गाड़ी रूट %1$s पर, %2$s की ओर</string>
    <plurals name="reports_sent" tools:ignore="ImpliedQuantity">
        <item quantity="one">%1$d`+nbsp+`रिपोर्ट भेजी गई</item>
        <item quantity="other">%1$d रिपोर्टें भेजी गईं</item>
    </plurals>
    <string name="started_ago">%1$d मिनट पहले शुरू</string>
</resources>
`, p.read(t, androidHindi))
}

// TestEditStringsXML_TagsThatCloseThemselves: an entry written as a tag that
// closes itself is written out to hold its text, and one of the other kind
// than its English is written again whole. A <resources/> has no room for a
// string, so it is an error rather than a broken file, as is a file with no
// <resources> at all.
func TestEditStringsXML_TagsThatCloseThemselves(t *testing.T) {
	english := []androidString{
		{key: "a", version: &version{forms: map[string]form{"": {}}}},
		{key: "b", version: &version{forms: map[string]form{"plural.one": {}, "plural.other": {}}}},
		{key: "c", version: &version{forms: map[string]form{"": {}}}},
	}
	trans := map[string]map[string]string{"a": {"": "A"}, "b": {"plural.one": "B1", "plural.other": "B2"}, "c": {"": "C"}}
	old := `<resources>
    <string name="a"/>
    <plurals name="b" />
    <plurals name="c">
        <item quantity="other">C</item>
    </plurals>
</resources>
`

	got, err := editStringsXML([]byte(old), english, map[string]*version{}, trans, "hi")

	require.NoError(t, err)
	assert.Equal(t, `<resources>
    <string name="a">A</string>
    <plurals name="b">
        <item quantity="one">B1</item>
        <item quantity="other">B2</item>
    </plurals>
    <string name="c">C</string>
</resources>
`, string(got))
	_, err = editStringsXML([]byte("<resources/>\n"), english, map[string]*version{}, trans, "hi")
	assert.ErrorContains(t, err, "<resources/>")
	_, err = editStringsXML([]byte("<!-- empty -->\n"), english, map[string]*version{}, trans, "hi")
	assert.ErrorContains(t, err, "no <resources>")
}

func TestExport_StartsWithAByteOrderMark(t *testing.T) {
	p := newSheetProject(t)
	sheet := p.export(t, "hi", "-xliff", p.xliff)

	body, err := os.ReadFile(sheet)
	require.NoError(t, err)

	// Without it, older Excel opens the sheet in the system's legacy encoding,
	// and saving it writes mojibake over the translations.
	assert.True(t, strings.HasPrefix(string(body), "\ufeffandroid,ios,form,"), "%.40q", body)
	code, out := p.importSheet(t, "hi", sheet, "-xliff", p.xliff)
	require.Equal(t, 0, code, out)
	assert.Contains(t, out, "nothing to change")
}

func TestExport_AFailedExportLeavesTheSheetAsItWas(t *testing.T) {
	p := newSheetProject(t)
	sheet := filepath.Join(p.dir, "hi.csv")
	require.NoError(t, os.WriteFile(sheet, []byte("a sheet with translations in it\n"), 0o644))
	var stdout, stderr bytes.Buffer

	code := run([]string{"export", "-root", p.root, "-xliff", filepath.Join(p.dir, "hi.xclco"), "-o", sheet, "hi"}, &stdout, &stderr)

	assert.Equal(t, 2, code)
	assert.Contains(t, stderr.String(), "hi.xclco")
	body, err := os.ReadFile(sheet)
	require.NoError(t, err)
	assert.Equal(t, "a sheet with translations in it\n", string(body))
}

func TestImport_AFileThatCannotBeWrittenLeavesTheOtherUnchanged(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root writes a read-only file")
	}
	tests := []struct {
		name  string
		edits []edit
	}{
		{"a language with a strings.xml", nil},
		{"a language with no strings.xml yet", []edit{func(t *testing.T, root string) {
			require.NoError(t, os.RemoveAll(filepath.Join(root, filepath.Dir(androidHindi))))
		}}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			p := newSheetProject(t, tc.edits...)
			valuesHi := filepath.Join(p.root, filepath.Dir(androidHindi))
			_, statErr := os.Stat(valuesHi)
			hadFolder := statErr == nil
			sheet := p.export(t, "hi", "-xliff", p.xliff)
			translate(t, sheet, map[string]string{
				"GPS is off":          "GPS बंद",             // Android alone, written first
				"OBA Vehicle Tracker": "OBA Vehicle Tracker", // iOS alone
			})
			var strings0 string
			if hadFolder {
				strings0 = p.read(t, androidHindi)
			}
			xliff0 := p.xliffText(t)
			require.NoError(t, os.Chmod(p.xliff, 0o444))

			code, out := p.importSheet(t, "hi", sheet, "-xliff", p.xliff)

			assert.Equal(t, 2, code, out)
			assert.Contains(t, out, "permission denied")
			assert.Equal(t, xliff0, p.xliffText(t))
			if hadFolder {
				assert.Equal(t, strings0, p.read(t, androidHindi), "Android keeps what it had when the XLIFF can't be written")
			} else {
				assert.NoDirExists(t, valuesHi, "no folder is left for a file that wasn't written")
			}
			for _, dir := range []string{valuesHi, p.dir} {
				entries, err := os.ReadDir(dir)
				if errors.Is(err, os.ErrNotExist) {
					continue
				}
				require.NoError(t, err)
				for _, e := range entries {
					assert.False(t, strings.HasPrefix(e.Name(), "."), "no temporary file is left in %s: %s", dir, e.Name())
				}
			}

			// Once it can be written, both files are, and the XLIFF keeps its mode.
			require.NoError(t, os.Chmod(p.xliff, 0o640))
			code, out = p.importSheet(t, "hi", sheet, "-xliff", p.xliff)
			require.Equal(t, 0, code, out)
			assert.Contains(t, p.read(t, androidHindi), `<string name="tracking_gps">GPS बंद</string>`)
			assert.Contains(t, p.xliffText(t), `<target state="translated">OBA Vehicle Tracker</target>`)
			info, err := os.Stat(p.xliff)
			require.NoError(t, err)
			assert.Equal(t, os.FileMode(0o640), info.Mode().Perm())
			info, err = os.Stat(filepath.Join(p.root, androidHindi))
			require.NoError(t, err)
			assert.Equal(t, os.FileMode(0o644), info.Mode().Perm(), "a new strings.xml is readable by all, as before")
		})
	}
}

func TestImport_RefusesTwoRowsForTheSameStringAndForm(t *testing.T) {
	tests := []struct{ name, rows, want string }{
		{"an Android string", "tracking_gps,,,,GPS बंद\nlogin_title,,,,साइन इन\ntracking_gps,,,,GPS बंद है\n",
			"hi.csv:4: tracking_gps has a row on line 2 already: keep one"},
		{"a plural form", "reports_sent,,one,,%1$d रिपोर्ट\nreports_sent,,other,,%1$d रिपोर्टें\nreports_sent,,one,,एक रिपोर्ट\n",
			"hi.csv:4: reports_sent (one) has a row on line 2 already: keep one"},
		{"an iOS string", ",CFBundleDisplayName,,,OBA Vehicle Tracker\n,CFBundleDisplayName,,,OBA\n",
			`hi.csv:3: iOS's "CFBundleDisplayName" has a row on line 2 already: keep one`},
		{"a shared row and the same iOS string's own", "login_title,Sign in,,,साइन इन\n,Sign in,,,लॉग इन\n",
			`hi.csv:3: iOS's "Sign in" has a row on line 2 already: keep one`},
		{"a shared row and the same Android string's own", "login_title,Sign in,,,साइन इन\nlogin_title,,,,लॉग इन\n",
			"hi.csv:3: login_title has a row on line 2 already: keep one"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			p := newSheetProject(t)
			strings0, xliff0 := p.read(t, androidHindi), p.xliffText(t)
			sheet := filepath.Join(p.dir, "hi.csv")
			require.NoError(t, os.WriteFile(sheet, []byte("android,ios,form,current,translation\n"+tc.rows), 0o644))

			code, out := p.importSheet(t, "hi", sheet, "-xliff", p.xliff)

			assert.Equal(t, 2, code, out)
			assert.Contains(t, out, tc.want)
			assert.Equal(t, strings0, p.read(t, androidHindi))
			assert.Equal(t, xliff0, p.xliffText(t))
		})
	}
}

func TestImport_RefusesAnotherAppsSpecifiers(t *testing.T) {
	tests := []struct {
		name  string
		texts map[string]string
		want  string
	}{
		{"Android's %s in an iOS row", map[string]string{"Route %1$@, %2$#@reports@": "रूट %s, %2$#@reports@"},
			"[placeholders] Route %@, %lld reports sent (hi): #1 (text) is %s: write it as the English does, %1$@"},
		{"Android's positional %1$s in an iOS row", map[string]string{"Route %1$@, %2$#@reports@": "रूट %1$s, %2$#@reports@"},
			"[placeholders] Route %@, %lld reports sent (hi): #1 (text) is %1$s: write it as the English does, %1$@"},
		{"Android's %d in an iOS row", map[string]string{"%2$lld report sent": "%2$d रिपोर्ट भेजी गई"},
			"[placeholders] Route %@, %lld reports sent (hi): @reports/plural.one: #2 (whole number) is %2$d: write it as the English does, %2$lld"},
		// Android's format has no %@ or %lld, so the existing check already
		// refuses them in a row only Android reads.
		{"iOS's %@ in an Android row", map[string]string{"Vehicle on route %1$s, heading to %2$s": "गाड़ी रूट %@ पर, %2$s की ओर"},
			"[placeholders] vehicle_route_stop (hi): placeholders #1 (text) missing"},
		{"iOS's %lld in an Android row", map[string]string{"Vehicle on route %1$s, heading to %2$s": "गाड़ी रूट %1$s पर, %2$lld की ओर"},
			"[placeholders] vehicle_route_stop (hi): placeholders #2 (text) missing"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			p := newSheetProject(t)
			strings0, xliff0 := p.read(t, androidHindi), p.xliffText(t)
			sheet := p.export(t, "hi", "-xliff", p.xliff)
			translate(t, sheet, tc.texts)

			code, out := p.importSheet(t, "hi", sheet, "-xliff", p.xliff)

			assert.Equal(t, 1, code, out)
			assert.Contains(t, out, tc.want)
			assert.Equal(t, strings0, p.read(t, androidHindi))
			assert.Equal(t, xliff0, p.xliffText(t))
		})
	}
}

func TestImport_ASharedRowTakesAndroidsSpecifiersAndWritesIOSs(t *testing.T) {
	p := newSheetProject(t)
	sheet := p.export(t, "hi", "-xliff", p.xliff)
	translate(t, sheet, map[string]string{"Can't reach %1$s. Check your connection.": "%s तक नहीं पहुँच सके।"})

	code, out := p.importSheet(t, "hi", sheet, "-xliff", p.xliff)

	require.Equal(t, 0, code, out)
	assert.Contains(t, p.read(t, androidHindi), `<string name="login_error_unreachable">%s तक नहीं पहुँच सके।</string>`)
	assert.Contains(t, p.xliffText(t), `<target state="translated">%@ तक नहीं पहुँच सके।</target>`)
}

// zeroUnit is a zero variation of the iOS string "%lld reports sent", which
// Hindi's plural rules don't list.
const zeroUnit = `<trans-unit id="%lld reports sent|==|plural.zero" xml:space="preserve">
        <source>No reports sent</source>
        <note>How many reports the trip sent.</note>
      </trans-unit>
      `

func TestExportAndImport_ASharedIOSPluralsExtraFormHasItsOwnRow(t *testing.T) {
	tests := []struct {
		name  string
		edits []edit
	}{
		{"Android's English has the form too", []edit{replace(androidSource, `<item quantity="one">%1$d report sent</item>`,
			`<item quantity="zero">No reports sent</item>
        <item quantity="one">%1$d report sent</item>`)}},
		{"Android's English doesn't", nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			p := newSheetProject(t, tc.edits...)
			p.editXLIFF(t, `<trans-unit id="Can't reach`, zeroUnit+`<trans-unit id="Can't reach`)
			strings0 := p.read(t, androidHindi)
			reportRows := func(sheet string) [][]string {
				var out [][]string
				for _, row := range readRows(t, sheet) {
					if cell(row, "android") == "reports_sent" || strings.HasPrefix(cell(row, "ios"), "%lld reports sent") {
						out = append(out, []string{cell(row, "android"), cell(row, "ios"), cell(row, "form"), cell(row, "english")})
					}
				}
				return out
			}
			want := [][]string{
				{"reports_sent", "%lld reports sent|==|plural.one", "one", "%1$d report sent"},
				{"reports_sent", "%lld reports sent|==|plural.other", "other", "%1$d reports sent"},
				{"", "%lld reports sent|==|plural.zero", "zero", "No reports sent"},
			}
			sheet := p.export(t, "hi", "-xliff", p.xliff)
			require.Equal(t, want, reportRows(sheet))

			translate(t, sheet, map[string]string{"No reports sent": "कोई रिपोर्ट नहीं भेजी गई"})
			code, out := p.importSheet(t, "hi", sheet, "-xliff", p.xliff)

			require.Equal(t, 0, code, out)
			assert.Contains(t, p.xliffText(t), `<source>No reports sent</source>
        <target state="translated">कोई रिपोर्ट नहीं भेजी गई</target>`)
			assert.Equal(t, strings0, p.read(t, androidHindi), "Android has no zero form in Hindi")

			// Translated, it keeps the same rows, and reads back unchanged.
			sheet = p.export(t, "hi", "-xliff", p.xliff)
			assert.Equal(t, want, reportRows(sheet))
			code, out = p.importSheet(t, "hi", sheet, "-xliff", p.xliff)
			require.Equal(t, 0, code, out)
			assert.Contains(t, out, "nothing to change")
		})
	}
}

func TestExport_MatchesSkipsAFileItCannotRead(t *testing.T) {
	p := newSheetProject(t)
	out := filepath.Join(p.dir, "hi.csv")
	var stdout, stderr bytes.Buffer

	code := run([]string{"export", "-root", p.root, "-xliff", p.xliff, "-matches", "testdata/matches-broken", "-o", out, "hi"}, &stdout, &stderr)

	require.Equal(t, 0, code, stderr.String())
	assert.Contains(t, stderr.String(), "l10n: skipped broken-android/res/values-hi/strings.xml:")
	assert.Contains(t, stderr.String(), "l10n: skipped broken-ios/Localizable.xcstrings:")
	for _, row := range readRows(t, out) {
		if cell(row, "english") == "Sign in" {
			assert.Equal(t, "लॉग इन", cell(row, "onebusaway"), "the apps it can read still give suggestions")
		}
	}

	// A folder that isn't there is still an error.
	stderr.Reset()
	code = run([]string{"export", "-root", p.root, "-matches", filepath.Join(p.dir, "no-such-folder"), "-o", out, "hi"}, &stdout, &stderr)
	assert.Equal(t, 2, code)
	assert.Contains(t, stderr.String(), "no-such-folder")
}
