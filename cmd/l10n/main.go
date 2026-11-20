// Command l10n checks the driver apps' translations: the Android app's
// strings.xml files and the iOS app's String Catalogs, against the glossary
// and the status table in docs/translations. It also writes a language's
// strings from both apps to one sheet for translators, and reads the sheet
// back into the apps. docs/translations/README.md says what each check means
// and how a language is added.
//
//	go run ./cmd/l10n check [-root dir] [-release] [-annotate] [-summary file]
//	go run ./cmd/l10n export [-root dir] [-xliff file] [-matches dir] [-o file] <language>
//	go run ./cmd/l10n import [-root dir] [-xliff file] <language> <sheet.csv>
//
// check exits 1 when a check fails, 0 when there are only warnings, and 2 when
// it could not run. The l10n workflow adds -annotate, so each finding shows on
// the pull request's diff, and -summary "$GITHUB_STEP_SUMMARY" for the
// coverage table. import exits 1 when it refuses a sheet.
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
)

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }

const usage = `usage:
  l10n check [-root dir] [-release] [-annotate] [-summary file]
  l10n export [-root dir] [-xliff file] [-matches dir] [-o file] <language>
  l10n import [-root dir] [-xliff file] <language> <sheet.csv>`

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, usage)
		return 2
	}
	switch args[0] {
	case "check":
		return runCheckCommand(args[1:], stdout, stderr)
	case "export":
		return runExportCommand(args[1:], stdout, stderr)
	case "import":
		return runImportCommand(args[1:], stdout, stderr)
	}
	fmt.Fprintln(stderr, usage)
	return 2
}

// parse parses a command's flags, and says whether to go on: false, with the
// exit code, for -h or a bad flag.
func parse(fs *flag.FlagSet, args []string) (bool, int) {
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return false, 0
		}
		return false, 2
	}
	return true, 0
}

func runCheckCommand(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("check", flag.ContinueOnError)
	fs.SetOutput(stderr)
	root := fs.String("root", ".", "the repository's root")
	release := fs.Bool("release", false, "also fail when a language that ships is under 95% in either app")
	annotate := fs.Bool("annotate", false, "also write each finding as a GitHub Actions annotation")
	summary := fs.String("summary", "", "append the coverage table to this file, such as $GITHUB_STEP_SUMMARY")
	if ok, code := parse(fs, args); !ok {
		return code
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

func runExportCommand(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("export", flag.ContinueOnError)
	fs.SetOutput(stderr)
	root := fs.String("root", ".", "the repository's root")
	xliff := fs.String("xliff", "", "the language's .xcloc folder, or the .xliff file in it, from xcodebuild -exportLocalizations; without it, the sheet has Android's strings alone")
	matches := fs.String("matches", "", "a folder of other OneBusAway apps' checkouts, whose translations of the same English the sheet suggests")
	out := fs.String("o", "", "the sheet to write (default <language>.csv)")
	if ok, code := parse(fs, args); !ok {
		return code
	}
	if fs.NArg() != 1 {
		fmt.Fprintln(stderr, usage)
		return 2
	}
	lang := fs.Arg(0)
	if *out == "" {
		*out = lang + ".csv"
	}
	f, err := os.Create(*out)
	if err != nil {
		fmt.Fprintln(stderr, "l10n:", err)
		return 2
	}
	err = exportSheet(*root, *xliff, *matches, lang, f)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		fmt.Fprintln(stderr, "l10n:", err)
		return 2
	}
	fmt.Fprintf(stdout, "wrote %s\n", *out)
	if *xliff == "" {
		fmt.Fprintln(stdout, "It has Android's strings alone: give the iOS ones with -xliff (docs/translations/README.md).")
	}
	return 0
}

func runImportCommand(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("import", flag.ContinueOnError)
	fs.SetOutput(stderr)
	root := fs.String("root", ".", "the repository's root")
	xliff := fs.String("xliff", "", "the .xcloc folder, or the .xliff file, the sheet's iOS strings were exported from; it is filled in place")
	if ok, code := parse(fs, args); !ok {
		return code
	}
	if fs.NArg() != 2 {
		fmt.Fprintln(stderr, usage)
		return 2
	}
	refused, changed, err := importSheet(*root, *xliff, fs.Arg(0), fs.Arg(1))
	if err != nil {
		fmt.Fprintln(stderr, "l10n:", err)
		return 2
	}
	if len(refused) > 0 {
		for _, f := range refused {
			fmt.Fprintln(stderr, f)
		}
		fmt.Fprintln(stderr, "l10n: the sheet was not imported: fix these and import it again. Nothing was written.")
		return 1
	}
	if len(changed) == 0 {
		fmt.Fprintln(stdout, "nothing to change")
	}
	for _, path := range changed {
		fmt.Fprintf(stdout, "wrote %s\n", path)
	}
	if *xliff != "" {
		fmt.Fprintln(stdout, "Now bring the iOS strings in with xcodebuild -importLocalizations (docs/translations/README.md).")
	}
	return 0
}
