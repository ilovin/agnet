# Agent Manager Test Checklist

Standardized test process to verify Agent Manager functionality after each development iteration.

## Pre-Test Setup

```bash
# 1. Ensure all services are running
pgrep -l agentd   # Should show agentd process
pgrep -l agentgw  # Should show agentgw process

# 2. If agentgw needs restart (required after agentd restart)
pkill agentgw; sleep 2; cd /Users/fengming.xie/Documents/project/phone-talk/agentgw && nohup ./agentgw start > /tmp/agentgw.log 2>&1 &
sleep 3

# 3. Verify nodes are connected
NODE_PATH=/Users/fengming.xie/Documents/project/phone-talk/agentapp/node_modules node -e "
const WebSocket = require('ws');
const ws = new WebSocket('ws://127.0.0.1:8080/ws?token=testtoken123');
ws.on('open', () => {
  ws.send(JSON.stringify({jsonrpc:'2.0',id:1,method:'node.list'}));
});
ws.on('message', (d) => console.log(JSON.parse(d).result));
setTimeout(() => ws.close(), 2000);
"
```

## Test 1: Node Connectivity

**Verify both local and remote nodes are connected:**

```bash
NODE_PATH=./agentapp/node_modules node test_conversation_complete.js 2>&1 | head -15
```

**Expected:**
- [ ] At least 2 nodes listed
- [ ] All nodes show status "connected"
- [ ] local-agentd node exists
- [ ] remote-ws node exists

## Test 2: Session Catalog

**Verify session catalog returns data from all nodes:**

```bash
# Should return managed agents, attachable processes, opencode files
```

**Expected:**
- [ ] `session.catalog_all` returns items for each connected node
- [ ] No errors in the errors array
- [ ] Each node shows managed, attachable, and opencodeFiles arrays

## Test 3: Agent Creation

**Create a new agent and verify it appears:**

```bash
# Or via test script which auto-creates if no attachable sessions
```

**Expected:**
- [ ] `agent.create` returns a valid agent ID
- [ ] Agent status becomes "idle" or "starting"
- [ ] `agent.status_changed` event is broadcast

## Test 4: Conversation History

**Verify conversation history API returns correct format:**

**Expected:**
- [ ] `conversation.history` returns events array
- [ ] Each event has `seq`, `role`, and `text` fields (flattened format)
- [ ] `lastSeq` field is present
- [ ] Events are sorted by sequence number

## Test 5: User Message Recording

**Send a message and verify it's recorded:**

```bash
NODE_PATH=./agentapp/node_modules node test_conversation_complete.js
```

**Expected:**
- [ ] `conversation.send` succeeds
- [ ] `conversation.message` event is broadcast with role=user
- [ ] User message appears in `conversation.history`
- [ ] Event count increases after sending message

## Test 6: Agent Reply Capture

**After user message, verify agent responses are captured:**

**Expected:**
- [ ] Assistant events are recorded in EventBuffer
- [ ] Can retrieve assistant messages via `conversation.history`
- [ ] Assistant role events have role="assistant"

## Test 7: Attach Functionality (Local)

**Attach to an existing local claude process:**

**Expected:**
- [ ] `session.attach` with pid succeeds for valid process
- [ ] Original process is NOT killed (verify with `ps aux | grep claude`)
- [ ] Agent can be interacted with after attach

## Test 8: Attach Functionality (Remote)

**Attach to an existing remote claude process via SSH tunnel:**

**Expected:**
- [ ] `session.attach` with pid succeeds for remote process
- [ ] Remote process is NOT killed
- [ ] Can view conversation history from remote session

## Full Automated Test

Run the complete test suite:

```bash
cd /Users/fengming.xie/Documents/project/phone-talk
NODE_PATH=./agentapp/node_modules node test_conversation_complete.js
```

**All tests must pass:**
- [ ] Connection
- [ ] Session Catalog
- [ ] Agent Available
- [ ] Message Send
- [ ] User Event Received
- [ ] User Message in History

## Troubleshooting

### Broken Pipe Error
If you see "broken pipe" errors, restart agentgw:
```bash
pkill agentgw; sleep 2; cd agentgw && nohup ./agentgw start > /tmp/agentgw.log 2>&1 &
```

### Session File Not Found
If attach fails with "no session file found", ensure claude process has a session file:
```bash
# Check for local claude sessions
ls -la ~/.claude/sessions/

# Check for remote claude sessions (via SSH)
ssh remote-ws 'ls -la ~/.claude/sessions/'
```

### Node Not Connected
If nodes show "disconnected", try reconnecting:
```bash
# Via JSON-RPC
{ "method": "node.connect", "params": { "nodeId": "local-agentd" } }
```

## Sign-off Criteria

Before marking development complete, verify:

1. [ ] Both local and remote nodes are connected
2. [ ] Session catalog shows sessions from both nodes
3. [ ] New agents can be created successfully
4. [ ] User messages are recorded and appear in history
5. [ ] Agent replies are captured (can view conversation)
6. [ ] Attach functionality works without killing original process
7. [ ] No ANSI garbage in UI (Flutter app displays text cleanly)
8. [ ] All automated tests pass

## Test Results Log

| Date | Tester | Result | Notes |
|------|--------|--------|-------|
| 2026-04-01 | Claude | ✓ PASS | User message recording fixed, all tests pass |
