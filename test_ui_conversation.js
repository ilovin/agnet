#!/usr/bin/env node
/**
 * Test Conversation UI - 创建对话并验证UI显示
 */
const WebSocket = require('ws');
const { chromium } = require('playwright');

const TOKEN = 'testtoken123';
const AGENTGW = 'ws://127.0.0.1:8080/ws';

function sleep(ms) {
  return new Promise(resolve => setTimeout(resolve, ms));
}

function rpcCall(ws, method, params = {}) {
  return new Promise((resolve, reject) => {
    const id = Math.floor(Math.random() * 1000000);
    const handler = (data) => {
      try {
        const msg = JSON.parse(data);
        if (msg.id === id) {
          ws.off('message', handler);
          if (msg.error) {
            reject(new Error(msg.error.message));
          } else {
            resolve(msg.result);
          }
        }
      } catch (e) {}
    };
    ws.on('message', handler);
    ws.send(JSON.stringify({ jsonrpc: '2.0', id, method, params }));
    setTimeout(() => {
      ws.off('message', handler);
      reject(new Error('RPC timeout'));
    }, 10000);
  });
}

async function setupConversation() {
  console.log('[Setup] Creating test conversation...');
  const ws = new WebSocket(AGENTGW + '?token=' + TOKEN);
  await new Promise((resolve, reject) => {
    ws.once('open', resolve);
    ws.once('error', reject);
  });

  // Create agent
  const agentResult = await rpcCall(ws, 'agent.create', {
    nodeId: 'local-agentd',
    name: 'UI-Test-Agent',
    provider: 'claude',
    workDir: '/tmp'
  });
  const agentId = agentResult.id;
  console.log(`  Created agent: ${agentId}`);

  // Wait for agent to start
  await sleep(3000);

  // Send a few messages
  for (let i = 0; i < 3; i++) {
    await rpcCall(ws, 'conversation.send', {
      nodeId: 'local-agentd',
      agentId: agentId,
      message: `Test message ${i + 1}: Hello from UI test!`,
      raw: false
    });
    await sleep(1000);
  }

  ws.close();
  return agentId;
}

async function testConversationUI() {
  console.log('=== Testing Conversation UI ===\n');

  // Setup conversation first
  const agentId = await setupConversation();
  await sleep(2000);

  const browser = await chromium.launch({ headless: true });
  const page = await browser.newPage();

  // Navigate to app
  console.log('[1/4] Opening Flutter Web app...');
  await page.goto('http://localhost:18086', { timeout: 60000 });
  await page.waitForTimeout(3000);
  console.log('  ✓ App loaded\n');

  // Login
  console.log('[2/4] Logging in...');
  await page.fill('input[type="text"]', 'testtoken123');
  await page.click('button:has-text("连接")');
  await page.waitForTimeout(2000);
  console.log('  ✓ Logged in\n');

  // Open conversation
  console.log('[3/4] Opening conversation...');
  await page.click(`text=UI-Test-Agent`);
  await page.waitForTimeout(2000);
  console.log('  ✓ Opened conversation\n');

  // Take screenshot
  console.log('[4/4] Taking screenshot...');
  await page.screenshot({
    path: '/Users/fengming.xie/Documents/project/phone-talk/conversation_ui_result.png',
    fullPage: false
  });
  console.log('  ✓ Screenshot saved\n');

  await browser.close();

  console.log('=== Test Complete ===');
  console.log('Screenshot: conversation_ui_result.png');
}

testConversationUI().catch(err => {
  console.error('Test failed:', err);
  process.exit(1);
});
