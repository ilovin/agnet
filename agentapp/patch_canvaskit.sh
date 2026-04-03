#!/bin/bash
# Patch flutter_bootstrap.js to use local CanvasKit instead of gstatic.com

BUILD_DIR="build/web"

if [ -f "$BUILD_DIR/flutter_bootstrap.js" ]; then
  # Replace gstatic CanvasKit URL with local path
  sed -i '' 's|https://www.gstatic.com/flutter-canvaskit|/canvaskit|g' "$BUILD_DIR/flutter_bootstrap.js"
  echo "Patched flutter_bootstrap.js to use local CanvasKit"
else
  echo "Error: flutter_bootstrap.js not found in $BUILD_DIR"
  exit 1
fi
