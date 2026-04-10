#!/usr/bin/env node
/**
 * Test Agent Reply with Long Wait - 等待 watcher 找到 JSONL 文件
 */
const WebSocket = require('ws');

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
    }, 15000);
  });
}

async function testAgentReply() {
  console.log('=== Agent Reply Test (Long Wait) ===\n');

  // 连接
  console.log('[1/7] Connecting...');
  const ws = new WebSocket(AGENTGW + '?token=' + TOKEN);
  await new Promise((resolve, reject) => {
    ws.once('open', resolve);
    ws.once('error', reject);
    setTimeout(() => reject(new Error('Connection timeout')), 5000);
  });
  console.log('  ✓ Connected\n');

  // 收集所有事件
  const events = [];
  ws.on('message', (data) => {
    try {
      const msg = JSON.parse(data);
      if (msg.method === 'conversation.message') {
        events.push({
          time: Date.now(),
          role: msg.params.role,
          raw: msg.params.raw,
          text: msg.params.text
        });
        const preview = msg.params.text?.substring(0, 50).replace(/\n/g, '\\n');
        console.log(`  [${events.length}] ${msg.params.role} raw=${msg.params.raw}: ${preview}`);
      }
    } catch (e) {}
  });

  // 创建 agent
  console.log('[2/7] Creating agent...');
  const agentResult = await rpcCall(ws, 'agent.create', {
    nodeId: 'local-agentd',
    name: 'test-agent-reply',
    provider: 'claude',
    workDir: '/tmp'
  });
  const agentId = agentResult.id;
  console.log(`  ✓ Agent: ${agentId}\n`);

  // 等待 agent 启动
  console.log('[3/7] Waiting for agent to start (3s)...');
  await sleep(3000);

  // 发送用户消息（触发 JSONL 文件创建）
  console.log('[4/7] Sending user message to trigger JSONL creation...');
  await rpcCall(ws, 'conversation.send', {
    nodeId: 'local-agentd',
    agentId: agentId,
    message: 'What is 2+2?',
    raw: false
  });
  console.log('  ✓ Message sent\n');

  // 等待更长时间，让 watcher 找到 JSONL 文件并解析
  console.log('[5/7] Waiting 45 seconds for watcher to find JSONL and parse replies...');
  for (let i = 0; i < 45; i++) {
    await sleep(1000);
    process.stdout.write('.');
    if ((i + 1) % 10 === 0) {
      console.log(` ${i + 1}s`);
    }
  }
  console.log('\n');

  // 获取历史记录
  console.log('[6/7] Getting conversation history...');
  const history = await rpcCall(ws, 'conversation.history', {
    nodeId: 'local-agentd',
    agentId: agentId
  });

  console.log(`\n  History: ${history.events?.length || 0} events`);
  if (history.events) {
    history.events.forEach(e => {
      const text = e.text?.substring(0, 80).replace(/\n/g, '\\n');
      console.log(`    [${e.seq}] ${e.role} raw=${e.raw}: ${text}`);
    });
  }

  // 分析结果
  console.log('\n[7/7] Analysis');
  console.log('='.repeat(60));
  const userEvents = events.filter(e => e.role === 'user');
  const assistantEvents = events.filter(e => e.role === 'assistant');
  const rawAssistantEvents = assistantEvents.filter(e => e.raw === true);
  const structuredAssistantEvents = assistantEvents.filter(e => e.raw === false);

  console.log(`Total events received: ${events.length}`);
  console.log(`  User: ${userEvents.length}`);
  console.log(`  Assistant: ${assistantEvents.length}`);
  console.log(`    - Raw (ANSI): ${rawAssistantEvents.length}`);
  console.log(`    - Structured (from JSONL): ${structuredAssistantEvents.length}`);

  // 历史记录分析
  const historyUser = history.events?.filter(e => e.role === 'user') || [];
  const historyAssistant = history.events?.filter(e => e.role === 'assistant') || [];
  const historyStructured = historyAssistant.filter(e => e.raw === false);

  console.log(`\nHistory analysis:`);
  console.log(`  User events: ${historyUser.length}`);
  console.log(`  Assistant events: ${historyAssistant.length}`);
  console.log(`  Structured assistant: ${historyStructured.length}`);

  if (historyStructured.length > 0) {
    console.log('\n✓ SUCCESS: Found structured assistant replies from watcher!');
    historyStructured.forEach(e => {
      console.log(`  - ${e.text?.substring(0, 100)}`);
    });
  } else {
    console.log('\n✗ No structured assistant replies found');
    console.log('  Watcher may not have found the JSONL file');
  }

  // 清理
  console.log('\n[Cleanup] Stopping agent...');
  try {
    await rpcCall(ws, 'agent.stop', { nodeId: 'local-agentd', agentId });
    console.log('  ✓ Agent stopped');
  } catch (e) {}
  ws.close();
}

testAgentReply().catch(err => {
  console.error('Test failed:', err.message);
  process.exit(1);
});
