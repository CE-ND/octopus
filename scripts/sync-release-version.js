const fs = require('fs');
const path = require('path');

const rootDir = path.resolve(__dirname, '..');
const rawVersion = process.env.RELEASE_VERSION || '';
const version = rawVersion.replace(/^v/i, '');

if (!/^\d+\.\d+\.\d+(-[0-9A-Za-z.-]+)?$/.test(version)) {
  throw new Error(`Invalid release version '${rawVersion}', expect a tag like v0.1.5`);
}

function writeVersion(file, apply) {
  const json = JSON.parse(fs.readFileSync(file, 'utf8'));
  apply(json);
  fs.writeFileSync(file, `${JSON.stringify(json, null, 2)}\n`);
  console.log(`${path.relative(rootDir, file)} -> ${version}`);
}

writeVersion(path.join(rootDir, 'package.json'), (json) => {
  json.version = version;
});

writeVersion(path.join(rootDir, 'installer-ui', 'electron-builder.json'), (json) => {
  json.extraMetadata.version = version;
});
