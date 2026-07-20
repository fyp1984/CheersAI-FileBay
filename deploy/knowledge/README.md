# FileBay 企业知识库内部试用部署

该编排在一台受控主机上运行 FileBay、合成脱敏 MySQL 数据源、官方 RAGFlow 与官方 Dify。FileBay 是业务人员的统一工作台；过渡期间保留仅本机可访问的 RAGFlow 管理界面，供管理员诊断和模型配置。

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

`third_party/ragflow/` 固定包含官方 RAGFlow `v0.26.4`（提交 `cb93883f3f8c975eecb2fed81210effeb3bdb06f`）源码；`bootstrap-trial.ps1` 只在 Git 忽略的 `runtime/ragflow/.env` 写入本机端口和资源限制，并下载 Dify `1.15.0`（提交 `3aa26fb6374bbd47e5469f7d7cc25f3e0075a60c`）的官方编排。FileBay 不修改 RAGFlow 核心代码、不直连其内部数据库或存储；所有企业扩展都位于 FileBay 的版本化适配器和治理服务层。`.runtime/`、`runtime/` 和 `.env` 都是本机运行状态，禁止提交。

首次启动后：

1. 首次安装时，由受控部署流程完成 RAGFlow 的模型、专用数据集与服务端 API 密钥配置；不要把该密钥写入 `.env` 或交给业务用户。
2. 仅在部署主机创建被 Git 忽略的绑定文件：`runtime/ragflow-binding/api-key`（密钥）与 `runtime/ragflow-binding/dataset-id`（数据集 ID），然后运行同一条 `docker compose ... up -d --force-recreate filebay`。FileBay 启动时读取这两个只读挂载文件；密钥不会进入镜像、仓库或浏览器。
3. 打开 `http://localhost:13080/knowledge`，完成 FileBay 初始化和登录；管理员从“检索引擎”查看受控连接状态。需要诊断检索引擎时，可在部署主机打开 `http://localhost:19080`。
4. 数据源使用 MySQL、`secret-ref:env/KB_MYSQL_DSN`、视图 `v_kb_masked_faq`，字段映射为 `record_id/question/answer_markdown/updated_at`；先“预览校验”，再“增量同步”。
5. 在 FileBay 创建 Dify 授权。Dify 的外部知识端点填写 `http://filebay:3000/api/knowledge/external/retrieval`；`knowledge_id` 与 FileBay 中的绑定记录完全一致，授权令牌只显示一次。

## 安全边界

- MySQL 容器仅含合成脱敏数据；读取账号只拥有目标视图的 `SELECT` 权限。
- FileBay 只持久化 `secret-ref:env/...`，不会保存 DSN、密码、原始表名或 SQL。
- Dify 不能直连 RAGFlow；它只调用 FileBay 的受控检索端点。检索结果会再次验证空间权限、密级、版本、生效状态和撤销状态。
- RAGFlow 管理界面仅映射到部署主机的回环地址 `127.0.0.1:19080`，不会对局域网开放；FileBay 通过 Docker 私有网络访问 `ragflow-cpu:9380`。业务检索与 Dify 调用仍必须经过 FileBay 的治理接口。
- 示例中的口令仅可用于本机合成数据；不可迁移到任何业务环境。

## 单仓库升级 RAGFlow

RAGFlow 源码由 `third_party/ragflow/` 管理，部署时仍作为独立容器运行。升级时必须在单独分支完成以下步骤：

1. 使用 `git subtree pull --prefix=third_party/ragflow https://github.com/infiniflow/ragflow.git <固定标签> --squash` 更新官方快照。
2. 更新 [third_party/RAGFLOW-VENDOR.md](../../third_party/RAGFLOW-VENDOR.md) 中的上游标签与提交号。
3. 在合成脱敏数据上运行 FileBay 的 RAGFlow 契约测试、索引/发布/撤销回归及 Docker 启动验收。
4. 不在 `third_party/ragflow/` 中写入 FileBay 业务逻辑、密钥或运行环境配置；需要的适配只放在 FileBay 的 `services/knowledge/ragflow` 和 `deploy/knowledge`。

## 停止与清理

```powershell
docker compose --env-file deploy/knowledge/.env -f deploy/knowledge/docker-compose.trial.yml down
```

内部试用数据位于 `deploy/knowledge/runtime/` 与 `deploy/knowledge/.runtime/`。若需清理，先人工确认目录无需要保留的审计证据，再删除这些本地目录。
