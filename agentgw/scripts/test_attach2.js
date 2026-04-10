const WebSocket = require('ws');

const ws = new WebSocket('ws://localhost:8080/ws?token=testtoken123');

ws.on('open', () => {
  console.log('Connected');
  
  // Attach to the real Claude process (PID 24184)
  ws.send(JSON.stringify({
    jsonrpc: '2.0',
    id: 1,
    method: 'session.attach',
    params: {
      nodeId: 'local-agentd',
      provider: 'claude',
      workDir: '/Users/fengming.xie/Documents/project/phone-talk',
      name: 'claude-attached',
      pid: 24184
    }
  }));
});

ws.on('message', (data) => {
  const msg = JSON.parse(data.toString());
  console.log('Response:', JSON.stringify(msg, null, 2));
  
  if (msg.id === 1 && msg.result) {
    const agentId = msg.result.id;
    console.log('\nAttached! Agent ID:', agentId);
    
    // Test conversation.send
    ws.send(JSON.stringify({
      jsonrpc: '2.0',
      id: 2,
      method: 'conversation.send',
      params: {
        nodeId: 'local-agentd',
        agentId: agentId,
        message: 'pwd'
      }
    }));
  } else if (msg.id === 2) {
    console.log('\nSend result:', msg);
    ws.close();
  }
});

ws.on('error', (err) => console.log('Error:', err.message));
