package main

import (
	"cmp"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"
)

type severity int

const (
	errorLevel severity = iota
	warning
)

func (s severity) String() string {
	if s == errorLevel {
		return "error"
	}
	return "warning"
}

// A finding is one thing a check found wrong, at a line of a file. key is the
// string's Android name or String Catalog key, "" for a finding about a whole
// language; lang is the language it is about, "en" for the English source.
type finding struct {
	severity severity
	rule     string
	file     string // relative to the root, with forward slashes
	line     int
	key      string
	lang     string
	message  string
}

func (f finding) String() string {
	what := "(" + f.lang + ")"
	if f.key != "" {
		what = f.key + " " + what
	}
	return oneLine(fmt.Sprintf("%s:%d: %s [%s] %s: %s", f.file, f.line, f.severity, f.rule, what, f.message))
}

// oneLine writes the line breaks in s as \r and \n, so text from the strings
// files, such as an iOS key, cannot start a line of its own, which GitHub
// Actions would read as a workflow command.
func oneLine(s string) string {
	return strings.NewReplacer("\r", `\r`, "\n", `\n`).Replace(s)
}

// count is how many of an app's strings a language has, of all it could.
type count struct{ done, total int }

func (c count) String() string { return fmt.Sprintf("%d of %d", c.done, c.total) }

// percent is c as a share; an app with nothing to translate counts as whole.
func (c count) percent() float64 {
	if c.total == 0 {
		return 100
	}
	return 100 * float64(c.done) / float64(c.total)
}

// coverage is one language's row of the summary.
type coverage struct {
	lang, name, status string
	android, ios       count
}

type report struct {
	findings []finding
	coverage []coverage
}

func (r *report) add(f finding) { r.findings = append(r.findings, f) }

func (r *report) errors() int {
	n := 0
	for _, f := range r.findings {
		if f.severity == errorLevel {
			n++
		}
	}
	return n
}

func (r *report) sort() {
	slices.SortStableFunc(r.findings, func(a, b finding) int {
		return cmp.Or(cmp.Compare(a.file, b.file), cmp.Compare(a.line, b.line), cmp.Compare(a.rule, b.rule), cmp.Compare(a.lang, b.lang))
	})
}

// write prints each finding, then the coverage. Under GitHub Actions each
// finding is also an annotation, so it shows on the pull request's diff.
func (r *report) write(w io.Writer, actions bool) {
	for _, f := range r.findings {
		fmt.Fprintln(w, f)
		if actions {
			fmt.Fprintf(w, "::%s file=%s,line=%d,title=%s::%s\n", f.severity, propertyEscape(f.file), f.line, propertyEscape("l10n "+f.rule), annotationEscape(f.message))
		}
	}
	for _, c := range r.coverage {
		fmt.Fprintln(w, oneLine(fmt.Sprintf("%s (%s) %s: Android %s, iOS %s", c.name, c.lang, c.status, c.android, c.ios)))
	}
	fmt.Fprintf(w, "%d errors, %d warnings\n", r.errors(), len(r.findings)-r.errors())
}

// annotationEscape escapes what a workflow command's message cannot hold.
func annotationEscape(s string) string {
	return strings.NewReplacer("%", "%25", "\r", "%0D", "\n", "%0A").Replace(s)
}

// propertyEscape escapes what a workflow command's property value cannot
// hold: what its message cannot, and the colon and comma that would end the
// value.
func propertyEscape(s string) string {
	return strings.NewReplacer("%", "%25", "\r", "%0D", "\n", "%0A", ":", "%3A", ",", "%2C").Replace(s)
}

// appendSummary adds the coverage table to the job summary at path.
func (r *report) appendSummary(path string) error {
	var b strings.Builder
	b.WriteString("### Translations\n\n| Language | Status | Android | iOS |\n|---|---|---|---|\n")
	for _, c := range r.coverage {
		fmt.Fprintf(&b, "| %s (%s) | %s | %s | %s |\n", c.name, c.lang, c.status, c.android, c.ios)
	}
	fmt.Fprintf(&b, "\n%d errors, %d warnings. `go run ./cmd/l10n check` lists them.\n", r.errors(), len(r.findings)-r.errors())
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	if _, err := f.WriteString(b.String()); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}
