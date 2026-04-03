const { firefox } = require('playwright');

async function test() {
  const browser = await firefox.launch({ headless: true });
  const page = await browser.newPage();

  page.on('console', msg => console.log('[CONSOLE]', msg.text()));

  await page.goto('http://localhost:18093', { timeout: 60000 });
  console.log('Page loaded, waiting 60s for Flutter...');
  await page.waitForTimeout(60000);

  const canvas = await page.$('canvas');
  const fltRoot = await page.$('[flt-renderer]');
  console.log('Canvas:', !!canvas, 'Flutter root:', !!fltRoot);

  const text = await page.$eval('body', el => el.innerText);
  console.log('Text preview:', text.slice(0, 200));

  await page.screenshot({ path: 'final_check.png', fullPage: true });
  console.log('Screenshot saved');

  await browser.close();
}

test();
