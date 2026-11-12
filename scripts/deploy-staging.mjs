import { spawnSync } from 'node:child_process';
import { existsSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';

const root = dirname(dirname(fileURLToPath(import.meta.url)));
const api = join(root, 'apps/api');
const flyConfig = join(api, 'fly.staging.local.toml');
const workerConfig = join(root, 'apps/gate-worker/wrangler.staging.local.jsonc');
const wrangler = join(root, 'node_modules/.bin/wrangler');
const args = process.argv.slice(2);
if (args.some(arg => arg !== '--dry-run')) {
  console.error('Usage: pnpm deploy:staging [--dry-run]');
  process.exit(1);
}
const dryRun = args.includes('--dry-run');

for (const config of [flyConfig, workerConfig]) {
  if (!existsSync(config)) {
    console.error(`Missing ${config}. Copy the corresponding staging example and configure your resources; see apps/gate-worker/README.md.`);
    process.exit(1);
  }
}

function run(command, commandArgs, options = {}) {
  const result = spawnSync(command, commandArgs, {
    cwd: root,
    stdio: 'inherit',
    ...options,
  });
  if (result.error) console.error(result.error.message);
  if (result.error || result.status !== 0) process.exit(result.status || 1);
}

const revision = spawnSync('git', ['rev-parse', '--short', 'HEAD'], { cwd: root, encoding: 'utf8' });
const changes = spawnSync('git', ['status', '--porcelain'], { cwd: root, encoding: 'utf8' });
console.log(`Staging source: ${revision.stdout?.trim() || 'unknown'}${changes.stdout?.trim() ? ' (with working-tree changes)' : ''}`);

// Finish local validation/builds before changing either remote deployment.
run('flyctl', ['config', 'validate', '--config', flyConfig], { cwd: api });
run('pnpm', ['--dir', 'apps/gate-worker', 'typecheck']);
run('pnpm', ['--dir', 'apps/web', 'build', '--mode', 'staging'], {
  env: { ...process.env, VITE_API_URL: '', VITE_POSTHOG_KEY: '' },
});
run(wrangler, ['deploy', '--dry-run', '--config', workerConfig]);

if (dryRun) {
  console.log('Staging validation complete. No remote build, migration or deployment performed.');
  process.exit(0);
}

run(wrangler, ['d1', 'migrations', 'apply', 'WAITLIST_DB', '--remote', '--config', workerConfig]);
run('flyctl', ['deploy', '--config', flyConfig, '--remote-only', '--ha=false', '--yes'], { cwd: api });
run(wrangler, ['deploy', '--config', workerConfig]);
console.log('Staging API and frontend deployed.');
