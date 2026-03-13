#!/usr/bin/env bash
set -euo pipefail

# 计算 ltfp 模块根目录，确保脚本可在任意工作目录执行。
ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

echo ">> [ltfp] run proto generation"
"${ROOT_DIR}/scripts/gen_proto.sh"

echo ">> [ltfp] run regression matrix"
make -C "${ROOT_DIR}" test-unit
make -C "${ROOT_DIR}" test-binding
make -C "${ROOT_DIR}" test-integration
make -C "${ROOT_DIR}" test-parity
make -C "${ROOT_DIR}" test-pressure
make -C "${ROOT_DIR}" test-compat

# 校验 pb/gen 目录是否存在未生成提交，避免 schema 与绑定代码漂移。
git -C "${ROOT_DIR}" diff --exit-code -- pb/gen/devbridge/loop/v2

# 可选执行跨模块兼容性测试；默认关闭以保持协议库独立开发节奏。
if [[ "${LTFP_REQUIRE_CROSS_MODULE:-0}" == "1" ]]; then
  echo ">> [ltfp] run cross-module compatibility tests"
  make -C "${ROOT_DIR}/.." test-go
fi

echo ">> [ltfp] release verification passed"
