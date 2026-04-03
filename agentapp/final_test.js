const { chromium } = require('playwright');

async function test() {
  const browser = await chromium.launch({ 
    headless: true,
    args: ['--no-sandbox', '--disable-setuid-sandbox']
  });
  const page = await browser.newPage();

  page.on('console', msg => {
    const text = msg.text();
    if (text.includes('Flutter') || text.includes('engine') || text.includes('app')) {
      console.log(`[${msg.type().toUpperCase()}] ${text}`);
    }
  });

  console.log('Navigating...');
  await page.goto('http://localhost:18086', { timeout: 60000 });

  console.log('Waiting 25s for full initialization...');
  await page.waitForTimeout(25000);

  // Check for Flutter elements
  const elements = await page.evaluate(() => ({
    canvas: document.querySelectorAll('canvas').length,
    fltRenderer: document.querySelectorAll('[flt-renderer]').length,
    fltGlassPane: document.querySelectorAll('flt-glass-pane').length,
    loadingVisible: (() => {
      const el = document.getElementById('loading');
      return el ? el.style.display !== 'none' : false;
    })(),
    bodyText: document.body.innerText.slice(0, 200)
  }));

  console.log('\n=== Flutter Elements ===');
  console.log(JSON.stringify(elements, null, 2));

  await page.screenshot({ path: 'final_test.png', fullPage: true });
  console.log('\nScreenshot saved to final_test.png');

  await browser.close();
}

test().catch(console.error);
