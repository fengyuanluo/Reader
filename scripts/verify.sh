# 本地验证脚本：逐行调用 Makefile target，与 CI 同源，避免清单漂移。
# 用法: ./scripts/verify.sh
set -eu

cd "$(dirname "$0")/.."

make -s generate
make -s test
make -s vet
make -s generated-check
make -s openapi-check
make -s security
make -s e2e
