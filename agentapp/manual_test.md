# Agent Manager 手动测试步骤

## 服务状态
- agentd (本地): 端口 7373 - ✓ 运行中
- agentd (远程): 端口 7374 - ✓ 运行中
- agentgw: 端口 8080 - ✓ 运行中
- Web 服务器: 端口 18086 - ✓ 运行中

## 已完成的修复

### 1. Flutter Web 配置修复
文件: `build/web/flutter_bootstrap.js`
- 移除了空的 build 配置 `{}`
- 添加了 `canvasKitBaseUrl` 配置
- 添加了 `onEntrypointLoaded` 回调确保应用正确启动

### 2. Attach 功能修复
文件: `agentd/internal/agent/manager.go`
- 修复了 Attach() 函数，不再 kill 本地进程
- 现在只创建文件 watcher 来监控现有会话

### 3. ANSI 序列处理增强
文件: `agentapp/lib/screens/agent_detail_screen.dart`
- 增强了 stripAnsi() 函数处理更多 ANSI 序列
- 添加了 stripTerminalDrawing() 处理边框字符
- 改进了方向键按钮的显示

## 手动测试步骤

1. 在 Chrome 中打开: http://localhost:18086

2. 预期行为:
   - 页面加载后显示 "Agent Manager" 标题
   - 显示两个节点:
     * "Local Agentd" (本地)
     * "Remote WS (SSH)" (远程)
   - 两个节点状态应为 "已连接"

3. 测试对话功能:
   - 点击任意节点进入 Dashboard
   - 点击 "Attach" 按钮附加到现有会话
   - 检查:
     * 界面不应有 ANSI 乱码 (如 `[?25h`, `─` 等)
     * 方向键按钮应正常显示
     * 输入框应可用

4. 测试远程节点:
   - 切换到 "Remote WS (SSH)" 节点
   - 验证可以查看远程 session

## 已知限制

在 headless/自动化测试环境中，Flutter Web 需要 WebGL 支持才能渲染。
在无头浏览器中 CanvasKit 会回退到 CPU 渲染，但可能无法正确显示 UI。
建议在真实 Chrome 浏览器中测试。
