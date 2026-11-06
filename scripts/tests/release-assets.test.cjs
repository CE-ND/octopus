const { test } = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');
const crypto = require('node:crypto');
const { collectReleaseAssets } = require('../collect-release-assets.cjs');

function releaseFixture(t, version = '0.2.0') {
  const temporary = fs.realpathSync(os.tmpdir());
  const root = fs.mkdtempSync(path.join(temporary, 'octopus-release-check-'));
  t.after(() => {
    const resolved = fs.realpathSync(root);
    assert(resolved.startsWith(temporary + path.sep) && path.basename(resolved).startsWith('octopus-release-check-'));
    assert(!fs.lstatSync(root).isSymbolicLink());
    fs.rmSync(resolved, { recursive: true, force: true });
  });
  const source = path.join(root, 'source');
  const destination = path.join(root, 'collected');
  fs.mkdirSync(source);
  const engine = `Octopus-${version}-windows-x64-setup.exe`;
  const data = Buffer.alloc(1000000, 42);
  const sha512 = crypto.createHash('sha512').update(data).digest('base64');
  const native = `Octopus Custom Setup ${version}.exe`;
  const mac = [`Octopus-${version}-mac-arm64.dmg`, `Octopus-${version}-mac-x64.dmg`];
  for (const name of [engine, native, ...mac]) fs.writeFileSync(path.join(source, name), data);
  fs.writeFileSync(path.join(source, `${engine}.blockmap`), 'blockmap-fixture');
  fs.writeFileSync(path.join(source, 'builder-debug.yml'), 'must-not-publish: true');
  const manifest = { version, files: [{ url: engine, sha512, size: data.length }], path: engine, sha512 };
  const manifestName = version.includes('-') ? `${version.split('-')[1].split('.')[0]}.yml` : 'latest.yml';
  const writeManifest = () => fs.writeFileSync(path.join(source, manifestName), JSON.stringify(manifest));
  writeManifest();
  return { source, destination, version, engine, native, mac, manifest, manifestName, writeManifest,
    collect: (includeMac = true) => collectReleaseAssets({ sourceDirs: [source], destination, version, includeMac }) };
}

test('release collection requires both installers, both Mac architectures, a matching manifest and a blockmap', async t => {
  const fixture = releaseFixture(t);
  const files = await fixture.collect();
  assert.equal(files.length, 6);
  assert(files.includes('Octopus.Custom.Setup.0.2.0.exe'), 'The native setup download link must have a stable public filename');
  assert(!files.includes('builder-debug.yml'));
  assert.deepEqual(fs.readdirSync(fixture.destination).sort(), files.sort());
  assert.equal(fs.readFileSync(path.join(fixture.destination, fixture.engine)).length, 1000000);
});

test('Windows artifacts can be staged independently and prereleases retain their own channel', async t => {
  const fixture = releaseFixture(t, '0.2.0-next.1');
  const files = await fixture.collect(false);
  assert.equal(files.length, 4);
  assert(files.includes('next.yml'));
  assert(!files.includes('latest.yml'));
});

test('a corrupt installer, wrong version, external manifest URL, or missing architecture stops publication', async t => {
  for (const scenario of ['corrupt', 'version', 'external-url', 'missing-mac', 'missing-blockmap']) {
    await t.test(scenario, async t => {
      const fixture = releaseFixture(t);
      if (scenario === 'corrupt') fs.appendFileSync(path.join(fixture.source, fixture.engine), 'corrupted');
      if (scenario === 'version') fixture.manifest.version = '0.1.0';
      if (scenario === 'external-url') fixture.manifest.files[0].url = 'https://example.com/other.exe';
      if (scenario === 'missing-mac') fs.unlinkSync(path.join(fixture.source, fixture.mac[0]));
      if (scenario === 'missing-blockmap') fs.unlinkSync(path.join(fixture.source, `${fixture.engine}.blockmap`));
      fixture.writeManifest();
      await assert.rejects(fixture.collect());
      assert(!fs.existsSync(fixture.destination), 'Do not stage a partial release');
    });
  }
});
