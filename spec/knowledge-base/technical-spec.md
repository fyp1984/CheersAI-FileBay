# CheersAI FileBay 生产知识库 MVP 技术规格

状态：冻结用于首个生产纵向闭环
依据：`docs/adr/0001-cheersai-knowledge-base-ragflow.md`
目标 RAGFlow：固定稳定版 `v0.26.4`，投产镜像仍须由部署环境记录 digest

## 1. 交付边界

本阶段交付一条真实、可审计、默认关闭的纵向闭环：

1. 已登录用户创建知识空间；FileBay 同时创建并绑定一个私有仓库。
2. 用户显式选择客户端已脱敏文件，并提交不含本地路径的脱敏清单。
3. FileBay 校验声明、大小、文件名、扩展名与 SHA-256，把文件写入绑定仓库并记录不可变修订。
4. 提交审核后，只有站点管理员可以批准或驳回；提交者不能批准自己的企业发布。
5. 批准事务同时创建发布快照、Outbox 和幂等索引任务。
6. Worker 通过 RAGFlow HTTP API 上传文件、写治理元数据并启动解析；结果和错误回写 FileBay。
7. 解析完成后进入“待评测”，管理员显式激活后才成为“可检索/已发布”。
8. 下架先提升撤销代次并阻断 FileBay 当前指针，再异步删除 RAGFlow 派生文档。
9. 所有治理和索引动作写追加式审计事件，不记录文件正文、完整查询或本地绝对路径。

本阶段不实现生成式回答和大模型调用。检索 Gateway、返回分块完整性清单校验和回答引用封装属于下一纵向闭环；在它们完成前，普通用户不能直接获得 RAGFlow API 密钥或访问 RAGFlow 管理界面。

## 2. 安全与兼容开关

- `[knowledge] ENABLED=false` 为默认值。关闭时不显示导航、不启动知识队列、知识路由返回 404。
- 生产模式只允许 HTTPS RAGFlow 地址；仅测试/本机开发可显式允许 loopback HTTP。
- API Key 只从服务端配置读取，页面、日志、数据库业务表和审计事件均不得保存或返回明文密钥。
- 单文件上限默认 32 MiB；允许扩展名首批为 `.pdf`、`.docx`、`.pptx`、`.xlsx`、`.md`、`.txt`。
- 服务端只接收用户显式提交的“已脱敏 Sandbox 产物”。API 不接受 `local_path`、`source_path` 等本地路径字段，也不提供服务端脱敏能力。
- 所有新增能力必须使用新表、新路由和特性开关，不修改现有仓库、Issue、Actions 或普通文件上传的默认行为。

## 3. 数据模型

### `knowledge_space`

- `id`, `owner_id`, `repo_id`, `name`, `slug`, `description`
- `status`: `active|archived`
- `publication_generation`, `revocation_generation`
- `created_by`, `created_unix`, `updated_unix`
- 唯一键：`repo_id`；`owner_id + slug`

### `knowledge_document`

- `id`, `space_id`, `title`, `repo_path`, `mime_type`
- `governance_status`: `draft|pending|rejected|approved|withdrawn`
- `current_revision_id`, `current_publication_id`
- `created_by`, `created_unix`, `updated_unix`
- 唯一键：`space_id + repo_path`

### `knowledge_revision`

- `id`, `document_id`, `revision_no`, `file_name`, `repo_path`
- `content_sha256`, `size`, `git_commit_sha`
- `mask_policy_version`, `mask_manifest_json`, `source_authorization`
- `created_by`, `created_unix`
- 修订插入后不可更新；唯一键：`document_id + revision_no` 与 `document_id + content_sha256`

### `knowledge_acl`

- `id`, `space_id`, `subject_type`, `subject_id`, `permission`, `effect`, `policy_version`
- 首期实际授权基线复用绑定私有仓库权限；显式 `deny` 始终优先。
- 发布快照记录批准时的 `acl_policy_version`，扩大范围必须重新审批。

### `knowledge_approval`

- `id`, `revision_id`, `status`: `pending|approved|rejected`
- `requested_by`, `requested_unix`, `decided_by`, `decided_unix`, `comment`
- 每个修订最多一个活动审核；决定不可覆盖，只能新建后续修订再次提交。

### `knowledge_publication`

- `id`, `space_id`, `document_id`, `revision_id`, `approval_id`
- `generation`, `governance_status`, `validity_status`, `index_status`
- `acl_policy_version`, `revocation_generation`, `is_current`
- `effective_unix`, `expires_unix`, `created_unix`, `updated_unix`
- “已发布”只等于：批准 + 当前有效 + 可检索 + 当前指针匹配。

### `knowledge_index_binding`

- FileBay 空间/文档/修订/发布标识
- `engine`, `engine_profile_version`, `dataset_id`, `engine_document_id`
- `content_sha256`, `status`, `last_success_unix`
- 每个发布在一个引擎配置中最多一个绑定。

### `knowledge_index_job`

- `publication_id`, `job_type`: `upsert|poll|delete|rebuild`
- `idempotency_key`, `status`: `queued|running|retry|succeeded|failed|cancelled`
- `attempt`, `max_attempts`, `next_attempt_unix`, `last_error`, 时间戳
- 幂等键：`owner_id:space_id:publication_id:generation:revision_sha256:engine_profile_version:job_type`

### `knowledge_outbox`

- `aggregate_type`, `aggregate_id`, `event_type`, `idempotency_key`, `payload_json`
- `status`: `pending|dispatched|failed`, `attempt`, `next_attempt_unix`, 时间戳
- 与业务状态变更在同一数据库事务写入。

### `knowledge_audit_event`

- `actor_id`, `action`, `entity_type`, `entity_id`, `space_id`
- `result`, `reason_code`, `trace_id`, `metadata_json`, `created_unix`
- 只追加，不提供更新或物理删除业务接口。

## 4. 授权规则

- 列表/详情/上传：站点管理员、空间所有者，或对绑定私有仓库至少有读取/写入权限的主体；上传要求写权限。
- 审批、激活、下架、手工重试：首期仅站点管理员。
- 审核人不得等于修订提交人；管理员提交自己的内容时必须由另一管理员审核。
- 任意显式 deny、空间归档、发布下架、过期、撤销代次不匹配均失败关闭。
- 未授权对象对外返回 404，避免对象枚举；审计内部记录 `denied`。

## 5. RAGFlow v0.26.4 合同

- 上传：`POST /api/v1/datasets/{dataset_id}/documents`，multipart 字段 `file`。
- 元数据：`PUT /api/v1/datasets/{dataset_id}/documents/{document_id}`，字段 `meta_fields`。
- 解析：`POST /api/v1/datasets/{dataset_id}/chunks`，字段 `document_ids`。
- 状态：`GET /api/v1/datasets/{dataset_id}/documents?id={document_id}`。
- 删除：`DELETE /api/v1/datasets/{dataset_id}/documents`，字段 `ids`。
- 后续 Gateway 检索：`POST /api/v1/retrieval`，必须同时传授权后的 `dataset_ids`/`document_ids`，FileBay 仍执行返回后二次授权。
- 客户端拒绝跨主机重定向、限制响应体 1 MiB、默认超时 10 秒、错误信息不得包含密钥或正文。

## 6. 状态转换

```text
draft -> pending -> approved -> queued -> indexing -> evaluation -> searchable
                  \-> rejected
searchable -> unpublished -> deleting -> deleted
任意索引步骤 -> failed -> queued（管理员重试，且代次仍有效）
```

- 只有合法前置状态允许转换，状态更新使用事务和受影响行数检查。
- 下架事务先清空 `current_publication_id`、提升 `revocation_generation`、写删除 Outbox，再返回成功。
- 旧代次任务在写 RAGFlow 前后均校验；不匹配即取消，不能复活旧发布。

## 7. 路由与页面

- `GET /knowledge`：可访问空间与状态总览。
- `POST /knowledge/spaces`：创建私有知识空间。
- `GET /knowledge/spaces/{id}`：文档、审核、索引任务与审计摘要。
- `POST /knowledge/spaces/{id}/documents`：multipart 上传脱敏文件。
- `POST /knowledge/documents/{id}/submit`：提交审核。
- `POST /knowledge/approvals/{id}/approve|reject`：管理员决定。
- `POST /knowledge/publications/{id}/activate|unpublish`：激活或下架。
- `POST /knowledge/jobs/{id}/retry`：管理员重试失败任务。

页面采用现有 Gitea 服务端模板和 CSRF/会话中间件；不引入新的前端框架或公共依赖。

## 8. 可观测性与回退

- 日志只记录 `trace_id`、FileBay ID、任务状态、RAGFlow HTTP 状态和稳定错误码。
- 特性开关关闭即可隐藏入口并停止新任务；已发布数据仍由下架/删除流程处理，不能靠关开关代替删除。
- RAGFlow 故障不影响普通 FileBay 仓库功能；任务保留并按退避重试，超过上限进入人工处理。
- 投产前必须完成真实 RAGFlow 环境、固定镜像 digest、备份恢复、删除传播和安全扫描门禁。
