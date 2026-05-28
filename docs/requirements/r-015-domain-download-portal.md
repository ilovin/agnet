---
name: R-015 Domain Normalization & Download Portal
description: 域名正规化 (ilovim.xyz 子域体系) + 公网下载门户 + 一键安装 + telemetry 上报
status: Completed
date: 2026-05-07
priority: High
source: /grill-me + /to-prd 会话产物
---

# R-015 域名正规化与下载门户建设

## 背景

当前 `ilovin.xyz` 裸域名硬编码于 `install.sh`、`agentapp` 默认服务器、`agentgw` 默认隧道地址等多处,无统一域名体系且无公网下载站,外部用户需先运行本地网关才能获取 APK。配置不幂等、缺乏用户侧诊断数据,远程排查困难。

## 需求摘要

建立以 `ilovim.xyz` 为根域的功能子域名体系,搭建公网下载门户,实现编译时注入+运行时覆盖的混合域名配置,并新增 telemetry 上报机制。

子域分配:
- `tunnel.ilovim.xyz`:WebSocket/gRPC-Web 隧道中继
- `download.ilovim.xyz`:静态交互向导页 (CDN)
- `api.ilovim.xyz`:轻量 API (版本/install.sh/telemetry)

## 完整 PRD

详见 `docs/plans/domain-download-portal-prd.md`,包含:

- 5 项 Problem Statement
- 20 条 User Stories
- 7 项 Implementation Decisions (域名体系、变量化、Release Manifest、一键安装、下载站、Telemetry、API、防刷)
- 5 项 Testing Decisions
- 6 项 Out of Scope
- 5 项 Further Notes (含 `ilovin.xyz` 迁移、APK 签名、`curl|sh` 安全)

## 验收标准 (高层)

- [ ] `ilovin.xyz` 全局替换为变量引用,旧域名 301 重定向 ≥3 个月
- [ ] `scripts/build.sh` 支持 `DOMAIN` 环境变量编译时注入
- [ ] `scripts/release.sh --publish` 自动上传 OSS + 刷新 CDN + 生成 manifest.json (含 SHA256)
- [ ] `api.ilovim.xyz/v1/install.sh` 支持 darwin/linux × arm64/amd64 平台检测
- [ ] `install.sh` 幂等配置 (首次生成 token,后续保留用户修改)
- [ ] `download.ilovim.xyz` 静态站点上线,场景卡片 (Mac/Linux/Android) 引导下载
- [ ] Telemetry 客户端模块上报 (启动/异常/主动),携带 OpenSSO accessToken,只传日志不传 config
- [ ] API 端点限流 (60 req/min/IP) + 访问日志
- [ ] Web 端验证下载站功能完整 (Chrome 现有标签页)

## 关联

- PRD 来源: 2026-05-07 `/grill-me` + `/to-prd` 会话
- 依赖: 现有 `agentgw` `localhost:7374/apk` 本地下载能力 (保留)
- 不影响范围: 实时节点状态仪表盘、iOS 公网分发、P2P 模式、Flutter Web 公网托管
