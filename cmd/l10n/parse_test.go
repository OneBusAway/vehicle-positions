package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
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
	} {
		assert.Equal(t, want, unescapeAndroid(in), in)
	}
}
