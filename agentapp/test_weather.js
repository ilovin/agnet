#!/usr/bin/env node
/**
 * Direct agentd end-to-end test: 
 * 1. Find or create "ut test" agent
 * 2. Send "今天天气如何"
 * 3. Wait for actual natural-language reply (not menu text)
 */
const WebSocket = require('ws');

const TOKEN = 'testtoken123';
const AGENTD = 'ws://localhost:7373/ws?token=' + TOKEN;

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
          if (msg.error) reject(new Error(msg.error.message));
          else resolve(msg.result);
        }
      } catch (e) {}
    };
    ws.on('message', handler);
    ws.send(JSON.stringify({ jsonrpc: '2.0', id, method, params }));
    setTimeout(() => {
      ws.off('message', handler);
      reject(new Error(`RPC timeout: ${method}`));
    }, 15000);
  });
}

async function makeWs() {
  const ws = new WebSocket(AGENTD);
  await new Promise((resolve, reject) => {
    ws.once('open', resolve);
    ws.once('error', reject);
  });
  return ws;
}

async function ensureUtTestAgent() {
  const ws = await makeWs();
  const agents = await rpcCall(ws, 'agent.list', {});
  const active = (agents || []).filter(a => 
    a.name === 'ut test' && 
    a.status !== 'stopped' && 
    a.status !== 'crashed'
  );
  
  if (active.length > 0) {
    console.log(`  Reusing active "ut test" agent: ${active[0].id} (status: ${active[0].status})`);
    ws.close();
    return active[0].id;
  }
  
  console.log('  No active "ut test" agent found, creating one...');
  const created = await rpcCall(ws, 'agent.create', {
    name: 'ut test',
    provider: 'claude',
    workDir: '/tmp',
  });
  ws.close();
  console.log(`  Created agent: ${created.id}`);
  await sleep(4000); // wait for claude to init
  return created.id;
}

async function waitForCleanReply(agentId, timeoutMs = 120000) {
  const ws = await makeWs();
  const started = Date.now();
  let cursor = 0;
  
  // Heuristics for "real" assistant content (not noise/menu text)
  const isRealReply = (text) => {
    if (!text || text.length < 10) return false;
    const lower = text.toLowerCase();
    // Skip if it's menu/control noise
    if (lower.includes('bypass permissions') || 
        lower.includes('shift+tab') ||
        lower.includes('╭─') || lower.includes('╰─') ||
        lower.includes('│') ||
        text.includes('\x1b[') || text.includes('\x1b]')) {
      return false;
    }
    // Must have some real content (Chinese chars, or reasonable English)
    const hasChinese = /[\u4e00-\u9fff]/.test(text);
    const hasRealContent = hasChinese || text.length > 50;
    return hasRealContent;
  };
  
  const seenSeqs = new Set();
  
  while (Date.now() - started < timeoutMs) {
    try {
      const history = await rpcCall(ws, 'conversation.history', { agentId, cursor });
      const events = history?.events || [];
      
      for (const e of events) {
        if (seenSeqs.has(e.seq)) continue;
        seenSeqs.add(e.seq);
        cursor = Math.max(cursor, e.seq);
        
        if (e.role === 'assistant' && !e.raw) {
          console.log(`  [seq=${e.seq}] Assistant (structured): ${JSON.stringify(e.text?.slice(0, 100))}`);
          if (isRealReply(e.text)) {
            ws.close();
            return { ok: true, text: e.text };
          }
        } else if (e.role === 'assistant' && e.raw) {
          // Raw PTY output - show but don't count as a "real reply"
          const preview = (e.text || '').replace(/\x1b\[[0-9;]*m/g, '').trim().slice(0, 80);
          if (preview.length > 3) {
            console.log(`  [seq=${e.seq}] Raw PTY: ${JSON.stringify(preview)}`);
          }
          // But if raw and not menu noise, also accept it
          if (isRealReply(e.text)) {
            ws.close();
            return { ok: true, text: e.text, raw: true };
          }
        }
      }
    } catch (e) {
      console.error('  history error:', e.message);
    }
    
    await sleep(2000);
    const elapsed = Math.floor((Date.now() - started) / 1000);
    if (elapsed % 10 === 0) {
      console.log(`  Waiting... ${elapsed}s / ${timeoutMs/1000}s`);
    }
  }
  
  ws.close();
  return { ok: false };
}

async function main() {
  console.log('=== End-to-End: 今天天气如何 ===\n');
  
  // Step 1: Find or create "ut test" agent
  console.log('[1/3] Ensuring "ut test" agent...');
  const agentId = await ensureUtTestAgent();
  console.log(`  Agent ID: ${agentId}\n`);
  
  // Step 2: Send "今天天气如何"
  console.log('[2/3] Sending "今天天气如何"...');
  const ws = await makeWs();
  
  // Subscribe to push events so we see real-time output
  ws.on('message', (data) => {
    try {
      const msg = JSON.parse(data);
      if (msg.method === 'conversation.message') {
        const { role, text } = msg.params || {};
        const preview = (text || '').replace(/\x1b\[[0-9;]*m/g, '').trim().slice(0, 80);
        if (preview.length > 3) {
          console.log(`  PUSH [${role}]: ${JSON.stringify(preview)}`);
        }
      }
    } catch (e) {}
  });
  
  await rpcCall(ws, 'conversation.send', {
    agentId,
    message: '今天天气如何',
  });
  console.log('  ✓ Message sent\n');
  ws.close();
  
  // Step 3: Wait for reply
  console.log('[3/3] Waiting for clean assistant reply...');
  const reply = await waitForCleanReply(agentId, 120000);
  
  if (reply.ok) {
    console.log('\n✅ SUCCESS: Got assistant reply!');
    console.log('  Text:', JSON.stringify(reply.text?.slice(0, 200)));
  } else {
    console.log('\n❌ FAILED: No clean assistant reply received in 120s');
    process.exit(1);
  }
}

main().catch(err => {
  console.error('Error:', err.message);
  process.exit(1);
});
