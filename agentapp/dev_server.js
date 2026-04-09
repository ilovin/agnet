const http = require('http');
const fs = require('fs');
const path = require('path');

const PORT = 18086;
const WEB_DIR = path.join(__dirname, 'build', 'web');

const MIME = {
  '.html': 'text/html',
  '.js': 'application/javascript',
  '.mjs': 'application/javascript',
  '.css': 'text/css',
  '.json': 'application/json',
  '.png': 'image/png',
  '.jpg': 'image/jpeg',
  '.svg': 'image/svg+xml',
  '.ico': 'image/x-icon',
  '.woff2': 'font/woff2',
  '.woff': 'font/woff',
  '.ttf': 'font/ttf',
  '.otf': 'font/otf',
  '.wasm': 'application/wasm',
};

const server = http.createServer((req, res) => {
  let urlPath = req.url.split('?')[0].split('#')[0];

  // Rewrite /fonts/* -> local build/web/fonts/* (for patched gstatic fallback URLs)
  if (urlPath.startsWith('/fonts/')) {
    const fontPath = path.join(WEB_DIR, urlPath);
    if (fontPath.startsWith(WEB_DIR) && fs.existsSync(fontPath)) {
      const ext = path.extname(fontPath).toLowerCase();
      res.writeHead(200, {
        'Content-Type': MIME[ext] || 'application/octet-stream',
        'Cache-Control': 'public, max-age=31536000',
        'Access-Control-Allow-Origin': '*',
      });
      fs.createReadStream(fontPath).pipe(res);
      return;
    }
    // Font not found locally - return 404
    res.writeHead(404);
    res.end('Not found');
    return;
  }

  // Default: serve from build/web
  if (urlPath === '/') urlPath = '/index.html';
  const filePath = path.join(WEB_DIR, urlPath);

  // Security: prevent directory traversal
  if (!filePath.startsWith(WEB_DIR)) {
    res.writeHead(403);
    res.end('Forbidden');
    return;
  }

  fs.stat(filePath, (err, stat) => {
    if (err || !stat.isFile()) {
      // SPA fallback
      const idx = path.join(WEB_DIR, 'index.html');
      res.writeHead(200, { 'Content-Type': 'text/html' });
      fs.createReadStream(idx).pipe(res);
      return;
    }
    const ext = path.extname(filePath).toLowerCase();
    res.writeHead(200, { 'Content-Type': MIME[ext] || 'application/octet-stream' });
    fs.createReadStream(filePath).pipe(res);
  });
});

server.listen(PORT, () => {
  console.log(`Dev server running at http://localhost:${PORT}`);
});
