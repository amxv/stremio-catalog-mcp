import test from 'node:test';
import assert from 'node:assert/strict';
import { existsSync, mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import path from 'node:path';
import { preparePrebuilt } from './prepare-prebuilt.mjs';

test('prebuilt output contains Astro files, route mapping, and no raw source tree', (t) => {
  const root = mkdtempSync(path.join(tmpdir(), 'docs-prebuilt-'));
  t.after(() => rmSync(root, { recursive: true, force: true }));
  mkdirSync(path.join(root, 'dist', 'docs', 'quickstart'), { recursive: true });
  writeFileSync(path.join(root, 'dist', 'index.html'), '<html>Home</html>');
  writeFileSync(path.join(root, 'dist', 'docs', 'quickstart', 'index.html'), 'Quickstart');
  preparePrebuilt(root);
  assert.equal(readFileSync(path.join(root, '.vercel/output/static/index.html'), 'utf8'), '<html>Home</html>');
  assert.equal(readFileSync(path.join(root, '.vercel/output/static/docs/quickstart/index.html'), 'utf8'), 'Quickstart');
  const config = JSON.parse(readFileSync(path.join(root, '.vercel/output/config.json'), 'utf8'));
  assert.equal(config.version, 3);
  assert.deepEqual(config.routes[0], { handle: 'filesystem' });
  assert.ok(config.routes.some(({ src, dest }) => src === '/(.*)/' && dest === '/$1/index.html'));
  assert.equal(existsSync(path.join(root, '.vercel/output/static/src')), false);
});

test('prebuilt output fails closed if the Astro build is missing', (t) => {
  const root = mkdtempSync(path.join(tmpdir(), 'docs-prebuilt-empty-'));
  t.after(() => rmSync(root, { recursive: true, force: true }));
  assert.throws(() => preparePrebuilt(root), /Astro dist\/index.html is missing/);
});
