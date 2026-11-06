const fs = require('node:fs');
const path = require('node:path');
const crypto = require('node:crypto');
const assert = require('node:assert/strict');
const { spawnSync } = require('node:child_process');
const { buildNativeInstaller } = require('./build-native-installer.cjs');
const { ensureToolchain, sha256 } = require('./installer-toolchain.cjs');

const root = path.resolve(__dirname, '..');
const wait = ms => new Promise(resolve => setTimeout(resolve, ms));

function run(command, args, options = {}) {
  const { quiet = false, ...spawnOptions } = options;
  const result = spawnSync(command, args, { cwd: root, windowsHide: true, encoding: 'utf8', timeout: 180000, ...spawnOptions });
  if (!quiet && result.stdout) process.stdout.write(result.stdout);
  if (!quiet && result.stderr) process.stderr.write(result.stderr);
  if (result.error) throw result.error;
  assert.equal(result.status, 0, `${path.basename(command)} returned ${result.status}`);
  return (result.stdout || '').trim();
}

async function main() {
  assert.equal(process.platform, 'win32', 'Run installer integration tests on Windows.');
  const guid = crypto.randomUUID();
  const suffix = guid.slice(0, 8);
  const directory = path.join(root, 'build', 'native-installer', 'tests', suffix);
  const app = path.join(directory, 'app');
  const installed = path.join(directory, '中文 空格', 'Octopus Installer Check');
  const executableName = 'Octopus Installer Check.exe';
  const uninstallerName = 'Uninstall Octopus Installer Check.exe';
  const harness = path.join(directory, 'install-harness.exe');
  const profile = path.join(directory, 'builder.json');
  assert(installed.startsWith(path.join(root, 'build', 'native-installer', 'tests') + path.sep));
  fs.mkdirSync(app, { recursive: true });
  fs.mkdirSync(path.join(app, 'resources'), { recursive: true });
  fs.writeFileSync(path.join(app, 'resources', 'version.txt'), '0.1.9');
  fs.writeFileSync(path.join(app, 'resources', 'obsolete.txt'), 'old application resource');
  const source = path.join(directory, 'fixture.cpp');
  fs.writeFileSync(source, '#include <windows.h>\nint WINAPI wWinMain(HINSTANCE,HINSTANCE,PWSTR,int) { return 0; }\n');
  const toolchain = await ensureToolchain();
  run(path.join(toolchain, 'bin', 'x86_64-w64-mingw32-clang++.exe'), ['-Os', '-static', '-municode', '-mwindows', source, '-o', path.join(app, executableName)]);
  fs.writeFileSync(profile, JSON.stringify({
    appId: `com.hureru.octopus.installercheck.${suffix}`,
    productName: 'Octopus Installer Check',
    extraMetadata: { name: `octopus-installer-check-${suffix}`, version: '0.1.9' },
    directories: { output: path.join(directory, 'nsis') },
    win: { icon: path.join(root, 'desktop', 'assets', 'icon.ico'), target: ['nsis'] },
    nsis: {
      guid, artifactName: 'test-engine.exe', oneClick: false, allowToChangeInstallationDirectory: true,
      include: path.join(root, 'installer-native', 'nsis', 'installer.nsh'),
      shortcutName: `Octopus Installer Check ${suffix}`,
      createDesktopShortcut: true, createStartMenuShortcut: true,
    },
  }, null, 2));
  const env = { ...process.env, ELECTRON_BUILDER_CACHE: path.join(root, 'build', 'electron-builder-cache') };
  run(process.execPath, [require.resolve('electron-builder/out/cli/cli.js'), '--prepackaged', app, '--config', profile, '--win', 'nsis', '--x64', '--publish', 'never'], { env });
  await buildNativeInstaller({
    payload: path.join(directory, 'nsis', 'test-engine.exe'), 'app-dir': app,
    'app-executable': executableName, 'product-directory': 'Octopus Installer Check',
    guid, out: harness, harness: true,
  });
  const expectedHash = await sha256(path.join(app, executableName));
  const results = [];
  const verify = async label => {
    assert.equal(await sha256(path.join(installed, executableName)), expectedHash, 'Installed executable must match the packaged app.');
    assert.equal(run(harness, ['--default-dir']), installed, 'NSIS registry must point to the selected Unicode path.');
    results.push({ check: label, passed: true });
  };
  const uninstall = async () => {
    const target = path.join(installed, uninstallerName);
    assert(target.startsWith(directory + path.sep) && fs.existsSync(target));
    run(target, ['/S', '/currentuser']);
    // The NSIS uninstaller relaunches from Temp; its initial process can exit
    // before the child has finished removing both files and registry entries.
    for (let i = 0; i < 150; ++i) {
      if (!fs.existsSync(path.join(installed, executableName)) && run(harness, ['--default-dir'], { quiet: true }) !== installed) break;
      await wait(100);
    }
    assert(!fs.existsSync(path.join(installed, executableName)), 'Uninstaller must remove the test executable.');
    assert.notEqual(run(harness, ['--default-dir']), installed, 'Uninstaller must remove the test install-location registry entry.');
  };
  run(harness, [installed]);
  await verify('fresh installation with Unicode and spaces');
  run(harness, [installed, '--desktop-shortcut']);
  await verify('upgrade existing installation');

  // 与桌面更新器使用相同的 NSIS 参数；验证真正跨版本更新、旧文件清理和目录继承。
  const retainedData = path.join(directory, 'user-data', 'accounts.json');
  fs.mkdirSync(path.dirname(retainedData), { recursive: true });
  fs.writeFileSync(retainedData, '{"fixture":"keep"}');
  fs.writeFileSync(path.join(app, 'resources', 'version.txt'), '0.1.10');
  fs.unlinkSync(path.join(app, 'resources', 'obsolete.txt'));
  const updateProfile = JSON.parse(fs.readFileSync(profile, 'utf8'));
  updateProfile.extraMetadata.version = '0.1.10';
  updateProfile.nsis.artifactName = 'test-update-engine.exe';
  const updateProfilePath = path.join(directory, 'update-builder.json');
  fs.writeFileSync(updateProfilePath, JSON.stringify(updateProfile, null, 2));
  run(process.execPath, [require.resolve('electron-builder/out/cli/cli.js'), '--prepackaged', app, '--config', updateProfilePath, '--win', 'nsis', '--x64', '--publish', 'never'], { env });
  const updateEngine = path.join(directory, 'nsis', 'test-update-engine.exe');
  run(updateEngine, ['--updated', '/S', '--force-run', `/D=${installed}`]);
  await verify('silent desktop update preserves the Unicode installation directory');
  assert.equal(fs.readFileSync(path.join(installed, 'resources', 'version.txt'), 'utf8'), '0.1.10');
  assert(!fs.existsSync(path.join(installed, 'resources', 'obsolete.txt')), 'Updates must remove obsolete program files.');
  assert.equal(fs.readFileSync(retainedData, 'utf8'), '{"fixture":"keep"}');
  results.push({ check: 'update replaces old resources and preserves separate user data', passed: true });
  // 不传 /D 时也必须继承注册表中的原目录，覆盖更新器的默认路径解析。
  run(updateEngine, ['--updated', '/S']);
  await verify('silent update inherits the registered installation directory');
  await uninstall();
  results.push({ check: 'uninstallation', passed: true });
  run(harness, [installed]);
  await verify('reinstall after uninstall');
  await uninstall();
  const report = { directory, guid, results, scope: 'current-user', finalTestAppRemoved: true };
  fs.writeFileSync(path.join(directory, 'results.json'), JSON.stringify(report, null, 2));
  fs.writeFileSync(path.join(root, 'build', 'native-installer', 'latest-integration-test.json'), JSON.stringify(report, null, 2));
  console.log(JSON.stringify(report, null, 2));
}

main().catch(error => { console.error(error.stack || error.message); process.exitCode = 1; });
