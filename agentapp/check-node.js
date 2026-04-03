const WebSocket = require('ws');

async function checkNode() {
  return new Promise((resolve, reject) => {
    const ws = new WebSocket('ws://localhost:8080/ws?token=testtoken123');

    ws.on('open', () => {
      console.log('Connected to agentgw');
      const listRequest = {
        jsonrpc: "2.0",
        id: 1,
        method: "agent.list",
        params: { nodeId: "local-agentd" }
      };
      ws.send(JSON.stringify(listRequest));
    });

    ws.on('message', (data) => {
      const msg = JSON.parse(data);
      console.log('Response:', JSON.stringify(msg, null, 2));
      ws.close();
      resolve(msg);
    });

    ws.on('error', (err) => {
      console.error('Error:', err);
      reject(err);
    });
  });
}

checkNode().catch(e => console.error('Failed:', e.message));
