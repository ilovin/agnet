const http = require('http');
const fs = require('fs');
const path = require('path');

const PORT = 18093;
const HOST = '0.0.0.0'; // Bind to all interfaces
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
  // CORS headers
  res.setHeader('Access-Control-Allow-Origin', '*');
  res.setHeader('Access-Control-Allow-Methods', 'GET, POST, OPTIONS');
  res.setHeader('Access-Control-Allow-Headers', 'Content-Type');

  if (req.method === 'OPTIONS') {
    res.writeHead(200);
    res.end();
    return;
  }

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
      res.writeHead(200, { 'Content-Type': contentType });
      res.end(content);
    }
  });
});

server.listen(PORT, HOST, () => {
  console.log(`========================================`);
  console.log(`Agent Manager Web Server`);
  console.log(`========================================`);
  console.log(`Local:   http://localhost:${PORT}/`);
  console.log(`Network: http://0.0.0.0:${PORT}/`);
  console.log(`----------------------------------------`);
  console.log(`Backend Services:`);
  console.log(`  agentd:  ws://localhost:7373`);
  console.log(`  agentgw: ws://localhost:8080`);
  console.log(`========================================`);
});
