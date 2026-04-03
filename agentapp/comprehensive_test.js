const { chromium } = require('playwright');

async function test() {
  const browser = await chromium.launch({ 
    headless: true,
    args: ['--no-sandbox', '--disable-setuid-sandbox']
  });
  const page = await browser.newPage();

  const logs = [];
  page.on('console', msg => {
    logs.push({type: msg.type(), text: msg.text()});
    console.log(`[${msg.type().toUpperCase()}] ${msg.text()}`);
  });
  page.on('pageerror', err => {
    logs.push({type: 'pageerror', text: err.message});
  });

  await page.goto('http://localhost:18086', { timeout: 60000 });
  await page.waitForTimeout(30000);

  console.log('\n=== Checking initialization sequence ===');
  
  // Check what happened
  const state = await page.evaluate(() => ({
    _flutterExists: !!window._flutter,
    loaderExists: !!(window._flutter && window._flutter.loader),
    buildConfigExists: !!(window._flutter && window._flutter.buildConfig),
    canvasKitExists: !!window.flutterCanvasKit,
    
    // Check if main.dart.js loaded
    mainDartLoaded: (() => {
      return Array.from(document.scripts).some(s => s.src.includes('main.dart.js'));
    })(),
    
    // Check document state
    readyState: document.readyState
  }));

  console.log(JSON.stringify(state, null, 2));

  // Try to force trigger didCreateEngineInitializer
  console.log('\n=== Attempting to trigger engine init ===');
  const forced = await page.evaluate(() => {
    try {
      if (window._flutter && window._flutter.loader && window._flutter.loader.didCreateEngineInitializer) {
        // Create a mock engine initializer
        const mockEngine = {
          initializeEngine: () => Promise.resolve({
            runApp: () => {
              console.log('Mock runApp called');
              return Promise.resolve();
            }
          })
        };
        window._flutter.loader.didCreateEngineInitializer(mockEngine);
        return 'triggered';
      }
      return 'loader not ready';
    } catch(e) {
      return 'error: ' + e.message;
    }
  });
  console.log('Forced init result:', forced);

  await page.waitForTimeout(5000);
  await browser.close();
}

test().catch(console.error);
