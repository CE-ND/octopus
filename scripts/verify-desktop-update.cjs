const fs = require('node:fs');
const path = require('node:path');
const crypto = require('node:crypto');
const assert = require('node:assert/strict');
const { createRequire } = require('node:module');
const { spawnSync } = require('node:child_process');
const updaterRequire = createRequire(require.resolve('electron-updater/package.json'));
const yaml = updaterRequire('js-yaml');
const semver = updaterRequire('semver');
const packageJson = require('../package.json');

async function verifyDesktopUpdate(directory) {
  const resources = path.join(directory, 'win-unpacked', 'resources');
  const feed = yaml.load(fs.readFileSync(path.join(resources, 'app-update.yml'), 'utf8'));
  for (const key of ['provider', 'owner', 'repo']) assert.equal(feed[key], packageJson.build.publish[key], `Packaged update feed: ${key}`);
  const channel = semver.prerelease(packageJson.version)?.[0] || 'latest';
  const manifest = yaml.load(fs.readFileSync(path.join(directory, `${channel}.yml`), 'utf8'));
  const installer = `Octopus-${packageJson.version}-windows-x64-setup.exe`;
  assert.equal(manifest.version, packageJson.version);
  assert.equal(manifest.path, installer);
  const digest = crypto.createHash('sha512');
  for await (const chunk of fs.createReadStream(path.join(directory, installer))) digest.update(chunk);
  const hash = digest.digest('base64');
  const entry = manifest.files.find(file => file.url === installer);
  assert(entry, 'Update manifest must reference the Windows NSIS installer');
  assert.equal(entry.sha512, hash);
  assert.equal(manifest.sha512, hash);
  assert.equal(entry.size, fs.statSync(path.join(directory, installer)).size);
  assert(fs.statSync(path.join(directory, `${installer}.blockmap`)).size > 0);

  // 用打包后的 Electron 在 Node 模式加载 ASAR 内的依赖；不会启动 Octopus 或安装更新。
  const modulePath = path.join(resources, 'app.asar', 'node_modules', 'electron-updater');
  const code = `const updater = require(${JSON.stringify(modulePath)}); if (typeof updater.NsisUpdater !== 'function') throw new Error('Packaged updater is unavailable');`;
  const result = spawnSync(path.join(directory, 'win-unpacked', 'Octopus.exe'), ['-e', code], {
    env: { ...process.env, ELECTRON_RUN_AS_NODE: '1' },
    windowsHide: true, encoding: 'utf8', timeout: 15000,
  });
  if (result.error) throw result.error;
  assert.equal(result.status, 0, result.stderr || 'Packaged updater dependency failed to load');
  console.log('Packaged update feed, runtime dependency, NSIS manifest, SHA-512 and blockmap verified.');
}

verifyDesktopUpdate(path.resolve(process.argv[2] || 'dist/desktop')).catch(error => { console.error(error.message); process.exitCode = 1; });
