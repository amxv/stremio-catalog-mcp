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

test('TV docs are opt-in and guide an agent through Vercel, ADB and real-device verification', () => {
  const guide = readFileSync(path.join(site, 'src/content/docs/tv-remote.md'), 'utf8');
  const quickstart = readFileSync(path.join(site, 'src/content/docs/quickstart.md'), 'utf8');
  for (const phrase of [
    'TV_OPEN_ENABLED', 'five tools', '**six**', 'tv_open_title', 'TV_SETUP_KEY',
    'FCM_SERVICE_ACCOUNT_JSON', 'firebase login', 'gcloud auth login',
    'Spark', '/tv/setup', 'android-tv/app/google-services.json',
    'adb pair', 'adb connect', 'adb devices -l', 'install -r',
    'uiautomator dump', 'SYSTEM_ALERT_WINDOW allow', 'adb disconnect',
    'accepted_by_fcm', 'Break' + 'ing Bad', 'Inception',
    'delivery_status', 'launch_requested', 'notification_posted',
    'not_confirmed', 'season', 'episode', 'command_id',
  ]) {
    assert.ok(guide.includes(phrase), `optional TV agent guide missing ${phrase}`);
  }
  assert.match(guide, /exactly one additional MCP tool/i);
  assert.match(guide, /No.*MCP.*key.*TV/i);
  assert.match(guide, /do not.*bill|no paid|billing/i);
  assert.match(quickstart, /If you don't want TV control/);
  const api = JSON.parse(readFileSync(path.join(repo, 'vercel.json'), 'utf8'));
  assert.ok(api.rewrites.some(route => route.source === '/tv/setup'));
  assert.ok(api.rewrites.some(route => route.source === '/tv/admin'));
  assert.ok(api.rewrites.some(route => route.source === '/tv/device/receipt'));
});

test('the MCP tools reference covers all seven tools with valid fenced JSON examples', () => {
  const guide = readFileSync(path.join(site, 'src/content/docs/tools.md'), 'utf8');
  const names = [...guide.matchAll(/^## \d+\. `([a-z_]+)`:/gm)].map((m) => m[1]);
  assert.deepEqual(names, [
    'title_lookup', 'catalogs_get', 'catalogs_update',
    'home_layout_get', 'home_layout_update', 'watch_history', 'tv_open_title',
  ]);
  for (const name of names) {
    const start = guide.indexOf('`' + name + '`:', guide.indexOf('## '));
    assert.ok(start > 0, `missing ${name} reference section`);
    const body = guide.slice(start, guide.indexOf('\n## ', start) < 0 ? undefined : guide.indexOf('\n## ', start));
    assert.match(body, /\*\*Input schema/);
    assert.match(body, /\*\*Call/);
    assert.match(body, /\*\*Example .*response/);
  }
  const blocks = [...guide.matchAll(/```json\n([\s\S]*?)\n```/g)].map((m) => m[1]);
  assert.ok(blocks.length >= 14, 'each of seven tools needs a JSON call and result');
  for (const [i, block] of blocks.entries()) {
    assert.doesNotThrow(() => JSON.parse(block), `invalid JSON code example ${i + 1}`);
  }
  assert.match(guide, /TMDB_READ_ACCESS_TOKEN/);
  assert.match(guide, /TV_OPEN_ENABLED=true/);
  assert.match(guide, /IMDb ratings come from Cinemeta/);
});
