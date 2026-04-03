#!/usr/bin/env node
/**
 * Test script to verify stream-json mode conversation flow
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
    console.log('Creating new "ut test" agent with stream-json mode...');
    const create = await rpc(ws, 'agent.create', {
      nodeId,
      name: 'ut test',
      provider: 'claude',
      workDir: '/tmp'
    });
    console.log('Created agent:', create);

    // Wait for agent to be ready
    await sleep(3000);
    utAgent = { id: create.id };
  } else {
    console.log('Found existing "ut test" agent:', utAgent.id);
  }

  // Get conversation history
  const history = await rpc(ws, 'conversation.history', {
    nodeId,
    agentId: utAgent.id,
    cursor: 0
  });
  console.log(`\nCurrent history: ${history.events?.length || 0} events, lastSeq: ${history.lastSeq}`);

  // Send test message
  const testMessage = 'What is 2+2? Answer with just the number.';
  console.log(`\nSending message: "${testMessage}"`);

  const beforeSeq = history.lastSeq || 0;

  await rpc(ws, 'conversation.send', {
    nodeId,
    agentId: utAgent.id,
    message: testMessage
  });
  console.log('Message sent!');

  // Poll for response
  console.log('Waiting for response...');
  let attempts = 0;
  let assistantResponse = null;

  while (attempts < 30 && !assistantResponse) {
    await sleep(1000);

    const newHistory = await rpc(ws, 'conversation.history', {
      nodeId,
      agentId: utAgent.id,
      cursor: beforeSeq
    });

    const events = newHistory.events || [];
    for (const ev of events) {
      if (ev.role === 'assistant' && ev.text) {
        assistantResponse = ev.text;
        console.log(`\nAssistant response: "${assistantResponse}"`);
        break;
      }
      if (ev.kind === 'permission_request') {
        console.log(`Permission request detected: ${ev.permissionRequest?.tool_name || 'unknown'}`);
      }
    }

    attempts++;
    if (attempts % 5 === 0) {
      console.log(`  ... polled ${attempts} times`);
    }
  }

  if (!assistantResponse) {
    console.log('\nNo assistant response received after 30 seconds');

    // Get final history for debugging
    const finalHistory = await rpc(ws, 'conversation.history', {
      nodeId,
      agentId: utAgent.id,
      cursor: 0
    });
    console.log('\nFinal history:');
    for (const ev of (finalHistory.events || []).slice(-10)) {
      console.log(`  [${ev.role}] ${(ev.text || '').slice(0, 100)}${(ev.text || '').length > 100 ? '...' : ''}`);
    }
  } else {
    console.log('\nTest PASSED: Got assistant response');
  }

  ws.close();
  process.exit(assistantResponse ? 0 : 1);
}

main().catch(err => {
  console.error('Test failed:', err.message);
  process.exit(1);
});
