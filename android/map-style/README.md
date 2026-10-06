# Street map style

`generate.mjs` builds the street map the Android app draws under the route, and
writes it to `android/app/src/main/assets/map/` with the fonts and icons it
reads. Those files are committed, so building the app needs neither Node nor
the network. Run this only to change them, with Node 18 or newer:

```bash
npm ci
npm run generate
```

What it writes:

- `style-light.json` and `style-dark.json`, from
  [`@protomaps/basemaps`](https://github.com/protomaps/basemaps) 5.7.2 with
  `lang: 'en'`, changed in two ways: every label is drawn in one font, Noto
  Sans Regular, and names in Latin letters only (below). The tile source reads
  `pmtiles://file://__MAP_FILE__`; the app puts the downloaded file's path
  there (`MapStyle.kt`).
- `fonts/Noto Sans Regular/`: the 7 glyph ranges a label in Latin letters can
  need, 0.66 MB.
- `sprites/`: the light and dark icon sheets, at 1x and 2x.
- `LICENSE.md` (the style is BSD-3-Clause, its design CC0, its icons MIT) and
  `fonts/OFL.txt` (the SIL Open Font License), which have to ship with them.

Everything is fetched from fixed commits of
[`protomaps/basemaps-assets`](https://github.com/protomaps/basemaps-assets) and
`protomaps/basemaps`, so a second run writes the same bytes.

**Why Latin letters only.** The style Protomaps generates for English falls
back to a place's local name, in its own script, when it has no English name.
Each script needs its own font: Devanagari alone is 6.6 MB. Rather than ship
every one, or draw labels with their letters missing, the app draws the English
name, or the local name when it is written in Latin letters, and leaves other
places unlabelled. Labels in local scripts are a later step.

The map files themselves are each agency's own: see section 12.6 of
`docs/deployment.md`.
