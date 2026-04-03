const { firefox } = require('playwright');
const http = require('http');
const fs = require('fs');
const path = require('path');

const PORT = 18092;
const WEB_ROOT = path.join(__dirname, 'build/web');

const MIME_TYPES = {
  '.html': 'text/html',
  '.js': 'application/javascript',
  '.wasm': 'application/wasm',
  '.json': 'application/json',
  '.css': 'text/css',
  '.png': 'image/png',
  '.ico': 'image/x-icon',
  '.svg': 'image/svg+xml',
  '.woff': 'font/woff',
  '.woff2': 'font/woff2',
  '.ttf': 'font/ttf',
};

const server = http.createServer((req, res) => {
  let filePath = path.join(WEB_ROOT, req.url === '/' ? 'index.html' : req.url);
  const ext = path.extname(filePath).toLowerCase();
  const contentType = MIME_TYPES[ext] || 'application/octet-stream';

  fs.readFile(filePath, (err, content) => {
    if (err) {
      if (err.code === 'ENOENT') {
        res.writeHead(404, { 'Content-Type': 'text/html' });
        res.end('<h1>404 Not Found</h1>');
      } else {
        res.writeHead(500);
        res.end(`Server Error: ${err.code}`);
      }
    } else {
      res.writeHead(200, {
        'Content-Type': contentType,
        'Access-Control-Allow-Origin': '*'
      });
      res.end(content);
    }
  });
});

server.listen(PORT, async () => {
  console.log(`Server running at http://localhost:${PORT}/`);
  await runTests();
  server.close();
  process.exit(0);
});

async function runTests() {
  console.log('\n========== COMPREHENSIVE TESTS (90s wait) ==========\n');

  const browser = await firefox.launch({ headless: true });
  const context = await browser.newContext({ viewport: { width: 1280, height: 720 } });
  const page = await context.newPage();

  const logs = [];
  const errors = [];

  page.on('console', msg => {
    logs.push(`[${msg.type()}] ${msg.text()}`);
    if (msg.type() === 'error') console.log(`[ERROR] ${msg.text()}`);
  });
  page.on('pageerror', err => {
    errors.push(err.message);
    console.log(`[PAGE ERROR] ${err.message}`);
  });

  // Load page
  await page.goto(`http://localhost:${PORT}`, { waitUntil: 'domcontentloaded', timeout: 10000 });
  console.log('✓ Page loaded');

  // Wait longer for Flutter
  console.log('Waiting 90 seconds for Flutter...');
  await page.waitForTimeout(90000);

  // Take multiple screenshots
  await page.screenshot({ path: 'test-result-1.png' });

  // Check HTML
  const html = await page.content();

  console.log('\n========== RESULTS ==========');
  console.log('Canvas elements:', await page.locator('canvas').count());
  console.log('flt-glass-pane:', html.includes('flt-glass-pane') ? '✓' : '✗');
  console.log('flt-canvas-container:', html.includes('flt-canvas-container') ? '✓' : '✗');
  console.log('WiFi icon:', html.includes('wifi') || html.includes('signal') ? '✓' : '✗');
  console.log('Settings icon:', html.includes('settings') ? '✓' : '✗');
  console.log('App title:', html.includes('Agent Manager') ? '✓' : '✗');

  const hasUI = html.includes('flt-glass-pane') || await page.locator('canvas').count() > 0;
  console.log('\nFlutter rendered:', hasUI ? '✓ PASS' : '✗ FAIL');

  await browser.close();
}
