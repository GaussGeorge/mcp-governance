#!/bin/bash
# ==================== MCP Server 部署脚本 ====================
# 支持本地部署和远程服务器部署
#
# 使用方式:
#   chmod +x deploy/deploy.sh
#   ./deploy/deploy.sh local      # 本地 Docker 部署
#   ./deploy/deploy.sh remote     # 远程服务器部署 (需配置 SSH)
#   ./deploy/deploy.sh stop       # 停止服务
#   ./deploy/deploy.sh logs       # 查看日志
#   ./deploy/deploy.sh test       # 部署后测试

set -euo pipefail

# ==================== 配置 ====================
PROJECT_NAME="mcp-server"
REMOTE_HOST="${REMOTE_HOST:-your-server.com}"
REMOTE_USER="${REMOTE_USER:-root}"
REMOTE_DIR="${REMOTE_DIR:-/opt/mcp-server}"
IMAGE_NAME="mcp-real-server:latest"

# 颜色输出
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m'

log_info() { echo -e "${GREEN}[INFO]${NC} $1"; }
log_warn() { echo -e "${YELLOW}[WARN]${NC} $1"; }
log_error() { echo -e "${RED}[ERROR]${NC} $1"; }

# ==================== 本地部署 ====================
deploy_local() {
    log_info "🚀 开始本地 Docker 部署..."

    # 检查 Docker
    if ! command -v docker &> /dev/null; then
        log_error "Docker 未安装，请先安装 Docker"
        exit 1
    fi

    # 创建 .env 文件（如果不存在）
    if [ ! -f .env ]; then
        log_info "📝 从模板创建 .env 文件..."
        cp .env.example .env
    fi

    # 构建镜像
    log_info "🔨 构建 Docker 镜像..."
    docker build -t ${IMAGE_NAME} .

    # 停止旧容器
    docker compose down 2>/dev/null || true

    # 启动服务
    log_info "🚀 启动 MCP 服务器..."
    docker compose up -d mcp-server

    # 等待启动
    log_info "⏳ 等待服务器就绪..."
    sleep 3

    # 检查健康状态
    if curl -sf http://localhost:8000/sse > /dev/null 2>&1; then
        log_info "✅ MCP 服务器启动成功!"
        log_info "📍 SSE 端点: http://localhost:8000/sse"
        log_info "📍 消息端点: http://localhost:8000/messages"
    else
        log_warn "⚠️ 服务器可能还在启动中，请稍后检查"
        docker compose logs mcp-server
    fi
}

# ==================== 生产环境部署 (含 Nginx) ====================
deploy_production() {
    log_info "🚀 开始生产环境部署 (含 Nginx)..."

    if [ ! -f .env ]; then
        cp .env.example .env
    fi

    docker build -t ${IMAGE_NAME} .
    docker compose --profile production down 2>/dev/null || true
    docker compose --profile production up -d

    sleep 5

    log_info "✅ 生产环境部署完成!"
    log_info "📍 HTTP: http://localhost:80"
    log_info "📍 SSE:  http://localhost:80/sse"
}

# ==================== 远程服务器部署 ====================
deploy_remote() {
    log_info "🚀 开始远程部署到 ${REMOTE_USER}@${REMOTE_HOST}:${REMOTE_DIR}..."

    # 检查 SSH 连接
    if ! ssh -o ConnectTimeout=5 ${REMOTE_USER}@${REMOTE_HOST} "echo ok" > /dev/null 2>&1; then
        log_error "无法连接到远程服务器 ${REMOTE_HOST}"
        log_info "请配置环境变量: REMOTE_HOST, REMOTE_USER, REMOTE_DIR"
        exit 1
    fi

    # 创建远程目录
    ssh ${REMOTE_USER}@${REMOTE_HOST} "mkdir -p ${REMOTE_DIR}"

    # 同步文件
    log_info "📦 同步文件到远程服务器..."
    rsync -avz --exclude='.git' --exclude='__pycache__' --exclude='.env' \
        --exclude='*.pyc' --exclude='.pytest_cache' \
        ./ ${REMOTE_USER}@${REMOTE_HOST}:${REMOTE_DIR}/

    # 远程构建和启动
    log_info "🔨 远程构建和启动..."
    ssh ${REMOTE_USER}@${REMOTE_HOST} << REMOTE_SCRIPT
        cd ${REMOTE_DIR}
        if [ ! -f .env ]; then
            cp .env.example .env
        fi
        docker build -t ${IMAGE_NAME} .
        docker compose down 2>/dev/null || true
        docker compose up -d mcp-server
        sleep 3
        docker compose logs --tail=20 mcp-server
REMOTE_SCRIPT

    log_info "✅ 远程部署完成!"
    log_info "📍 SSE 端点: http://${REMOTE_HOST}:8000/sse"
}

# ==================== 不使用 Docker 的本地直接运行 ====================
deploy_direct() {
    log_info "🚀 直接启动 (不使用 Docker)..."

    # 检查 Python
    if ! command -v python3 &> /dev/null; then
        log_error "Python3 未安装"
        exit 1
    fi

    # 创建虚拟环境
    if [ ! -d "venv" ]; then
        log_info "📦 创建虚拟环境..."
        python3 -m venv venv
    fi

    # 激活虚拟环境并安装依赖
    source venv/bin/activate
    log_info "📦 安装依赖..."
    pip install -r requirements.txt

    # 创建 .env
    if [ ! -f .env ]; then
        cp .env.example .env
    fi

    # 启动服务器
    log_info "🚀 启动 MCP 服务器..."
    python -m server.main
}

# ==================== 停止服务 ====================
stop_services() {
    log_info "🛑 停止服务..."
    docker compose --profile production down 2>/dev/null || true
    docker compose down 2>/dev/null || true
    log_info "✅ 服务已停止"
}

# ==================== 查看日志 ====================
show_logs() {
    docker compose logs -f --tail=100 mcp-server
}

# ==================== 部署后测试 ====================
test_deployment() {
    log_info "🧪 测试部署..."

    local url="${1:-http://localhost:8000}"

    # 测试 SSE 端点
    log_info "测试 SSE 端点..."
    if curl -sf "${url}/sse" > /dev/null 2>&1; then
        log_info "✅ SSE 端点正常"
    else
        log_error "❌ SSE 端点无响应"
    fi

    # 测试工具调用 (通过发送 JSON-RPC)
    log_info "测试工具列表..."
    RESPONSE=$(curl -sf -X POST "${url}/messages" \
        -H "Content-Type: application/json" \
        -d '{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}' 2>/dev/null || echo "FAIL")

    if [ "$RESPONSE" != "FAIL" ]; then
        log_info "✅ 工具列表响应: $(echo $RESPONSE | head -c 200)"
    else
        log_warn "⚠️ 工具列表请求失败 (SSE 模式下需使用 MCP 客户端)"
    fi

    log_info "🧪 测试完成"
}

# ==================== 主入口 ====================
case "${1:-help}" in
    local)
        deploy_local
        ;;
    production)
        deploy_production
        ;;
    remote)
        deploy_remote
        ;;
    direct)
        deploy_direct
        ;;
    stop)
        stop_services
        ;;
    logs)
        show_logs
        ;;
    test)
        test_deployment "${2:-http://localhost:8000}"
        ;;
    *)
        echo "==================== MCP Server 部署工具 ===================="
        echo ""
        echo "用法: $0 <command>"
        echo ""
        echo "命令:"
        echo "  local       本地 Docker 部署 (仅 MCP 服务器)"
        echo "  production  生产环境部署 (MCP + Nginx)"
        echo "  remote      远程服务器部署 (通过 SSH + rsync)"
        echo "  direct      本地直接运行 (不使用 Docker，创建 venv)"
        echo "  stop        停止所有服务"
        echo "  logs        查看服务日志"
        echo "  test [url]  部署后测试"
        echo ""
        echo "环境变量 (远程部署):"
        echo "  REMOTE_HOST  远程服务器地址 (默认: your-server.com)"
        echo "  REMOTE_USER  SSH 用户名 (默认: root)"
        echo "  REMOTE_DIR   远程目录 (默认: /opt/mcp-server)"
        echo ""
        echo "示例:"
        echo "  $0 local                    # 本地 Docker 快速启动"
        echo "  $0 direct                   # 本地 venv 直接运行"
        echo "  REMOTE_HOST=1.2.3.4 $0 remote  # 部署到远程服务器"
        ;;
esac
