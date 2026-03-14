#!/bin/bash
# ==================== MCP 集成测试启动脚本 ====================
#
# 自动化完整流程：
#   1. 启动 Python MCP Bridge 服务器
#   2. 等待 Bridge 就绪
#   3. 运行 Go 集成测试
#   4. 关闭 Bridge
#
# 用法：
#   ./integration/run.sh                           # 快速测试
#   ./integration/run.sh quick                     # 快速对比
#   ./integration/run.sh full                      # 完整测试
#   ./integration/run.sh single rajomon step       # 单策略
#   ./integration/run.sh cross-pattern             # 全量对比

set -e

# ==================== 配置 ====================
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
MCP_SERVER_DIR="$PROJECT_ROOT/mcp_server"
BRIDGE_PORT=${BRIDGE_PORT:-9000}
BRIDGE_PID=""

# 颜色输出
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

log_info() { echo -e "${BLUE}[INFO]${NC} $1"; }
log_success() { echo -e "${GREEN}[OK]${NC} $1"; }
log_warn() { echo -e "${YELLOW}[WARN]${NC} $1"; }
log_error() { echo -e "${RED}[ERROR]${NC} $1"; }

# ==================== 清理函数 ====================
cleanup() {
    if [ -n "$BRIDGE_PID" ] && kill -0 "$BRIDGE_PID" 2>/dev/null; then
        log_info "正在关闭 Python MCP Bridge (PID: $BRIDGE_PID)..."
        kill "$BRIDGE_PID" 2>/dev/null || true
        wait "$BRIDGE_PID" 2>/dev/null || true
        log_success "Bridge 已关闭"
    fi
}
trap cleanup EXIT

# ==================== 检查依赖 ====================
log_info "检查依赖..."

# 检查 Go
if ! command -v go &>/dev/null; then
    log_error "Go 未安装"
    exit 1
fi
log_success "Go $(go version | awk '{print $3}')"

# 检查 Python
PYTHON_CMD=""
if command -v python3 &>/dev/null; then
    PYTHON_CMD="python3"
elif command -v python &>/dev/null; then
    PYTHON_CMD="python"
fi

if [ -z "$PYTHON_CMD" ]; then
    log_error "Python 未安装"
    exit 1
fi
log_success "Python $($PYTHON_CMD --version 2>&1 | awk '{print $2}')"

# 检查 MCP Server 目录
if [ ! -d "$MCP_SERVER_DIR/server" ]; then
    log_error "MCP Server 目录不存在: $MCP_SERVER_DIR"
    exit 1
fi
log_success "MCP Server 目录已找到"

# ==================== 安装 Python 依赖 ====================
log_info "检查 Python 依赖..."
cd "$MCP_SERVER_DIR"

# 检查 uvicorn 和 starlette
$PYTHON_CMD -c "import uvicorn; import starlette" 2>/dev/null || {
    log_warn "安装缺少的 Python 依赖..."
    $PYTHON_CMD -m pip install uvicorn starlette --quiet
}
log_success "Python 依赖已就绪"

# ==================== 启动 Python MCP Bridge ====================
log_info "启动 Python MCP Bridge (端口: $BRIDGE_PORT)..."

cd "$MCP_SERVER_DIR"
$PYTHON_CMD -m server.bridge --port "$BRIDGE_PORT" --log-level warning &
BRIDGE_PID=$!
log_info "Bridge PID: $BRIDGE_PID"

# 等待 Bridge 就绪
log_info "等待 Bridge 就绪..."
MAX_WAIT=30
for i in $(seq 1 $MAX_WAIT); do
    if curl -s "http://localhost:$BRIDGE_PORT/health" >/dev/null 2>&1; then
        log_success "Bridge 已就绪 (${i}s)"
        break
    fi
    if ! kill -0 "$BRIDGE_PID" 2>/dev/null; then
        log_error "Bridge 进程意外退出"
        exit 1
    fi
    sleep 1
    if [ "$i" -eq "$MAX_WAIT" ]; then
        log_error "Bridge 启动超时 (${MAX_WAIT}s)"
        exit 1
    fi
done

# 显示可用工具
TOOLS=$(curl -s "http://localhost:$BRIDGE_PORT/health" | python3 -c "import sys,json; print(', '.join(json.load(sys.stdin).get('tools',[])))" 2>/dev/null || echo "未知")
log_success "可用工具: $TOOLS"

# ==================== 运行 Go 集成测试 ====================
cd "$PROJECT_ROOT"

# 解析命令行参数
MODE="${1:-quick}"
STRATEGY="${2:-rajomon}"
PATTERN="${3:-step}"

log_info "运行集成测试 (模式: $MODE)"
echo ""

# 构建并运行
GO_ARGS="-mode=$MODE -bridge-url=http://localhost:$BRIDGE_PORT"

case "$MODE" in
    quick)
        go run ./integration/ $GO_ARGS
        ;;
    full)
        go run ./integration/ $GO_ARGS -runs=3
        ;;
    single)
        go run ./integration/ $GO_ARGS -strategy="$STRATEGY" -pattern="$PATTERN"
        ;;
    cross-pattern)
        go run ./integration/ $GO_ARGS
        ;;
    *)
        go run ./integration/ $GO_ARGS
        ;;
esac

echo ""
log_success "集成测试完成！"
log_info "CSV 结果目录: integration/output/"

# 检查是否有可视化脚本
if [ -f "$SCRIPT_DIR/plot_integration.py" ]; then
    echo ""
    log_info "生成可视化图表..."
    cd "$PROJECT_ROOT"
    conda run -n mcp-env --no-capture-output python "$SCRIPT_DIR/plot_integration.py" || log_warn "可视化图表生成失败 (可忽略)"
fi
