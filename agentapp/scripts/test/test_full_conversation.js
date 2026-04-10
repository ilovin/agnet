#!/usr/bin/env node
/**
 * Full Conversation Test - 验证当前对话系统实现
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

async function testFullConversation() {
  console.log('=== Full Conversation Test ===\n');

  // 1. Connect to agentgw
  console.log('[1/10] Connecting to agentgw...');
  const ws = new WebSocket(AGENTGW + WS_TOKEN);
  await new Promise((resolve, reject) => {
    ws.once('open', resolve);
    ws.once('error', reject);
    setTimeout(() => reject(new Error('Connection timeout')), 5000);
  });
  console.log('  ✓ Connected\n');

  // Track all events
  const events = [];
  ws.on('message', (data) => {
    try {
      const msg = JSON.parse(data);
      if (msg.method === 'conversation.message') {
        events.push({
          time: Date.now(),
          role: msg.params.role,
          text: msg.params.text?.substring(0, 50),
          raw: msg.params.raw === true
        });
      }
    } catch (e) {}
  });

  // 2. List nodes
  console.log('[2/10] Listing nodes...');
  const nodes = await rpcCall(ws, 'node.list');
  console.log(`  Nodes: ${nodes.map(n => `${n.name}(${n.status})`).join(', ')}`);
  const connectedNodes = nodes.filter(n => n.status === 'connected');
  if (connectedNodes.length === 0) {
    throw new Error('No connected nodes');
  }
  console.log(`  ✓ ${connectedNodes.length} nodes connected\n`);

  // 3. Create new agent
  console.log('[3/10] Creating new agent...');
  const agentResult = await rpcCall(ws, 'agent.create', {
    nodeId: 'local-agentd',
    name: 'test-full-conversation',
    provider: 'claude',
    workDir: '/tmp'
  });
  const agentId = agentResult.id;
  console.log(`  ✓ Created agent: ${agentId}\n`);

  // 4. Wait for agent to start
  console.log('[4/10] Waiting for agent to start...');
  await sleep(2000);

  // 5. Get initial history
  console.log('[5/10] Getting initial history...');
  const historyBefore = await rpcCall(ws, 'conversation.history', {
    nodeId: 'local-agentd',
    agentId: agentId
  });
  console.log(`  Events: ${historyBefore.events?.length || 0}, LastSeq: ${historyBefore.lastSeq}`);
  if (historyBefore.events?.length > 0) {
    const lastEvent = historyBefore.events[historyBefore.events.length - 1];
    console.log(`  Last event: seq=${lastEvent.seq}, role=${lastEvent.role}, raw=${lastEvent.raw}`);
  }
  console.log();

  // 6. Send user message
  console.log('[6/10] Sending user message...');
  const userMessage = `Test message at ${new Date().toISOString()}`;
  const sendResult = await rpcCall(ws, 'conversation.send', {
    nodeId: 'local-agentd',
    agentId: agentId,
    message: userMessage,
    raw: false
  });
  console.log(`  ✓ Message sent: ${sendResult.ok}\n`);

  // 7. Wait for events
  console.log('[7/10] Waiting for events...');
  await sleep(3000);
  console.log(`  Events received: ${events.length}`);
  events.forEach((e, i) => {
    console.log(`    [${i+1}] role=${e.role}, raw=${e.raw}, text=${e.text?.substring(0, 40)}`);
  });
  console.log();

  // 8. Get history after send
  console.log('[8/10] Getting history after send...');
  const historyAfter = await rpcCall(ws, 'conversation.history', {
    nodeId: 'local-agentd',
    agentId: agentId
  });
  console.log(`  Events: ${historyAfter.events?.length || 0}, LastSeq: ${historyAfter.lastSeq}`);

  // 9. Analyze results
  console.log('[9/10] Analyzing results...');
  const userEvents = events.filter(e => e.role === 'user');
  const assistantEvents = events.filter(e => e.role === 'assistant');
  const rawEvents = events.filter(e => e.raw === true);

  console.log(`  User events: ${userEvents.length}`);
  console.log(`  Assistant events: ${assistantEvents.length}`);
  console.log(`  Raw events: ${rawEvents.length}`);

  // 10. Final summary
  console.log('\n[10/10] Test Summary');
  console.log('='.repeat(60));

  const tests = [
    { name: 'Connection', pass: true },
    { name: 'Node List', pass: connectedNodes.length > 0 },
    { name: 'Agent Creation', pass: !!agentId },
    { name: 'User Message Send', pass: sendResult.ok === true },
    { name: 'User Events Received', pass: userEvents.length > 0 },
    { name: 'Assistant Events Received', pass: assistantEvents.length > 0 },
    { name: 'Raw Flag Present', pass: rawEvents.length > 0 },
    { name: 'History API Works', pass: historyAfter.events !== null },
  ];

  let passCount = 0;
  tests.forEach(t => {
    const status = t.pass ? '✓' : '✗';
    console.log(`  ${status} ${t.name}`);
    if (t.pass) passCount++;
  });

  console.log('='.repeat(60));
  console.log(`Result: ${passCount}/${tests.length} tests passed`);

  const allPassed = passCount === tests.length;
  console.log(allPassed ? '\n✓ ALL TESTS PASSED' : '\n✗ SOME TESTS FAILED');

  // Cleanup
  console.log('\n[Cleanup] Stopping agent...');
  try {
    await rpcCall(ws, 'agent.stop', {
      nodeId: 'local-agentd',
      agentId: agentId
    });
    console.log('  ✓ Agent stopped');
  } catch (e) {
    console.log(`  Note: ${e.message}`);
  }

  ws.close();
  process.exit(allPassed ? 0 : 1);
}

testFullConversation().catch(err => {
  console.error('\nTest failed:', err.message);
  process.exit(1);
});
