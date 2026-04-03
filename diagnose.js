const { firefox } = require('playwright');

async function diagnose() {
  const browser = await firefox.launch({ headless: true });
  const page = await browser.newPage();

  const errors = [];
  const consoleLogs = [];

  page.on('console', msg => {
    const text = `[${msg.type()}] ${msg.text()}`;
    consoleLogs.push(text);
    console.log(text);
  });

  page.on('pageerror', err => {
    errors.push(err.message);
    console.log('[PAGE ERROR]', err.message);
  });

  page.on('response', resp => {
    if (!resp.ok()) {
      console.log(`[HTTP ${resp.status()}] ${resp.url()}`);
    }
  });

  await page.goto('http://localhost:18093', { timeout: 60000 });

  // Wait 30s
  await page.waitForTimeout(30000);

  // Get all resources loaded
  const html = await page.content();

  console.log('\n========== DIAGNOSIS ==========');
  console.log('HTML length:', html.length);
  console.log('Console logs:', consoleLogs.length);
  console.log('Errors:', errors.length);

  // Check if main.dart.js loaded
  const hasMainDart = html.includes('main.dart.js');
  console.log('main.dart.js in HTML:', hasMainDart);

  // Check if canvaskit is referenced
  const hasCanvaskit = html.includes('canvaskit');
  console.log('canvaskit referenced:', hasCanvaskit);

  await page.screenshot({ path: 'diagnose-session.png', fullPage: true });
  console.log('Screenshot saved');

  await browser.close();
}

diagnose();
