param(
    [string]$OutputDir = "dist/native",
    [switch]$SkipBuild
)

$ErrorActionPreference = "Stop"

function Require-Command {
    param([string]$Name)
    if (-not (Get-Command $Name -ErrorAction SilentlyContinue)) {
        throw "Missing required command: $Name"
    }
}

function Copy-Tree {
    param(
        [string]$Source,
        [string]$Destination
    )
    New-Item -ItemType Directory -Force -Path $Destination | Out-Null
    robocopy $Source $Destination /MIR /XD .git node_modules runtime .runtime logs tmp temp __pycache__ .pytest_cache .mypy_cache .ruff_cache .venv /XF .env *.pem *.key *.sqlite *.db | Out-Host
    if ($LASTEXITCODE -gt 7) {
        throw "robocopy failed from $Source to $Destination with exit code $LASTEXITCODE"
    }
}

$repoRoot = (Resolve-Path (Join-Path $PSScriptRoot "..\..")).Path
Set-Location $repoRoot

Require-Command git
Require-Command Compress-Archive

$commit = (git rev-parse --short=12 HEAD).Trim()
$branch = (git rev-parse --abbrev-ref HEAD).Trim()
$version = "cheersai-filebay-native-$commit"
$outputPath = Join-Path $repoRoot $OutputDir
$stagingRoot = Join-Path $outputPath "staging"
$staging = Join-Path $stagingRoot $version

Remove-Item -Recurse -Force $stagingRoot -ErrorAction SilentlyContinue
New-Item -ItemType Directory -Force -Path $staging | Out-Null

if (-not $SkipBuild) {
    Require-Command go
    Require-Command pnpm
    Require-Command npm

    pnpm install --frozen-lockfile
    Remove-Item -Force "public/assets/js/index.js" -ErrorAction SilentlyContinue
    Remove-Item -Force "public/assets/css/index.css" -ErrorAction SilentlyContinue
    pnpm exec webpack --disable-interpret

    $env:GOOS = "linux"
    $env:GOARCH = "amd64"
    $env:CGO_ENABLED = "1"
    $env:TAGS = "bindata timetzdata sqlite sqlite_unlock_notify"
    New-Item -ItemType Directory -Force -Path (Join-Path $staging "bin") | Out-Null
    go build -v -tags $env:TAGS -ldflags "-s -w -X main.Version=0.0.0-filebay-$commit -X main.Tags=$env:TAGS" -o (Join-Path $staging "bin/filebay") .

    Push-Location "third_party/ragflow/web"
    try {
        npm ci
        $env:VITE_BASE_URL = "/ragflow/"
        $env:VITE_BUILD_SOURCEMAP = "false"
        $env:VITE_MINIFY = "esbuild"
        $env:NODE_OPTIONS = "--max-old-space-size=3072"
        npm run build
    } finally {
        Pop-Location
    }
} else {
    New-Item -ItemType Directory -Force -Path (Join-Path $staging "bin") | Out-Null
    "SkipBuild placeholder. Build on a machine with Go, pnpm, npm before production use." | Set-Content -Encoding UTF8 (Join-Path $staging "bin/README.txt")
}

Copy-Tree -Source (Join-Path $repoRoot "public") -Destination (Join-Path $staging "filebay-public")
Copy-Tree -Source (Join-Path $repoRoot "templates") -Destination (Join-Path $staging "filebay-templates")
Copy-Tree -Source (Join-Path $repoRoot "options") -Destination (Join-Path $staging "filebay-options")
Copy-Tree -Source (Join-Path $repoRoot "custom") -Destination (Join-Path $staging "filebay-custom")
Copy-Tree -Source (Join-Path $repoRoot "third_party/ragflow") -Destination (Join-Path $staging "third_party/ragflow")
Copy-Tree -Source (Join-Path $repoRoot "deploy/native") -Destination (Join-Path $staging "deploy/native")
Copy-Tree -Source (Join-Path $repoRoot "deploy/knowledge/ragflow-ui") -Destination (Join-Path $staging "deploy/knowledge/ragflow-ui")

$manifest = [ordered]@{
    name = $version
    branch = $branch
    commit = $commit
    built_at = (Get-Date).ToString("s")
    includes = @(
        "bin/filebay",
        "filebay-public",
        "filebay-templates",
        "filebay-options",
        "filebay-custom",
        "third_party/ragflow",
        "deploy/native",
        "deploy/knowledge/ragflow-ui"
    )
    excludes = @(
        ".env",
        "runtime",
        ".runtime",
        "logs",
        "*.pem",
        "*.key",
        "*.sqlite",
        "*.db"
    )
}
$manifest | ConvertTo-Json -Depth 5 | Set-Content -Encoding UTF8 (Join-Path $staging "manifest.json")

$archive = Join-Path $outputPath "$version.zip"
Remove-Item -Force $archive -ErrorAction SilentlyContinue
Compress-Archive -Path (Join-Path $stagingRoot $version) -DestinationPath $archive -CompressionLevel Optimal

$shaPath = "$archive.sha256"
$hash = (Get-FileHash -Algorithm SHA256 $archive).Hash.ToLowerInvariant()
"$hash  $(Split-Path -Leaf $archive)" | Set-Content -Encoding ASCII $shaPath

Write-Host "Release bundle created:"
Write-Host $archive
Write-Host $shaPath
