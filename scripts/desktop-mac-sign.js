const { execFileSync } = require('child_process');

module.exports = async function signMacApp(configuration) {
  execFileSync(
    '/usr/bin/codesign',
    ['--force', '--deep', '--sign', '-', '--timestamp=none', configuration.app],
    { stdio: 'inherit' }
  );
};
