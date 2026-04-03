const { firefox } = require('playwright');

(async () => {
  const browser = await firefox.launch({ headless: true });
  const context = await browser.newContext({ viewport: { width: 1280, height: 720 } });
  const page = await context.newPage();

  // 收集所有日志和错误
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

  // 监控所有请求
  page.on('request', req => {
    console.log(`[REQUEST] ${req.method()} ${req.url()}`);
  });

  page.on('response', resp => {
    const status = resp.status();
    if (status >= 400) {
      console.log(`[RESPONSE ${status}] ${resp.url()}`);
    }
  });

  await page.goto('http://localhost:18089', { waitUntil: 'networkidle', timeout: 60000 }).catch(e => {
    console.log('Network idle timeout:', e.message);
  });

  console.log('\n=== Waiting 30 seconds for Flutter... ===\n');
  await page.waitForTimeout(30000);

  // 检查HTML内容
  const html = await page.content();
  console.log('\n=== HTML Content Check ===');
  console.log('HTML length:', html.length);
  console.log('Contains flt-glass-pane:', html.includes('flt-glass-pane'));
  console.log('Contains flt-canvas-container:', html.includes('flt-canvas-container'));
  console.log('Contains main.dart.js:', html.includes('main.dart.js'));

  // 截图
  await page.screenshot({ path: 'debug-loading.png', fullPage: true });
  console.log('\nScreenshot saved');

  await browser.close();
})();
