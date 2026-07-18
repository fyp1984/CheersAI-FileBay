# FileBay 与 RAGFlow 配置及使用指南

本文用于内部试用环境的管理员、技术人员和业务人员。系统分工明确：FileBay 负责资料治理、审批、权限和生命周期；RAGFlow 只负责解析、建立索引和检索。业务人员日常只使用 FileBay。

## 一、使用入口

| 系统 | 地址 | 主要用途 |
| --- | --- | --- |
| RAGFlow | `http://127.0.0.1:19080/` | 配置模型、提供索引和检索能力 |
| FileBay 企业知识库 | `http://127.0.0.1:13080/knowledge` | 提交资料、审核发布、检索和权限管理 |

不要在 RAGFlow 的“数据来源”页面直接接入企业业务资料。正式资料必须先进入 FileBay 的数据治理和审批链路。

## 二、首次配置：管理员与技术人员

### 1. 配置 RAGFlow 模型

打开 RAGFlow，登录后点击右上角头像，进入“设置”或“模型供应商”。选择企业实际使用的模型供应商，并配置：

- 供应商 API 地址（若该供应商要求）；
- 供应商 API 密钥；
- 至少一个嵌入模型。

嵌入模型是必需项：它负责将已审批的资料建立为可检索索引。聊天模型可在后续需要问答生成时再配置。模型供应商的密钥不要发送到聊天、提交到 Git，或写入示例配置文件。

### 2. 绑定 FileBay 与 RAGFlow

当前内部试用环境已完成绑定。以后迁移 RAGFlow、轮换密钥或更换专用知识库时，只更新下面两个本机文件：

```text
E:\CheerAI-FileBay\deploy\knowledge\runtime\ragflow-binding\api-key
E:\CheerAI-FileBay\deploy\knowledge\runtime\ragflow-binding\dataset-id
```

| 文件 | 内容 |
| --- | --- |
| `api-key` | 在 RAGFlow 头像菜单的“配置/API”中生成的 API 密钥 |
| `dataset-id` | 仅供 FileBay 使用的 RAGFlow 数据集 ID |

修改后，在仓库根目录执行：

```powershell
docker compose --env-file deploy/knowledge/.env -f deploy/knowledge/docker-compose.trial.yml --profile cpu --profile elasticsearch up -d --force-recreate filebay
```

这两个文件位于 Git 忽略的 `runtime/` 目录，密钥不会进入镜像或仓库。不要把密钥写入 `.env.example`、README 或 Git 提交。

### 3. 验证连接

打开 FileBay 的“企业知识库”页面。如果没有出现红色“RAGFlow 配置尚未就绪”提示，说明 FileBay 已读取绑定配置。

若仍显示该提示：

1. 确认 RAGFlow 容器正在运行；
2. 确认 `api-key` 与 `dataset-id` 两个文件都存在且非空；
3. 重新执行上一节的 `docker compose ... up -d --force-recreate filebay`；
4. 在浏览器中按 `Ctrl + F5` 强制刷新 FileBay 页面。

## 三、业务人员日常操作

业务人员不需要登录 RAGFlow，也不需要填写数据库连接、模型地址或密钥。

1. 打开 FileBay 企业知识库，点击“开始使用”，创建知识空间，例如“客服知识库”或“制度知识库”。
2. 点击“我要提交资料”，登记资料来源并提交已经脱敏的文件。
3. 等待管理员在“待我处理”中审核。系统依次检查资料来源、内容质量、权限范围、敏感信息、格式可检索性和发布条件。
4. 审核通过后，管理员批准索引并发布。FileBay 才会将脱敏后的可发布内容交给 RAGFlow 建立索引。
5. 点击“我要查资料”，输入问题。FileBay 调用 RAGFlow 检索后，会再次校验用户权限、资料密级、版本和发布状态，再展示引用结果。

提交人不能审核自己提交的资料。完整流程演示至少需要一个提交账号和一个管理员审核账号。

## 四、数据库资料接入

数据库接入仅由技术管理员在 FileBay 的“数据库接入”入口操作：

- 只允许已审批的脱敏只读视图；
- 不允许连接原始业务表；
- 不允许在 FileBay 表单中保存密码、DSN 或 SQL；
- 首次接入先执行“预览脱敏视图”，确认数据正确后再执行“增量同步”；
- 同步后的记录仍会进入审核、索引和发布流程，不会直接对外检索。

示例环境提供的是合成脱敏 MySQL 数据，仅用于验证流程，不能替换为真实业务库。

## 五、职责边界

```text
已脱敏业务资料
        ↓
FileBay：来源登记 → 审核 → 权限与版本控制 → 发布
        ↓（仅已发布的脱敏内容）
RAGFlow：解析 → 索引 → 检索
        ↓
FileBay：二次权限校验 → 引用展示 → 审计记录
```

Dify 如需使用企业知识，只能调用 FileBay 的外部知识检索接口；Dify 不应直接保存或使用 RAGFlow 密钥。
