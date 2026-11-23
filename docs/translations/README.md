# Translating the driver apps

This folder holds what the Android and iOS driver apps' translations are checked against. The plan they follow is
proposal #495, and the workflow here may change.

## Where the strings live

- **Android:** `android/app/src/main/res/values/strings.xml` is the English. Each language has its own
  `values-<language>/strings.xml` with the same names: `values-hi`, `values-fil`, `values-pt-rBR`, `values-b+zh+Hans`.
- **iOS:** `ios/VehicleTracker/Resources/Localizable.xcstrings`, a String Catalog that holds the English and every
  language together.

Each app's file is the source of truth for that app. Most iOS strings say word for word what an Android string
says, so a language should translate them the same way in both. The check warns where it doesn't.

## The files here

- **`glossary.csv`** has one row per term:
  - **`term`**: the English word.
  - **`meaning`**: what it means in these apps.
  - **`do_not_translate`**: `yes` for a name that stays as it is in every language.
  - **`forbidden_<language>`**: words that language must not use for the term, separated by `;`.
  - **`<language>`**: optional; the approved word, for translators and reviewers to follow.

  `forbidden_en` keeps the house rule: the apps say "vehicle", never "bus", because agencies run vans, shuttles and
  cars too. For another language, a forbidden word is refused only in a string whose English uses the term. In
  Hindi, बस is "bus" in a sentence about the vehicle but also means "just".
- **`status.csv`** has one row per language:
  - **`language`**: a BCP 47 tag such as `hi` or `pt-BR`.
  - **`name`**: the language's name.
  - **`status`**: one of three:
    - `source`, which is English alone;
    - `reviewed`, read in full by someone fluent, named in `reviewer` with `reviewed_on`;
    - `draft`, which ships but is still waiting for that read.
  - **`review_issue`**: the issue asking for a review.

## Rules for English strings

- Give every string that takes a value a note saying what the value is: an XML comment just above it on Android,
  `comment:` in `String(localized:)` on iOS. Translators see one string at a time.
- Write whole sentences. A sentence built from pieces can't be put in another language's word order.
- Write numbers that vary as placeholders, and plural sentences as plurals (`<plurals>`, or a plural variation in
  the catalog).
- When a string's meaning changes, give it a new name or key, so the old translations aren't shown for the new
  meaning.
- New strings can land in English alone. The app shows English for them until each language catches up, and one
  pull request before each release translates what is missing.

## Rules for translations

- Keep every placeholder (`%1$s`, `%d`, `%@`, `%lld`), with the same type. Reorder them only by position (`%2$s`).
  On iOS, write each one as the English does: `%@`, not Android's `%s`, and `%lld`, not `%d`.
  A plural form may leave out the count, as in "one report".
- Give a plural every form your language needs, by the Unicode CLDR rules in `cmd/l10n/plurals.go`. Hindi and
  Swahili need `one` and `other`. Arabic needs six forms, and Polish, Russian and Ukrainian need `few` and `many` as
  well. In Spanish, French, Italian and Portuguese, `many` is used only for exact millions, so it can repeat `other`.
  On iOS those four need only `one` and `other`: Xcode offers no more, and iOS reads millions from `other`.
  Where the English is a plural, the translation is one too, even if its forms read the same. Only an iOS
  translation into a language with `other` alone, such as Chinese or Vietnamese, may be a single string.
- Write numbers in digits, in your language's own digits if you like (`२४` for 24), never as words.
- Keep web addresses and the glossary's `do_not_translate` terms exactly as they are.
- Use the glossary's word for each term, and none of its forbidden ones.
- A machine or AI draft is a start, not a translation. Say in the pull request which strings were drafted. On iOS,
  each drafted string keeps Xcode's `machine_translated` state until someone fluent has read it: import a drafted
  sheet with `-draft` (below), and the check refuses a `machine_translated` string in a `reviewed` language.
  Android strings have no such state, so there the language's row in `status.csv` is what marks a draft. The
  language stays `draft` until a fluent reviewer has read every string in context and approved the pull request.

## Translating in a spreadsheet

Translators and reviewers work in one sheet per language, a CSV file with both apps' strings, which any spreadsheet
opens. The tool writes it from the apps and writes it back into them.

1. On a Mac with Xcode, export the iOS strings. Xcode writes them to an `.xcloc` folder:

   ```
   cd ios/VehicleTracker && xcodegen generate
   xcodebuild -exportLocalizations -project VehicleTracker.xcodeproj -localizationPath /tmp/l10n -exportLanguage hi
   ```

2. From the repository's root, write the sheet, `hi.csv`:

   ```
   go run ./cmd/l10n export -xliff /tmp/l10n/hi.xcloc hi
   ```

   Without `-xliff`, the sheet has the Android strings alone. With `-matches <folder>`, a folder holding checkouts
   of other OneBusAway apps, the sheet also shows how those apps translate the same English. The tool reads only
   that folder and downloads nothing. A file there it can't read is skipped, with a warning.

3. Open the sheet in Google Sheets, or in Excel through Data > From Text/CSV with the file origin set to UTF-8,
   and save it as CSV (in Excel, "CSV UTF-8"). The sheet starts with a byte order mark, which tells Excel it is
   UTF-8, but an older Excel opening the file directly may still read it in another encoding and garble every
   letter outside English. A garbled sheet imported would write that over the translations.

   Fill in the `translation` column. A cell left empty keeps the `current` translation. Don't change the
   `android`, `ios` or `form` columns: they say where each translation goes, and two rows for the same string
   and form are refused. In a row with an `ios` id and no `android` name, write placeholders as its English does.

4. Write the sheet back, let Xcode bring the iOS strings into the String Catalog, then run the check:

   ```
   go run ./cmd/l10n import -xliff /tmp/l10n/hi.xcloc hi hi.csv
   xcodebuild -importLocalizations -project ios/VehicleTracker/VehicleTracker.xcodeproj -localizationPath /tmp/l10n/hi.xcloc
   go run ./cmd/l10n check
   ```

   A machine or AI draft goes in with `-draft` (`import -draft -xliff …`): each iOS string the sheet fills in is
   then marked in the XLIFF as a machine translation (`state-qualifier="leveraged-mt"`), which Xcode imports as
   `machine_translated`. A fluent reviewer's sheet goes in without it, and each string it fills in is then read.
   To mark a draft read as it is, copy it from the `current` column into `translation`.

   The import writes `values-hi/strings.xml` and fills in the XLIFF in the `.xcloc` folder. It never writes a
   String Catalog itself, because Apple keeps the right to change that format. Xcode may also bring the catalog up
   to date with the code as it exports and imports, which then shows as extra changes in `Localizable.xcstrings`.

What the import does:
- It refuses a sheet whose placeholders or plural forms break the check. It names each row that does, and writes
  nothing.
- It writes both files or neither: when one can't be written, the other is left as it was.
- It leaves each iOS string the sheet doesn't fill in as it is, state and all, so a draft stays one until a sheet
  without `-draft` fills it in.
- It changes in `strings.xml` only what the sheet changes: a string with new text gets it, and one the file lacks
  goes in after the string before it in English. The file's comments, layout and other strings stay as they are,
  so a sheet read back with no edits changes nothing. A language with no `strings.xml` yet gets one in English
  order, without comments.

The sheet's columns:
- **`android`**, **`ios`** and **`form`**: the string's Android name, its iOS id, and the plural form a row is
  for. A plural has a row for each form the language needs. A form only iOS has, such as a zero variation the
  language's plural rules don't list, has a row of its own.
- **`screen`**: where the string shows, from the first word of its Android name.
- **`english`**: the English. A string both apps say alike and translate alike has one row. That row is in
  Android's placeholders (`%1$s`), and the import writes iOS's (`%@`) for the iOS app.
- **`note`**: the developer's note.
- **`placeholders`**: what each value is, such as `#1 (text)`.
- **`glossary`**: the glossary's word for each term the English uses, and the words to avoid.
- **`onebusaway`**: suggestions from other OneBusAway apps.
- **`current`** and **`translation`**: the translation now, and the new one.

The sheet doesn't have screenshots or a length hint for tight places yet. A string's note says where it shows.

## Adding a language

- Its translations, through a sheet as above.
- A row in `status.csv`.
- If its plural rules aren't in `cmd/l10n/plurals.go` yet, a row there, from CLDR's `plurals.xml`.

## The check

```
go run ./cmd/l10n check            # what CI runs on every pull request touching strings
go run ./cmd/l10n check -release   # also fails a language under 95% in either app
```

It exits 1 when a rule below fails, and prints warnings without failing. Each finding names the file, the line,
the string and the language. Under GitHub Actions they show on the pull request's diff, and the job summary has a
coverage table.

Errors:

- **`placeholders`**: a translation takes values its English doesn't, or leaves one out, or on iOS writes a value
  another way than its English (`%s` or `%d` where the English has `%@` or `%lld`).
- **`plurals`**: a plural is missing a form its language needs (English included), or a translation is a single
  string where the English is a plural.
- **`numbers`**: a number in the English isn't in the translation in digits.
- **`urls`**: a web address in the English isn't in the translation unchanged.
- **`do-not-translate`**: a `do_not_translate` term in the English isn't in the translation unchanged.
- **`forbidden`**: the English uses a `forbidden_en` word such as "bus", or a translation uses a forbidden word for a
  term its English uses.
- **`ios-state`**: one of three catalog states:
  - an entry is `stale` (Xcode no longer finds it in the code; delete it);
  - a translation `needs_review` (its English changed);
  - a `reviewed` language has a `machine_translated` string.
- **`status`**: a language with translations has no row in `status.csv`, a status isn't `source`, `reviewed` or
  `draft`, or a `reviewed` language doesn't say who reviewed it and when.
- **`release-coverage`** (with `-release` only): a language has under 95% of either app's strings.

Warnings:

- **`consistency`**: an iOS translation matches none of Android's translations of the same English. Android may
  word one English differently on different screens, and iOS may take any of those wordings.
- **`note`**: an English string takes a value but has no note saying what it is.
