const { firefox } = require('playwright');

async function verifyUI() {
  console.log('=== Verifying Flutter Web UI displays sessions ===');

  const browser = await firefox.launch({ headless: true });
  const page = await browser.newPage();

  // Capture all console messages
  page.on('console', msg => {
    console.log(`[${msg.type()}]`, msg.text());
  });

  try {
    // Open Flutter Web app
    console.log('Loading page...');
    await page.goto('http://localhost:18093', { timeout: 60000 });
    console.log('Page loaded');

    // Wait for Flutter to initialize (CanvasKit needs more time)
    console.log('Waiting for Flutter initialization (60s)...');
    await page.waitForTimeout(60000);

    // Take screenshot
    await page.screenshot({ path: 'verify-final.png', fullPage: true });
    console.log('Screenshot saved: verify-final.png');

    // Get page content
    const bodyText = await page.$eval('body', el => el.innerText);
    console.log('\n=== Page Content ===');
    console.log(bodyText.slice(0, 500));

    // Check for session/agent display
    const hasAgent = bodyText.includes('Test Agent');
    const hasNode = bodyText.includes('Local Agentd') || bodyText.includes('已连接');
    const hasEmptyState = bodyText.includes('暂无节点') || bodyText.includes('无连接');

    console.log('\n=== Verification Results ===');
    console.log('Has Test Agent:', hasAgent);
    console.log('Has Connected Node:', hasNode);
    console.log('Shows Empty State:', hasEmptyState);

    if (hasAgent && hasNode) {
      console.log('\n✅ SUCCESS: WebSocket session is correctly displayed in UI!');
    } else if (hasEmptyState) {
      console.log('\n❌ ISSUE: No sessions displayed - showing empty state');
    } else {
      console.log('\n⚠️ Check screenshot for details');
    }

  } catch (e) {
    console.error('Error:', e.message);
    await page.screenshot({ path: 'error.png' });
  }

  await browser.close();
}

verifyUI();
