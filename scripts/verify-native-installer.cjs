const fs = require('node:fs');
const path = require('node:path');
const assert = require('node:assert/strict');
const { spawn, spawnSync } = require('node:child_process');
const { sha256 } = require('./installer-toolchain.cjs');

const root = path.resolve(__dirname, '..');
const wait = ms => new Promise(resolve => setTimeout(resolve, ms));

async function main() {
  const pkg = JSON.parse(fs.readFileSync(path.join(root, 'package.json'), 'utf8'));
  const executable = process.argv[2] ? path.resolve(process.argv[2]) : path.join(root, 'dist', 'installer-ui', `Octopus Custom Setup ${pkg.version}.exe`);
  const metadata = JSON.parse(fs.readFileSync(`${executable}.json`, 'utf8'));
  const directory = path.join(root, 'build', 'native-installer', 'verification', Date.now().toString());
  fs.mkdirSync(directory, { recursive: true });
  assert.equal(metadata.version, pkg.version);
  const timings = [];
  // Each probe creates a real visible window without activating it. Preview
  // mode is forced by --smoke-test, so none of these launches can install.
  for (const scale of [1, 1, 1.5, 2]) {
    const report = path.join(directory, `startup-${timings.length + 1}.json`);
    const started = performance.now();
    const child = spawn(executable, ['--smoke-test', report, '--preview-scale', String(scale)], { stdio: 'ignore', windowsHide: false });
    let spawnError;
    child.once('error', error => { spawnError = error; });
    const exited = new Promise(resolve => child.once('exit', (code, signal) => resolve({ code, signal })));
    let result;
    for (let attempt = 0; attempt < 600; ++attempt) {
      if (spawnError) throw spawnError;
      if (fs.existsSync(report)) {
        try { result = JSON.parse(fs.readFileSync(report, 'utf8')); } catch { /* Writer has not closed the report yet. */ }
        if (result) break;
      }
      if (child.exitCode !== null) break;
      await wait(25);
    }
    if (!result) {
      child.kill();
      throw new Error(`Native installer did not render a window: requested scale ${scale}`);
    }
    const launchToPaintMs = Math.round(performance.now() - started);
    const status = await exited;
    assert.equal(status.code, 0);
    assert.equal(result.background_loaded, true);
    assert.equal(result.visible, true);
    assert.equal(result.payload_bytes, metadata.payloadBytes);
    timings.push({ requestedScale: scale, launchToPaintMs, ...result });
  }
  const selfTest = path.join(directory, 'self-test.txt');
  const checked = spawnSync(executable, ['--self-test', selfTest], { windowsHide: true, encoding: 'utf8', timeout: 30000 });
  if (checked.error) throw checked.error;
  assert.equal(checked.status, 0);
  const checks = fs.readFileSync(selfTest, 'utf8');
  assert(!checks.includes('FAIL '));
  if (!metadata.preview) assert(checks.includes('PASS embedded payload SHA-256'));
  assert.equal(await sha256(executable), metadata.sha256);
  const summary = { executable, bytes: metadata.bytes, payloadBytes: metadata.payloadBytes, version: metadata.version,
    timings, selfTests: checks.split(/\r?\n/).filter(line => line.startsWith('PASS ')),
    scope: 'Preview-only process launch to first visible paint; no installation performed.' };
  fs.writeFileSync(path.join(directory, 'results.json'), JSON.stringify(summary, null, 2));
  fs.writeFileSync(path.join(root, 'build', 'native-installer', 'latest-verification.json'), JSON.stringify(summary, null, 2));
  console.log(JSON.stringify(summary, null, 2));
}

main().catch(error => { console.error(error.stack || error.message); process.exitCode = 1; });
