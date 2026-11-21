package main

import "strings"

// cardinalCategories is each language's plural categories, from CLDR 48's
// common/supplemental/plurals.xml (cardinal rules), for English and the
// languages the translation proposal (#495) names. Android picks a <plurals>
// item, and iOS a plural variation, by these. Add a language's row when it is
// added to status.csv.
var cardinalCategories = map[string][]string{
	"am":  {"one", "other"},
	"ar":  {"zero", "one", "two", "few", "many", "other"},
	"de":  {"one", "other"},
	"en":  {"one", "other"},
	"es":  {"one", "many", "other"},
	"fa":  {"one", "other"},
	"fi":  {"one", "other"},
	"fil": {"one", "other"},
	"fr":  {"one", "many", "other"},
	"hi":  {"one", "other"},
	"it":  {"one", "many", "other"},
	"ja":  {"other"},
	"km":  {"other"},
	"ko":  {"other"},
	"lo":  {"other"},
	"om":  {"one", "other"},
	"pa":  {"one", "other"},
	"pl":  {"one", "few", "many", "other"},
	"pt":  {"one", "many", "other"},
	"ru":  {"one", "few", "many", "other"},
	"so":  {"one", "other"},
	"sw":  {"one", "other"},
	"ti":  {"one", "other"},
	"uk":  {"one", "few", "many", "other"},
	"vi":  {"other"},
	"zh":  {"other"},
}

// xcodeCategories is where an iOS translation needs fewer plural categories
// than CLDR gives. Xcode offers Spanish, French, Italian and Portuguese one and
// other alone, and none of iOS's own strings in them has many, CLDR's form for
// a million and more, which iOS then reads from other.
var xcodeCategories = map[string][]string{
	"es": {"one", "other"},
	"fr": {"one", "other"},
	"it": {"one", "other"},
	"pt": {"one", "other"},
}

// pluralCategoriesFor is the plural categories one app's strings need in a
// language: pluralCategories, but fewer on iOS where Xcode needs fewer.
func pluralCategoriesFor(app, lang string) []string {
	base, _, _ := strings.Cut(lang, "-")
	if cats, ok := xcodeCategories[base]; ok && app == ios {
		return cats
	}
	return pluralCategories(lang)
}

// pluralCategories is the plural categories a language's strings need, by its
// language subtag: pt-BR goes by pt. A language CLDR gives no rules, such as
// Haitian Creole or Samoan, uses CLDR's root rules, which have "other" alone.
func pluralCategories(lang string) []string {
	base, _, _ := strings.Cut(lang, "-")
	if cats, ok := cardinalCategories[base]; ok {
		return cats
	}
	return []string{"other"}
}
