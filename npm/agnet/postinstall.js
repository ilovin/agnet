#!/usr/bin/env node
'use strict';

const fs = require('fs');
const path = require('path');
const os = require('os');

const pkgDir = path.resolve(__dirname);
const installDir = path.join(os.homedir(), '.agentgw');

function getPlatform() {
  const p = os.platform();
  const a = os.arch();
  if (p === 'darwin' && (a === 'arm64' || a === 'x64')) return 'darwin-arm64';
  if (p === 'linux' && a === 'x64') return 'linux-amd64';
  if (p === 'linux' && a === 'arm64') return 'linux-arm64';
  return null;
}

const platform = getPlatform();
if (!platform) {
  console.log('agnet: unsupported platform, skipping binary install');
  process.exit(0);
}

const srcDir = path.join(pkgDir, 'platform', platform);
if (!fs.existsSync(srcDir)) {
  console.log('agnet: platform binaries not bundled, will download on first run');
  process.exit(0);
}

fs.mkdirSync(installDir, { recursive: true });

for (const name of ['agentgw', 'agentd']) {
  const src = path.join(srcDir, name);
  // Legacy flat install (for direct user execution)
  const dst = path.join(installDir, name);
  // Platform-structured install (for install.sh upgrade path)
  const platformDst = path.join(installDir, 'platform', platform);
  fs.mkdirSync(platformDst, { recursive: true });
  const platformBin = path.join(platformDst, name);
  if (fs.existsSync(src)) {
    fs.copyFileSync(src, dst);
    fs.chmodSync(dst, 0o755);
    fs.copyFileSync(src, platformBin);
    fs.chmodSync(platformBin, 0o755);
    console.log(`agnet: installed ${name} (${platform})`);
  }
}

const installSrc = path.join(pkgDir, 'install.sh');
if (fs.existsSync(installSrc)) {
  fs.copyFileSync(installSrc, path.join(installDir, 'install.sh'));
}

const staticSrc = path.join(pkgDir, 'static');
const staticDst = path.join(installDir, 'static');
if (fs.existsSync(staticSrc)) {
  fs.rmSync(staticDst, { recursive: true, force: true });
  fs.cpSync(staticSrc, staticDst, { recursive: true });
}

console.log(`agnet: installed to ${installDir}`);
