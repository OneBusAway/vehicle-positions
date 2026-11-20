package main

import (
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode"
)

// Placeholders as each app's format writes them. Android's are Java format
// specifiers ("%1$s", "%d", "%.1f"); a String Catalog's are printf's with %@
// for an object ("%@", "%lld", "%1$@"), and "%#@name@" for a substitution.
var (
	androidPlaceholder = regexp.MustCompile(`%(?:(\d+)\$)?[-#+ 0,(<]*\d*(?:\.\d+)?([tT][a-zA-Z]|[a-zA-Z%])`)
	iosPlaceholder     = regexp.MustCompile(`%(?:(\d+)\$)?(?:#@([A-Za-z0-9_]+)@|[-+ #0']*(?:\d+|\*)?(?:\.(?:\d+|\*))?(?:hh|h|ll|l|q|L|z|t|j)?([@dDiuUxXoOfFeEgGaAcCsSp%]))`)
)

// placeholderType names the kind of value a conversion takes, "" for one that
// takes none (%% and %n).
func placeholderType(conversion string) string {
	switch conversion[0] {
	case 's', 'S', '@':
		return "string"
	case 'd', 'D', 'i', 'u', 'U', 'x', 'X', 'o', 'O':
		return "int"
	case 'f', 'F', 'e', 'E', 'g', 'G', 'a', 'A':
		return "float"
	case 'c', 'C':
		return "char"
	case '%', 'n':
		return ""
	}
	return conversion
}

// placeholders lists text's placeholders in order as "argument:type", the
// argument numbered as the format would take it, or "plural:name" for a
// String Catalog substitution. Two texts take the same values when they have
// the same placeholders in any order.
func placeholders(format, text string) []string {
	var out []string
	next := 1
	re := androidPlaceholder
	if format == ios {
		re = iosPlaceholder
	}
	for _, m := range re.FindAllStringSubmatch(text, -1) {
		conversion := m[len(m)-1]
		if format == ios && m[2] != "" {
			out = append(out, "plural:"+m[2])
			continue
		}
		kind := placeholderType(conversion)
		if kind == "" {
			continue
		}
		arg := next
		if m[1] != "" {
			arg, _ = strconv.Atoi(m[1])
		} else {
			next++
		}
		out = append(out, fmt.Sprintf("%d:%s", arg, kind))
	}
	return out
}

// withoutPlaceholders is text with each placeholder replaced by a space.
func withoutPlaceholders(format, text string) string {
	if format == ios {
		return iosPlaceholder.ReplaceAllString(text, " ")
	}
	return androidPlaceholder.ReplaceAllString(text, " ")
}

// zeros are the zero digits of the numbering systems a translation may write
// its numbers in: ASCII, Arabic-Indic, Extended Arabic-Indic (Persian),
// Devanagari, Bengali, Gurmukhi, Gujarati, Odia, Tamil, Telugu, Kannada,
// Malayalam, Thai, Lao, Khmer, Myanmar and fullwidth.
var zeros = []rune{'0', 0x660, 0x6F0, 0x966, 0x9E6, 0xA66, 0xAE6, 0xB66, 0xBE6, 0xC66, 0xCE6, 0xD66, 0xE50, 0xED0, 0x17E0, 0x1040, 0xFF10}

func asciiDigit(r rune) rune {
	for _, z := range zeros {
		if r >= z && r <= z+9 {
			return '0' + (r - z)
		}
	}
	return r
}

var digitRun = regexp.MustCompile(`[0-9]+`)

// numbers lists the numbers text writes in digits, outside its placeholders,
// as ASCII: "२४" is 24, as a translation into Hindi may write it.
func numbers(format, text string) []string {
	return digitRun.FindAllString(strings.Map(asciiDigit, withoutPlaceholders(format, text)), -1)
}

var urlPattern = regexp.MustCompile(`https?://[^\s"'<>]+`)

// urls lists text's web addresses, without punctuation that ends a sentence.
func urls(text string) []string {
	found := urlPattern.FindAllString(text, -1)
	for i, u := range found {
		found[i] = strings.TrimRight(u, ".,;:!?)")
	}
	return found
}

// containsWord reports whether text uses word: as a whole word, ignoring
// case, for a word in Latin letters, and anywhere in text for one in a script
// that does not mark word ends the same way.
func containsWord(text, word string) bool {
	if strings.IndexFunc(word, func(r rune) bool { return r > unicode.MaxLatin1 }) < 0 {
		return regexp.MustCompile(`(?i)\b` + regexp.QuoteMeta(word) + `\b`).MatchString(text)
	}
	return strings.Contains(strings.ToLower(text), strings.ToLower(word))
}

// comparable is text as two apps' strings are compared: its placeholders as
// %1, %2 by argument, typographic apostrophes plain, and space collapsed, so
// Android's "Can\'t reach %1$s." and iOS's "Can’t reach %@." are the same.
func comparable(format, text string) string {
	next := 1
	re := androidPlaceholder
	if format == ios {
		re = iosPlaceholder
	}
	text = re.ReplaceAllStringFunc(text, func(p string) string {
		m := re.FindStringSubmatch(p)
		if format == ios && m[2] != "" {
			return "%#@" + m[2] + "@"
		}
		if placeholderType(m[len(m)-1]) == "" {
			return p
		}
		arg := next
		if m[1] != "" {
			arg, _ = strconv.Atoi(m[1])
		} else {
			next++
		}
		return "%" + strconv.Itoa(arg)
	})
	text = strings.ReplaceAll(text, "’", "'")
	return strings.Join(strings.Fields(text), " ")
}

// sameValues reports whether a translation takes the values its English does.
// A plural form may leave out a whole-number placeholder, since it can say
// "one report" without the number, but takes no value the English lacks.
func sameValues(english, translated []string, pluralForm bool) (missing, extra []string) {
	for _, p := range translated {
		if !slices.Contains(english, p) && !slices.Contains(extra, p) {
			extra = append(extra, p)
		}
	}
	for _, p := range english {
		if slices.Contains(translated, p) || slices.Contains(missing, p) {
			continue
		}
		if pluralForm && strings.HasSuffix(p, ":int") {
			continue
		}
		missing = append(missing, p)
	}
	return missing, extra
}
