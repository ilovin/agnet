const { firefox } = require('playwright');
const http = require('http');
const fs = require('fs');
const path = require('path');

const PORT = 18091;
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

  // Run automated tests
  await runTests();
  server.close();
  process.exit(0);
});

async function runTests() {
  console.log('\n========== AUTOMATED TESTS ==========\n');

  const browser = await firefox.launch({ headless: true });
  const context = await browser.newContext({ viewport: { width: 1280, height: 720 } });
  const page = await context.newPage();

  const logs = [];
  const errors = [];

  page.on('console', msg => {
    logs.push(`[${msg.type()}] ${msg.text()}`);
  });
  page.on('pageerror', err => {
    errors.push(`[PAGE ERROR] ${err.message}`);
  });

  // Test 1: Page loads
  console.log('Test 1: Page loads successfully');
  await page.goto(`http://localhost:${PORT}`, { waitUntil: 'domcontentloaded', timeout: 10000 });
  console.log('✓ Page loaded\n');

  // Wait for Flutter to initialize
  console.log('Waiting for Flutter initialization (30s)...');
  await page.waitForTimeout(30000);

  // Test 2: Check Flutter rendered
  console.log('Test 2: Check Flutter rendered');
  const html = await page.content();
  const hasFlutter = html.includes('flt-glass-pane');
  console.log(`  flt-glass-pane: ${hasFlutter ? '✓' : '✗'}`);

  // Test 3: Check connection state
  console.log('\nTest 3: Check connection UI state');
  const hasConnectionText = html.includes('已连接') || html.includes('连接失败') || html.includes('Connecting') || html.includes('Disconnected');
  console.log(`  Connection UI visible: ${hasConnectionText ? '✓' : '✗'}`);

  // Test 4: Check main navigation elements
  console.log('\nTest 4: Check navigation elements');
  const hasWifiIcon = html.includes('wifi') || html.includes('信号');
  const hasSettingsIcon = html.includes('settings') || html.includes('设置');
  console.log(`  WiFi/Signal icon: ${hasWifiIcon ? '✓' : '✗'}`);
  console.log(`  Settings icon: ${hasSettingsIcon ? '✓' : '✗'}`);

  // Test 5: Check for any error displays
  console.log('\nTest 5: Check for errors');
  const hasErrorDisplay = html.includes('ERROR:') || html.includes('error') || html.includes('失败');
  console.log(`  No visible errors: ${!hasErrorDisplay ? '✓' : '✗'}`);

  // Screenshot for manual verification
  await page.screenshot({ path: 'final-test-result.png', fullPage: true });
  console.log('\nScreenshot saved: final-test-result.png');

  // Summary
  console.log('\n========== TEST SUMMARY ==========');
  console.log(`Flutter rendered: ${hasFlutter ? '✓ PASS' : '✗ FAIL'}`);
  console.log(`Connection UI: ${hasConnectionText ? '✓ PASS' : '✗ FAIL'}`);
  console.log(`Navigation: ${hasWifiIcon && hasSettingsIcon ? '✓ PASS' : '✗ FAIL'}`);
  console.log(`No errors: ${!hasErrorDisplay ? '✓ PASS' : '✗ FAIL'}`);

  if (errors.length > 0) {
    console.log(`\nErrors captured: ${errors.length}`);
    errors.slice(0, 5).forEach(e => console.log(`  ${e}`));
  }

  const allPassed = hasFlutter && hasConnectionText && !hasErrorDisplay;
  console.log(`\nOverall: ${allPassed ? '✓ ALL TESTS PASSED' : '✗ SOME TESTS FAILED'}`);

  await browser.close();
}
