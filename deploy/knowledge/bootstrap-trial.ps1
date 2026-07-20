param(
    [string]$RuntimeRoot = (Join-Path $PSScriptRoot ".runtime")
)

$ErrorActionPreference = "Stop"

function New-RandomSecret {
    param([int]$Bytes = 32)
    $buffer = New-Object byte[] $Bytes
    $rng = [System.Security.Cryptography.RandomNumberGenerator]::Create()
    try {
        $rng.GetBytes($buffer)
    } finally {
        $rng.Dispose()
    }
    return [Convert]::ToBase64String($buffer).TrimEnd('=').Replace('+', 'A').Replace('/', 'B')
}

function Get-VerifiedVendor {
    param(
        [string]$Name,
        [string]$Repository,
        [string]$Tag,
        [string]$Commit
    )
    $target = Join-Path $RuntimeRoot $Name
    if (-not (Test-Path (Join-Path $target '.git'))) {
        git clone --depth 1 --branch $Tag $Repository $target
    }
    $actual = (git -C $target rev-parse HEAD).Trim()
    if ($actual -ne $Commit) {
        throw "$Name 版本校验失败：期望 $Commit，实际 $actual。请删除 $target 后重新运行。"
    }
    return $target
}

function Set-EnvValue {
    param([string]$Path, [string]$Name, [string]$Value)
    $escaped = [regex]::Escape($Name)
    $content = Get-Content -LiteralPath $Path -Raw
    $pattern = "(?m)^$escaped=.*$"
    $replacement = "$Name=$Value"
    if ($content -match $pattern) {
        $content = [regex]::Replace($content, $pattern, $replacement)
    } else {
        $content += "`n$replacement`n"
    }
    Set-Content -LiteralPath $Path -Value $content -NoNewline
}

function Ensure-RandomEnvValue {
    param([string]$Path, [string]$Name, [int]$Bytes, [string[]]$UnsafeValues = @())
    $line = Get-Content -LiteralPath $Path | Where-Object { $_ -match ("^" + [regex]::Escape($Name) + "=") } | Select-Object -First 1
    $current = if ($line) { ($line -split '=', 2)[1] } else { "" }
    if ([string]::IsNullOrWhiteSpace($current) -or $UnsafeValues -contains $current) {
        Set-EnvValue $Path $Name (New-RandomSecret $Bytes)
    }
}

New-Item -ItemType Directory -Force -Path $RuntimeRoot | Out-Null

# RAGFlow is vendored inside the FileBay repository. Keep its runtime
# configuration outside the source tree so starting a trial never dirties the
# version-controlled upstream snapshot or records local credentials.
$ragflow = (Resolve-Path (Join-Path $PSScriptRoot "..\..\third_party\ragflow")).Path
if (-not (Test-Path (Join-Path $ragflow "docker/docker-compose.yml"))) {
    throw "未找到仓库内的 RAGFlow 源码。请使用完整的 FileBay 仓库检出后再执行部署。"
}
$ragflowRuntime = Join-Path $PSScriptRoot "runtime/ragflow"
New-Item -ItemType Directory -Force -Path $ragflowRuntime | Out-Null
$ragflowEnv = Join-Path $ragflowRuntime ".env"
if (-not (Test-Path $ragflowEnv)) {
    Copy-Item (Join-Path $ragflow "docker/.env") $ragflowEnv
}

$dify = Get-VerifiedVendor -Name "dify" -Repository "https://github.com/langgenius/dify.git" -Tag "1.15.0" -Commit "3aa26fb6374bbd47e5469f7d7cc25f3e0075a60c"

Set-EnvValue $ragflowEnv "RAGFLOW_IMAGE" "infiniflow/ragflow:v0.26.4"
# Keep the official container-internal MySQL port unchanged. Only move the
# optional host exposure away from a developer machine's local MySQL (3306).
Set-EnvValue $ragflowEnv "EXPOSE_MYSQL_PORT" "13306"
Set-EnvValue $ragflowEnv "REDIS_PORT" "16379"
# Trial machines frequently allocate only 4 GB to Docker Desktop. Limit the
# Elasticsearch JVM explicitly so it can coexist with FileBay and RAGFlow.
Set-EnvValue $ragflowEnv "ES_JAVA_OPTS" "-Xms512m -Xmx512m"
Set-EnvValue $ragflowEnv "SVR_WEB_HTTP_PORT" "19080"
Set-EnvValue $ragflowEnv "SVR_WEB_HTTPS_PORT" "19443"
Set-EnvValue $ragflowEnv "SVR_HTTP_PORT" "19380"

$difyDocker = Join-Path $dify "docker"
$difyEnv = Join-Path $difyDocker ".env"
$difyEnvCreated = $false
if (-not (Test-Path $difyEnv)) {
    Copy-Item (Join-Path $difyDocker ".env.example") $difyEnv
    $difyEnvCreated = $true
}
Set-EnvValue $difyEnv "EXPOSE_NGINX_PORT" "18080"
Set-EnvValue $difyEnv "EXPOSE_NGINX_SSL_PORT" "18443"
Ensure-RandomEnvValue $difyEnv "SECRET_KEY" 32
Ensure-RandomEnvValue $difyEnv "INIT_PASSWORD" 18
Ensure-RandomEnvValue $difyEnv "DB_PASSWORD" 24 @("difyai123456")
Ensure-RandomEnvValue $difyEnv "REDIS_PASSWORD" 24 @("difyai123456")

Write-Host "已准备仓库内的官方 RAGFlow v0.26.4 与 Dify 1.15.0 编排。"
Write-Host "下一步：复制 deploy/knowledge/.env.example 为 .env，填写 RAGFlow 专用 API 密钥和数据集 ID，然后执行 README 中的 docker compose 命令。"
