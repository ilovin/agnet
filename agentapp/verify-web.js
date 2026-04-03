const { firefox } = require('playwright');

(async () => {
  const browser = await firefox.launch({
    headless: true
  });

  const context = await browser.newContext({
    viewport: { width: 1280, height: 720 }
  });

  const page = await context.newPage();

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

  await page.route('**/*', async (route, request) => {
    const url = request.url();
    if (url.includes('canvaskit') || url.includes('main.dart')) {
      console.log(`[REQUEST] ${url}`);
    }
    await route.continue();
  });

  await page.goto('http://localhost:18086', { waitUntil: 'domcontentloaded', timeout: 10000 });
  console.log('DOM loaded\n');

  // 等待90秒
  await page.waitForTimeout(90000);

  // 诊断检查
  console.log('\n=== Diagnostics ===');

  // 检查 _flutter 对象
  const flutterObj = await page.evaluate(() => {
    return {
      hasFlutter: typeof window._flutter !== 'undefined',
      hasLoader: window._flutter && typeof window._flutter.loader !== 'undefined',
      hasBuildConfig: window._flutter && typeof window._flutter.buildConfig !== 'undefined',
    };
  });
  console.log('Flutter object:', flutterObj);

  // 检查 CanvasKit
  const canvasKitStatus = await page.evaluate(() => {
    return {
      hasCanvasKit: typeof window.flutterCanvasKit !== 'undefined',
      canvasKitLoaded: window.flutterCanvasKitLoaded !== undefined,
    };
  });
  console.log('CanvasKit status:', canvasKitStatus);

  // 检查HTML中是否有错误显示
  const html = await page.content();
  console.log('\nContains error display:', html.includes('ERROR:') || html.includes('UNHANDLED:'));

  // 检查是否有 flutter-view 元素
  const hasFlutterView = await page.evaluate(() => {
    return document.querySelector('flt-glass-pane') !== null ||
           document.querySelector('[flt-glass-pane]') !== null ||
           document.querySelector('[id*="flutter"]') !== null;
  });
  console.log('Has Flutter view element:', hasFlutterView);

  await page.screenshot({ path: 'verify-step10-proxy-8080.png', fullPage: true });
  console.log('\nScreenshot saved');

  await browser.close();
})();
