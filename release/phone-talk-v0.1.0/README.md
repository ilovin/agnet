# Agent Manager v0.1.0

远程管理 AI Agent 的工具集。

## 快速安装

```bash
tar xzf phone-talk-v0.1.0.tar.gz
cd phone-talk-v0.1.0
./install.sh
```

安装脚本会自动：
- 扫描 SSH 配置发现远程节点
- 部署 agentd 到选中的节点
- 启动本地 agentgw 网关
- 生成 Token 和连接二维码

## 手机连接

1. 安装 agentapp.apk（Android）或 agentapp.ipa（iOS）
2. 打开 app → 扫描终端显示的二维码
3. 自动连接，开始使用

## 日常管理



## 手动添加连接

URL:  ws://<你的IP>:8080/ws
Token: 安装时生成的 Token

## 文件说明

```
bin/agentd-linux        # 远程服务器 Agent 守护进程
bin/agentgw-macos-arm64 # macOS 网关
bin/agentgw-linux       # Linux 网关
bin/agentapp.apk        # Android App
install.sh              # 一键安装脚本
scripts/                # 部署与辅助脚本（可被管理 UI 调用）
```

## 架构

```
手机 App ──WebSocket──► agentgw ──SSH tunnel──► agentd ──PTY──► Claude/OpenCode
```
