#!/usr/bin/env node
/**
 * Test weather question with permission handling
 */
const WebSocket = require('ws');

const URL = 'ws://127.0.0.1:8080/ws?token=testtoken123';

function rpc(ws, method, params = {}) {
  return new Promise((resolve, reject) => {
    const id = Math.floor(Math.random() * 1e6);
    const handler = (data) => {
      try {
        const msg = JSON.parse(data);
        if (msg.id === id) {
          ws.off('message', handler);
          if (msg.error) reject(new Error(JSON.stringify(msg.error)));
          else resolve(msg.result);
        }
      } catch {}
    };
    ws.on('message', handler);
    ws.send(JSON.stringify({ jsonrpc: '2.0', id, method, params }));
    setTimeout(() => {
      ws.off('message', handler);
      reject(new Error('timeout'));
    }, 30000);
  });
}

async function sleep(ms) {
  return new Promise(r => setTimeout(r, ms));
}

async function main() {
  console.log('Connecting to agentgw...');
  const ws = new WebSocket(URL);

  await new Promise((resolve, reject) => {
    ws.once('open', resolve);
    ws.once('error', reject);
  });
  console.log('Connected!');

  const nodeId = 'local-agentd';

  // List existing agents
  const list = await rpc(ws, 'agent.list', { nodeId });
  const agents = Array.isArray(list) ? list : (list?.agents || []);
  console.log(`Found ${agents.length} agents`);

  // Find or create "ut test" agent
  let utAgent = agents.find(a => a.name === 'ut test' && !['stopped', 'crashed'].includes(a.status));

  if (!utAgent) {
    console.log('Creating new "ut test" agent...');
    const create = await rpc(ws, 'agent.create', {
      nodeId,
      name: 'ut test',
      provider: 'claude',
      workDir: '/tmp'
    });
    console.log('Created agent:', create.id);
    utAgent = { id: create.id, status: 'idle' };
    await sleep(3000);
  } else {
    console.log('Found existing "ut test" agent:', utAgent.id, 'status:', utAgent.status);
  }

  // Get conversation history
  const history = await rpc(ws, 'conversation.history', {
    nodeId,
    agentId: utAgent.id,
    cursor: 0
  });
  console.log(`Current history: ${history.events?.length || 0} events`);

  // Send weather question
  const weatherQuestion = '今天天气如何';
  console.log(`\nSending: "${weatherQuestion}"`);

  const beforeSeq = history.lastSeq || 0;

  await rpc(ws, 'conversation.send', {
    nodeId,
    agentId: utAgent.id,
    message: weatherQuestion
  });
  console.log('Message sent!');

  // Poll for response
  console.log('Waiting for response...');
  let attempts = 0;
  let assistantResponse = null;
  let permissionMenuDetected = false;

  while (attempts < 45 && !assistantResponse) {
    await sleep(1000);

    const newHistory = await rpc(ws, 'conversation.history', {
      nodeId,
      agentId: utAgent.id,
      cursor: beforeSeq
    });

    const events = newHistory.events || [];
    for (const ev of events) {
      if (ev.role === 'assistant' && ev.text) {
        // Check if this is actual content or just menu text
        const text = ev.text;
        if (text.includes('bypass') || text.includes('permission') || text.includes('shift+tab')) {
          console.log(`  Permission menu text detected in event`);
          permissionMenuDetected = true;
        } else if (text.length > 20 && !text.startsWith('[')) {
          assistantResponse = text;
          console.log(`\nAssistant response: "${assistantResponse.substring(0, 200)}..."`);
          break;
        }
      }
    }

    attempts++;
    if (attempts % 5 === 0) {
      console.log(`  ... polled ${attempts} times`);
    }
  }

  if (assistantResponse) {
    console.log('\n=== TEST PASSED ===');
    console.log('Got natural language response for weather question');
  } else {
    console.log('\n=== TEST FAILED ===');
    console.log(permissionMenuDetected ?
      'Permission menu was not automatically resolved' :
      'No response received');

    // Debug: show last few events
    const finalHistory = await rpc(ws, 'conversation.history', {
      nodeId,
      agentId: utAgent.id,
      cursor: 0
    });
    console.log('\nLast 5 events:');
    for (const ev of (finalHistory.events || []).slice(-5)) {
      console.log(`  [${ev.role}] ${(ev.text || '').substring(0, 80)}...`);
    }
  }

  ws.close();
  process.exit(assistantResponse ? 0 : 1);
}

main().catch(err => {
  console.error('Test failed:', err.message);
  process.exit(1);
});
