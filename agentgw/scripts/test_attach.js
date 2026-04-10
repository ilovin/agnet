const WebSocket = require('ws');

const ws = new WebSocket('ws://localhost:8080/ws?token=testtoken123');

ws.on('open', () => {
  console.log('Connected to agentgw');
  
  // First get session catalog for local-agentd
  ws.send(JSON.stringify({
    jsonrpc: '2.0',
    id: 1,
    method: 'session.catalog',
    params: { nodeId: 'local-agentd' }
  }));
});

ws.on('message', (data) => {
  const msg = JSON.parse(data.toString());
  console.log('Response:', JSON.stringify(msg, null, 2));
  
  if (msg.id === 1) {
    // Got catalog, try to attach to first attachable session if any
    const attachable = msg.result?.attachable || [];
    if (attachable.length > 0) {
      const session = attachable[0];
      console.log('\nAttempting to attach to:', session);
      
      ws.send(JSON.stringify({
        jsonrpc: '2.0',
        id: 2,
        method: 'session.attach',
        params: {
          nodeId: 'local-agentd',
          provider: session.provider,
          workDir: session.workDir,
          name: `${session.provider}-attached`,
          pid: session.pid
        }
      }));
    } else {
      console.log('\nNo attachable sessions found');
      ws.close();
    }
  } else if (msg.id === 2) {
    console.log('\nAttach result:', msg);
    ws.close();
  }
});

ws.on('error', (err) => console.log('Error:', err.message));
