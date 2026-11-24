import { test } from 'node:test';
import assert from 'node:assert/strict';
import { mkdtempSync, writeFileSync, readFileSync, chmodSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { dirname, join, resolve } from 'node:path';
import { spawnSync } from 'node:child_process';

for (const [failures, expectedStatus, expectedAttempts, expectedDelays] of [
 [0, 0, 1, []], [2, 0, 3, ['5', '10']], [99, 1, 4, ['5', '10', '20']]
]) test(`module download: ${failures} failures, bounded retries and preserved errors`, () => {
 const dir = mkdtempSync(join(tmpdir(), 'calnode-go-download-'));
 try {
  const state = join(dir, 'attempts').replaceAll('\\', '/');
  const log = join(dir, 'calls').replaceAll('\\', '/');
  const delays = join(dir, 'delays').replaceAll('\\', '/');
  const go = `#!/bin/sh\nn=0; if [ -f "$TEST_STATE" ]; then read -r n < "$TEST_STATE"; fi\nn=$((n+1)); echo "$n" > "$TEST_STATE"\necho "$*|$GODEBUG" >> "$TEST_LOG"\n[ "$n" -gt "$TEST_FAILURES" ]\n`;
  writeFileSync(join(dir, 'go'), go); chmodSync(join(dir, 'go'), 0o755);
  writeFileSync(join(dir, 'sleep'), '#!/bin/sh\necho "$1" >> "$TEST_DELAYS"\n'); chmodSync(join(dir, 'sleep'), 0o755);
  const shell = process.platform === 'win32' ? 'C:/Program Files/Git/bin/bash.exe' : 'sh';
  // Shell constructs its PATH to avoid MSYS rewriting a Windows PATH list.
  const result = spawnSync(shell, ['-c', 'PATH="$TEST_BIN:$PATH"; export PATH; exec sh "$TEST_SCRIPT"'], {
   encoding: 'utf8', env: { ...process.env, TEST_BIN: dir.replaceAll('\\', '/').replace(/^([A-Za-z]):\//, (_, drive) => '/'+drive.toLowerCase()+'/'), TEST_SCRIPT: resolve('scripts/download-go-modules.sh').replaceAll('\\', '/'),
    TEST_STATE: state, TEST_LOG: log, TEST_DELAYS: delays, TEST_FAILURES: String(failures), GODEBUG: 'toolchaintrace=1' }
  });
  assert.ifError(result.error); assert.equal(result.status, expectedStatus, result.stderr);
  assert.equal(Number(readFileSync(state, 'utf8').trim()), expectedAttempts);
  const calls = readFileSync(log, 'utf8').trim().split('\n');
  assert.ok(calls.every(line => line === 'mod download|toolchaintrace=1,http2client=0'));
  if (expectedDelays.length) assert.deepEqual(readFileSync(delays, 'utf8').trim().split('\n'), expectedDelays);
  if (expectedStatus) assert.match(result.stderr, /failed after four attempts/);
 } finally { assert.equal(dirname(resolve(dir)), resolve(tmpdir())); rmSync(dir, { recursive: true, force: true }); }
});
