#!/usr/bin/env node
const { execFileSync } = require('node:child_process');

const PLATFORMS = {
  'linux-x64': '@riquito/yln-linux-x64',
  'linux-arm64': '@riquito/yln-linux-arm64',
  'darwin-x64': '@riquito/yln-darwin-x64',
  'darwin-arm64': '@riquito/yln-darwin-arm64',
  'win32-x64': '@riquito/yln-win32-x64',
};

const key = `${process.platform}-${process.arch}`;
const pkg = PLATFORMS[key];
if (!pkg) {
  console.error(`yln: unsupported platform ${key}`);
  process.exit(1);
}

const binName = process.platform === 'win32' ? 'yln.exe' : 'yln';

let binPath;
try {
  binPath = require.resolve(`${pkg}/bin/${binName}`);
} catch {
  console.error(
    `yln: could not locate the ${pkg} package.\n` +
      `This usually means npm skipped the optional platform dependency. ` +
      `Try reinstalling with: npm install -g @riquito/yln`
  );
  process.exit(1);
}

try {
  execFileSync(binPath, process.argv.slice(2), { stdio: 'inherit' });
} catch (err) {
  if (typeof err.status === 'number') process.exit(err.status);
  console.error(err.message);
  process.exit(1);
}
