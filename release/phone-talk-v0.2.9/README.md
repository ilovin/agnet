# Agent Manager v0.2.9

远程管理 AI Agent 的工具集。

## 快速安装

    tar xzf phone-talk-v0.2.9.tar.gz
    cd phone-talk-v0.2.9
    ./install.sh

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

    # 重启本地服务（安装后常用）
    bash ./install.sh restart

    # 查看运行状态
    bash ./install.sh status

    # 停止本地服务
    bash ./install.sh stop

    # 查看帮助
    bash ./install.sh --help

## 手动添加连接

URL:  ws://<你的IP>:8080/ws
Token: 安装时生成的 Token

## 文件说明

    platform/darwin-arm64/agentd  # macOS 本地 Agent 守护进程
    platform/linux-amd64/agentd        # 远程服务器 Agent 守护进程 (Linux)
    platform/darwin-arm64/agentgw # macOS 网关
    platform/linux-amd64/agentgw       # Linux 网关
    bin/agentapp.apk        # Android App
    install.sh              # 一键安装脚本
    scripts/                # 部署与辅助脚本（可被管理 UI 调用）

## 架构

    手机 App ──WebSocket──► agentgw ──SSH tunnel──► agentd ──PTY──► Claude/OpenCode
