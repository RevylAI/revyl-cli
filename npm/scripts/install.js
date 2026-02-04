/**
 * Revyl CLI postinstall script
 * 
 * Downloads the appropriate binary for the current platform
 * from GitHub releases.
 */

const https = require('https');
const fs = require('fs');
const path = require('path');
const os = require('os');
const { execSync } = require('child_process');

const REPO = 'RevylAI/revyl-cli';
const VERSION = process.env.REVYL_VERSION || 'latest';

// Determine platform and architecture
function getPlatformInfo() {
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
      throw new Error(`Unsupported platform: ${platform}`);
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
      throw new Error(`Unsupported architecture: ${arch}`);
  }
  
  const ext = platform === 'win32' ? '.exe' : '';
  return {
    platform: platformStr,
    arch: archStr,
    binaryName: `revyl-${platformStr}-${archStr}${ext}`,
    ext
  };
}

// Download file from URL
function downloadFile(url, dest) {
  return new Promise((resolve, reject) => {
    const file = fs.createWriteStream(dest);
    
    const request = https.get(url, (response) => {
      // Handle redirects
      if (response.statusCode === 302 || response.statusCode === 301) {
        file.close();
        fs.unlinkSync(dest);
        return downloadFile(response.headers.location, dest)
          .then(resolve)
          .catch(reject);
      }
      
      if (response.statusCode !== 200) {
        file.close();
        fs.unlinkSync(dest);
        reject(new Error(`Failed to download: ${response.statusCode}`));
        return;
      }
      
      response.pipe(file);
      
      file.on('finish', () => {
        file.close();
        resolve();
      });
    });
    
    request.on('error', (err) => {
      file.close();
      fs.unlinkSync(dest);
      reject(err);
    });
  });
}

// Get the download URL for the binary
function getDownloadUrl(info, version) {
  const tag = version === 'latest' ? 'latest/download' : `download/${version}`;
  return `https://github.com/${REPO}/releases/${tag}/${info.binaryName}`;
}

async function main() {
  console.log('Installing Revyl CLI...');
  
  try {
    const info = getPlatformInfo();
    console.log(`Platform: ${info.platform}/${info.arch}`);
    
    const binDir = path.join(__dirname, '..', 'bin');
    if (!fs.existsSync(binDir)) {
      fs.mkdirSync(binDir, { recursive: true });
    }
    
    const binaryPath = path.join(binDir, info.binaryName);
    const url = getDownloadUrl(info, VERSION);
    
    console.log(`Downloading from: ${url}`);
    await downloadFile(url, binaryPath);
    
    // Make executable on Unix
    if (os.platform() !== 'win32') {
      fs.chmodSync(binaryPath, 0o755);
    }
    
    console.log(`Installed: ${binaryPath}`);
    console.log('Revyl CLI installed successfully!');
    console.log('Run "revyl --help" to get started.');
    
  } catch (err) {
    console.error(`Installation failed: ${err.message}`);
    console.error('');
    console.error('You can manually download the binary from:');
    console.error(`https://github.com/${REPO}/releases`);
    process.exit(1);
  }
}

main();
