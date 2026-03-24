# CheersAI-FileBay

面向隐私与安全隔离的“脱敏文件管理网盘”，支持端云同步与按用户隔离访问，
基于 Gitea 二次改造。

[English](./README.md) | [繁體中文](./README.zh-tw.md)

## 背景

为了解决 Desktop 产品中大模型（Agent）与知识库使用过程中的数据安全问题，
CheersAI-FileBay 提供一套自动化机制：将用户本地脱敏后的文件同步至私有云端，
供 Agent 在授权、审计、隔离的前提下安全调用。

## 目标

- 用户私有：以用户为边界的权限控制与数据隔离
- 端云同步：脱敏文件从本地到私有云的自动同步
- 安全隔离：最小权限、可审计、可追溯
- 服务端能力：私有 Git 服务端与扩展 API 作为基础能力

## 构建

在仓库根目录执行：

    TAGS="bindata" make build

如需 SQLite 支持：

    TAGS="bindata sqlite sqlite_unlock_notify" make build

`build` 目标拆分为：

- `make backend`：需要 Go Stable，版本以 [go.mod](/go.mod) 为准
- `make frontend`：需要 Node.js LTS（或更高）与 pnpm

## 运行

当前默认服务端二进制名仍为 `gitea`（上游命名尚未统一改名），运行方式：

    ./gitea web

## 安全与合规

- 建议优先部署在私有网络，端到端启用 TLS
- 避免将管理面暴露在公网
- 漏洞披露与安全流程以 [SECURITY.md](SECURITY.md) 为准

## 许可证与上游

- 本仓库包含来自上游 Gitea 的衍生成果，遵循 [LICENSE](LICENSE)（MIT）
- 上游项目：Gitea（Git with a cup of tea）

## 商标声明

Gitea 与 Gogs 为其各自权利人的商标。CheersAI-FileBay 与上游商标权利人无隶属或背书关系。
