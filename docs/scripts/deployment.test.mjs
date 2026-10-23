import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const site = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
const repo = path.resolve(site, '..');

// Tests run against the committed workflow and Vercel project configuration.
test('filtered GitHub pushes trigger deployment only for actual site inputs', () => {
  const workflow = readFileSync(path.join(repo, '.github/workflows/docs-deploy.yml'), 'utf8');
  for (const event of ['push', 'pull_request']) {
    const block = workflow.match(new RegExp('^  ' + event + ':\\n([\\s\\S]*?)(?=^  (?:push|pull_request|workflow_dispatch):|^permissions:)', 'm'))?.[1];
    assert.ok(block, 'missing event: ' + event);
    assert.match(block, /branches: \[main\]/);
    const found = [...block.matchAll(/^      - "([^"]+)"$/gm)].map(m => m[1]);
    assert.deepEqual(found, ["docs/**"]);
  }
  assert.match(workflow, /secrets\.VERCEL_TOKEN/);
  assert.match(workflow, /vars\.VERCEL_ORG_ID/);
  assert.match(workflow, /vars\.VERCEL_PROJECT_ID/);
  assert.match(workflow, /vercel@latest deploy --prebuilt --prod/);
  assert.match(workflow, /working-directory: \./);
  assert.match(workflow, /prepare-prebuilt\.mjs/);
});

test('only Actions creates Vercel deployments', () => {
  const config = JSON.parse(readFileSync(path.join(repo, 'docs/vercel.json'), 'utf8'));
  assert.equal(config.git.deploymentEnabled, false);
  assert.equal(Object.hasOwn(config, 'ignoreCommand'), false);
});
