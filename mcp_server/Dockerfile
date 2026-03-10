# ==================== MCP Server Dockerfile ====================
# 多阶段构建，优化镜像体积

# ---------- 阶段 1: 构建依赖 ----------
FROM python:3.12-slim AS builder

WORKDIR /app

# 安装构建依赖
RUN apt-get update && apt-get install -y --no-install-recommends \
    gcc \
    && rm -rf /var/lib/apt/lists/*

# 先安装依赖（利用 Docker 层缓存）
COPY requirements.txt .
RUN pip install --no-cache-dir --prefix=/install -r requirements.txt

# ---------- 阶段 2: 运行时镜像 ----------
FROM python:3.12-slim AS runtime

WORKDIR /app

# 安装运行时依赖
RUN apt-get update && apt-get install -y --no-install-recommends \
    curl \
    && rm -rf /var/lib/apt/lists/*

# 从 builder 阶段复制已安装的 Python 包
COPY --from=builder /install /usr/local

# 复制应用代码
COPY server/ ./server/
COPY client/ ./client/
COPY .env.example .env

# 创建非 root 用户
RUN useradd -m -r mcpuser && chown -R mcpuser:mcpuser /app
USER mcpuser

# 暴露端口
EXPOSE 8000

# 健康检查
HEALTHCHECK --interval=30s --timeout=10s --start-period=5s --retries=3 \
    CMD curl -f http://localhost:8000/sse || exit 1

# 环境变量
ENV MCP_TRANSPORT=sse \
    MCP_SERVER_HOST=0.0.0.0 \
    MCP_SERVER_PORT=8000 \
    MCP_LOG_LEVEL=INFO \
    PYTHONUNBUFFERED=1

# 启动命令
CMD ["python", "-m", "server.main"]
