const WebSocket = require('ws');
const URL = 'ws://127.0.0.1:8080/ws?token=testtoken123';
const NODE_ID = 'local-agentd';

function sleep(ms){return new Promise(r=>setTimeout(r,ms));}
function clean(t){return (t||'').replace(/\x1b\[[^a-zA-Z]*[a-zA-Z]/g,'').replace(/\x1b\][^\x07]*\x07/g,'').replace(/\r/g,'').trim();}
function rpc(ws,method,params={}){return new Promise((resolve,reject)=>{const id=Math.floor(Math.random()*1e6);const h=(raw)=>{try{const m=JSON.parse(raw);if(m.id!==id)return;ws.off('message',h);if(m.error)reject(new Error(JSON.stringify(m.error)));else resolve(m.result);}catch{}};ws.on('message',h);ws.send(JSON.stringify({jsonrpc:'2.0',id,method,params}));setTimeout(()=>{ws.off('message',h);reject(new Error('timeout '+method));},25000);});}

(async()=>{
  const ws = new WebSocket(URL);
  await new Promise((res,rej)=>{ws.once('open',res);ws.once('error',rej);});
  const list=await rpc(ws,'agent.list',{nodeId:NODE_ID});
  const agents=Array.isArray(list)?list:(list?.agents||[]);
  const ut=agents.find(a=>a.name==='ut test' && !['stopped','crashed'].includes(a.status));
  if(!ut) throw new Error('no active ut test');
  await rpc(ws,'conversation.send',{nodeId:NODE_ID,agentId:ut.id,message:'今天天气如何'});
  console.log('sent');
  let cursor=0; const start=Date.now();
  while(Date.now()-start<300000){
    const h=await rpc(ws,'conversation.history',{nodeId:NODE_ID,agentId:ut.id,cursor});
    for(const e of (h.events||[])){
      cursor=Math.max(cursor,e.seq||0);
      const t=clean(e.text);
      if(!t) continue;
      if(e.role==='assistant') console.log('A',e.seq,JSON.stringify(t.slice(0,140)));
      if(e.role==='assistant' && /[\u4e00-\u9fff]/.test(t) && /[。！？?!.]/.test(t) && !/bypass|shift\+tab|cycle\)|ctrl\+g|\/effort|checking for updates|auto-update/i.test(t)){
        console.log('FOUND',JSON.stringify(t.slice(0,300)));
        process.exit(0);
      }
    }
    await sleep(1500);
  }
  console.log('timeout no natural reply');
  process.exit(1);
})().catch(e=>{console.error('ERR',e.message);process.exit(1);});
