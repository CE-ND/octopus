const fs = require('node:fs');
const path = require('node:path');
const crypto = require('node:crypto');
const { createRequire } = require('node:module');
const updaterRequire = createRequire(require.resolve('electron-updater/package.json'));
const yaml = updaterRequire('js-yaml');
const semver = updaterRequire('semver');

async function sha512(file) {
  const hash = crypto.createHash('sha512');
  for await (const chunk of fs.createReadStream(file)) hash.update(chunk);
  return hash.digest('base64');
}

async function collectReleaseAssets({ sourceDirs, destination, version, includeMac = true }) {
  version = version.replace(/^v/, '');
  if (!semver.valid(version)) throw new Error('A valid release version is required');
  const files = new Map();
  for (const directory of sourceDirs) {
    for (const item of fs.readdirSync(directory, { withFileTypes: true })) {
      if (!item.isFile()) continue;
      if (files.has(item.name)) throw new Error(`Duplicate release asset: ${item.name}`);
      files.set(item.name, path.join(directory, item.name));
    }
  }
  const requireAsset = name => {
    const file = files.get(name);
    if (!file || fs.statSync(file).size === 0) throw new Error(`Missing release asset: ${name}`);
    return file;
  };
  const nativeName = [`Octopus Custom Setup ${version}.exe`, `Octopus.Custom.Setup.${version}.exe`].find(name => files.has(name));
  if (!nativeName) throw new Error('Missing native Windows setup');
  const engineName = `Octopus-${version}-windows-x64-setup.exe`;
  const channel = semver.prerelease(version)?.[0] || 'latest';
  const manifestName = `${channel}.yml`;
  const engine = requireAsset(engineName);
  const manifest = yaml.load(fs.readFileSync(requireAsset(manifestName), 'utf8'));
  const expectedHash = await sha512(engine);
  const entry = manifest?.files?.find(file => file.url === engineName);
  if (manifest?.version !== version || manifest.path !== engineName || !entry || manifest.files.length !== 1) {
    throw new Error('The Windows update manifest must reference this release and its NSIS installer');
  }
  if (entry.sha512 !== expectedHash || manifest.sha512 !== expectedHash || entry.size !== fs.statSync(engine).size) {
    throw new Error('The Windows update manifest checksum or size does not match the installer');
  }
  const selected = [nativeName, engineName, `${engineName}.blockmap`, manifestName];
  if (includeMac) selected.push(`Octopus-${version}-mac-arm64.dmg`, `Octopus-${version}-mac-x64.dmg`);
  // 先验证完整集合再复制，禁止发布只有安装包、没有更新描述的半成品。
  for (const name of selected) {
    const file = requireAsset(name);
    if (/\.(exe|dmg)$/.test(name) && fs.statSync(file).size < 1000000) throw new Error(`Installer is unexpectedly small: ${name}`);
  }
  fs.mkdirSync(destination, { recursive: true });
  const published = selected.map(name => name === nativeName ? `Octopus.Custom.Setup.${version}.exe` : name);
  for (let index = 0; index < selected.length; index++) {
    fs.copyFileSync(files.get(selected[index]), path.join(destination, published[index]), fs.constants.COPYFILE_EXCL);
  }
  return published;
}

if (require.main === module) {
  const root = path.resolve(__dirname, '..');
  const windows = process.argv[2] === 'windows';
  if (!windows && process.argv[2] !== 'all') throw new Error('Usage: node scripts/collect-release-assets.cjs windows|all');
  collectReleaseAssets({
    sourceDirs: windows ? [path.join(root, 'dist', 'desktop'), path.join(root, 'dist', 'installer-ui')] : [path.join(root, 'dist', 'release')],
    destination: path.join(root, 'dist', windows ? 'release-windows' : 'publish'),
    version: process.env.RELEASE_VERSION || require('../package.json').version,
    includeMac: !windows,
  }).then(files => { for (const file of files) console.log(file); }).catch(error => { console.error(error.message); process.exitCode = 1; });
}

module.exports = { collectReleaseAssets };
