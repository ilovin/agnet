const WebSocket = require('ws');
const ws = new WebSocket('ws://localhost:8080/ws?token=testtoken123');
ws.on('open', () => {
  ws.send(JSON.stringify({jsonrpc: '2.0', id: 1, method: 'session.catalog', params: {nodeId: 'remote-ws'}}));
});
ws.on('message', (data) => {
  const msg = JSON.parse(data.toString());
  if (msg.result) {
    const attachable = msg.result.attachable || [];
    console.log('Remote attachable sessions:', attachable.length);
    attachable.forEach((s, i) => {
      console.log(`  ${i}: ${s.provider} PID ${s.pid} - ${s.workDir}`);
    });
  } else {
    console.log('Error:', msg.error);
  }
  ws.close();
});
ws.on('error', (err) => console.log('Error:', err.message));
setTimeout(() => ws.close(), 5000);
