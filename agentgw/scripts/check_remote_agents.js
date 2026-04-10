const WebSocket = require('ws');

const ws = new WebSocket('ws://localhost:8080/ws?token=testtoken123');

ws.on('open', () => {
  console.log('Connected to agentgw');
  
  // Get agents from remote-ws
  ws.send(JSON.stringify({
    jsonrpc: '2.0',
    id: 1,
    method: 'agent.list',
    params: { nodeId: 'remote-ws' }
  }));
});

ws.on('message', (data) => {
  const msg = JSON.parse(data.toString());
  console.log('Remote Agents:', JSON.stringify(msg, null, 2));
  ws.close();
});

ws.on('error', (err) => console.log('Error:', err.message));

setTimeout(() => ws.close(), 5000);
