# 浏览器测试方案 (Browser Testing Plan)

## 目标
以浏览器显示作为唯一验收标准，建立端到端测试体系。

## 方案C: Playwright + integration_test

### 1. Playwright (JavaScript) - 冒烟测试 + 截图比对
职责：
- 页面加载测试
- 路由切换测试
- 基本元素渲染验证
- 截图比对 (visual regression)
- 性能基准测试

### 2. Flutter integration_test (Dart) - 复杂交互测试
职责：
- Provider状态管理测试
- WebSocket连接模拟
- 表单交互测试
- Agent对话流测试

## 目录结构
```
agentapp/
├── integration_test/          # Dart integration tests
│   └── app_test.dart
├── e2e/                       # Playwright tests
│   ├── playwright.config.js
│   ├── tests/
│   │   ├── smoke.spec.js      # 冒烟测试
│   │   ├── visual.spec.js     # 视觉回归测试
│   │   └── flows.spec.js      # 用户流程测试
│   └── screenshots/           # 基准截图
├── scripts/
│   └── test-browser.sh        # 一键测试脚本
└── pubspec.yaml               # 添加integration_test依赖
```

## 验收标准
1. 所有Playwright测试通过
2. 所有integration_test测试通过
3. 截图比对差异 < 1%
4. 首屏加载时间 < 3秒

## 运行命令
```bash
# 一键运行所有浏览器测试
./scripts/test-browser.sh

# 单独运行Playwright
cd agentapp && npx playwright test

# 单独运行integration_test
flutter test integration_test/app_test.dart -d chrome
```
