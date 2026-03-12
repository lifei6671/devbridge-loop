#!/usr/bin/env bash
set -euo pipefail

# 计算 ltfp 模块根目录，确保脚本可在任意工作目录执行。
ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
PROTO_DIR="${ROOT_DIR}/proto"
OUT_DIR="${ROOT_DIR}/pb/gen"

# 校验 protoc 是否可用，避免执行时出现难以定位的失败。
if ! command -v protoc >/dev/null 2>&1; then
  echo "error: protoc not found, please install protobuf compiler first"
  exit 1
fi

# 校验 go 插件是否可用；协议生成依赖 protoc-gen-go 与 protoc-gen-go-grpc。
if ! command -v protoc-gen-go >/dev/null 2>&1; then
  echo "error: protoc-gen-go not found, install with: go install google.golang.org/protobuf/cmd/protoc-gen-go@latest"
  exit 1
fi
if ! command -v protoc-gen-go-grpc >/dev/null 2>&1; then
  echo "error: protoc-gen-go-grpc not found, install with: go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest"
  exit 1
fi

mkdir -p "${OUT_DIR}"

# 收集 proto 文件列表，供 protoc 统一生成。
mapfile -t PROTO_FILES < <(find "${PROTO_DIR}" -name "*.proto" -print | sort)
if [[ "${#PROTO_FILES[@]}" -eq 0 ]]; then
  echo "error: no .proto files found under ${PROTO_DIR}"
  exit 1
fi

echo ">> generating go bindings to ${OUT_DIR}"
protoc \
  --proto_path="${PROTO_DIR}" \
  --go_out="${OUT_DIR}" \
  --go_opt=paths=source_relative \
  --go-grpc_out="${OUT_DIR}" \
  --go-grpc_opt=paths=source_relative \
  "${PROTO_FILES[@]}"

echo ">> done"

