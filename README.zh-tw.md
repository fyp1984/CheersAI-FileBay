# CheersAI-FileBay

以隱私與安全隔離為核心的「去識別化（脫敏）檔案管理網盤」，
支援端雲同步與按使用者隔離存取，基於 Gitea 二次改造。

[English](./README.md) | [简体中文](./README.zh-cn.md)

## 背景

為了解決 Desktop 產品中大模型（Agent）與知識庫使用過程的資料安全問題，
CheersAI-FileBay 提供自動化機制：將使用者本地已脫敏的檔案同步至私有雲端，
讓 Agent 在授權、稽核、隔離前提下安全存取。

## 目標

- 使用者私有：以使用者為邊界的權限控管與資料隔離
- 端雲同步：脫敏檔案從本地到私有雲的自動同步
- 安全隔離：最小權限、可稽核、可追溯
- 服務端能力：私有 Git 服務端與擴充 API 作為基礎能力

## 建置

在倉庫根目錄執行：

    TAGS="bindata" make build

如需 SQLite 支援：

    TAGS="bindata sqlite sqlite_unlock_notify" make build

`build` 目標拆分為：

- `make backend`：需要 Go Stable，版本以 [go.mod](/go.mod) 為準
- `make frontend`：需要 Node.js LTS（或更高）與 pnpm

## 執行

目前預設服務端二進位名稱仍為 `gitea`（上游命名尚未統一改名），執行方式：

    ./gitea web

## 安全與合規

- 建議優先部署於私有網路，端到端啟用 TLS
- 避免將管理介面暴露於公網
- 漏洞揭露與安全流程以 [SECURITY.md](SECURITY.md) 為準

## 授權與上游

- 本倉庫包含來自上游 Gitea 的衍生成果，遵循 [LICENSE](LICENSE)（MIT）
- 上游專案：Gitea（Git with a cup of tea）

## 商標聲明

Gitea 與 Gogs 為其各自權利人的商標。CheersAI-FileBay 與上游商標權利人無隸屬或背書關係。
