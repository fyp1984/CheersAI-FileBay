# FileBay → RAGFlow 隔离 PoC

这是一个独立的嵌套 Go 模块，用于验证“客户端脱敏文件 → RAGFlow 数据集”的最小安全闭环。它不会被 FileBay 主程序导入，也没有修改主项目的路由、模型、数据库、配置或构建流程。

## 默认安全行为

- 所有网络操作默认关闭；只有 `CHEERSAI_POC_ALLOW_NETWORK=1` 才允许请求 RAGFlow。
- 远程 RAGFlow 地址必须使用 HTTPS；明文 HTTP 仅允许 `localhost` 或环回 IP，供本机测试使用。
- 只接收用户明确选中的已脱敏文件、匹配的 SHA-256 和非路径型 `source-id`。
- API Key 只从 `RAGFLOW_API_KEY` 环境变量读取，不接受命令行参数。
- 自动化测试只使用本机 `httptest.Server`，不连接真实 RAGFlow。
- 上传成功后若元数据或解析失败，只执行一次尽力清理，不自动重试写操作。

## 本地验证

```powershell
Set-Location contrib\knowledge-ragflow-poc
$goFiles = Get-ChildItem -Recurse -Filter *.go | ForEach-Object { $_.FullName }
C:\Go\bin\gofmt.exe -w $goFiles
C:\Go\bin\go.exe test ./... -count=1
C:\Go\bin\go.exe test ./... -race -count=1
C:\Go\bin\go.exe vet ./...
```

## 手工发布已脱敏文件

以下命令只用于隔离测试环境。先在本地完成脱敏并生成 SHA-256，再显式开启网络：

```powershell
$env:CHEERSAI_POC_ALLOW_NETWORK = "1"
$env:RAGFLOW_API_KEY = "<仅放在本机环境变量中的测试 Key>"

C:\Go\bin\go.exe run ./cmd/poc `
  -base-url "https://ragflow-test.example" `
  -dataset-id "<现有测试数据集 ID>" `
  -masked-file "<用户明确选择的已脱敏文件>" `
  -source-id "<非路径型不透明 ID>" `
  -sha256 "<已脱敏文件的 64 位小写 SHA-256>" `
  -mask-policy-version "mask-policy-v1" `
  -publication-id "<测试发布 ID>" `
  -publication-generation "1"
```

命令成功时只输出 RAGFlow 文档 ID。不要将真实 API Key、原始文件、客户数据、日志或生成数据提交到仓库。

接口契约、数据流和验收门槛分别见 `spec/technical-spec.md`、`spec/data-flow.md` 与 `docs/development/acceptance-standard.md`。
