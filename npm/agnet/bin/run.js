#!/usr/bin/env node
'use strict';

const { spawnSync } = require('child_process');
const path = require('path');
const fs = require('fs');
const os = require('os');

const installDir = path.join(os.homedir(), '.agentgw');
const agentgw = path.join(installDir, 'agentgw');
const installSh = path.join(installDir, 'install.sh');

const cmd = process.argv[2] || 'help';
const args = process.argv.slice(3);

function die(msg) {
  console.error(msg);
  process.exit(1);
}

function run(bin, binArgs) {
  const result = spawnSync(bin, binArgs, { stdio: 'inherit', env: process.env });
  process.exit(result.status ?? 1);
}

function runSh(subcmd, extraArgs) {
  if (!fs.existsSync(installSh)) die('install.sh not found. Reinstall: npm i -g @ai-alignment/agnet');
  const shArgs = subcmd ? [installSh, subcmd, ...extraArgs] : [installSh, ...extraArgs];
  run('bash', shArgs);
}

function ensureAgentgw() {
  if (!fs.existsSync(agentgw)) die('agentgw not found. Run: agnet setup');
}

const HELP = `
agnet — Remote AI Agent management toolkit

Usage: agnet <command> [options]

Commands:
  setup     Interactive setup (network mode, token, remote nodes)
  start     Start agentd + agentgw services
  stop      Stop all services
  restart   Restart all services
  status    Show service status
  qr        Start agentgw with QR code
  update    Update to latest version (via npm)
  purge     Remove all local + remote data

  login     Register with tunnelhub
  logout    Unregister from tunnelhub
  version   Show version

Examples:
  agnet setup          # First-time setup
  agnet start          # Start services
  agnet qr             # Start with QR code for mobile
  agnet status         # Check status
`;

switch (cmd) {
  case 'help': case '--help': case '-h':
    console.log(HELP);
    process.exit(0);

  case 'setup':
    runSh(null, args);
    break;

  case 'start':
    runSh('start', args);
    break;

  case 'stop':
    runSh('stop', args);
    break;

  case 'restart':
    runSh('restart', args);
    break;

  case 'purge':
    runSh('purge', args);
    break;

  case 'update':
    console.log('Updating @ai-alignment/agnet...');
    run('npm', ['update', '-g', '@ai-alignment/agnet']);
    break;

  case 'qr':
    ensureAgentgw();
    run(agentgw, ['start', '--qr', ...args]);
    break;

  default:
    ensureAgentgw();
    run(agentgw, [cmd, ...args]);
    break;
}
