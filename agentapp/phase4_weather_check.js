const WebSocket = require('ws');

const URL = 'ws://127.0.0.1:8080/ws?token=testtoken123';
const NODE_ID = 'local-agentd';

function sleep(ms) { return new Promise(r => setTimeout(r, ms)); }

function clean(text) {
  return (text || '')
    .replace(/\x1b\[[^a-zA-Z]*[a-zA-Z]/g, '')
    .replace(/\x1b\][^\x07]*\x07/g, '')
    .replace(/\r/g, '')
    .trim();
}

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
    }, 25000);
  });
}

(async () => {
  const ws = new WebSocket(URL);
  await new Promise((resolve, reject) => {
    ws.once('open', resolve);
    ws.once('error', reject);
  });

  const list = await rpc(ws, 'agent.list', { nodeId: NODE_ID });
  const agents = Array.isArray(list) ? list : (list?.agents || []);
  let ut = agents.find(a => a.name === 'ut test' && !['stopped', 'crashed'].includes(a.status));

  if (!ut) {
    const created = await rpc(ws, 'session.create', {
      nodeId: NODE_ID,
      name: 'ut test',
      provider: 'claude',
      workDir: '/tmp',
    });
    ut = { id: created.id };
    console.log('CREATED', ut.id);
    await sleep(2500);
  } else {
    console.log('REUSE', ut.id, ut.status);
  }

  await rpc(ws, 'session.attach', { nodeId: NODE_ID, agentId: ut.id });

  // Start from current tail to avoid reprocessing old noisy history
  const baseline = await rpc(ws, 'conversation.history', {
    nodeId: NODE_ID,
    agentId: ut.id,
    cursor: 0,
  });
  let cursor = baseline.lastSeq || 0;

  await rpc(ws, 'conversation.send', {
    nodeId: NODE_ID,
    agentId: ut.id,
    message: '今天天气如何',
  });
  console.log('SENT weather question from cursor', cursor);

  const start = Date.now();
  let resolvedCount = 0;

  while (Date.now() - start < 180000) {
    const hist = await rpc(ws, 'conversation.history', {
      nodeId: NODE_ID,
      agentId: ut.id,
      cursor,
    });

    const events = hist.events || [];
    for (const e of events) {
      cursor = Math.max(cursor, e.seq || 0);
      const t = clean(e.text);
      if (!t) continue;

      if (/bypass\s*permissions|shift\+tab/i.test(t)) {
        if (resolvedCount < 5) {
          await rpc(ws, 'conversation.key', { nodeId: NODE_ID, agentId: ut.id, key: 'tab' });
          await rpc(ws, 'conversation.key', { nodeId: NODE_ID, agentId: ut.id, key: 'enter' });
          resolvedCount++;
          console.log('RESOLVED permission prompt', resolvedCount);
          await sleep(300);
        }
        continue;
      }

      if (e.role === 'assistant') {
        if (/bypass|shift\+tab|cycle\)|ctrl\+g|\/effort|checking for updates|auto-update|^[-─│╭╰╮╯\s]+$/i.test(t)) continue;
        if (/^[✢✳✶✻✽·⏺⠂⠐\s]+$/i.test(t)) continue;
        if (t.length < 20) continue;
        // Require natural-language looking content
        const hasZh = /[\u4e00-\u9fff]/.test(t);
        const hasSentence = /[。！？?!.]/.test(t);
        if (!(hasZh && hasSentence)) continue;

        console.log('ASSISTANT_REPLY', JSON.stringify(t.slice(0, 500)));
        process.exit(0);
      }
    }

    await sleep(1200);
  }

  console.log('FAILED: no assistant reply in 180s');
  process.exit(1);
})().catch((e) => {
  console.error('ERROR', e.message);
  process.exit(1);
});
