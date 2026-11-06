const fs = require('node:fs');
const path = require('node:path');
const crypto = require('node:crypto');
const { Readable } = require('node:stream');
const { pipeline } = require('node:stream/promises');
const { spawnSync } = require('node:child_process');
const { createRequire } = require('node:module');

const root = path.resolve(__dirname, '..');
const version = '20261104';
const name = `llvm-mingw-${version}-ucrt-x86_64`;
const expectedHash = '552191b5754c49a5a20723276a6fc56541ea1922c4c50b7b669022333fc1b40f';

function packageRequire() {
  const desktopRequire = createRequire(require.resolve('electron-builder/package.json'));
  return createRequire(desktopRequire.resolve('app-builder-lib/package.json'));
}

function sha256(file) {
  return new Promise((resolve, reject) => {
    const digest = crypto.createHash('sha256');
    const stream = fs.createReadStream(file);
    stream.on('data', chunk => digest.update(chunk));
    stream.on('error', reject);
    stream.on('end', () => resolve(digest.digest('hex')));
  });
}

async function ensureToolchain() {
  if (process.platform !== 'win32') throw new Error('The native Windows installer must be built on Windows.');
  const directory = process.env.OCTOPUS_LLVM_MINGW || path.join(root, 'build', 'toolchains', name);
  const compiler = path.join(directory, 'bin', 'x86_64-w64-mingw32-clang++.exe');
  if (fs.existsSync(compiler)) return directory;
  if (process.env.OCTOPUS_LLVM_MINGW) throw new Error(`Compiler not found: ${compiler}`);
  const destination = path.join(root, 'build', 'toolchains');
  fs.mkdirSync(destination, { recursive: true });
  const archive = path.join(destination, `${name}.zip`);
  if (!fs.existsSync(archive)) {
    console.log(`Downloading the pinned LLVM-MinGW toolchain (${version}) into build/toolchains.`);
    const response = await fetch(`https://github.com/mstorsjo/llvm-mingw/releases/download/${version}/${name}.zip`, {
      signal: AbortSignal.timeout(300000),
    });
    if (!response.ok || !response.body) throw new Error(`Toolchain download failed: HTTP ${response.status}`);
    const temporary = `${archive}.part`;
    await pipeline(Readable.fromWeb(response.body), fs.createWriteStream(temporary));
    if (await sha256(temporary) !== expectedHash) throw new Error('Toolchain SHA-256 verification failed.');
    fs.renameSync(temporary, archive);
  }
  if (await sha256(archive) !== expectedHash) throw new Error('Cached toolchain SHA-256 verification failed.');
  const sevenZip = packageRequire()('7zip-bin').path7za;
  const extraction = spawnSync(sevenZip, ['x', archive, `-o${destination}`, '-y', '-bso0', '-bsp0'], {
    stdio: 'inherit', windowsHide: true,
  });
  if (extraction.error) throw extraction.error;
  if (extraction.status !== 0 || !fs.existsSync(compiler)) throw new Error('Toolchain extraction failed.');
  return directory;
}

module.exports = { ensureToolchain, packageRequire, sha256 };
if (require.main === module) ensureToolchain().then(directory => console.log(directory)).catch(error => {
  console.error(error.message); process.exitCode = 1;
});
