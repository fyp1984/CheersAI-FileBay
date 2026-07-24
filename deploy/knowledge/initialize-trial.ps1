<#
.SYNOPSIS
    Prepares a repeatable, local-only FileBay + RAGFlow internal trial.

.DESCRIPTION
    Creates only deployment-local runtime state. No key, dataset identifier,
    original file, or database content is written to the repository.

    A supplied RAGFlow API key is held in memory only long enough to create an
    optional dataset, then is written to the Git-ignored binding directory for
    the FileBay container to read. It is never written to .env or printed.
#>
[CmdletBinding()]
param(
    [switch]$StartServices,
    [switch]$CreateDataset,
    [string]$RagflowDatasetId,
    [string]$RagflowDatasetName = "FileBay 企业知识库（内部试用）",
    [string]$EmbeddingModel,
    [switch]$SkipBootstrap
)

$ErrorActionPreference = "Stop"

function Require-Command {
    param([string]$Name)

    if (-not (Get-Command $Name -ErrorAction SilentlyContinue)) {
        throw "未找到必需命令：$Name。请先安装并确认其位于 PATH 中。"
    }
}

function Get-PlainTextSecret {
    param([SecureString]$Secret)

    $pointer = [Runtime.InteropServices.Marshal]::SecureStringToBSTR($Secret)
    try {
        return [Runtime.InteropServices.Marshal]::PtrToStringBSTR($pointer)
    } finally {
        [Runtime.InteropServices.Marshal]::ZeroFreeBSTR($pointer)
    }
}

function Write-LocalBindingFile {
    param(
        [string]$Path,
        [string]$Value,
        [string]$Label
    )

    if ([string]::IsNullOrWhiteSpace($Value) -or $Value.Contains("`r") -or $Value.Contains("`n")) {
        throw "$Label 不能为空且不能包含换行符。"
    }

    [System.IO.File]::WriteAllText($Path, $Value.Trim(), [System.Text.UTF8Encoding]::new($false))
}

function Invoke-RagflowDatasetCreate {
    param(
        [string]$ApiKey,
        [string]$DatasetName,
        [string]$SelectedEmbeddingModel,
        [int]$Port
    )

    $headers = @{ Authorization = "Bearer $ApiKey" }
    $body = @{
        name = $DatasetName
        description = "由 FileBay 内部试用初始化脚本创建的脱敏知识库。"
        permission = "me"
        chunk_method = "naive"
    }
    if (-not [string]::IsNullOrWhiteSpace($SelectedEmbeddingModel)) {
        $body.embedding_model = $SelectedEmbeddingModel.Trim()
    }

    $uri = "http://127.0.0.1:$Port/ragflow/api/v1/datasets"
    try {
        $response = Invoke-RestMethod -Method Post -Uri $uri -Headers $headers -ContentType "application/json" -Body ($body | ConvertTo-Json -Depth 4)
    } catch {
        throw "无法创建 RAGFlow 数据集。请确认服务已启动、API 密钥有效，并已配置可用的 Embedding 模型。详细信息：$($_.Exception.Message)"
    }

    if ($response.code -ne 0 -or [string]::IsNullOrWhiteSpace($response.data.id)) {
        throw "RAGFlow 未返回有效的数据集 ID。请确认 API 密钥和 Embedding 模型配置。"
    }
    return [string]$response.data.id
}

Require-Command git
Require-Command docker

$deploymentRoot = $PSScriptRoot
$envExample = Join-Path $deploymentRoot ".env.example"
$envFile = Join-Path $deploymentRoot ".env"
$bindingDirectory = Join-Path $deploymentRoot "runtime\\ragflow-binding"
$bindingApiKey = Join-Path $bindingDirectory "api-key"
$bindingDatasetId = Join-Path $bindingDirectory "dataset-id"
$composeArguments = @(
    "compose",
    "--env-file", $envFile,
    "-f", (Join-Path $deploymentRoot "docker-compose.trial.yml"),
    "--profile", "cpu",
    "--profile", "elasticsearch"
)

if (-not (Test-Path $envFile)) {
    Copy-Item -LiteralPath $envExample -Destination $envFile
    Write-Host "已创建本机部署配置：$envFile"
}

if (-not $SkipBootstrap) {
    & (Join-Path $deploymentRoot "bootstrap-trial.ps1")
}

New-Item -ItemType Directory -Force -Path $bindingDirectory | Out-Null

if ($CreateDataset -and -not $StartServices) {
    throw "使用 -CreateDataset 时必须同时指定 -StartServices，以便脚本通过本机统一前端调用 RAGFlow。"
}
if (-not $CreateDataset -and -not [string]::IsNullOrWhiteSpace($RagflowDatasetId)) {
    $RagflowApiKey = Read-Host "请输入 RAGFlow 专用 API 密钥（仅本机使用）" -AsSecureString
} elseif ($CreateDataset) {
    $RagflowApiKey = Read-Host "请输入 RAGFlow 专用 API 密钥（仅本机使用）" -AsSecureString
}

if ($StartServices) {
    & docker @composeArguments up -d --build
    if ($LASTEXITCODE -ne 0) {
        throw "Docker Compose 启动失败。请先处理上面的容器错误。"
    }
}

$plainApiKey = $null
try {
    if ($CreateDataset) {
        $plainApiKey = Get-PlainTextSecret $RagflowApiKey
        $portLine = Get-Content -LiteralPath $envFile | Where-Object { $_ -match "^FILEBAY_HTTP_PORT=" } | Select-Object -First 1
        $port = if ($portLine) { [int](($portLine -split "=", 2)[1]) } else { 13080 }
        $RagflowDatasetId = Invoke-RagflowDatasetCreate -ApiKey $plainApiKey -DatasetName $RagflowDatasetName -SelectedEmbeddingModel $EmbeddingModel -Port $port
        Write-Host "已创建 RAGFlow 数据集并取得本机绑定 ID。"
    }

    if ($RagflowApiKey) {
        if (-not $plainApiKey) {
            $plainApiKey = Get-PlainTextSecret $RagflowApiKey
        }
        Write-LocalBindingFile -Path $bindingApiKey -Value $plainApiKey -Label "RAGFlow API 密钥"
        Write-LocalBindingFile -Path $bindingDatasetId -Value $RagflowDatasetId -Label "RAGFlow 数据集 ID"
        Write-Host "已写入本机 RAGFlow 绑定。该目录受 Git 忽略，不会提交。"

        if ($StartServices) {
            & docker @composeArguments up -d --force-recreate filebay
            if ($LASTEXITCODE -ne 0) {
                throw "FileBay 未能重新加载 RAGFlow 绑定。"
            }
        }
    }
} finally {
    $plainApiKey = $null
}

if (-not (Test-Path $bindingApiKey) -or -not (Test-Path $bindingDatasetId)) {
    Write-Warning "基础服务已准备好，但尚未建立 FileBay-RAGFlow 绑定。请在 RAGFlow 创建专用 API 密钥后，重新执行本脚本并传入 -RagflowDatasetId，或使用 -CreateDataset。"
}

Write-Host "初始化完成。浏览器入口：http://127.0.0.1:13080/knowledge"
