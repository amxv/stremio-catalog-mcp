import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const docsRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
const page = readFileSync(path.join(docsRoot, 'src/pages/index.astro'), 'utf8');
const css = readFileSync(path.join(docsRoot, 'src/styles/landing.css'), 'utf8');

test('landing page uses SVGs, not Unicode symbols that render as colored emoji on iOS', () => {
  assert.match(page, /<Asterisk class="cta-decoration"\s*\/>/);
  assert.match(page, /<ArrowUpRight\s*\/>/);
  assert.doesNotMatch(page, /✳|↗/, 'use vector decorations and arrow icons instead of emoji-prone glyphs');
});

test('decorative background stays behind CTA content and disappears on small screens', () => {
  assert.match(css, /\.cta-band > div\s*\{[^}]*z-index:\s*1/);
  assert.match(css, /\.cta-decoration\s*\{[^}]*pointer-events:\s*none/);
  assert.match(css, /@media\s*\(max-width:\s*650px\)[\s\S]*?\.cta-band \.cta-decoration\s*\{\s*display:\s*none;/);
});
