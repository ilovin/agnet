#!/bin/bash
# Build Flutter Web with local resources and apply patches
set -e
cd "$(dirname "$0")"

echo ">>> Building Flutter Web (no CDN)..."
https_proxy=${https_proxy:-http://proxy.nioint.com:8080} flutter build web --no-web-resources-cdn

echo ">>> Patching gstatic font URLs in main.dart.js..."
cd build/web
sed -i '' 's|https://fonts.gstatic.com/s/|/fonts/|g' main.dart.js

if grep -q 'gstatic' main.dart.js; then
  echo "WARNING: gstatic references still found!"
  exit 1
fi

echo ">>> Build complete. Serve with: node dev_server.js"
echo "    or: cd build/web && python3 -m http.server 18086"
