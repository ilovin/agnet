const WebSocket = require('ws');

const ws = new WebSocket('ws://localhost:8080/ws?token=testtoken123');
let agentId = null;
const events = [];

ws.on('open', () => {
  console.log('Connected');
  ws.send(JSON.stringify({
    jsonrpc: '2.0', id: 1,
    method: 'agent.create',
    params: { nodeId: 'local-agentd', name: 'test', provider: 'claude', workDir: '/tmp' }
  }));
});

ws.on('message', (data) => {
  const msg = JSON.parse(data.toString());
  
  if (msg.method) {
    events.push({method: msg.method, role: msg.params?.role, text: msg.params?.text?.slice(0, 50)});
    console.log('[EVENT]', msg.method, msg.params?.role, `"${msg.params?.text?.slice(0, 40)}"`);
    return;
  }
  
  if (msg.id === 1) {
    agentId = msg.result?.id;
    console.log('Agent:', agentId);
    setTimeout(() => {
      console.log('\n=== User sending: echo hello ===');
      ws.send(JSON.stringify({
        jsonrpc: '2.0', id: 2,
        method: 'conversation.send',
        params: { nodeId: 'local-agentd', agentId, message: 'echo hello' }
      }));
    }, 3000);
    
    setTimeout(() => {
      console.log('\n=== History ===');
      ws.send(JSON.stringify({
        jsonrpc: '2.0', id: 3,
        method: 'conversation.history',
        params: { nodeId: 'local-agentd', agentId, cursor: 0 }
      }));
    }, 8000);
    
    setTimeout(() => {
      console.log('\n=== Summary ===');
      console.log('Total events:', events.length);
      console.log('User events:', events.filter(e => e.role === 'user').length);
      console.log('Assistant events:', events.filter(e => e.role === 'assistant').length);
      ws.close();
    }, 10000);
  } else if (msg.id === 2) {
    console.log('Send:', msg.error || 'OK');
  } else if (msg.id === 3) {
    const evs = msg.result?.events || [];
    console.log('History entries:', evs.length);
    evs.forEach((e, i) => console.log(`  ${i}: [${e.role}] "${e.text?.slice(0, 50)}"`));
  }
});

ws.on('error', (err) => console.log('Error:', err.message));
