const fs = require('node:fs');
const path = require('node:path');
const { spawnSync } = require('node:child_process');
const { ensureToolchain, sha256 } = require('./installer-toolchain.cjs');

const root = path.resolve(__dirname, '..');
const nativeRoot = path.join(root, 'installer-native');

function options(argv) {
  const result = {};
  for (let i = 0; i < argv.length; ++i) {
    const flag = argv[i];
    if (flag === '--preview') result.preview = true;
    else if (['--payload', '--app-dir', '--app-executable', '--product-directory', '--guid', '--out'].includes(flag)) {
      if (!argv[i + 1] || argv[i + 1].startsWith('--')) throw new Error(`Missing value for ${flag}`);
      result[flag.slice(2)] = argv[++i];
    } else throw new Error(`Unknown argument ${flag}`);
  }
  return result;
}

function run(executable, args, cwd, toolchain) {
  const env = { ...process.env };
  const pathKey = Object.keys(env).find(key => key.toLowerCase() === 'path') || 'Path';
  env[pathKey] = `${path.join(toolchain, 'bin')};${env[pathKey] || ''}`;
  const result = spawnSync(executable, args, { cwd, env, stdio: 'inherit', windowsHide: true });
  if (result.error) throw result.error;
  if (result.status !== 0) throw new Error(`${path.basename(executable)} exited with code ${result.status}`);
}

async function buildNativeInstaller(config = {}) {
  const pkg = JSON.parse(fs.readFileSync(path.join(root, 'package.json'), 'utf8'));
  const preview = !!config.preview;
  const payload = config.payload && path.resolve(config.payload);
  const appDirectory = config['app-dir'] && path.resolve(config['app-dir']);
  const appExecutable = config['app-executable'] || `${pkg.build.productName}.exe`;
  const productDirectory = config['product-directory'] || pkg.build.productName;
  const guid = config.guid || pkg.build.nsis.guid;
  if (!/^[0-9a-f-]{36}$/i.test(guid || '')) throw new Error('A stable NSIS GUID is required.');
  if (path.basename(appExecutable) !== appExecutable || /["\r\n]/.test(appExecutable + productDirectory)) throw new Error('Invalid application metadata.');
  if (!preview && (!payload || !appDirectory || !fs.existsSync(payload))) throw new Error('Use --payload and --app-dir to build a complete installer, or --preview for UI only.');
  const background = config.harness ? path.join(root, 'desktop', 'assets', 'icon.png') : path.join(nativeRoot, 'assets', 'background.png');
  if (!fs.existsSync(background)) throw new Error(`Generated background asset is missing: ${background}`);
  const output = config.out ? path.resolve(config.out) : preview
    ? path.join(root, 'build', 'native-installer', 'preview', 'Octopus Setup Preview.exe')
    : path.join(root, 'dist', 'installer-ui', `Octopus Custom Setup ${pkg.version}.exe`);
  const buildDirectory = path.join(root, 'build', 'native-installer', preview ? 'preview-objects' : 'release-objects');
  fs.mkdirSync(buildDirectory, { recursive: true });
  fs.mkdirSync(path.dirname(output), { recursive: true });
  const payloadBytes = preview ? 0 : fs.statSync(payload).size;
  if (payloadBytes > 0xffffffff) throw new Error('Installer payload exceeds the Windows resource size limit.');
  const payloadHash = preview ? '' : await sha256(payload);
  const installedHash = preview ? '' : await sha256(path.join(appDirectory, appExecutable));
  const wide = value => `L${JSON.stringify(value)}`;
  const header = `#pragma once
#include <windows.h>
inline constexpr wchar_t kVersion[] = ${wide(pkg.version)};
inline constexpr wchar_t kProductDirectory[] = ${wide(productDirectory)};
inline constexpr wchar_t kAppExecutable[] = ${wide(appExecutable)};
inline constexpr wchar_t kUninstallerExecutable[] = ${wide(`Uninstall ${productDirectory}.exe`)};
inline constexpr wchar_t kRegistryKey[] = ${wide(`Software\\${guid}`)};
inline constexpr DWORD kPayloadBytes = ${payloadBytes}u;
inline constexpr char kPayloadSha256[] = ${JSON.stringify(payloadHash)};
inline constexpr char kInstalledExeSha256[] = ${JSON.stringify(installedHash)};
`;
  fs.writeFileSync(path.join(buildDirectory, 'generated_config.h'), header);
  const resourcePath = file => JSON.stringify(file.replaceAll('\\', '/'));
  const numbers = pkg.version.split('-')[0].split('.').map(Number);
  const resource = `#include <windows.h>
#include ${resourcePath(path.join(nativeRoot, 'resource.h'))}
1 RT_MANIFEST ${resourcePath(path.join(nativeRoot, 'setup.manifest'))}
IDI_SETUP ICON ${resourcePath(path.join(root, 'desktop', 'assets', 'icon.ico'))}
IDR_BACKGROUND RCDATA ${resourcePath(background)}
${preview ? '' : `IDR_PAYLOAD RCDATA ${resourcePath(payload)}`}
1 VERSIONINFO
 FILEVERSION ${numbers.join(',')},0
 PRODUCTVERSION ${numbers.join(',')},0
 FILEFLAGSMASK 0x3fL
 FILEFLAGS 0
 FILEOS 0x40004L
 FILETYPE 0x1L
BEGIN
 BLOCK "StringFileInfo"
 BEGIN
  BLOCK "040904b0"
  BEGIN
   VALUE "FileDescription", "Octopus Setup\\0"
   VALUE "FileVersion", "${pkg.version}\\0"
   VALUE "ProductName", "Octopus Setup\\0"
   VALUE "ProductVersion", "${pkg.version}\\0"
  END
 END
 BLOCK "VarFileInfo"
 BEGIN
  VALUE "Translation", 0x409, 1200
 END
END
`;
  const rc = path.join(buildDirectory, 'setup.rc');
  const object = path.join(buildDirectory, 'setup.o');
  fs.writeFileSync(rc, resource);
  const toolchain = await ensureToolchain();
  console.log(`Building ${preview ? 'native UI preview' : 'native Windows installer'} (${pkg.version}).`);
  run(path.join(toolchain, 'bin', 'x86_64-w64-mingw32-windres.exe'), ['-i', rc, '-o', object, '-O', 'coff'], buildDirectory, toolchain);
  run(path.join(toolchain, 'bin', 'x86_64-w64-mingw32-clang++.exe'), [
    '-std=c++20', '-O2', '-static', '-municode', config.harness ? '-mconsole' : '-mwindows', '-Wall', '-Wextra',
    '-DUNICODE', '-D_UNICODE', '-DNOMINMAX', '-DWINVER=0x0A00', '-D_WIN32_WINNT=0x0A00',
    '-I', buildDirectory, path.join(nativeRoot, config.harness ? 'tests/install-harness.cpp' : 'src/main.cpp'), path.join(nativeRoot, 'src', 'install.cpp'),
    object, '-o', output,
    '-ld2d1', '-ldwrite', '-lwindowscodecs', '-ldwmapi', '-lole32', '-lshell32', '-lcomctl32',
    '-luser32', '-lgdi32', '-ladvapi32', '-lbcrypt', '-luuid',
    '-Wl,--nxcompat,--dynamicbase,--high-entropy-va,--strip-debug',
  ], buildDirectory, toolchain);
  const result = { output, bytes: fs.statSync(output).size, sha256: await sha256(output), payloadBytes, payloadHash, installedHash, version: pkg.version, preview };
  fs.writeFileSync(`${output}.json`, `${JSON.stringify(result, null, 2)}\n`);
  console.log(JSON.stringify(result, null, 2));
  return result;
}

module.exports = { buildNativeInstaller };
if (require.main === module) buildNativeInstaller(options(process.argv.slice(2))).catch(error => {
  console.error(error.message); process.exitCode = 1;
});
