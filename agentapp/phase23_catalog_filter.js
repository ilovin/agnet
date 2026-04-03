const WebSocket = require('ws');

const URL = 'ws://127.0.0.1:8080/ws?token=testtoken123';

function rpc(ws, method, params = {}) {
  return new Promise((resolve, reject) => {
    const id = Math.floor(Math.random() * 1e6);
    const onMsg = (raw) => {
      try {
        const msg = JSON.parse(raw);
        if (msg.id !== id) return;
        ws.off('message', onMsg);
        if (msg.error) reject(new Error(JSON.stringify(msg.error)));
        else resolve(msg.result);
      } catch {}
    };
    ws.on('message', onMsg);
    ws.send(JSON.stringify({ jsonrpc: '2.0', id, method, params }));
    setTimeout(() => {
      ws.off('message', onMsg);
      reject(new Error(`timeout ${method}`));
    }, 20000);
  });
}

(async () => {
  const ws = new WebSocket(URL);
  await new Promise((resolve, reject) => {
    ws.once('open', resolve);
    ws.once('error', reject);
  });

  const cat = await rpc(ws, 'session.catalog', { nodeId: 'local-agentd' });
  const att = cat.attachable || [];
  const filtered = att.filter(x => x.workDir === '/tmp' || JSON.stringify(x).includes('ut test'));

  console.log('TOTAL', att.length, 'FILTERED', filtered.length);
  console.log(JSON.stringify(filtered.slice(0, 30), null, 2));
})();
