#!/usr/bin/env bash
# build.sh - Linux 本地一键编译脚本
# 将 web 前端和 Go 后端编译为可执行文件 new-api
#
# 用法:
#   ./build.sh                         # 完整编译（前端 + 后端）
#   ./build.sh --skip-frontend         # 仅编译后端（需已存在 web/dist/index.html）
#   ./build.sh --frontend-only         # 仅编译前端
#   ./build.sh --output-dir /tmp/output # 指定输出目录

set -euo pipefail

SKIP_FRONTEND=false
FRONTEND_ONLY=false
OUTPUT_DIR="."

while [[ $# -gt 0 ]]; do
  case "$1" in
    --skip-frontend|-SkipFrontend)
      SKIP_FRONTEND=true
      shift
      ;;
    --frontend-only|-FrontendOnly)
      FRONTEND_ONLY=true
      shift
      ;;
    --output-dir|-OutputDir)
      OUTPUT_DIR="${2:-}"
      if [[ -z "$OUTPUT_DIR" ]]; then
        echo "FAIL: --output-dir 需要指定目录"
        exit 1
      fi
      shift 2
      ;;
    -h|--help)
      echo "用法:"
      echo "  ./build.sh"
      echo "  ./build.sh --skip-frontend"
      echo "  ./build.sh --frontend-only"
      echo "  ./build.sh --output-dir /tmp/output"
      exit 0
      ;;
    *)
      echo "FAIL: 未知参数: $1"
      exit 1
      ;;
  esac
done

write_step() { echo -e "\033[36m\n>> $1\033[0m"; }
write_ok()   { echo -e "\033[32m   OK: $1\033[0m"; }
write_fail() { echo -e "\033[31m   FAIL: $1\033[0m"; }

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$ROOT"

VERSION=""
if [[ -f VERSION ]]; then
  VERSION="$(tr -d '\r\n' < VERSION | xargs || true)"
fi
if [[ -z "${VERSION// }" ]]; then
  VERSION="$(git describe --tags --always 2>/dev/null || true)"
fi
if [[ -z "${VERSION// }" ]]; then
  VERSION="v0.0.0-dev"
fi
echo -e "\033[33mVersion: $VERSION\033[0m"

write_step "检查编译工具"
if ! command -v go >/dev/null 2>&1; then
  write_fail "未找到 Go，请先安装 Go 1.25+"
  exit 1
fi
write_ok "Go: $(go version)"

if [[ "$SKIP_FRONTEND" == false ]]; then
  if ! command -v bun >/dev/null 2>&1; then
    write_fail "未找到 Bun，请先安装 Bun (https://bun.sh)"
    exit 1
  fi
  write_ok "Bun: v$(bun --version)"

  write_step "删除上一次前端构建"
  if [[ -d web/dist ]]; then
    rm -rf web/dist
    write_ok "已删除 web/dist"
  fi

  pushd web >/dev/null
  write_step "安装前端依赖 (bun install --frozen-lockfile)"
  bun install --frozen-lockfile
  write_ok "前端依赖安装完成"

  write_step "编译 web 前端"
  DISABLE_ESLINT_PLUGIN=true VITE_REACT_APP_VERSION="$VERSION" bun run build
  write_ok "web 前端编译完成 -> web/dist"
  popd >/dev/null
else
  write_step "跳过前端编译 (--skip-frontend)"
  if [[ ! -f web/dist/index.html ]]; then
    write_fail "web/dist/index.html 不存在，请先编译前端"
    exit 1
  fi
  write_ok "前端产物已存在，跳过"
fi

if [[ "$FRONTEND_ONLY" == true ]]; then
  echo -e "\033[32m\n前端编译完成（--frontend-only 模式，跳过后端编译）\033[0m"
  exit 0
fi

write_step "编译 Go 后端 (CGO_ENABLED=0)"
WINDOWS_OUTPUT_NAME="new-api.exe"
LINUX_OUTPUT_NAME="new-api"

if [[ "$OUTPUT_DIR" != "." ]]; then
  mkdir -p "$OUTPUT_DIR"
  WINDOWS_OUTPUT_PATH="$OUTPUT_DIR/$WINDOWS_OUTPUT_NAME"
  LINUX_OUTPUT_PATH="$OUTPUT_DIR/$LINUX_OUTPUT_NAME"
else
  WINDOWS_OUTPUT_PATH="$WINDOWS_OUTPUT_NAME"
  LINUX_OUTPUT_PATH="$LINUX_OUTPUT_NAME"
fi

if [[ -f "$WINDOWS_OUTPUT_PATH" ]]; then
  rm -f "$WINDOWS_OUTPUT_PATH"
  write_ok "已删除 $WINDOWS_OUTPUT_PATH"
fi
if [[ -f "$LINUX_OUTPUT_PATH" ]]; then
  rm -f "$LINUX_OUTPUT_PATH"
  write_ok "已删除 $LINUX_OUTPUT_PATH"
fi

LDFLAGS="-s -w -X github.com/QuantumNous/new-api/common.Version=$VERSION"

write_step "编译 Windows amd64"
CGO_ENABLED=0 GOEXPERIMENT=greenteagc GOOS=windows GOARCH=amd64 \
  go build -ldflags "$LDFLAGS" -o "$WINDOWS_OUTPUT_PATH"
write_ok "Windows 版本 Go 编译完成 -> $WINDOWS_OUTPUT_PATH"

write_step "编译 Linux amd64"
CGO_ENABLED=0 GOEXPERIMENT=greenteagc GOOS=linux GOARCH=amd64 \
  go build -ldflags "$LDFLAGS" -o "$LINUX_OUTPUT_PATH"
write_ok "Linux 版本 Go 编译完成 -> $LINUX_OUTPUT_PATH"

if command -v stat >/dev/null 2>&1; then
  SIZE_BYTES="$(stat -c%s "$LINUX_OUTPUT_PATH")"
  SIZE_MB="$(awk "BEGIN { printf \"%.1f\", $SIZE_BYTES / 1024 / 1024 }")"
else
  SIZE_MB="未知"
fi

echo -e "\033[32m\n========================================\033[0m"
echo -e "\033[32m 编译成功!\033[0m"
echo -e "\033[32m 输出文件: $LINUX_OUTPUT_PATH\033[0m"
echo -e "\033[32m 文件大小: ${SIZE_MB} MB\033[0m"
echo -e "\033[32m 版本号:   $VERSION\033[0m"
echo -e "\033[32m========================================\033[0m"
echo -e "\033[33m\n运行: ./$LINUX_OUTPUT_NAME\033[0m"
echo -e "\033[33m默认监听: http://localhost:3000\n\033[0m"
