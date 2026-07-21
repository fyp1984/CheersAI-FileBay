# RAGFlow 上游源码快照

本目录下的 `ragflow/` 是 CheersAI FileBay 单仓库交付的一部分。它仍是独立运行的索引与检索服务，不与 FileBay 的 Go 应用编译为同一进程。

## 固定基线

- 上游：`https://github.com/infiniflow/ragflow.git`
- 标签：`v0.26.4`
- 提交：`cb93883f3f8c975eecb2fed81210effeb3bdb06f`
- 引入方式：`git subtree --squash`

## 边界

- FileBay 是文件、权限、版本、审核、发布和审计的权威系统。
- RAGFlow 只处理已发布的脱敏副本，提供解析、切分、索引和检索候选。
- 业务用户只使用 FileBay 统一界面；RAGFlow 管理端仅供运维配置模型和排障。
- 不向 `third_party/ragflow/` 提交 FileBay 业务改造、真实数据、密钥或运行期 `.env`。

## 升级

使用 `git subtree pull --prefix=third_party/ragflow https://github.com/infiniflow/ragflow.git <tag> --squash` 引入新的官方版本。升级后必须更新本文件，并验证 FileBay 的 RAGFlow 适配器契约、索引发布、检索、撤销与权限过滤。
