# FileBay 企业知识库内部试用部署

主试用编排在一台受控主机上运行 FileBay、合成脱敏 MySQL 数据源与官方 RAGFlow。Dify 是可选的私网调用方，使用独立编排启动。浏览器只访问 FileBay 的一个前端端口：FileBay 使用 `/knowledge`，RAGFlow 使用 `/ragflow/`。二者是独立后端服务，但在同一域名、端口与统一导航下运行；FileBay 负责企业知识治理，RAGFlow 保留模型、数据集和检索配置。

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

`third_party/ragflow/` 固定包含官方 RAGFlow `v0.26.4`（提交 `cb93883f3f8c975eecb2fed81210effeb3bdb06f`）源码；`bootstrap-trial.ps1` 只在 Git 忽略的 `runtime/ragflow/.env` 写入本机资源限制，并下载 Dify `1.15.0`（提交 `3aa26fb6374bbd47e5469f7d7cc25f3e0075a60c`）的官方编排。FileBay 不修改 RAGFlow 核心代码、不直连其内部数据库或存储；所有企业扩展都位于 FileBay 的版本化适配器和治理服务层。`.runtime/`、`runtime/` 和 `.env` 都是本机运行状态，禁止提交。

首次启动后：

1. 首次安装时，由受控部署流程完成 RAGFlow 的模型、专用数据集与服务端 API 密钥配置；不要把该密钥写入 `.env` 或交给业务用户。
2. 仅在部署主机创建被 Git 忽略的绑定文件：`runtime/ragflow-binding/api-key`（密钥）与 `runtime/ragflow-binding/dataset-id`（数据集 ID），然后运行同一条 `docker compose ... up -d --force-recreate filebay`。FileBay 进程从这两个只读挂载文件读取绑定；密钥不会进入镜像、仓库、浏览器或持久化 `app.ini`。旧试用环境升级后会在下一次启动时清除 `app.ini` 中遗留的 RAGFlow 密钥项。
3. 打开 `http://localhost:13080/knowledge`，完成 FileBay 初始化和登录；管理员从“检索引擎”可打开 RAGFlow 配置工作区，业务人员日常只需使用 FileBay。
4. RAGFlow 配置工作区为同一前端端口下的 `http://localhost:13080/ragflow/`。该页面保留上游的模型供应商、数据集、解析和检索测试功能，顶部导航会在“知识库”前显示“工作台”链接，方便返回 FileBay；FileBay 的“检索引擎”也提供反向入口。首次进入会默认使用简体中文与浅色主题，之后仍可在 RAGFlow 中自行切换语言和主题。
5. 数据源使用 MySQL、`secret-ref:env/KB_MYSQL_DSN`、视图 `v_kb_masked_faq`，字段映射为 `record_id/question/answer_markdown/updated_at`；先“预览校验”，再“增量同步”。
6. 在 FileBay 创建 Dify 授权。Dify 的外部知识端点填写 `http://filebay:3000/api/knowledge/external/retrieval`；`knowledge_id` 与 FileBay 中的绑定记录完全一致，授权令牌只显示一次。

### 可选：启动私网 Dify

主试用命令不会启动 Dify，避免其占用额外内存并与 RAGFlow 上游依赖发生服务名冲突。确实需要验证 Dify 外部知识库时，先确认主试用栈处于运行状态，再单独执行：

```powershell
docker compose --env-file deploy/knowledge/.env -f deploy/knowledge/docker-compose.dify.private.yml up -d
```

该独立项目仅将 Dify `api` 加入 `filebay_knowledge_trial` 私网以调用 `http://filebay:3000`，不发布 nginx、插件调试或任何可选向量数据库端口到宿主机。Dify 管理界面如需运维访问，必须通过单独的受控反向代理与身份认证提供，不得临时增加默认端口映射。

## 安全边界

- MySQL 容器仅含合成脱敏数据；读取账号只拥有目标视图的 `SELECT` 权限。
- FileBay 只持久化 `secret-ref:env/...`，不会保存 DSN、密码、原始表名或 SQL。
- Dify 不能直连 RAGFlow；它只调用 FileBay 的受控检索端点。检索结果会再次验证空间权限、密级、版本、生效状态和撤销状态。
- 只有统一前端网关映射到宿主机回环地址 `127.0.0.1:13080`；RAGFlow 原始 Web 端口、API、管理 API、MCP 与内部服务端口均不映射到宿主机。FileBay 仍只通过 Docker 私有网络访问 `ragflow-cpu:9380`。
- Dify 是可选的私网调用方：主试用栈默认不启动它；单独启动时也不发布 nginx、插件调试或管理界面端口到宿主机。未配置 Dify 应用授权时，不会参与 FileBay 的业务检索链路；需要访问 Dify 管理界面时，应在受控运维环境单独增加反向代理和身份验证，不得临时暴露默认端口。
- RAGFlow 页面通过部署层构建为 `/ragflow/` 路由并注入轻量的 FileBay 品牌与返回导航。该适配位于 `deploy/knowledge/ragflow-ui/` 与 `deploy/knowledge/gateway/`，升级 RAGFlow 时无需修改 `third_party/ragflow/`。
- 示例中的口令仅可用于本机合成数据；不可迁移到任何业务环境。

## 运行保护与观察

- Compose 为 FileBay、统一前端和 RAGFlow 配置了保守的 CPU、内存与进程数上限：分别为 `1.5 CPU / 1 GiB / 256`、`0.5 CPU / 256 MiB / 128`、`2.5 CPU / 3 GiB / 512`。这些限制用于避免单一服务耗尽 Docker Desktop；Dify、MySQL 和 Elasticsearch 仍需占用剩余资源，因此 8 GiB 是最低建议而非容量承诺。
- `docker compose ... ps` 会显示 FileBay、统一前端和 RAGFlow 的健康状态。FileBay 使用 `/api/healthz`；RAGFlow 使用其 nginx 服务面 liveness 探针，深度依赖健康请通过 RAGFlow 管理工作区或其受控内部诊断执行，避免健康探针因慢依赖而反复重启正常服务。
- 浏览器入口仍只有 `127.0.0.1:13080`。健康检查与资源保护全部在容器内部运行，不增加宿主机端口。

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
