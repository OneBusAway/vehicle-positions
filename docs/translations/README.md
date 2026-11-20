# Translating the driver apps

This folder holds what the Android and iOS driver apps' translations are checked against. The plan they follow is
proposal #495; until the maintainer has agreed to it, the apps ship in English only, and the workflow here may change.

## Where the strings live

- **Android:** `android/app/src/main/res/values/strings.xml` is the English. Each language has its own
  `values-<language>/strings.xml` with the same names: `values-hi`, `values-pt-rBR`, `values-b+zh+Hans`.
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
  A plural form may leave out the count, as in "one report".
- Give a plural every form your language needs, by the Unicode CLDR rules in `cmd/l10n/plurals.go`. Hindi and
  Swahili need `one` and `other`. Arabic needs six forms, and Polish, Russian and Ukrainian need `few` and `many` as
  well. In Spanish, French, Italian and Portuguese, `many` is used only for exact millions, so it can repeat `other`.
- Write numbers in digits, in your language's own digits if you like (`२४` for 24), never as words.
- Keep web addresses and the glossary's `do_not_translate` terms exactly as they are.
- Use the glossary's word for each term, and none of its forbidden ones.
- A machine or AI draft is a start, not a translation. Say in the pull request which strings were drafted. On iOS,
  leave Xcode's `machine_translated` state until someone fluent has read them. The language stays `draft` until a
  fluent reviewer has read every string in context and approved the pull request.

## Adding a language

Until the export and import tool lands, add the files by hand:
- `values-<language>/strings.xml` for Android.
- The language in the String Catalog for iOS, through Xcode.
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

- **`placeholders`**: a translation takes values its English doesn't, or leaves one out.
- **`plurals`**: a plural is missing a form its language needs (English included).
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

- **`consistency`**: the two apps translate the same English differently.
- **`note`**: an English string takes a value but has no note saying what it is.
