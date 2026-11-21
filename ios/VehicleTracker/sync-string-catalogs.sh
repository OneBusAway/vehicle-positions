#!/usr/bin/env bash
# Brings the String Catalogs in Resources/ up to date with the code, as Xcode extracts it: every String(localized:)
# and Info.plist string, with its comment. Run it and commit Resources/*.xcstrings whenever you add, change or remove
# a string, so translators get it with its comment (#530).
#
# With --check it fails instead when the committed catalogs and the code disagree, which is what CI runs. The check
# compares the strings, their comments and their English, not the bytes, so a different Xcode version that writes
# the same catalog in another layout still passes.
set -euo pipefail
cd "$(dirname "$0")"

check=false
if [ "${1:-}" = "--check" ]; then check=true; fi

work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
cp Resources/*.xcstrings "$work/"

xcodegen generate --quiet
# Xcode builds the app to find its strings and rewrites both catalogs in place; the .xcloc it also writes is not used.
xcodebuild -exportLocalizations -project VehicleTracker.xcodeproj -scheme VehicleTracker \
  -localizationPath "$work/export" -exportLanguage en -quiet

python3 - "$check" "$work" Resources/*.xcstrings <<'PY'
import json, os, sys

check, before_dir, paths = sys.argv[1] == "true", sys.argv[2], sys.argv[3:]

def write(path, catalog):
    with open(path, "w", encoding="utf-8") as f:
        f.write(json.dumps(catalog, ensure_ascii=False, indent=2, sort_keys=True, separators=(",", " : ")) + "\n")

def strip_state(node):
    if isinstance(node, dict):
        return {k: strip_state(v) for k, v in node.items() if k != "state"}
    return node

def meaning(catalog):
    """Each string's comment, whether it is translated, whether Xcode still finds it, and its English."""
    source = catalog.get("sourceLanguage", "en")
    return {
        key: (
            entry.get("comment"),
            entry.get("shouldTranslate", True),
            entry.get("extractionState") == "stale",
            strip_state(entry.get("localizations", {}).get(source)),
        )
        for key, entry in catalog.get("strings", {}).items()
    }

problems = []
for path in paths:
    with open(path, encoding="utf-8") as f:
        catalog = json.load(f)
    if os.path.basename(path) == "InfoPlist.xcstrings":
        # The export copies the app's name in with its build-time value, which would replace an agency's
        # APP_DISPLAY_NAME on the home screen, so it is never kept.
        for key in ("CFBundleDisplayName", "CFBundleName"):
            catalog["strings"].pop(key, None)
        write(path, catalog)
    elif not open(path, "rb").read().endswith(b"\n"):
        with open(path, "a", encoding="utf-8") as f:
            f.write("\n")  # Xcode leaves off the final newline.
    if not check:
        continue
    with open(os.path.join(before_dir, os.path.basename(path)), encoding="utf-8") as f:
        before = meaning(json.load(f))
    after = meaning(catalog)
    for key in sorted(after.keys() - before.keys()):
        problems.append(f"{path}: new in the code, missing from the catalog: {key!r}")
    for key in sorted(before.keys() & after.keys()):
        if before[key] != after[key]:
            what = "no longer in the code" if after[key][2] and not before[key][2] else "its comment or English changed"
            problems.append(f"{path}: {key!r}: {what}")
    for key in sorted(before.keys() - after.keys()):
        problems.append(f"{path}: in the catalog, gone from the export: {key!r}")

if problems:
    for p in problems:
        print(p)
    fix = "Run ios/VehicleTracker/sync-string-catalogs.sh and commit Resources/*.xcstrings."
    print(f"\n{len(problems)} string(s) differ. {fix}")
    if os.environ.get("GITHUB_ACTIONS") == "true":
        print(f"::error::The String Catalogs don't match the code. {fix}")
    sys.exit(1)
PY
