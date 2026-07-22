# build.ps1 - Windows 本地一键编译脚本
# 将 web 前端和 Go 后端编译为可执行文件 new-api.exe
#
# 用法:
#   .\build.ps1                 # 完整编译（前端 + 后端）
#   .\build.ps1 -SkipFrontend   # 仅编译后端（需已存在 web/dist/index.html）
#   .\build.ps1 -FrontendOnly   # 仅编译前端
#   .\build.ps1 -OutputDir C:\output  # 指定输出目录

param(
    [switch]$SkipFrontend,
    [switch]$FrontendOnly,
    [string]$OutputDir = "."
)

$ErrorActionPreference = "Stop"

function Write-Step($msg)  { Write-Host "`n>> $msg" -ForegroundColor Cyan }
function Write-OK($msg)    { Write-Host "   OK: $msg" -ForegroundColor Green }
function Write-Fail($msg)  { Write-Host "   FAIL: $msg" -ForegroundColor Red }

$Root = Split-Path -Parent $MyInvocation.MyCommand.Path
Set-Location $Root

$Version = ""
if (Test-Path "VERSION") {
    $raw = Get-Content "VERSION" -Raw
    if ($raw) { $Version = $raw.Trim() }
}
if ([string]::IsNullOrWhiteSpace($Version)) {
    try {
        $Version = (git describe --tags --always 2>$null).Trim()
    } catch {}
}
if ([string]::IsNullOrWhiteSpace($Version)) {
    $Version = "v0.0.0-dev"
}
Write-Host "Version: $Version" -ForegroundColor Yellow

Write-Step "检查编译工具"
$goVersion = & go version 2>$null
if (-not $goVersion) {
    Write-Fail "未找到 Go，请先安装 Go 1.25+"
    exit 1
}
Write-OK "Go: $goVersion"

if (-not $SkipFrontend) {
    $bunVersion = & bun.cmd --version 2>$null
    if (-not $bunVersion) {
        Write-Fail "未找到 Bun，请先安装 Bun (https://bun.sh)"
        exit 1
    }
    Write-OK "Bun: v$bunVersion"

    Write-Step "删除上一次前端构建"
    if (Test-Path "web\dist") {
        Remove-Item "web\dist" -Recurse -Force
        Write-OK "已删除 web/dist"
    }

    Write-Step "安装前端依赖 (bun install --frozen-lockfile)"
    Push-Location "web"
    try {
        & bun.cmd install --frozen-lockfile
        if ($LASTEXITCODE -ne 0) {
            Write-Fail "前端依赖安装失败"
            exit 1
        }
        Write-OK "前端依赖安装完成"
    } finally {
        Pop-Location
    }

    Write-Step "编译 web 前端"
    $env:DISABLE_ESLINT_PLUGIN = "true"
    $env:VITE_REACT_APP_VERSION = $Version
    Push-Location "web"
    try {
        & bun.cmd run build
        if ($LASTEXITCODE -ne 0) {
            Write-Fail "web 前端编译失败"
            exit 1
        }
        Write-OK "web 前端编译完成 -> web/dist"
    } finally {
        Pop-Location
        Remove-Item Env:DISABLE_ESLINT_PLUGIN -ErrorAction SilentlyContinue
        Remove-Item Env:VITE_REACT_APP_VERSION -ErrorAction SilentlyContinue
    }
} else {
    Write-Step "跳过前端编译 (-SkipFrontend)"
    if (-not (Test-Path "web\dist\index.html")) {
        Write-Fail "web/dist/index.html 不存在，请先编译前端"
        exit 1
    }
    Write-OK "前端产物已存在，跳过"
}

if ($FrontendOnly) {
    Write-Host "`n前端编译完成（-FrontendOnly 模式，跳过后端编译）" -ForegroundColor Green
    exit 0
}

Write-Step "编译 Go 后端 (CGO_ENABLED=0)"
$env:CGO_ENABLED = "0"
$env:GOEXPERIMENT = "greenteagc"
$ldflags = "-s -w -X 'github.com/QuantumNous/new-api/common.Version=$Version'"

$windowsOutputName = "new-api.exe"
$linuxOutputName = "new-api"
if ($OutputDir -ne ".") {
    if (-not (Test-Path $OutputDir)) {
        New-Item -ItemType Directory -Path $OutputDir -Force | Out-Null
    }
    $windowsOutputPath = Join-Path $OutputDir $windowsOutputName
    $linuxOutputPath = Join-Path $OutputDir $linuxOutputName
} else {
    $windowsOutputPath = $windowsOutputName
    $linuxOutputPath = $linuxOutputName
}

if (Test-Path $windowsOutputPath) {
    Remove-Item $windowsOutputPath -Force
    Write-OK "已删除 $windowsOutputPath"
}
if (Test-Path $linuxOutputPath) {
    Remove-Item $linuxOutputPath -Force
    Write-OK "已删除 $linuxOutputPath"
}

try {
    Write-Step "编译 Windows amd64"
    $env:GOOS = "windows"
    $env:GOARCH = "amd64"
    & go build -ldflags $ldflags -o $windowsOutputPath
    if ($LASTEXITCODE -ne 0) {
        Write-Fail "Windows 版本 Go 编译失败"
        exit 1
    }
    Write-OK "Windows 版本 Go 编译完成 -> $windowsOutputPath"

    Write-Step "编译 Linux amd64"
    $env:GOOS = "linux"
    $env:GOARCH = "amd64"
    & go build -ldflags $ldflags -o $linuxOutputPath
    if ($LASTEXITCODE -ne 0) {
        Write-Fail "Linux 版本 Go 编译失败"
        exit 1
    }
    Write-OK "Linux 版本 Go 编译完成 -> $linuxOutputPath"
} finally {
    Remove-Item Env:CGO_ENABLED -ErrorAction SilentlyContinue
    Remove-Item Env:GOEXPERIMENT -ErrorAction SilentlyContinue
}

$size = [math]::Round((Get-Item $windowsOutputPath).Length / 1MB, 1)
Write-Host "`n========================================" -ForegroundColor Green
Write-Host " 编译成功!" -ForegroundColor Green
Write-Host " 输出文件: $windowsOutputPath" -ForegroundColor Green
Write-Host " 文件大小: ${size} MB" -ForegroundColor Green
Write-Host " 版本号:   $Version" -ForegroundColor Green
Write-Host "========================================" -ForegroundColor Green
Write-Host "`n运行: .\$windowsOutputName" -ForegroundColor Yellow
Write-Host "默认监听: http://localhost:3000`n" -ForegroundColor Yellow
