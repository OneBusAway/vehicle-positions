package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValuesDirLanguage(t *testing.T) {
	for dir, want := range map[string]string{
		"values-hi":        "hi",
		"values-sw":        "sw",
		"values-pt-rBR":    "pt-BR",
		"values-b+zh+Hans": "zh-Hans",
		"values-b+fil":     "fil",
		// Not languages: a night theme and a smallest-width bucket, whose
		// "sw" is not Swahili.
		"values-night":    "",
		"values-sw600dp":  "",
		"values-hi-night": "",
		"values":          "",
	} {
		assert.Equal(t, want, valuesDirLanguage(dir), dir)
	}
}

func TestPlaceholders(t *testing.T) {
	tests := []struct {
		format, text string
		want         []string
	}{
		{"android", "Can't reach %1$s.", []string{"1:string"}},
		{"android", "%d of %d, 100%% done", []string{"1:int", "2:int"}},
		{"android", "%2$s before %1$.1f km%n", []string{"2:string", "1:float"}},
		{"ios", "Route %@ to %@", []string{"1:string", "2:string"}},
		{"ios", "%lld min, %1$ld again", []string{"1:int", "1:int"}},
		{"ios", "Route %1$@, %2$#@reports@", []string{"1:string", "plural:reports"}},
		{"ios", "%.1f km, 50%%", []string{"1:float"}},
	}
	for _, tc := range tests {
		assert.Equal(t, tc.want, placeholders(tc.format, tc.text), "%s %q", tc.format, tc.text)
	}
}

func TestNumbers(t *testing.T) {
	assert.Equal(t, []string{"24", "7"}, numbers("android", "Within 24 hours, %1$d stops, 7 days"))
	assert.Equal(t, []string{"24"}, numbers("android", "२४ घंटे"), "Devanagari digits")
	assert.Equal(t, []string{"24"}, numbers("android", "٢٤ ساعة"), "Arabic-Indic digits")
	assert.Empty(t, numbers("ios", "%lld min, %1$@"))
}

func TestPluralCategories(t *testing.T) {
	assert.Equal(t, []string{"one", "other"}, pluralCategories("hi"))
	assert.Equal(t, []string{"one", "other"}, pluralCategories("sw"))
	assert.Equal(t, []string{"zero", "one", "two", "few", "many", "other"}, pluralCategories("ar"))
	assert.Equal(t, []string{"one", "few", "many", "other"}, pluralCategories("pl"))
	assert.Equal(t, []string{"one", "many", "other"}, pluralCategories("pt-BR"), "a region takes its language's rules")
	assert.Equal(t, []string{"other"}, pluralCategories("zh-Hans"))
	assert.Equal(t, []string{"other"}, pluralCategories("vi"))
	assert.Equal(t, []string{"other"}, pluralCategories("ht"), "a language CLDR has no rules for uses the root's")
}

// TestPluralCategoriesFor: iOS needs one and other alone where Xcode offers
// no more, and every other language and app keeps CLDR's categories.
func TestPluralCategoriesFor(t *testing.T) {
	for _, lang := range []string{"es", "fr", "it", "pt-BR"} {
		assert.Equal(t, []string{"one", "other"}, pluralCategoriesFor(ios, lang), lang)
		assert.Equal(t, []string{"one", "many", "other"}, pluralCategoriesFor(android, lang), lang)
	}
	assert.Equal(t, []string{"one", "few", "many", "other"}, pluralCategoriesFor(ios, "pl"))
	assert.Equal(t, []string{"one", "other"}, pluralCategoriesFor(ios, "hi"))
	assert.Equal(t, []string{"other"}, pluralCategoriesFor(ios, "vi"))
}

// TestUnescapeAndroid: a string's text as aapt builds it, \u escapes
// included.
func TestUnescapeAndroid(t *testing.T) {
	for in, want := range map[string]string{
		`Starting\u2026`:      "Starting\u2026",
		`%1$s \u2192 %2$s`:    "%1$s \u2192 %2$s",
		`\u2014`:              "\u2014",
		`Caf\u00E9`:           "Caf\u00e9",
		`Can\'t reach %1$s`:   "Can't reach %1$s",
		`"  two  spaces "`:    "  two  spaces ",
		`one\ntwo`:            "one\ntwo",
		`\uZZ12 is not hex`:   `\uZZ12 is not hex`,
		`too short \u20`:      `too short \u20`,
		`ends with \u`:        `ends with \u`,
		`a backslash \\u2026`: `a backslash \u2026`,
		// aapt trims and collapses ASCII's spaces alone: a no-break space,
		// as French writes before ? and :, stays where it is.
		"Langue\u00a0: %1$s":      "Langue\u00a0: %1$s",
		"a \u00a0 b":              "a \u00a0 b",
		"\u00a0at the ends\u202f": "\u00a0at the ends\u202f",
	} {
		assert.Equal(t, want, unescapeAndroid(in), in)
	}
}

// TestEscapeAndroid_DirectionControlsStayVisible: an isolate a translation
// needs, such as around https:// where the app can't put it in as a value, is
// written as a \u escape, not as an invisible character, and reads back the
// same.
func TestEscapeAndroid_DirectionControlsStayVisible(t *testing.T) {
	in := "\u0639\u0646\u0648\u0627\u0646 \u2068https://\u2069. \u200f"
	out := escapeAndroid(in)
	assert.Equal(t, "\u0639\u0646\u0648\u0627\u0646 \\u2068https://\\u2069. \\u200f", out)
	assert.Equal(t, in, unescapeAndroid(out))
	for _, r := range out {
		assert.False(t, isDirectionControl(r), "%U written raw", r)
	}
}

// TestEscapeAndroid_LeavesANoBreakSpaceUnquoted: aapt keeps a no-break space,
// so a string with one is written as it is, not quoted as one whose spaces
// would collapse, and reads back the same.
func TestEscapeAndroid_LeavesANoBreakSpaceUnquoted(t *testing.T) {
	in := "Langue\u00a0: %1$s"
	assert.Equal(t, in, escapeAndroid(in))
	assert.Equal(t, in, unescapeAndroid(escapeAndroid(in)))
}

// TestExportAndImport_KeepANoBreakSpace: the sheet shows a no-break space as
// the app does, a sheet that says what the file says changes nothing, and an
// edit around the space writes it back. Read as a plain space, the sheet
// showed a plain space, so an edit dropped it.
func TestExportAndImport_KeepANoBreakSpace(t *testing.T) {
	const french = "android/app/src/main/res/values-fr/strings.xml"
	const nbsp = "\u00a0" // written as it is, as values-fr writes it
	const file = `<?xml version="1.0" encoding="utf-8"?>
<resources>
    <string name="started_ago">Démarré il y a %1$d` + nbsp + `min</string>
</resources>
`
	p := newSheetProject(t)
	path := filepath.Join(p.root, french)
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte(file), 0o644))

	sheet := p.export(t, "fr")
	var current string
	for _, row := range readRows(t, sheet) {
		if cell(row, "android") == "started_ago" {
			current = cell(row, "current")
		}
	}
	assert.Equal(t, "Démarré il y a %1$d"+nbsp+"min", current, "the sheet shows what the app shows")

	translate(t, sheet, map[string]string{"Started %1$d min ago": "Démarré il y a %1$d" + nbsp + "min"})
	code, out := p.importSheet(t, "fr", sheet)
	require.Equal(t, 0, code, out)
	assert.Equal(t, "nothing to change\n", out, "the sheet says what the file says")
	assert.Equal(t, file, p.read(t, french))

	translate(t, sheet, map[string]string{"Started %1$d min ago": "Parti il y a %1$d" + nbsp + "min"})
	code, out = p.importSheet(t, "fr", sheet)
	require.Equal(t, 0, code, out)
	assert.Contains(t, p.read(t, french), `<string name="started_ago">Parti il y a %1$d`+nbsp+`min</string>`)
}

// TestCanonicalTag: a tag in any case is written as BCP 47 writes it, the
// case the app's folders and Xcode's files are named in.
func TestCanonicalTag(t *testing.T) {
	for tag, want := range map[string]string{
		"pt-BR":      "pt-BR",
		"pt-br":      "pt-BR",
		"PT-BR":      "pt-BR",
		"zh-hans":    "zh-Hans",
		"ZH-HANT":    "zh-Hant",
		"Hi":         "hi",
		"FIL":        "fil",
		"es-419":     "es-419",
		"sr-latn-rs": "sr-Latn-RS",
		// After a singleton, a subtag is neither a script nor a region.
		"de-ch-x-ab-test": "de-CH-x-ab-test",
	} {
		assert.Equal(t, want, canonicalTag(tag), tag)
	}
}

// TestCanonicalTag_TheAppsLanguagesInAnyCaseReachTheirFolders: each language
// the app has, typed in lower case or in capitals, goes to its own folder.
func TestCanonicalTag_TheAppsLanguagesInAnyCaseReachTheirFolders(t *testing.T) {
	dirs, err := os.ReadDir(filepath.Join("../..", androidRes))
	require.NoError(t, err)
	langs := 0
	for _, d := range dirs {
		lang := valuesDirLanguage(d.Name())
		if lang == "" {
			continue
		}
		langs++
		for _, typed := range []string{strings.ToLower(lang), strings.ToUpper(lang)} {
			assert.Equal(t, d.Name(), valuesDirFor(canonicalTag(typed)), typed)
		}
	}
	assert.Greater(t, langs, 1)
}

// TestExportAndImport_ATagInLowerCase: pt-br is pt-BR. Its sheet shows
// values-pt-rBR's strings, the glossary's pt-BR words and the iOS strings in
// Xcode's pt-BR.xliff, and its import writes those files. Before, pt-br read
// and wrote values-b+pt+br, so the sheet showed nothing translated, and the
// import gave aapt two values for each string in pt-rBR, which failed the
// build.
func TestExportAndImport_ATagInLowerCase(t *testing.T) {
	const ptBR = "android/app/src/main/res/values-pt-rBR/strings.xml"
	p := newSheetProject(t,
		replace(glossaryFile, "forbidden_hi\n", "forbidden_hi,pt-BR\n"),
		replace(glossaryFile, ",गाड़ी,बस\n", ",गाड़ी,बस,veículo\n"),
		func(t *testing.T, root string) {
			path := filepath.Join(root, ptBR)
			require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
			require.NoError(t, os.WriteFile(path, []byte(`<?xml version="1.0" encoding="utf-8"?>
<resources>
    <string name="login_title">Entrar</string>
</resources>
`), 0o644))
		})
	xcloc := filepath.Join(p.dir, "pt-BR.xcloc")
	inside := filepath.Join(xcloc, "Localized Contents", "pt-BR.xliff")
	require.NoError(t, os.MkdirAll(filepath.Dir(inside), 0o755))
	require.NoError(t, os.Rename(p.xliff, inside))

	sheet := p.export(t, "pt-br", "-xliff", xcloc)

	iosRows := 0
	for _, row := range readRows(t, sheet) {
		switch cell(row, "android") {
		case "login_title":
			assert.Equal(t, "Entrar", cell(row, "current"))
		case "vehicle_route_stop":
			assert.Contains(t, cell(row, "glossary"), "vehicle: veículo")
		}
		if cell(row, "ios") != "" {
			iosRows++
		}
	}
	assert.NotZero(t, iosRows, "the sheet has pt-BR.xliff's strings")

	translate(t, sheet, map[string]string{"Sign in": "Iniciar sessão"})
	code, out := p.importSheet(t, "pt-br", sheet, "-xliff", xcloc)

	require.Equal(t, 0, code, out)
	assert.Contains(t, p.read(t, ptBR), `<string name="login_title">Iniciar sessão</string>`)
	assert.NoDirExists(t, filepath.Join(p.root, "android/app/src/main/res/values-b+pt+br"))
	xliff, err := os.ReadFile(inside)
	require.NoError(t, err)
	assert.Contains(t, string(xliff), `<target state="translated">Iniciar sessão</target>`)
}

// TestImport_ALowerCaseScriptWritesItsOwnFolder: zh-hans goes to
// values-b+zh+Hans. Before, it started values-b+zh+hans, which a Mac's disk
// takes for values-b+zh+Hans but Linux, where CI builds the app, does not.
func TestImport_ALowerCaseScriptWritesItsOwnFolder(t *testing.T) {
	p := newSheetProject(t)
	sheet := p.export(t, "zh-hans")
	translate(t, sheet, map[string]string{"Sign in": "登录"})

	code, out := p.importSheet(t, "zh-hans", sheet)

	require.Equal(t, 0, code, out)
	dirs, err := os.ReadDir(filepath.Join(p.root, androidRes))
	require.NoError(t, err)
	var names []string
	for _, d := range dirs {
		names = append(names, d.Name())
	}
	assert.Contains(t, names, "values-b+zh+Hans")
	assert.NotContains(t, names, "values-b+zh+hans")
}
