#!/usr/bin/env node
/**
 * Revyl CLI wrapper for npm
 * 
 * This script finds and executes the platform-specific Revyl binary
 * that was downloaded during npm install.
 */

const { spawn } = require('child_process');
const path = require('path');
const fs = require('fs');
const os = require('os');

// Determine the binary name based on platform
function getBinaryName() {
  const platform = os.platform();
  const arch = os.arch();
  
  let platformStr;
  switch (platform) {
    case 'darwin':
      platformStr = 'darwin';
      break;
    case 'linux':
      platformStr = 'linux';
      break;
    case 'win32':
      platformStr = 'windows';
      break;
    default:
      console.error(`Unsupported platform: ${platform}`);
      process.exit(1);
  }
  
  let archStr;
  switch (arch) {
    case 'x64':
      archStr = 'amd64';
      break;
    case 'arm64':
      archStr = 'arm64';
      break;
    default:
      console.error(`Unsupported architecture: ${arch}`);
      process.exit(1);
  }
  
  const ext = platform === 'win32' ? '.exe' : '';
  return `revyl-${platformStr}-${archStr}${ext}`;
}

// Find the binary
function findBinary() {
  const binaryName = getBinaryName();
  
  // Check in the package's bin directory
  const packageBinPath = path.join(__dirname, '..', 'bin', binaryName);
  if (fs.existsSync(packageBinPath)) {
    return packageBinPath;
  }
  
  // Check in node_modules/.bin
  const nodeModulesBinPath = path.join(__dirname, '..', '..', '.bin', binaryName);
  if (fs.existsSync(nodeModulesBinPath)) {
    return nodeModulesBinPath;
  }
  
  // Check if installed globally
  const globalPath = path.join(__dirname, '..', binaryName);
  if (fs.existsSync(globalPath)) {
    return globalPath;
  }
  
  console.error(`Revyl binary not found: ${binaryName}`);
  console.error('Try reinstalling: npm install -g @revyl/cli');
  process.exit(1);
}

// Run the binary
const binaryPath = findBinary();
const args = process.argv.slice(2);

const child = spawn(binaryPath, args, {
  stdio: 'inherit',
  env: process.env
});

child.on('error', (err) => {
  console.error(`Failed to start Revyl: ${err.message}`);
  process.exit(1);
});

child.on('exit', (code) => {
  process.exit(code || 0);
});
