#!/usr/bin/env node
/**
 * Dashboard Screenshot Test - Capture dashboard UI for verification
 * Uses existing Chrome tab at localhost:7374
 */
const { chromium } = require('playwright');

const AGENTGW_URL = 'http://localhost:7374';
const SCREENSHOT_DIR = '/tmp';

async function sleep(ms) {
  return new Promise(resolve => setTimeout(resolve, ms));
}

async function captureDashboard() {
  console.log('=== Dashboard Screenshot Test ===\n');
  console.log(`Target URL: ${AGENTGW_URL}/dashboard`);
  console.log(`Screenshot dir: ${SCREENSHOT_DIR}\n`);

  const browser = await chromium.launch({ headless: true });
  const page = await browser.newPage({
    viewport: { width: 1440, height: 900 }
  });

  // Capture console logs and errors
  const logs = [];
  page.on('console', msg => {
    const text = `[${msg.type()}] ${msg.text()}`;
    logs.push(text);
  });
  page.on('pageerror', err => {
    const text = `[PAGE ERROR] ${err.message}`;
    logs.push(text);
    console.log(text);
  });

  try {
    // Step 1: Navigate to dashboard
    console.log('[1/4] Navigating to dashboard...');
    await page.goto(`${AGENTGW_URL}/dashboard`, {
      timeout: 60000,
      waitUntil: 'networkidle'
    });
    console.log('  Page request sent\n');

    // Step 2: Wait for Flutter to load
    console.log('[2/4] Waiting for Flutter Web to initialize...');
    // Wait for flutter-first-frame or just wait a reasonable time for CanvasKit
    await sleep(15000);

    // Check Flutter state
    const flutterState = await page.evaluate(() => {
      return {
        hasFlutter: !!window._flutter,
        hasLoader: !!(window._flutter && window._flutter.loader),
        documentReady: document.readyState,
        title: document.title,
        bodyText: document.body ? document.body.innerText.slice(0, 200) : 'no body'
      };
    });
    console.log('  Flutter state:', JSON.stringify(flutterState, null, 2));
    console.log('  Waited 15s for Flutter load\n');

    // Step 3: Take full-page screenshot
    console.log('[3/4] Capturing full-page screenshot...');
    const dashboardPath = `${SCREENSHOT_DIR}/dashboard_screenshot.png`;
    await page.screenshot({
      path: dashboardPath,
      fullPage: true
    });
    console.log(`  Screenshot saved: ${dashboardPath}`);

    // Also take a viewport-sized screenshot
    const viewportPath = `${SCREENSHOT_DIR}/dashboard_viewport.png`;
    await page.screenshot({
      path: viewportPath,
      fullPage: false
    });
    console.log(`  Viewport screenshot saved: ${viewportPath}\n`);

    // Step 4: Check page content
    console.log('[4/4] Checking page content...');
    const pageInfo = await page.evaluate(() => {
      return {
        url: window.location.href,
        title: document.title,
        bodyText: document.body ? document.body.innerText : 'no body'
      };
    });
    console.log(`  URL: ${pageInfo.url}`);
    console.log(`  Title: ${pageInfo.title}`);
    console.log(`  Body text preview: ${pageInfo.bodyText.slice(0, 300)}\n`);

    // Console logs summary
    if (logs.length > 0) {
      console.log('=== Console Logs (first 20) ===');
      logs.slice(0, 20).forEach(l => console.log('  ' + l));
      console.log('');
    }

    console.log('=== Test Complete ===');
    console.log(`Dashboard screenshot: ${dashboardPath}`);
    console.log(`Viewport screenshot:  ${viewportPath}`);

    await browser.close();
    return {
      success: true,
      dashboardPath,
      viewportPath,
      flutterState,
      pageInfo
    };

  } catch (err) {
    console.error('\n[ERROR] Test failed:', err.message);

    // Save error screenshot
    const errorPath = `${SCREENSHOT_DIR}/dashboard_error.png`;
    await page.screenshot({ path: errorPath, fullPage: true });
    console.log(`Error screenshot saved: ${errorPath}`);

    await browser.close();
    return {
      success: false,
      error: err.message,
      errorPath
    };
  }
}

captureDashboard().then(result => {
  if (!result.success) {
    process.exit(1);
  }
}).catch(err => {
  console.error('Unhandled error:', err);
  process.exit(1);
});
