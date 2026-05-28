# i-013: OpenCode Session 缺少 Thinking/Toolcall 信息

**Date**: 2026-05-07
**Reporter**: Sisyphus (Manager)
**Status**: Open
**Priority**: Medium
**Component**: OpenCode / Agent Infrastructure

## 问题描述

当前 OpenCode session 在运行时不再显示 thinking 过程和 toolcall 信息，导致无法判断：
1. 系统是否正在处理请求
2. 具体调用了哪些工具
3. 工具调用的参数和结果

## 影响

- 调试困难：无法追踪工具调用链
- 透明度降低：用户不知道系统在做什么
- 问题排查：无法确定是模型问题还是工具问题

## 重现步骤

1. 启动 OpenCode session
2. 执行任何需要工具调用的任务
3. 观察输出：只有最终结果，没有中间过程

## 预期行为

Session 应该显示：
- Thinking: [思考过程]
- Toolcall: [工具名称] [参数]
- Toolresult: [结果摘要]

## 实际行为

只显示最终结果，没有任何中间过程信息。

## 可能原因

1. OpenCode 配置变更（可能默认关闭了详细输出）
2. 模型配置变更（可能使用了不支持 thinking 的模型）
3. 前端渲染问题（信息存在但未显示）

## 临时 workaround

- 通过检查文件系统变更（git diff, ls）来推断工具是否被执行
- 手动验证每个工具调用的结果

## 建议修复

1. 检查 OpenCode 配置（`~/.opencode/config.json`）
2. 检查模型配置（是否支持 thinking）
3. 检查环境变量（`OPENAI_VERBOSE` 等）
4. 如果需要，回滚到上一个已知正常的配置

## 关联

- 影响所有使用 OpenCode 的开发任务
- 特别影响 Manager 模式的透明度
