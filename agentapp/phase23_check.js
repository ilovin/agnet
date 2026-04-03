const WebSocket = require('ws');

const URL = 'ws://127.0.0.1:8080/ws?token=testtoken123';

function sleep(ms) { return new Promise(r => setTimeout(r, ms)); }

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

  const nodeId = 'local-agentd';
  const list = await rpc(ws, 'agent.list', { nodeId });
  const agents = Array.isArray(list) ? list : (list?.agents || []);

  let ut = agents.find(a => a.name === 'ut test' && !['stopped', 'crashed'].includes(a.status));

  if (!ut) {
    const created = await rpc(ws, 'session.create', {
      nodeId,
      name: 'ut test',
      provider: 'claude',
      workDir: '/tmp',
    });
    console.log('CREATED', created.id);
    await sleep(2500);

    const list2 = await rpc(ws, 'agent.list', { nodeId });
    const agents2 = Array.isArray(list2) ? list2 : (list2?.agents || []);
    ut = agents2.find(a => a.id === created.id) || { id: created.id };
  } else {
    console.log('REUSE', ut.id, ut.status);
  }

  // Attach fixed managed session by agentId
  const attachRes = await rpc(ws, 'session.attach', {
    nodeId,
    agentId: ut.id,
  });
  console.log('ATTACH_OK', JSON.stringify(attachRes));

  const sendRes = await rpc(ws, 'conversation.send', {
    nodeId,
    agentId: ut.id,
    message: 'ping',
  });
  console.log('SEND_OK', JSON.stringify(sendRes));

  process.exit(0);
})().catch((e) => {
  console.error('ERROR', e.message);
  process.exit(1);
});
