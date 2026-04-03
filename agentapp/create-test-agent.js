const { firefox } = require('playwright');
const WebSocket = require('ws');

// Test creating agent through agentd WebSocket
async function createTestAgent() {
  return new Promise((resolve, reject) => {
    const ws = new WebSocket('ws://localhost:7373/ws?token=testtoken123');

    ws.on('open', () => {
      console.log('Connected to agentd');

      // Create agent request
      const createRequest = {
        jsonrpc: "2.0",
        id: 1,
        method: "agent.create",
        params: {
          name: "Test Agent",
          provider: "claude",
          cmd: "echo",
          args: ["test"],
          workDir: "/tmp"
        }
      };

      ws.send(JSON.stringify(createRequest));
    });

    ws.on('message', (data) => {
      const msg = JSON.parse(data);
      console.log('Response:', msg);

      if (msg.id === 1) {
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

(async () => {
  try {
    console.log('Creating test agent...');
    const result = await createTestAgent();
    console.log('Agent created:', result);
  } catch (e) {
    console.error('Failed:', e.message);
  }
})();
