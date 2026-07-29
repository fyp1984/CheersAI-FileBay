# CheersAI FileBay + RAGFlow 裸机部署包

本目录用于没有 Docker、不能从 GitHub 拉取代码的服务器。目标是把
FileBay 与 RAGFlow 部署成两个独立 systemd 后端，并通过一个本机 Nginx
入口提供访问：

- `http://127.0.0.1:13080/knowledge`：FileBay 企业知识库工作台
- `http://127.0.0.1:13080/ragflow/`：RAGFlow 配置与检索测试工作区

对外访问应由既有受控反向代理和 TLS 层转发到 `127.0.0.1:13080`。不要把
RAGFlow、MySQL、Redis、Elasticsearch、MinIO 等原始端口直接暴露到公网。

## 文件说明

| 文件 | 用途 |
| --- | --- |
| `build-release-bundle.ps1` | 在本地构建并打包 Linux amd64 离线发布包 |
| `scripts/server-audit.sh` | 服务器只读体检和版本差异采集 |
| `scripts/install-native.sh` | 服务器解包、生成本地密钥、安装 systemd 与 Nginx 配置 |
| `systemd/*.service` | FileBay、RAGFlow API、RAGFlow Worker 服务单元 |
| `nginx/cheersai-filebay-native.conf` | 单入口 Nginx 反向代理模板 |
| `templates/*.template` | 服务器本地配置模板，不含真实密钥 |

## 本地生成发布包

在仓库根目录执行：

```powershell
powershell -ExecutionPolicy Bypass -File deploy/native/build-release-bundle.ps1
```

输出位于 `dist/native/`：

- `cheersai-filebay-native-<commit>.zip`
- `cheersai-filebay-native-<commit>.zip.sha256`

发布包会包含 FileBay Linux amd64 可执行文件、FileBay 静态资源、固定版本的
`third_party/ragflow`、RAGFlow Web 构建产物、部署脚本和模板。发布包不会包含
`.env`、运行目录、数据库密码、API Key、用户数据或本机测试数据。

如果只想生成结构包并跳过耗时构建，可用于排查脚本：

```powershell
powershell -ExecutionPolicy Bypass -File deploy/native/build-release-bundle.ps1 -SkipBuild
```

## 上传到服务器

使用一次性 SSH Key 或受控跳板机上传，不要把密码写进命令或脚本：

```bash
scp dist/native/cheersai-filebay-native-<commit>.zip root@49.232.100.69:/root/
scp dist/native/cheersai-filebay-native-<commit>.zip.sha256 root@49.232.100.69:/root/
```

## 服务器只读体检

先体检，不覆盖现有部署：

```bash
bash /root/cheersai-native/cheersai-filebay-native-<commit>/deploy/native/scripts/server-audit.sh
```

体检结果默认写入 `/var/tmp/cheersai-audit-<timestamp>`，里面包含系统版本、
资源、端口、systemd、Nginx 配置摘要、现有部署目录和日志索引。

## 服务器安装

解包并安装：

```bash
cd /root
sha256sum -c cheersai-filebay-native-<commit>.zip.sha256
unzip cheersai-filebay-native-<commit>.zip -d /root/cheersai-native
cd /root/cheersai-native/cheersai-filebay-native-<commit>
sudo bash deploy/native/scripts/install-native.sh --install-deps
```

`--install-deps` 会安装常见系统包。Elasticsearch、MinIO 和 Python 3.13
如果不在服务器默认软件源内，需要先按团队的软件源策略安装到本机，并绑定到
回环地址或私网地址。

安装脚本会：

- 创建 `filebay`、`ragflow` 专用系统用户；
- 安装到 `/opt/cheersai-filebay/releases/<version>`；
- 创建 `/etc/cheersai-filebay`、`/etc/cheersai-ragflow`；
- 在服务器本地生成运行密钥，并设置为服务账号可读；
- 安装 systemd 单元和 Nginx 单入口配置；
- 只监听 `127.0.0.1:13080`。

RAGFlow `v0.26.4` 需要 Python `3.13.x`。如果服务器默认 `python3` 不是
3.13，安装脚本会保留配置和文件，但不会伪装成 RAGFlow 已经可启动；请先安装
Python 3.13，再重新执行安装脚本或手工创建 `/var/lib/cheersai-ragflow/venv`。

## 启停命令

```bash
sudo systemctl start filebay ragflow-api ragflow-worker
sudo systemctl stop filebay ragflow-api ragflow-worker
sudo systemctl restart filebay ragflow-api ragflow-worker
sudo systemctl status filebay ragflow-api ragflow-worker
```

查看日志：

```bash
sudo journalctl -u filebay -n 200 --no-pager
sudo journalctl -u ragflow-api -n 200 --no-pager
sudo journalctl -u ragflow-worker -n 200 --no-pager
```

检查入口：

```bash
curl -I http://127.0.0.1:13080/knowledge
curl -I http://127.0.0.1:13080/ragflow/
curl -s http://127.0.0.1:13080/api/healthz
```

## 安全边界

- 不提交 `.env`、真实密钥、数据库密码、用户数据、原始文件或未脱敏数据。
- 服务器上的 `/etc/cheersai-filebay/secrets` 与
  `/etc/cheersai-ragflow/secrets` 只允许对应服务账号读取。
- RAGFlow 作为独立服务部署，FileBay 只通过受控接口调用，不改动 RAGFlow
  上游核心源码。
