const fs = require('fs');
const path = require('path');

const bootstrapPath = 'build/web/flutter_bootstrap.js';
let content = fs.readFileSync(bootstrapPath, 'utf8');

// Extract engine version
const versionMatch = content.match(/flutter-canvaskit\/([a-f0-9]+)\//);
if (versionMatch) {
  const engineVersion = versionMatch[1];
  console.log('Engine version:', engineVersion);

  // Create directory structure
  const canvaskitDir = 'build/web/canvaskit';
  const versionDir = path.join(canvaskitDir, engineVersion);
  const chromiumDir = path.join(versionDir, 'chromium');

  fs.mkdirSync(versionDir, { recursive: true });
  fs.mkdirSync(chromiumDir, { recursive: true });

  // Copy files
  ['canvaskit.js', 'canvaskit.wasm'].forEach(f => {
    const src = path.join(canvaskitDir, f);
    const dst = path.join(versionDir, f);
    if (fs.existsSync(src)) {
      fs.copyFileSync(src, dst);
      console.log('Copied', f);
    }
  });

  // Also copy chromium variant
  const chromiumSrc = path.join(canvaskitDir, 'chromium');
  if (fs.existsSync(chromiumSrc)) {
    fs.readdirSync(chromiumSrc).forEach(f => {
      fs.copyFileSync(path.join(chromiumSrc, f), path.join(chromiumDir, f));
    });
    console.log('Copied chromium files');
  }

  // Replace CDN URL
  const cdnPattern = new RegExp('https://www\\.gstatic\\.com/flutter-canvaskit/' + engineVersion, 'g');
  content = content.replace(cdnPattern, '/canvaskit/' + engineVersion);
  fs.writeFileSync(bootstrapPath, content);
  console.log('Modified flutter_bootstrap.js');
}
