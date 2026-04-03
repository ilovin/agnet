const { firefox } = require('playwright');
const fs = require('fs');
const path = require('path');
const http = require('http');

const PORT = 18087;
const WEB_ROOT = path.join(__dirname, 'build/web');

// MIME types
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

// Start server
const server = http.createServer((req, res) => {
  let filePath = path.join(WEB_ROOT, req.url === '/' ? 'index.html' : req.url);
  const ext = path.extname(filePath).toLowerCase();
  const contentType = MIME_TYPES[ext] || 'application/octet-stream';

  fs.readFile(filePath, (err, content) => {
    if (err) {
      if (err.code === 'ENOENT') {
        console.log(`[404] ${req.url}`);
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

  // Run test
  const browser = await firefox.launch({ headless: true });
  const context = await browser.newContext({ viewport: { width: 1280, height: 720 } });
  const page = await context.newPage();

  page.on('console', msg => console.log(`[${msg.type()}] ${msg.text()}`));
  page.on('pageerror', err => console.log('[PAGE ERROR]', err.message));

  await page.goto(`http://localhost:${PORT}`, { waitUntil: 'domcontentloaded', timeout: 10000 });
  console.log('DOM loaded, waiting...\n');

  await page.waitForTimeout(90000);

  const html = await page.content();
  console.log('\n=== Results ===');
  console.log('Canvas elements:', await page.locator('canvas').count());
  console.log('Contains flt-glass-pane:', html.includes('flt-glass-pane'));

  await page.screenshot({ path: 'verify-18087.png', fullPage: true });
  console.log('\nScreenshot saved');

  await browser.close();
  server.close();
  process.exit(0);
});
