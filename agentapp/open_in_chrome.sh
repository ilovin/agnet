#!/bin/bash

# Start Chrome with remote debugging and open Agent Manager
/Applications/Google\ Chrome.app/Contents/MacOS/Google\ Chrome \
  --remote-debugging-port=9222 \
  --no-first-run \
  --no-default-browser-check \
  --user-data-dir=/tmp/chrome-agent-manager \
  http://localhost:18086 &

echo "Chrome started with remote debugging on port 9222"
echo "Agent Manager URL: http://localhost:18086"
echo ""
echo "等待 10 秒后截图..."
sleep 10
