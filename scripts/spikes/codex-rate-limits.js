#!/usr/bin/env node
'use strict';
/*
 * THROW-AWAY spike for #142: read the Codex quotas from `codex app-server` (account/rateLimits/read).
 *
 * Speaks JSON-RPC (one JSON object per line) over the stdio of the unmodified `codex` binary, which uses the
 * user's own login in ~/.codex. Never reads auth.json. Not part of the build, lint or tests.
 * Checked against codex-cli 0.163.0 (Linux).
 * Usage: node scripts/spikes/codex-rate-limits.js [--raw] [--bin PATH] [--repeat N] [--timeout MS]
 *   --raw     print the full response (values included) instead of the summary
 *   --repeat  N reads on the SAME process (shows the cost of keeping it alive)
 */
const { spawn } = require('node:child_process');
const readline = require('node:readline');

const args = process.argv.slice(2);
const flag = (name, fallback) => {
  const i = args.indexOf(name);
  return i >= 0 ? args[i + 1] : fallback;
};
const bin = flag('--bin', process.platform === 'win32' ? 'codex.cmd' : 'codex');
const repeat = Number(flag('--repeat', '1'));
const timeoutMs = Number(flag('--timeout', '15000'));
const raw = args.includes('--raw');

function probe() {
  const t0 = process.hrtime.bigint();
  // shell: true is only needed for the codex.cmd shim on Windows (not tested here).
  const child = spawn(bin, ['app-server'], { stdio: ['pipe', 'pipe', 'pipe'], shell: process.platform === 'win32' });
  const pending = new Map();
  let nextId = 1;
  const send = (msg) => child.stdin.write(JSON.stringify(msg) + '\n');
  const call = (method, params) =>
    new Promise((resolve, reject) => {
      const id = nextId++;
      const timer = setTimeout(() => reject(new Error(`timeout on ${method}`)), timeoutMs);
      pending.set(id, { resolve, reject, timer });
      send({ id, method, params });
    });
  readline.createInterface({ input: child.stdout }).on('line', (line) => {
    let msg;
    try {
      msg = JSON.parse(line);
    } catch {
      return;
    }
    const p = typeof msg.id === 'number' ? pending.get(msg.id) : undefined;
    if (!p) return;
    pending.delete(msg.id);
    clearTimeout(p.timer);
    if (msg.error) p.reject(new Error(`${msg.error.code}: ${msg.error.message}`));
    else p.resolve(msg.result);
  });
  child.stderr.resume();
  const spawnFailed = new Promise((_, reject) => child.on('error', reject));
  const exited = new Promise((_, reject) => child.on('exit', (c) => reject(new Error(`codex exited early (${c})`))));

  const run = async () => {
    const out = { reads: [] };
    await call('initialize', { clientInfo: { name: 'ia-hypermiler-spike', title: null, version: '0.0.0' } });
    send({ method: 'initialized' });
    out.initMs = Number(process.hrtime.bigint() - t0) / 1e6;
    out.account = await call('account/read', { refreshToken: false });
    for (let i = 0; i < repeat; i++) {
      const t1 = process.hrtime.bigint();
      out.reads.push({ result: await call('account/rateLimits/read', null), ms: Number(process.hrtime.bigint() - t1) / 1e6 });
    }
    return out;
  };
  return Promise.race([run(), spawnFailed, exited])
    .then((out) => ({ out, child, t0 }))
    .finally(() => child.kill());
}

const fmtWindow = (w) =>
  w ? `${w.usedPercent}% used, window ${w.windowDurationMins ?? '?'} min, resets ${w.resetsAt ? new Date(w.resetsAt * 1000).toISOString() : '?'}` : 'none';

(async () => {
  const cpu0 = process.cpuUsage();
  const { out, t0 } = await probe();
  const wall = Number(process.hrtime.bigint() - t0) / 1e6;
  if (raw) {
    console.log(JSON.stringify(out, null, 1));
    return;
  }
  const acct = out.account.account;
  console.log('account:', acct ? acct.type : 'none (requiresOpenaiAuth=' + out.account.requiresOpenaiAuth + ')', acct && acct.planType ? `plan=${acct.planType}` : '');
  const buckets = out.reads[0].result.rateLimitsByLimitId ?? { [out.reads[0].result.rateLimits.limitId ?? 'default']: out.reads[0].result.rateLimits };
  for (const [id, b] of Object.entries(buckets)) {
    console.log(`[${id}] ${b.limitName ?? ''} plan=${b.planType}`);
    console.log('  primary  :', fmtWindow(b.primary));
    console.log('  secondary:', fmtWindow(b.secondary));
    if (b.credits) console.log('  credits  :', JSON.stringify({ ...b.credits, balance: b.credits.balance ? '<redacted>' : b.credits.balance }));
  }
  console.log(`init ${out.initMs.toFixed(0)} ms, reads ms: ${out.reads.map((r) => r.ms.toFixed(0)).join(', ')}, wall ${wall.toFixed(0)} ms`);
  const c = process.cpuUsage(cpu0);
  console.log(`spike process CPU ${((c.user + c.system) / 1000).toFixed(0)} ms (codex child CPU: use /usr/bin/time -v)`);
})().catch((e) => {
  console.error('probe failed:', e.message);
  process.exit(1);
});
