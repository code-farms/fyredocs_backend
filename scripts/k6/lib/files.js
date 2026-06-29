// Fixture loading. open() is init-context only, so all binaries are read once
// at module load. Missing fixtures are skipped (try/catch) so a partial set
// still works; a scenario that needs a missing category fails loudly via pickFile.
import { SIZE_CLASSES } from '../config.js';
import { weightedPick } from './util.js';

const DIR = '../fixtures/out';

// category -> { ext, contentType }
const META = {
  pdf:           { ext: 'pdf',  contentType: 'application/pdf' },
  'scanned-pdf': { ext: 'pdf',  contentType: 'application/pdf' },
  docx:          { ext: 'docx', contentType: 'application/vnd.openxmlformats-officedocument.wordprocessingml.document' },
  xlsx:          { ext: 'xlsx', contentType: 'application/vnd.openxmlformats-officedocument.spreadsheetml.sheet' },
  pptx:          { ext: 'pptx', contentType: 'application/vnd.openxmlformats-officedocument.presentationml.presentation' },
  image:         { ext: 'png',  contentType: 'image/png' },
  html:          { ext: 'html', contentType: 'text/html' },
};

const SIZES = ['small', 'medium', 'large'];

// registry[category][size] = { data: ArrayBuffer, filename, contentType }
const registry = {};
for (const [cat, meta] of Object.entries(META)) {
  registry[cat] = {};
  for (const size of SIZES) {
    const path = `${DIR}/${cat}/${size}.${meta.ext}`;
    try {
      // eslint-disable-next-line no-undef
      const data = open(path, 'b');
      registry[cat][size] = { data, filename: `${size}.${meta.ext}`, contentType: meta.contentType };
    } catch (_e) {
      // fixture not generated for this (category,size) — leave absent
    }
  }
}

// Pick a file for a category. If sizeName omitted, weighted-pick a size class
// (falling back to any available size for that category).
export function pickFile(category, sizeName) {
  const cat = registry[category];
  if (!cat) throw new Error(`fixture category '${category}' unknown`);
  let size = sizeName;
  if (!size) size = weightedPick(SIZE_CLASSES).name;
  let f = cat[size];
  if (!f) {
    // requested size not generated — use whatever exists for this category
    const avail = SIZES.map((s) => cat[s]).filter(Boolean);
    if (avail.length === 0) {
      throw new Error(`no fixtures for category '${category}'. Run fixtures/generate.sh`);
    }
    f = avail[0];
  }
  return f;
}
