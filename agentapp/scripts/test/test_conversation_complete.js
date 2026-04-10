#!/usr/bin/env node
/**
 * Complete Conversation Test - Test user message recording and agent replies
 */
const http = require('http');
const WebSocket = require('ws');

const TOKEN = 'testtoken123';
const AGENTGW = 'ws://127.0.0.1:8080/ws';
const WS_TOKEN = '?token=' + TOKEN;

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

async function waitForEvent(ws, eventName, timeout = 15000) {
  return new Promise((resolve, reject) => {
    const handler = (data) => {
      try {
        const msg = JSON.parse(data);
        if (msg.method === eventName) {
          ws.off('message', handler);
          resolve(msg.params);
        }
      } catch (e) {}
    };
    ws.on('message', handler);
    setTimeout(() => {
      ws.off('message', handler);
      reject(new Error(`Event ${eventName} timeout`));
    }, timeout);
  });
}

async function testConversation() {
  console.log('=== Complete Conversation Test ===\n');

  // 1. Connect to agentgw
  console.log('[1/8] Connecting to agentgw...');
  const ws = new WebSocket(AGENTGW + WS_TOKEN);
  await new Promise((resolve, reject) => {
    ws.once('open', resolve);
    ws.once('error', reject);
    setTimeout(() => reject(new Error('Connection timeout')), 5000);
  });
  console.log('  ✓ Connected to agentgw\n');

  // Set up event listener
  const events = [];
  ws.on('message', (data) => {
    try {
      const msg = JSON.parse(data);
      if (msg.method) {
        events.push({ time: Date.now(), method: msg.method, params: msg.params });
      }
    } catch (e) {}
  });

  // 2. List nodes
  console.log('[2/8] Listing nodes...');
  const nodes = await rpcCall(ws, 'node.list');
  console.log('  Nodes:', nodes.map(n => `${n.id} (${n.name}): ${n.status}`).join(', '));
  const connectedNodes = nodes.filter(n => n.status === 'connected');
  if (connectedNodes.length === 0) {
    throw new Error('No connected nodes found');
  }
  console.log(`  ✓ Found ${connectedNodes.length} connected nodes\n`);

  // 3. Get session catalog to find available sessions
  console.log('[3/8] Getting session catalog...');
  const catalog = await rpcCall(ws, 'session.catalog_all');
  console.log(`  Node items: ${catalog.items?.length || 0}`);
  console.log(`  Errors: ${catalog.errors?.length || 0}`);

  // Find first node with available agents
  let targetAgent = null;
  let targetNodeId = null;

  for (const item of catalog.items || []) {
    // Try to use existing managed agent
    if (item.managed && item.managed.length > 0) {
      const active = item.managed.find(a => a.status !== 'stopped');
      if (active) {
        console.log(`  Using existing managed agent: ${active.id} on node ${item.nodeId}`);
        targetAgent = active;
        targetNodeId = item.nodeId;
        break;
      }
    }

    // Try to attach to an existing process
    if (!targetAgent && item.attachable && item.attachable.length > 0) {
      const proc = item.attachable[0];
      console.log(`  Attaching to process PID ${proc.pid} (${proc.provider}) on node ${item.nodeId}...`);
      try {
        const result = await rpcCall(ws, 'session.attach', { nodeId: item.nodeId, pid: proc.pid });
        console.log(`  ✓ Attached, agent ID: ${result.id}`);
        targetAgent = { id: result.id, status: 'idle' };
        targetNodeId = item.nodeId;
        await sleep(1000);
        break;
      } catch (e) {
        console.log(`  ✗ Attach failed: ${e.message}`);
      }
    }
  }

  // Create new agent if needed (use first connected node)
  if (!targetAgent) {
    const firstNode = catalog.items?.[0];
    if (!firstNode) {
      throw new Error('No available nodes to create agent');
    }
    targetNodeId = firstNode.nodeId;
    console.log(`  Creating new agent on node ${targetNodeId}...`);
    const result = await rpcCall(ws, 'agent.create', {
      nodeId: targetNodeId,
      name: 'test-agent',
      provider: 'claude',
      workDir: '/tmp'
    });
    console.log(`  ✓ Created agent: ${result.id}`);
    targetAgent = { id: result.id, status: 'idle' };
    await sleep(2000);
  }
  console.log();

  // 4. Get conversation history (before sending message)
  console.log('[4/8] Getting conversation history (before)...');
  const historyBefore = await rpcCall(ws, 'conversation.history', { nodeId: targetNodeId, agentId: targetAgent.id });
  console.log(`  Events count: ${historyBefore.events?.length || 0}`);
  console.log(`  Last sequence: ${historyBefore.lastSeq}`);
  if (historyBefore.events && historyBefore.events.length > 0) {
    console.log('  Last 3 events:');
    historyBefore.events.slice(-3).forEach(e => {
      console.log(`    - [${e.seq}] ${e.role}: ${e.text?.substring(0, 50) || '(no text)'}`);
    });
  }
  console.log();

  // 5. Send a user message
  console.log('[5/8] Sending user message...');
  const testMessage = `Hello! This is a test message at ${new Date().toISOString()}`;
  events.length = 0;
  await rpcCall(ws, 'conversation.send', {
    nodeId: targetNodeId,
    agentId: targetAgent.id,
    message: testMessage,
    raw: false
  });
  console.log(`  ✓ Message sent: "${testMessage.substring(0, 50)}..."`);
  console.log();

  // 6. Check for conversation.message event
  console.log('[6/8] Waiting for conversation.message event...');
  await sleep(500);
  const userMessageEvents = events.filter(e => e.method === 'conversation.message' && e.params?.role === 'user');
  console.log(`  User message events received: ${userMessageEvents.length}`);
  if (userMessageEvents.length > 0) {
    const lastEvent = userMessageEvents[userMessageEvents.length - 1];
    console.log(`  ✓ Event received: role=${lastEvent.params.role}, text=${lastEvent.params.text?.substring(0, 50)}`);
  } else {
    console.log('  ✗ No user message event received');
  }
  console.log();

  // 7. Get conversation history (after sending message)
  console.log('[7/8] Getting conversation history (after)...');
  await sleep(1000);
  const historyAfter = await rpcCall(ws, 'conversation.history', { nodeId: targetNodeId, agentId: targetAgent.id });
  console.log(`  Events count: ${historyAfter.events?.length || 0}`);
  console.log(`  Last sequence: ${historyAfter.lastSeq}`);

  // Check if user message is in history
  const userMessagesInHistory = historyAfter.events?.filter(e => e.role === 'user' && e.text?.includes('test message')) || [];
  console.log(`  User messages in history: ${userMessagesInHistory.length}`);

  if (historyAfter.events && historyAfter.events.length > 0) {
    console.log('  All events:');
    historyAfter.events.forEach(e => {
      const text = e.text?.substring(0, 60).replace(/\n/g, '\\n') || '(no text)';
      console.log(`    - [${e.seq}] ${e.role}: ${text}`);
    });
  }
  console.log();

  // 8. Summary
  console.log('[8/8] Test Summary');
  console.log('='.repeat(50));
  const tests = [
    { name: 'Connection', pass: true },
    { name: 'Session Catalog', pass: !!catalog },
    { name: 'Agent Available', pass: !!targetAgent },
    { name: 'Message Send', pass: true },
    { name: 'User Event Received', pass: userMessageEvents.length > 0 },
    { name: 'User Message in History', pass: userMessagesInHistory.length > 0 },
  ];

  tests.forEach(t => {
    console.log(`  ${t.pass ? '✓' : '✗'} ${t.name}`);
  });

  const allPassed = tests.every(t => t.pass);
  console.log('='.repeat(50));
  console.log(allPassed ? '✓ ALL TESTS PASSED' : '✗ SOME TESTS FAILED');

  ws.close();
  process.exit(allPassed ? 0 : 1);
}

testConversation().catch(err => {
  console.error('Test failed:', err.message);
  process.exit(1);
});
