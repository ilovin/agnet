const WebSocket = require('ws');

async function listNodes() {
  return new Promise((resolve, reject) => {
    const ws = new WebSocket('ws://localhost:8080/ws?token=testtoken123');

    ws.on('open', () => {
      console.log('Connected to agentgw');
      const request = {
        jsonrpc: "2.0",
        id: 1,
        method: "node.list",
        params: {}
      };
      ws.send(JSON.stringify(request));
    });

    ws.on('message', (data) => {
      const msg = JSON.parse(data);
      console.log('Nodes:', JSON.stringify(msg, null, 2));
      ws.close();
      resolve(msg);
    });

    ws.on('error', (err) => {
      console.error('Error:', err);
      reject(err);
    });
  });
}

listNodes().catch(e => console.error('Failed:', e.message));
