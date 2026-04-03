const WebSocket = require('ws');

async function listAgents() {
  return new Promise((resolve, reject) => {
    const ws = new WebSocket('ws://localhost:8080/ws?token=testtoken123');
    
    ws.on('open', () => {
      console.log('Connected to agentgw');
      
      // First add the node if not exists
      const addNodeRequest = {
        jsonrpc: "2.0",
        id: 1,
        method: "node.add",
        params: {
          name: "Local Agentd",
          host: "localhost",
          token: "testtoken123",
          agentdPort: 7373
        }
      };
      
      ws.send(JSON.stringify(addNodeRequest));
    });
    
    ws.on('message', (data) => {
      const msg = JSON.parse(data);
      console.log('Response:', JSON.stringify(msg, null, 2));
      
      if (msg.id === 1) {
        // Node added, now query agent.list
        const listRequest = {
          jsonrpc: "2.0",
          id: 2,
          method: "agent.list",
          params: {
            nodeId: msg.result?.nodeId || "local-agentd"
          }
        };
        ws.send(JSON.stringify(listRequest));
      }
      
      if (msg.id === 2) {
        ws.close();
        resolve(msg);
      }
    });
    
    ws.on('error', (err) => {
      console.error('WebSocket error:', err);
      reject(err);
    });
  });
}

listAgents().then(result => {
  console.log('\nAgent list:', JSON.stringify(result, null, 2));
}).catch(e => {
  console.error('Failed:', e.message);
});
