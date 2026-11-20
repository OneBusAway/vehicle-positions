package main

import "strings"

// The two apps' string formats, which write placeholders differently.
const (
	android = "android"
	ios     = "ios"
)

// A unit is one translatable string of one app: an Android <string> or
// <plurals>, or one String Catalog entry.
type unit struct {
	app  string
	key  string
	note string // what the developer wrote for translators
	// stale is a String Catalog entry Xcode no longer finds in the code.
	stale  bool
	source *version
	trans  map[string]*version // by language
}

// A version is one language's text of a unit, as forms by path: "" for a
// plain string, "plural.one" for a plural form, and for a String Catalog
// "device.iphone/plural.one" for one nested in a device variation and
// "@name/plural.one" for one in the substitution called name.
type version struct {
	file  string
	line  int
	forms map[string]form
}

type form struct {
	value string
	state string // a String Catalog stringUnit's state; "" on Android
}

// pluralGroup splits a form path into the part before its plural category
// and the category, ok false when the form is not a plural form.
func pluralGroup(path string) (prefix, category string, ok bool) {
	i := strings.LastIndex(path, "plural.")
	if i < 0 {
		return "", "", false
	}
	return path[:i], path[i+len("plural."):], true
}

// counterpart is the English form a translated form at path is checked
// against: the same form, or for a plural form the English has no such
// category of, the English "other" of that group, as it is too for a single
// string where the English varies by plural; failing both, the plain string.
func (u *unit) counterpart(path string) (form, bool) {
	if f, ok := u.source.forms[path]; ok {
		return f, true
	}
	prefix, _, _ := pluralGroup(path)
	if f, ok := u.source.forms[prefix+"plural.other"]; ok {
		return f, true
	}
	f, ok := u.source.forms[""]
	return f, ok
}
