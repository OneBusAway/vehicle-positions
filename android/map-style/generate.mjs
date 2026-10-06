// Builds the street map style the driver app draws under the route, and copies the fonts and
// icons it reads, into ../app/src/main/assets/map/. Those files are committed, so building the
// app needs neither Node nor the network. Run this only to change them:
//
//   npm ci
//   npm run generate
//
// Everything is fetched from fixed commits, so running it again writes the same bytes.

import { layers, namedFlavor } from '@protomaps/basemaps';
import { mkdir, rm, writeFile } from 'node:fs/promises';

const OUT = new URL('../app/src/main/assets/map/', import.meta.url);

const BASEMAPS_ASSETS = 'https://raw.githubusercontent.com/protomaps/basemaps-assets/028c18f713baecad011301ff7a69acc39bcc2ae7';
const BASEMAPS_LICENSE = 'https://raw.githubusercontent.com/protomaps/basemaps/f7bcb4869434030ba23271f43e9dbcf6bfc5d63f/LICENSE.md';

const FONT = 'Noto Sans Regular';

// The glyph ranges a label in Latin letters can need: Basic Latin and Latin-1, Latin Extended-A
// and -B, spacing modifiers, combining marks (and Greek), Latin Extended Additional (Vietnamese),
// punctuation and currency, and letterlike symbols and arrows.
const GLYPH_RANGES = [0, 256, 512, 768, 7680, 8192, 8448];

// MapStyle.kt replaces this with the downloaded file's path.
const MAP_FILE = '__MAP_FILE__';

const ATTRIBUTION = '<a href="https://www.openstreetmap.org/copyright">&copy; OpenStreetMap contributors</a>';

// The English name, or the name itself when Protomaps has not marked it as written in another
// script; otherwise no label. A non-Latin script needs a font of its own, several megabytes
// each, so until there is a way to ship one per agency such places go unlabelled rather than
// being drawn with their letters missing.
const LATIN_LABEL = ['case', ['has', 'script'], ['get', 'name:en'], ['coalesce', ['get', 'name:en'], ['get', 'name']]];

const NAME_FIELDS = ['name', 'name:en', 'pgf:name', 'name2', 'pgf:name2', 'name3', 'pgf:name3', 'script'];

function readsNames(expression) {
  const json = JSON.stringify(expression);
  return NAME_FIELDS.some((field) => json.includes(`"${field}"`));
}

function streetStyle(flavor) {
  const style = {
    version: 8,
    glyphs: 'asset://map/fonts/{fontstack}/{range}.pbf',
    sprite: `asset://map/sprites/${flavor}`,
    sources: {
      protomaps: { type: 'vector', url: `pmtiles://file://${MAP_FILE}`, attribution: ATTRIBUTION },
    },
    layers: layers('protomaps', namedFlavor(flavor), { lang: 'en' }),
  };
  for (const layer of style.layers) {
    const layout = layer.layout;
    if (!layout || !('text-field' in layout)) continue;
    if (readsNames(layout['text-field'])) layout['text-field'] = LATIN_LABEL;
    layout['text-font'] = [FONT];
  }
  const json = JSON.stringify(style);
  // Anything left over here is a label the app has no font for.
  for (const leftover of ['Noto Sans Medium', 'Noto Sans Italic', 'Devanagari', 'pgf:']) {
    if (json.includes(leftover)) throw new Error(`style-${flavor}.json still mentions ${leftover}`);
  }
  return json;
}

async function fetchBytes(url) {
  const response = await fetch(url);
  if (!response.ok) throw new Error(`${url}: HTTP ${response.status}`);
  return Buffer.from(await response.arrayBuffer());
}

async function save(path, bytes) {
  const file = new URL(path, OUT);
  await mkdir(new URL('.', file), { recursive: true });
  // Text files end with a newline, as the rest of the repository's do.
  if (/\.(json|md|txt)$/.test(path) && bytes.at(-1) !== 0x0a) bytes = Buffer.concat([bytes, Buffer.from('\n')]);
  await writeFile(file, bytes);
  console.log(`${path} (${bytes.length} bytes)`);
}

await rm(OUT, { recursive: true, force: true });

for (const flavor of ['light', 'dark']) {
  await save(`style-${flavor}.json`, Buffer.from(streetStyle(flavor) + '\n'));
  for (const sheet of [flavor, `${flavor}@2x`]) {
    for (const extension of ['json', 'png']) {
      await save(`sprites/${sheet}.${extension}`, await fetchBytes(`${BASEMAPS_ASSETS}/sprites/v4/${sheet}.${extension}`));
    }
  }
}

for (const start of GLYPH_RANGES) {
  const range = `${start}-${start + 255}.pbf`;
  await save(`fonts/${FONT}/${range}`, await fetchBytes(`${BASEMAPS_ASSETS}/fonts/${encodeURIComponent(FONT)}/${range}`));
}

await save('fonts/OFL.txt', await fetchBytes(`${BASEMAPS_ASSETS}/fonts/OFL.txt`));
await save('LICENSE.md', await fetchBytes(BASEMAPS_LICENSE));
