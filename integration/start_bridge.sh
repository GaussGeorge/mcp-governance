#!/bin/bash
# 启动 Python MCP Bridge 后台进程
# 用法: bash integration/start_bridge.sh

set -e

BRIDGE_PORT=${BRIDGE_PORT:-9000}
BRIDGE_DIR="$(cd "$(dirname "$0")/../mcp_server" && pwd)"
LOG_FILE="/tmp/mcp_bridge.log"
PID_FILE="/tmp/mcp_bridge.pid"

# 先清理旧进程
if [ -f "$PID_FILE" ]; then
    OLD_PID=$(cat "$PID_FILE")
    if kill -0 "$OLD_PID" 2>/dev/null; then
        echo "正在停止旧的 Bridge 进程 (PID: $OLD_PID)..."
        kill "$OLD_PID" 2>/dev/null || true
        sleep 1
    fi
    rm -f "$PID_FILE"
fi

# 也清理可能残留的端口占用
lsof -ti:$BRIDGE_PORT | xargs kill -9 2>/dev/null || true
sleep 0.5

echo "启动 Python MCP Bridge (端口: $BRIDGE_PORT)..."
cd "$BRIDGE_DIR"
conda run -n mcp-env --no-capture-output python -m server.bridge --port $BRIDGE_PORT --workers 1 > "$LOG_FILE" 2>&1 &
BRIDGE_PID=$!
echo "$BRIDGE_PID" > "$PID_FILE"
echo "Bridge PID: $BRIDGE_PID"

# 等待启动
echo "等待 Bridge 就绪..."
for i in $(seq 1 15); do
    if curl -s "http://localhost:$BRIDGE_PORT/health" > /dev/null 2>&1; then
        echo "✅ Bridge 已就绪 (${i}s)"
        curl -s "http://localhost:$BRIDGE_PORT/health" | python3 -m json.tool 2>/dev/null || true
        exit 0
    fi
    sleep 1
done

echo "❌ Bridge 启动超时"
cat "$LOG_FILE"
exit 1
