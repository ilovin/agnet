const WebSocket = require('ws');

const ws = new WebSocket('ws://localhost:8080/ws?token=testtoken123');

ws.on('open', () => {
  console.log('Connected to agentgw');
  
  ws.send(JSON.stringify({
    jsonrpc: '2.0',
    id: 1,
    method: 'node.list',
    params: {}
  }));
});

ws.on('message', (data) => {
  const msg = JSON.parse(data.toString());
  console.log('Nodes:', JSON.stringify(msg, null, 2));
});

setTimeout(() => ws.close(), 2000);
