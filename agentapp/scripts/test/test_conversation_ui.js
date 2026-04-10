#!/usr/bin/env node
/**
 * Test Conversation UI - 验证对话界面显示
 */
const { chromium } = require('playwright');

async function testConversationUI() {
  console.log('=== Testing Conversation UI ===\n');

  const browser = await chromium.launch({ headless: true });
  const page = await browser.newPage();

  // Navigate to app
  console.log('[1/5] Opening Flutter Web app...');
  await page.goto('http://localhost:18086', { timeout: 60000 });
  await page.waitForTimeout(3000);
  console.log('  ✓ App loaded\n');

  // Login
  console.log('[2/5] Logging in...');
  await page.fill('input[type="text"]', 'testtoken123');
  await page.click('button:has-text("连接")');
  await page.waitForTimeout(2000);
  console.log('  ✓ Logged in\n');

  // Click on an agent to open conversation
  console.log('[3/5] Opening agent conversation...');
  const agentCard = await page.locator('text=test-full-conversation').first();
  if (await agentCard.isVisible().catch(() => false)) {
    await agentCard.click();
    console.log('  ✓ Opened conversation\n');
  } else {
    console.log('  ✗ Agent not found, trying first agent...');
    const firstAgent = await page.locator('[data-testid="agent-card"]').first();
    if (firstAgent) await firstAgent.click();
  }

  await page.waitForTimeout(2000);

  // Take screenshot of conversation
  console.log('[4/5] Taking screenshot...');
  await page.screenshot({ path: '/Users/fengming.xie/Documents/project/phone-talk/conversation_ui_test.png', fullPage: false });
  console.log('  ✓ Screenshot saved\n');

  // Check for message bubbles
  console.log('[5/5] Checking message structure...');
  const messages = await page.locator('.chat-message, [role="listitem"]').count();
  console.log(`  Found ${messages} message elements`);

  // Check if messages are displayed separately
  const hasUserMessages = await page.locator('text=Test message').first().isVisible().catch(() => false);
  console.log(`  User messages visible: ${hasUserMessages}`);

  await browser.close();

  console.log('\n=== Test Complete ===');
  console.log('Screenshot saved to: conversation_ui_test.png');
}

testConversationUI().catch(err => {
  console.error('Test failed:', err);
  process.exit(1);
});
