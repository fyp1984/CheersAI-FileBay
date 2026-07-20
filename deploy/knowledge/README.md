# FileBay 企业知识库内部试用部署

该编排在一台受控主机上运行 FileBay、合成脱敏 MySQL 数据源、官方 RAGFlow 与官方 Dify。所有服务仅通过 Docker 私有网络通信；宿主机只暴露 FileBay 和 Dify 的回环地址。

## 前置条件

- Docker Desktop（Linux containers）已启动；本机内部试用建议至少分配 8 GB 内存。
- 已在主机上安装 Git 与 PowerShell 7。
- 仅使用本目录的合成脱敏数据；真实业务库必须另建经审批的只读脱敏视图。

## 启动

在仓库根目录执行：

```powershell
Copy-Item deploy/knowledge/.env.example deploy/knowledge/.env
powershell -ExecutionPolicy Bypass -File deploy/knowledge/bootstrap-trial.ps1
docker compose --env-file deploy/knowledge/.env -f deploy/knowledge/docker-compose.trial.yml --profile cpu --profile elasticsearch up -d --build
```

`bootstrap-trial.ps1` 固定下载 RAGFlow `v0.26.4`（提交 `cb93883f3f8c975eecb2fed81210effeb3bdb06f`）和 Dify `1.15.0`（提交 `3aa26fb6374bbd47e5469f7d7cc25f3e0075a60c`）的官方编排，再只添加私有网络覆盖文件。FileBay 不修改 RAGFlow 源码、不直连其内部数据库或存储；所有企业扩展都位于 FileBay 的版本化适配器和治理服务层。`.runtime/`、`runtime/` 和 `.env` 都是本机运行状态，禁止提交。

首次启动后：

1. 打开 `http://localhost:19380`，在 RAGFlow 中配置可用的嵌入模型，创建仅供 FileBay 使用的数据集与 API 密钥。
2. 不要把 RAGFlow API 密钥写入 `.env`。仅在本机创建被 Git 忽略的绑定文件：`runtime/ragflow-binding/api-key`（密钥）与 `runtime/ragflow-binding/dataset-id`（数据集 ID），然后运行同一条 `docker compose ... up -d --force-recreate filebay`。FileBay 启动时读取这两个只读挂载文件；密钥不会进入镜像或仓库，只会存在于本机运行时配置中。
3. 打开 `http://localhost:13080/knowledge`，完成 FileBay 初始化和登录；数据源使用 MySQL、`secret-ref:env/KB_MYSQL_DSN`、视图 `v_kb_masked_faq`，字段映射为 `record_id/question/answer_markdown/updated_at`。
4. 先“预览校验”，再“增量同步”；同步内容会进入六项审核、索引、评测、生效链路。
5. 在 FileBay 创建 Dify 授权。Dify 的外部知识端点填写 `http://filebay:3000/api/knowledge/external/retrieval`；`knowledge_id` 与 FileBay 中的绑定记录完全一致，授权令牌只显示一次。

## 安全边界

- MySQL 容器仅含合成脱敏数据；读取账号只拥有目标视图的 `SELECT` 权限。
- FileBay 只持久化 `secret-ref:env/...`，不会保存 DSN、密码、原始表名或 SQL。
- Dify 不能直连 RAGFlow；它只调用 FileBay 的受控检索端点。检索结果会再次验证空间权限、密级、版本、生效状态和撤销状态。
- 示例中的口令仅可用于本机合成数据；不可迁移到任何业务环境。

## 停止与清理

```powershell
docker compose --env-file deploy/knowledge/.env -f deploy/knowledge/docker-compose.trial.yml down
```

内部试用数据位于 `deploy/knowledge/runtime/` 与 `deploy/knowledge/.runtime/`。若需清理，先人工确认目录无需要保留的审计证据，再删除这些本地目录。
