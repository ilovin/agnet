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

async function ensureUtTestAgent() {
  const ws = new WebSocket(AGENTGW + '?token=' + TOKEN);
  await new Promise((resolve, reject) => {
    ws.once('open', resolve);
    ws.once('error', reject);
  });

  const nodeId = 'local-agentd';
  const targetName = 'ut test';

  const list = await rpcCall(ws, 'agent.list', { nodeId });
  const agents = Array.isArray(list) ? list : (list?.agents || []);

  const active = agents
    .filter((a) => a && a.name === targetName && a.status !== 'stopped' && a.status !== 'crashed')
    .sort((a, b) => {
      const p = { working: 0, starting: 1, idle: 2, stopped: 3, crashed: 4 };
      return (p[a.status] ?? 9) - (p[b.status] ?? 9);
    });

  if (active.length > 0) {
    ws.close();
    console.log(`  Reusing active agent: ${active[0].id}`);
    return active[0].id;
  }

  const created = await rpcCall(ws, 'session.create', {
    nodeId,
    name: targetName,
    provider: 'claude',
    workDir: '/tmp',
  });
  ws.close();
  return created.id;
}

async function setupConversation() {
  console.log('[Setup] Reusing fixed UT session...');
  const agentId = await ensureUtTestAgent();
  console.log(`  Using agent: ${agentId}`);
  await sleep(3000);
  return agentId;
}

async function waitForAssistantReply(agentId, marker, timeoutMs = 60000) {
  const ws = new WebSocket(AGENTGW + '?token=' + TOKEN);
  await new Promise((resolve, reject) => {
    ws.once('open', resolve);
    ws.once('error', reject);
  });

  const started = Date.now();
  let cursor = 0;
  let lastAssistant = '';

  while (Date.now() - started < timeoutMs) {
    const history = await rpcCall(ws, 'conversation.history', {
      nodeId: 'local-agentd',
      agentId,
      cursor,
    });

    const events = history?.events || [];
    if (events.length > 0) {
      cursor = history?.lastSeq || cursor;
      for (const e of events) {
        if (e.role === 'assistant' && e.text) {
          lastAssistant = String(e.text);
          if (lastAssistant.includes(marker)) {
            ws.close();
            return {
              ok: true,
              lastAssistant,
            };
          }
        }
      }
    }

    await sleep(1200);
  }

  ws.close();
  return {
    ok: false,
    lastAssistant,
  };
}

async function testConversationUI() {
  console.log('=== Testing Conversation UI ===\n');

  // Setup conversation first
  const agentId = await setupConversation();
  await sleep(2000);

  const browser = await chromium.launch({ headless: true });
  const page = await browser.newPage();

  // Navigate to app
  console.log('[1/5] Opening Flutter Web app...');
  await page.goto('http://localhost:18086/#/dashboard', { timeout: 60000 });
  await page.waitForTimeout(4000);
  console.log('  ✓ App loaded\n');

  // Login only when login form is visible
  console.log('[2/5] Ensuring connected state...');
  const tokenInput = page.locator('input[type="text"]').first();
  if (await tokenInput.count()) {
    await tokenInput.fill('testtoken123');
    const connectBtn = page.locator('button:has-text("连接")').first();
    if (await connectBtn.count()) {
      await connectBtn.click();
      await page.waitForTimeout(2000);
    }
    console.log('  ✓ Login flow executed\n');
  } else {
    console.log('  ✓ Login form not visible, reusing existing session\n');
  }

  // Open conversation directly by route to avoid canvas text-click flakiness
  console.log('[3/5] Opening conversation route...');
  await page.goto(`http://localhost:18086/#/agent/local-agentd/${agentId}`, { timeout: 60000 });
  await page.waitForTimeout(3000);
  console.log('  ✓ Opened conversation route\n');

  // Send deterministic probe message through RPC
  console.log('[4/5] Sending probe and waiting for assistant reply...');
  const probeWs = new WebSocket(AGENTGW + '?token=' + TOKEN);
  await new Promise((resolve, reject) => {
    probeWs.once('open', resolve);
    probeWs.once('error', reject);
  });

  const marker = 'PT_UI_PROBE_OK_20260402';
  await rpcCall(probeWs, 'conversation.send', {
    nodeId: 'local-agentd',
    agentId,
    message: `Please reply with exactly: ${marker}`,
    raw: false,
  });
  probeWs.close();

  const reply = await waitForAssistantReply(agentId, marker, 90000);
  if (!reply.ok) {
    throw new Error(`assistant reply missing marker, lastAssistant=${JSON.stringify(reply.lastAssistant?.slice(0, 400) || '')}`);
  }
  console.log(`  ✓ Assistant replied with marker: ${marker}\n`);

  // Take screenshot
  console.log('[5/5] Taking screenshot...');
  await page.screenshot({
    path: '/Users/fengming.xie/Documents/project/phone-talk/conversation_ui_result.png',
    fullPage: false
  });
  console.log('  ✓ Screenshot saved\n');

  await browser.close();

  console.log('=== Test Complete ===');
  console.log('Screenshot: conversation_ui_result.png');
  console.log(`AgentId: ${agentId}`);
  console.log(`AssistantMarker: ${marker}`);
}

testConversationUI().catch(err => {
  console.error('Test failed:', err);
  process.exit(1);
});
