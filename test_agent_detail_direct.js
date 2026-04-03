const { firefox } = require('playwright');

async function test() {
  const browser = await firefox.launch({ headless: true });
  const page = await browser.newPage();

  page.on('console', msg => console.log('[CONSOLE]', msg.text()));

  // Direct access to agent detail page
  await page.goto('http://localhost:18093/#/agent/local-agentd/claude-attached-76592', { timeout: 60000 });
  
  console.log('Waiting for Flutter...');
  await page.waitForTimeout(45000);

  await page.screenshot({ path: 'agent_detail_direct.png', fullPage: true });
  console.log('Screenshot saved');

  // Get page text
  const text = await page.$eval('body', el => el.innerText);
  console.log('Page text:', text.slice(0, 500));

  await browser.close();
}

test();
