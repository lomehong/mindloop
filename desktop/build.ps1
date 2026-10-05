# mindloop 桌面壳一键构建：
#   1. viewer 前端（bun）
#   2. sidecar（go build -> externalBin 命名 mindloop-x86_64-pc-windows-msvc.exe）
#   3. tauri bundle（NSIS 安装包；-SkipBundle 时只做 cargo build 便于快速验证）
#
# 用法：
#   powershell -ExecutionPolicy Bypass -File .\build.ps1              # 完整打包
#   powershell ... -File .\build.ps1 -SkipBundle                      # 只构建不打包
#   powershell ... -File .\build.ps1 -Dev                             # 开发态（tauri dev）
param(
    [switch]$SkipBundle,
    [switch]$Dev
)

$ErrorActionPreference = "Stop"
Set-Location -Path $PSScriptRoot
$desktop = $PSScriptRoot
$root = Split-Path -Parent $desktop
$tauri = Join-Path $desktop "src-tauri"

# 1) viewer 前端
Write-Host "== [1/3] viewer 前端 =="
Push-Location (Join-Path $root "web/static")
bun install
bun run build
if ($LASTEXITCODE -ne 0) { throw "viewer build failed" }
Pop-Location

# 2) sidecar：Go 二进制按 Tauri externalBin 命名约定落盘
Write-Host "== [2/3] sidecar (mindloop.exe) =="
$binDir = Join-Path $tauri "binaries"
New-Item -ItemType Directory -Force $binDir | Out-Null
$sidecar = Join-Path $binDir "mindloop-x86_64-pc-windows-msvc.exe"
Push-Location $root
# -tags release：前端产物（步骤 1 已构建）嵌入二进制——sidecar 自包含，
# 换机/分发后仪表盘开箱即用，不依赖磁盘上的源码目录。
go build -tags release -o $sidecar ./cmd/mindloop
if ($LASTEXITCODE -ne 0) { throw "go build failed" }
Pop-Location

# 3) viewer 静态资源随壳分发（bundle.resources 映射到资源目录 viewer/）
Write-Host "== [3/3] viewer 资源同步 =="
$resDir = Join-Path $tauri "viewer-static"
if (Test-Path $resDir) { Remove-Item -Recurse -Force $resDir }
Copy-Item -Recurse (Join-Path $root "web/static/build/client") $resDir

if ($Dev) {
    Write-Host "== tauri dev =="
    Set-Location $desktop
    # CLI 本地优先（desktop/package.json 的 devDependency）——部分网络
    # 环境对 registry.npmjs.org 的 TLS 链验证不稳，临时下载会挂。
    if (Test-Path (Join-Path $desktop "node_modules\@tauri-apps\cli")) { bun run tauri dev }
    else { bunx @tauri-apps/cli@2 dev }
    return
}

if ($SkipBundle) {
    Write-Host "== cargo build (skip bundle) =="
    Set-Location $tauri
    cargo build
    if ($LASTEXITCODE -ne 0) { throw "cargo build failed" }
    Write-Host "dev 二进制：$tauri\target\debug\mindloop-desktop.exe"
    return
}

Write-Host "== tauri bundle =="
Set-Location $desktop
if (Test-Path (Join-Path $desktop "node_modules\@tauri-apps\cli")) { bun run tauri build }
else { bunx @tauri-apps/cli@2 build }
if ($LASTEXITCODE -ne 0) { throw "tauri build failed" }

Write-Host ""
Write-Host "产物："
Write-Host "  NSIS 安装包：$tauri\target\release\bundle\nsis\"
Write-Host "  可执行目录 ：$tauri\target\release\（mindloop.exe + 资源，整体拷走即便携版）"
