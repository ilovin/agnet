const WebSocket = require('ws');

const nodes = [
  { id: 'local-agentd', name: 'Local' },
  { id: 'remote-ws', name: 'Remote WS' }
];

async function checkNode(node) {
  return new Promise((resolve) => {
    const ws = new WebSocket('ws://localhost:8080/ws?token=testtoken123');
    
    ws.on('open', () => {
      ws.send(JSON.stringify({
        jsonrpc: '2.0',
        id: 1,
        method: 'agent.list',
        params: { nodeId: node.id }
      }));
    });
    
    ws.on('message', (data) => {
      const msg = JSON.parse(data.toString());
      console.log(`\n=== ${node.name} Agents ===`);
      console.log(JSON.stringify(msg, null, 2));
      ws.close();
      resolve();
    });
    
    ws.on('error', (err) => {
      console.log(`\n=== ${node.name} Error ===`);
      console.log(err.message);
      ws.close();
      resolve();
    });
    
    setTimeout(() => { ws.close(); resolve(); }, 5000);
  });
}

(async () => {
  for (const node of nodes) {
    await checkNode(node);
  }
})();
