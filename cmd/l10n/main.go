// Command l10n checks the driver apps' translations: the Android app's
// strings.xml files and the iOS app's String Catalogs, against the glossary
// and the status table in docs/translations. docs/translations/README.md says
// what each check means and how translations are added.
//
//	go run ./cmd/l10n check [-root dir] [-release] [-annotate] [-summary file]
//
// It exits 1 when a check fails, 0 when there are only warnings, and 2 when it
// could not run. The l10n workflow adds -annotate, so each finding shows on the
// pull request's diff, and -summary "$GITHUB_STEP_SUMMARY" for the coverage
// table.
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
)

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }

const usage = "usage: l10n check [-root dir] [-release] [-annotate] [-summary file]"

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] != "check" {
		fmt.Fprintln(stderr, usage)
		return 2
	}
	fs := flag.NewFlagSet("check", flag.ContinueOnError)
	fs.SetOutput(stderr)
	root := fs.String("root", ".", "the repository's root")
	release := fs.Bool("release", false, "also fail when a language that ships is under 95% in either app")
	annotate := fs.Bool("annotate", false, "also write each finding as a GitHub Actions annotation")
	summary := fs.String("summary", "", "append the coverage table to this file, such as $GITHUB_STEP_SUMMARY")
	if err := fs.Parse(args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}

	r, err := runCheck(*root, options{release: *release})
	if err != nil {
		fmt.Fprintln(stderr, "l10n:", err)
		return 2
	}
	r.write(stdout, *annotate)
	if *summary != "" {
		if err := r.appendSummary(*summary); err != nil {
			fmt.Fprintln(stderr, "l10n: writing the job summary:", err)
			return 2
		}
	}
	if r.errors() > 0 {
		return 1
	}
	return 0
}
