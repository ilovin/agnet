# R-006 Portal 本机 QR 连接

## 需求概述
在 portal 网页（`portal/index.html`）中新增"尝试连接本机 gw"按钮，点击后通过跨域请求获取本地 agentgw 的连接信息，并显示二维码供手机 App 扫描连接。

## 背景
- `agentgw` 默认端口 7374，已有终端二维码功能（`ws://IP:PORT/ws|TOKEN` 格式）
- `agentgw` 没有 CORS，也没有供 portal 调用的免认证端点
- portal 是纯静态 HTML，无构建步骤
- agentapp 扫码期望 `ws://URL|TOKEN`

## 验收标准
1. `go test ./agentgw/...` 全部通过
2. `go build ./agentgw/cmd/agentgw` 成功
3. Portal 在浏览器中：能看到按钮；未启动 gw 时友好报错；启动后显示正确格式二维码
4. CORS 头正确
5. 不破坏现有 portal 功能

## 技术方案

### 1. agentgw 后端（Go）
在 `agentgw/cmd/agentgw/main.go` 下：

**a) 添加 CORS 包装函数**，给新端点加响应头 `Access-Control-Allow-Origin: *` 等。

**b) 新端点 `GET /local-info`**：
- 免认证
- 返回 JSON：`{"wsUrl": "ws://192.168.x.x:7374/ws", "token": "..."}`
- IP 用现有的 `getLocalIP()`，PORT 用 `cfg.Port`
- 需要 CORS 头，因为 portal 跨域 fetch

**c) Go 单元测试**：为新端点写至少一个测试，验证 JSON 结构和 CORS 头。运行 `go test ./agentgw/...` 确保通过。

### 2. portal/index.html 前端

**a) 布局**：在".guide"（快速开始）区域右侧/下方新增交互区，放按钮"尝试连接本机 gw"和二维码显示区（默认隐藏）。

**b) 交互**：
- 点击按钮 → `fetch('http://localhost:7374/local-info')`
- 成功：拼接 `wsUrl + '|' + token`，生成二维码图片显示
- 失败：显示"未检测到本地网关，请先安装并启动 agentgw"

**c) 二维码生成**：
- **不要外部 CDN**
- 在 `portal/` 目录下新增一个纯 JS 二维码库文件（如 `qrcode.min.js`，可用 Kazuhiko Arase 的 qrcode-generator，约 14KB）
- 生成 150x150px 二维码，匹配 `.qr-code` 样式

## 开发流程
1. 先写 Go 测试（TDD）
2. 实现 Go 端点
3. 改 portal
4. 本地启动 agentgw 验证
5. 浏览器打开 portal/index.html 点按钮验证
