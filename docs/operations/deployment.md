# Agentd 部署指南

## 概述

agentd 是 Agent Manager 系统的节点代理，运行在远程机器上（如 ws），负责管理本地 AI agent 进程（claude、opencode 等）。

## 构建

### 环境要求

- Go 1.22+
- 代理设置（如需下载依赖）: `http_proxy=http://proxy.nioint.com:8080`

### 本地开发构建 (macOS)

```bash
cd agentd
go build -o agentd ./cmd/agentd/
```

### 交叉编译 Linux 版本（用于远程服务器）

```bash
cd agentd

# 设置代理（如需）
export http_proxy=http://proxy.nioint.com:8080
export https_proxy=http://proxy.nioint.com:8080

# 使用纯 Go SQLite 驱动（无需 CGO）
# go.mod 中使用 modernc.org/sqlite 替代 mattn/go-sqlite3

# 删除 vendor 目录（如果有）
rm -rf vendor

# 交叉编译
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o agentd-linux ./cmd/agentd/
```

## 部署到远程服务器

### 1. 复制二进制文件

```bash
scp agentd-linux ws:~/agentd.new
```

### 2. 重启服务

```bash
ssh ws "
  # 停止旧服务
  pkill agentd 2>/dev/null
  sleep 1

  # 替换二进制
  mv ~/agentd.new ~/agentd

  # 启动新服务
  nohup ~/agentd start > /tmp/agentd.log 2>&1 &
  sleep 2

  # 验证状态
  cat /tmp/agentd.log
  ps aux | grep agentd | grep -v grep
"
```

## 配置

agentd 配置文件位置: `~/.agentd/config.yaml`

示例配置:
```yaml
port: 7373
token: <your-token>
data_dir: /home/<user>/.agentd/data
```

## 验证

```bash
# 通过 WebSocket 测试
node -e "
const net = require('net');
const crypto = require('crypto');
const key = crypto.randomBytes(16).toString('base64');
const req = [
  'GET /ws?token=<token> HTTP/1.1',
  'Host: localhost:7373',
  'Upgrade: websocket',
  'Connection: Upgrade',
  'Sec-WebSocket-Key: ' + key,
  'Sec-WebSocket-Version: 13',
  '',''
].join('\r\n');
const s = net.connect(7373, '127.0.0.1', () => { s.write(req); });
let upgraded = false;
function sendMsg(s, msg) {
  const buf = Buffer.from(msg);
  const frame = Buffer.alloc(6 + buf.length);
  frame[0] = 0x81; frame[1] = 0x80 | buf.length;
  const mask = crypto.randomBytes(4);
  mask.copy(frame, 2);
  for (let i=0;i<buf.length;i++) frame[6+i] = buf[i] ^ mask[i%4];
  s.write(frame);
}
s.on('data', (d) => {
  if (!upgraded) {
    upgraded = true;
    sendMsg(s, JSON.stringify({jsonrpc:'2.0',id:1,method:'agent.list',params:{}}));
  } else {
    const payload = d.slice(2);
    console.log(payload.toString());
    s.destroy();
  }
});
"
```

## 常见问题

### 1. CGO/SQLite 错误

**错误**: `Binary was compiled with 'CGO_ENABLED=0', go-sqlite3 requires cgo`

**解决**: 使用 modernc.org/sqlite 替代 mattn/go-sqlite3（纯 Go 实现，无需 CGO）

### 2. vendor 目录不一致

**错误**: `inconsistent vendoring`

**解决**: 删除 vendor 目录 `rm -rf vendor`，使用 go modules 模式

### 3. 网络下载失败

**错误**: `dial tcp 142.250.x.x:443: i/o timeout`

**解决**: 设置代理
```bash
export http_proxy=http://proxy.nioint.com:8080
export https_proxy=http://proxy.nioint.com:8080
```

## 目录结构

```
agentd/
├── cmd/agentd/         # 主程序入口
│   └── main.go
├── internal/
│   ├── agent/          # Agent 管理
│   ├── config/         # 配置加载
│   ├── eventbuf/       # 事件缓冲区
│   ├── pty/            # PTY 进程管理
│   ├── store/          # SQLite 存储
│   ├── watcher/        # Claude JSONL 监控
│   └── ws/             # WebSocket JSON-RPC 服务
├── go.mod
└── go.sum
```
