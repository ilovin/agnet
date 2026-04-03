const { firefox } = require('playwright');

(async () => {
  const browser = await firefox.launch({ headless: true });
  const context = await browser.newContext({ viewport: { width: 1280, height: 720 } });
  const page = await context.newPage();

  // 收集所有日志
  const logs = [];
  page.on('console', msg => {
    const log = `[${msg.type()}] ${msg.text()}`;
    logs.push(log);
    console.log(log);
  });

  page.on('pageerror', err => {
    const log = `[PAGE ERROR] ${err.message}`;
    logs.push(log);
    console.log(log);
  });

  // 监控网络请求
  page.on('request', req => {
    const url = req.url();
    if (url.includes('ws://') || url.includes('wss://') || url.includes('localhost:737')) {
      console.log(`[WS REQUEST] ${url}`);
    }
  });

  page.on('requestfailed', req => {
    const url = req.url();
    if (url.includes('ws://') || url.includes('wss://')) {
      console.log(`[WS FAILED] ${url}: ${req.failure()?.errorText}`);
    }
  });

  await page.goto('http://localhost:18093', { waitUntil: 'domcontentloaded', timeout: 10000 });
  console.log('\n=== Page loaded, waiting for WebSocket... ===\n');

  // 等待20秒让应用初始化
  await page.waitForTimeout(20000);

  // 检查页面状态
  const html = await page.content();
  console.log('\n=== Page Status ===');
  console.log('Contains flt-glass-pane:', html.includes('flt-glass-pane'));
  console.log('Contains "连接":', html.includes('连接'));
  console.log('Contains "Connections":', html.includes('Connections'));
  console.log('Contains "Dashboard":', html.includes('Dashboard'));

  // 截图
  await page.screenshot({ path: 'diagnose-session.png', fullPage: true });
  console.log('\nScreenshot saved to diagnose-session.png');

  // 输出所有日志
  console.log('\n=== All Logs ===');
  logs.forEach(l => console.log(l));

  await browser.close();
})();
