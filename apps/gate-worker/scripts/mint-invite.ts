// Mint a single-use invite code for the closed-beta gate.
//
// Usage:
//   pnpm mint -- --user alice [--remote]
//
// Generates a 12-char Crockford-base32 code, writes invite:<code> to the
// BETA_INVITES KV namespace via wrangler, and prints the code + a templated
// invite message ready to share. Defaults to local KV (wrangler's miniflare
// store); pass --remote to write to the deployed CF KV namespace.

import { spawnSync } from 'node:child_process';
import { randomBytes } from 'node:crypto';
import { mkdirSync, appendFileSync } from 'node:fs';
import { join, resolve } from 'node:path';

// Crockford base32 — no I, L, O, U; case-insensitive for users typing it.
const ALPHABET = '0123456789ABCDEFGHJKMNPQRSTVWXYZ';
const CODE_LEN = 12;
const APP_BASE_URL = process.env.APP_URL ?? 'https://app.<your-domain>';
const OUTPUT_DIR = resolve(import.meta.dirname, 'output'); // gitignored
const LOG_FILE = join(OUTPUT_DIR, 'minted.log');

function parseArgs(argv: string[]): { user: string; remote: boolean } {
  let user = '';
  let remote = false;
  for (let i = 0; i < argv.length; i++) {
    const a = argv[i];
    if (a === '--') continue; // pnpm sometimes passes `--` through
    if (a === '--user') user = argv[++i] ?? '';
    else if (a === '--remote') remote = true;
    else if (a === '--help' || a === '-h') usage(0);
    else {
      console.error(`unknown arg: ${a}`);
      usage(1);
    }
  }
  if (!user) {
    console.error('--user <label> required');
    usage(1);
  }
  return { user, remote };
}

function usage(code: number): never {
  console.error('usage: pnpm mint -- --user <label> [--remote]');
  process.exit(code);
}

function generateCode(): string {
  const bytes = randomBytes(CODE_LEN);
  let out = '';
  for (let i = 0; i < CODE_LEN; i++) {
    out += ALPHABET[bytes[i] % ALPHABET.length];
  }
  return out;
}

function writeKV(code: string, user: string, remote: boolean): void {
  const value = JSON.stringify({
    user_label: user,
    created_at: Math.floor(Date.now() / 1000),
  });
  // Use the root-installed wrangler with the gitignored config that holds the
  // real KV namespace ID; the committed wrangler.toml only has a placeholder.
  const args = [
    'kv', 'key', 'put',
    '--config', 'wrangler.local.toml',
    '--binding=BETA_INVITES',
    remote ? '--remote' : '--local',
    `invite:${code}`,
    value,
  ];
  const res = spawnSync('../../node_modules/.bin/wrangler', args, { stdio: 'inherit' });
  if (res.status !== 0) {
    console.error('wrangler kv put failed');
    process.exit(res.status ?? 1);
  }
}

function logMint(code: string, user: string, remote: boolean): void {
  mkdirSync(OUTPUT_DIR, { recursive: true });
  const line = `${new Date().toISOString()}\t${remote ? 'remote' : 'local'}\t${user}\t${code}\n`;
  appendFileSync(LOG_FILE, line);
}

function main(): void {
  const { user, remote } = parseArgs(process.argv.slice(2));
  const code = generateCode();
  writeKV(code, user, remote);
  logMint(code, user, remote);

  const target = remote ? 'production KV' : 'local KV (miniflare)';
  console.log('');
  console.log(`✓ Minted invite for "${user}" in ${target}`);
  console.log('');
  console.log('────────────────────────────────────────');
  console.log(`Code: ${code}`);
  console.log('────────────────────────────────────────');
  console.log('');
  console.log('Share this message:');
  console.log('');
  console.log(`  Welcome to Orbital Markets — closed beta.`);
  console.log(`  Visit ${APP_BASE_URL} and enter:`);
  console.log(`      ${code}`);
  console.log('');
  console.log(`Logged to ${LOG_FILE.replace(process.cwd() + '/', '')}`);
}

main();
