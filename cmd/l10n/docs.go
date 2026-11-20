package main

import (
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// The translators' files.
const (
	glossaryPath = "docs/translations/glossary.csv"
	statusPath   = "docs/translations/status.csv"
)

// A term is one row of the glossary.
type term struct {
	term string
	// inEnglish finds the term in an English string, words built on it
	// ("vehicles") included.
	inEnglish *regexp.Regexp
	// keep is a name or brand that stays as it is in every language.
	keep bool
	// forbidden is the words each language must not use for the term, "en"
	// included: there, they are refused in every English string, the house
	// rule's "vehicle, never bus".
	forbidden map[string][]string
}

// readGlossary reads glossary.csv: term, meaning, do_not_translate, then
// forbidden_<language> columns, and <language> columns with each language's
// approved word, which are for translators and reviewers to follow.
func readGlossary(root string) ([]term, error) {
	rows, err := readCSV(filepath.Join(root, glossaryPath))
	if err != nil {
		return nil, err
	}
	var out []term
	for _, row := range rows {
		t := term{term: row.get("term"), keep: row.get("do_not_translate") == "yes", forbidden: map[string][]string{}}
		if t.term == "" {
			continue
		}
		t.inEnglish = regexp.MustCompile(`(?i)\b` + regexp.QuoteMeta(t.term))
		for col, value := range row.fields {
			if lang, ok := strings.CutPrefix(col, "forbidden_"); ok {
				for _, w := range strings.Split(value, ";") {
					if w = strings.TrimSpace(w); w != "" {
						t.forbidden[lang] = append(t.forbidden[lang], w)
					}
				}
			}
		}
		out = append(out, t)
	}
	return out, nil
}

// A language is one row of status.csv.
type language struct {
	lang, name, status, reviewer, reviewedOn string
	line                                     int
}

// The statuses a language can have: English is the source, and a translated
// language is reviewed by someone fluent or still a draft.
const (
	statusSource   = "source"
	statusReviewed = "reviewed"
	statusDraft    = "draft"
)

func readStatus(root string) ([]language, error) {
	rows, err := readCSV(filepath.Join(root, statusPath))
	if err != nil {
		return nil, err
	}
	out := make([]language, 0, len(rows))
	for _, row := range rows {
		out = append(out, language{
			lang: row.get("language"), name: row.get("name"), status: row.get("status"),
			reviewer: row.get("reviewer"), reviewedOn: row.get("reviewed_on"), line: row.line,
		})
	}
	return out, nil
}

type csvRow struct {
	line   int
	fields map[string]string
}

func (r csvRow) get(col string) string { return strings.TrimSpace(r.fields[col]) }

// readCSV reads a CSV file with a header row into rows keyed by column.
func readCSV(path string) ([]csvRow, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	r := csv.NewReader(f)
	// A row may stop short of the last columns: adding a language's columns
	// to the glossary need not touch every row.
	r.FieldsPerRecord = -1
	header, err := r.Read()
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	var rows []csvRow
	for {
		rec, err := r.Read()
		if err != nil {
			if errors.Is(err, io.EOF) {
				return rows, nil
			}
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		line, _ := r.FieldPos(0)
		row := csvRow{line: line, fields: map[string]string{}}
		for i, col := range header {
			if i < len(rec) {
				row.fields[strings.TrimSpace(col)] = rec[i]
			}
		}
		rows = append(rows, row)
	}
}
