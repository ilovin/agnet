const WebSocket = require('ws');

const ws = new WebSocket('ws://localhost:8080/ws?token=testtoken123');
let agentId = null;
const events = [];

ws.on('open', () => {
  console.log('Connected');
  
  // Create a new agent
  ws.send(JSON.stringify({
    jsonrpc: '2.0',
    id: 1,
    method: 'agent.create',
    params: {
      nodeId: 'local-agentd',
      name: 'test-conversation',
      provider: 'claude',
      workDir: '/tmp'
    }
  }));
});

ws.on('message', (data) => {
  const msg = JSON.parse(data.toString());
  
  if (msg.method) {
    events.push(msg);
    console.log('[EVENT]', msg.method, 'text length:', msg.params?.text?.length);
    return;
  }
  
  if (msg.id === 1) {
    if (msg.error) {
      console.log('Create error:', msg.error);
      ws.close();
      return;
    }
    agentId = msg.result?.id;
    console.log('Created agent:', agentId);
    
    // Wait for agent to start and collect initial events
    setTimeout(() => {
      console.log('\n=== Sending message ===');
      ws.send(JSON.stringify({
        jsonrpc: '2.0',
        id: 2,
        method: 'conversation.send',
        params: {
          nodeId: 'local-agentd',
          agentId: agentId,
          message: 'echo "test message 1"'
        }
      }));
    }, 3000);
    
    // Check history after some time
    setTimeout(() => {
      console.log('\n=== Checking history ===');
      ws.send(JSON.stringify({
        jsonrpc: '2.0',
        id: 3,
        method: 'conversation.history',
        params: {
          nodeId: 'local-agentd',
          agentId: agentId,
          cursor: 0
        }
      }));
    }, 10000);
    
    setTimeout(() => {
      console.log('\n=== Summary ===');
      console.log('Events received:', events.length);
      console.log('History events: will be shown above');
      ws.close();
    }, 12000);
  } else if (msg.id === 2) {
    console.log('Send result:', msg.error || 'success');
  } else if (msg.id === 3) {
    const events = msg.result?.events || [];
    console.log('History count:', events.length);
    events.forEach((e, i) => {
      const text = e.text || '';
      console.log(`  ${i}: [${e.role}] len=${text.length} text="${text.slice(0, 80).replace(/\n/g, '\\n')}"`);
    });
  }
});

ws.on('error', (err) => console.log('Error:', err.message));
