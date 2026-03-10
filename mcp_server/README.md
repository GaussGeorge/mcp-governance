# MCP Real Server — 真实 MCP 工具服务器

> 基于 [MCP Python SDK](https://github.com/modelcontextprotocol/python-sdk) 构建的生产级 MCP 工具服务器

[![Python](https://img.shields.io/badge/Python-3.10%2B-blue)](https://python.org/)
[![MCP](https://img.shields.io/badge/MCP-1.2%2B-purple)](https://modelcontextprotocol.io/)
[![License](https://img.shields.io/badge/License-MIT-green)](#)

---

## 概述

本项目是一个 **真实的 MCP (Model Context Protocol) 服务器**，区别于 `loadtest/server.go` 中的 Mock 服务器。它提供了可实际使用的工具，支持 AI Agent（如 Claude Desktop、Cursor 等）通过标准 MCP 协议调用。

### 与 Mock 服务器的区别

| 特性     | `loadtest/server.go` (Mock) | `mcp_server/` (真实)      |
| -------- | --------------------------- | ------------------------- |
| 工具实现 | `time.Sleep()` 模拟延迟     | 真实 API 调用、计算引擎   |
| 天气查询 | 返回固定字符串              | 调用 Open-Meteo API       |
| 数学计算 | 无                          | SymPy 符号计算引擎        |
| 文本处理 | 无                          | 分析、转换、编码、正则    |
| 网页抓取 | 无                          | httpx + BeautifulSoup     |
| 传输协议 | 仅 HTTP                     | SSE + stdio               |
| 部署方式 | 嵌入测试流程                | Docker / 云服务器独立部署 |
| 用途     | 压测治理算法                | 生产环境工具服务          |

---

## 项目结构

```
mcp_server/
├── server/                     # MCP 服务器
│   ├── main.py                 # 服务器主入口 (FastMCP)
│   ├── config.py               # 配置管理 (pydantic-settings)
│   └── tools/                  # 工具集
│       ├── weather.py          # 🌤️ 天气查询 (Open-Meteo API)
│       ├── calculator.py       # 🧮 计算器 (SymPy)
│       ├── text_processor.py   # 📝 文本处理
│       └── web_search.py       # 🌐 网页抓取
├── client/                     # MCP 客户端
│   ├── main.py                 # 客户端 (交互/演示模式)
│   └── agent.py                # 简易 Agent 示例
├── tests/                      # 测试
│   ├── test_server.py          # 服务器测试
│   ├── test_tools.py           # 工具单元测试
│   ├── test_client.py          # 客户端测试
│   └── test_integration.py     # 集成测试
├── deploy/                     # 部署文件
│   ├── nginx.conf              # Nginx 反向代理配置
│   └── deploy.sh               # 一键部署脚本
├── Dockerfile                  # Docker 镜像
├── docker-compose.yml          # Docker Compose 编排
├── requirements.txt            # Python 依赖
├── pyproject.toml              # 项目元数据
├── .env.example                # 环境变量模板
└── README.md                   # 本文档
```

---

## 快速开始

### 前置条件

- Python 3.10+
- pip 或 uv

### 1. 安装依赖

```bash
cd mcp_server

# 创建虚拟环境 (推荐)
python3 -m venv venv
source venv/bin/activate  # macOS/Linux
# venv\Scripts\activate   # Windows

# 安装依赖
pip install -r requirements.txt
```

### 2. 配置环境变量

```bash
cp .env.example .env
# 编辑 .env 按需修改配置
```

### 3. 启动服务器

```bash
# === SSE 模式 (用于网络访问，默认) ===
python -m server.main
# 服务器将在 http://localhost:8000 启动

# === stdio 模式 (用于 Claude Desktop 等本地客户端) ===
MCP_TRANSPORT=stdio python -m server.main

# === 使用 MCP CLI 开发调试 ===
mcp dev server/main.py
```

### 4. 连接客户端

```bash
# 演示模式 (自动调用所有工具)
python -m client.main --demo

# 交互模式
python -m client.main --interactive

# Agent 模式 (自然语言调用工具)
python -m client.agent
```

---

## 工具列表

### 🌤️ 天气查询 (`weather.py`)

| 工具名                 | 功能         | 参数                    |
| ---------------------- | ------------ | ----------------------- |
| `get_weather`          | 查询当前天气 | `city`: 城市名 (中英文) |
| `get_weather_forecast` | 天气预报     | `city`, `days`: 1-7 天  |

使用免费的 [Open-Meteo API](https://open-meteo.com/)，**无需 API Key**。

### 🧮 计算器 (`calculator.py`)

| 工具名         | 功能           | 参数                            |
| -------------- | -------------- | ------------------------------- |
| `calculate`    | 数学表达式计算 | `expression`: 表达式            |
| `unit_convert` | 单位转换       | `value`, `from_unit`, `to_unit` |
| `statistics`   | 统计分析       | `numbers`: 数字列表             |

支持：四则运算、三角函数、对数、阶乘、符号积分/微分（基于 SymPy）。

### 📝 文本处理 (`text_processor.py`)

| 工具名           | 功能         | 参数                  |
| ---------------- | ------------ | --------------------- |
| `text_analyze`   | 文本统计分析 | `text`                |
| `text_transform` | 文本转换     | `text`, `operation`   |
| `text_encode`    | 编码/哈希    | `text`, `encoding`    |
| `json_format`    | JSON 格式化  | `json_text`, `indent` |
| `regex_match`    | 正则匹配     | `text`, `pattern`     |

### 🌐 网页抓取 (`web_search.py`)

| 工具名          | 功能          | 参数                |
| --------------- | ------------- | ------------------- |
| `fetch_webpage` | 抓取网页内容  | `url`, `max_length` |
| `extract_links` | 提取页面链接  | `url`, `max_links`  |
| `url_info`      | 获取 URL 信息 | `url`               |

---

## 运行测试

```bash
# 运行所有单元测试
pytest tests/ -v

# 运行特定测试文件
pytest tests/test_tools.py -v

# 运行带覆盖率的测试
pytest tests/ --cov=server --cov-report=term-missing

# 仅运行计算器测试
pytest tests/test_tools.py::TestCalculatorTools -v
```

---

## 部署指南

### 方式一：本地直接运行

```bash
# 使用部署脚本
chmod +x deploy/deploy.sh
./deploy/deploy.sh direct
```

### 方式二：Docker 本地部署

```bash
# 构建并启动
./deploy/deploy.sh local

# 停止
./deploy/deploy.sh stop


# 或手动操作
docker build -t mcp-real-server .
docker compose up -d mcp-server
```

### 方式三：Docker 生产环境 (含 Nginx)

```bash
./deploy/deploy.sh production
# Nginx 在 80 端口提供反向代理
```

### 方式四：远程服务器部署

```bash
# 配置远程服务器信息
export REMOTE_HOST=your-server-ip
export REMOTE_USER=root
export REMOTE_DIR=/opt/mcp-server

# 一键部署
./deploy/deploy.sh remote
```

---

## 云服务器迁移指南

### AWS EC2 / 阿里云 ECS / 腾讯云 CVM

1. **创建实例**：推荐 2 核 4G 以上配置，Ubuntu 22.04
2. **安装 Docker**：
   ```bash
   curl -fsSL https://get.docker.com | sh
   sudo usermod -aG docker $USER
   ```
3. **部署 MCP 服务器**：

   ```bash
   # 方法 A: 使用部署脚本 (从本地推送)
   REMOTE_HOST=<your-ip> ./deploy/deploy.sh remote

   # 方法 B: 在服务器上直接操作
   git clone <your-repo> /opt/mcp-server
   cd /opt/mcp-server/mcp_server
   cp .env.example .env
   docker compose up -d
   ```

4. **配置安全组/防火墙**：
   - 开放 8000 端口 (MCP SSE)
   - 或开放 80/443 端口 (通过 Nginx)
5. **配置域名 + HTTPS** (可选)：
   ```bash
   # 安装 certbot
   sudo apt install certbot python3-certbot-nginx
   sudo certbot --nginx -d your-domain.com
   ```

### Kubernetes (K8s) 部署

```yaml
# k8s-deployment.yaml (示例)
apiVersion: apps/v1
kind: Deployment
metadata:
  name: mcp-server
spec:
  replicas: 3
  selector:
    matchLabels:
      app: mcp-server
  template:
    metadata:
      labels:
        app: mcp-server
    spec:
      containers:
        - name: mcp-server
          image: mcp-real-server:latest
          ports:
            - containerPort: 8000
          env:
            - name: MCP_TRANSPORT
              value: "sse"
          resources:
            limits:
              cpu: "1"
              memory: "512Mi"
---
apiVersion: v1
kind: Service
metadata:
  name: mcp-server
spec:
  selector:
    app: mcp-server
  ports:
    - port: 80
      targetPort: 8000
  type: LoadBalancer
```

### Serverless 部署 (AWS Lambda / 云函数)

> ⚠️ SSE 传输需要长连接，不适合 Serverless。建议使用 Streamable HTTP 传输或容器化部署。

---

## 集成 Claude Desktop

在 Claude Desktop 配置文件中添加：

**macOS**: `~/Library/Application Support/Claude/claude_desktop_config.json`
**Windows**: `%APPDATA%\Claude\claude_desktop_config.json`

```json
{
  "mcpServers": {
    "mcp-real-server": {
      "command": "python",
      "args": ["-m", "server.main"],
      "cwd": "/path/to/mcp_server",
      "env": {
        "MCP_TRANSPORT": "stdio"
      }
    }
  }
}
```

或通过 SSE 远程连接：

```json
{
  "mcpServers": {
    "mcp-real-server": {
      "url": "http://your-server:8000/sse"
    }
  }
}
```

---

## 集成 Cursor IDE

在项目根目录创建 `.cursor/mcp.json`：

```json
{
  "mcpServers": {
    "mcp-tools": {
      "command": "python",
      "args": ["-m", "server.main"],
      "cwd": "/path/to/mcp_server",
      "env": {
        "MCP_TRANSPORT": "stdio"
      }
    }
  }
}
```

---

## 与 Rajomon 治理框架集成

本 MCP 服务器可以替代 `loadtest/server.go` 中的 Mock 工具，用于测试 Rajomon 治理引擎在真实工具调用下的表现：

```
loadtest/loader.go → HTTP POST → MCPGovernor (Go) → 转发到 → MCP Real Server (Python)
                                   ↑ 动态定价                      ↑ 真实工具处理
```

要实现这种集成，需要在 Go 侧将 Mock 工具的 handler 替换为 HTTP 代理，将请求转发到本 Python MCP 服务器。

---

## 添加新工具

在 `server/tools/` 目录下创建新文件：

```python
# server/tools/my_tool.py
import logging

logger = logging.getLogger("mcp-server.tools.my_tool")

_mcp = None
def _get_mcp():
    global _mcp
    if _mcp is None:
        from server.main import mcp
        _mcp = mcp
    return _mcp

mcp = _get_mcp()

@mcp.tool()
async def my_tool(param1: str, param2: int = 10) -> str:
    """
    工具描述 (会显示给 AI Agent)。

    Args:
        param1: 参数1说明
        param2: 参数2说明 (默认10)
    """
    logger.info(f"my_tool 被调用: {param1}, {param2}")
    # 实现你的工具逻辑
    return f"结果: {param1} × {param2}"
```

然后在 `server/main.py` 中注册：

```python
from server.tools import my_tool  # 添加这行
logger.info("✅ 自定义工具已注册")
```

---

## 环境变量说明

| 变量名                   | 默认值            | 说明                      |
| ------------------------ | ----------------- | ------------------------- |
| `MCP_SERVER_NAME`        | `mcp-real-server` | 服务器名称                |
| `MCP_SERVER_HOST`        | `0.0.0.0`         | 监听地址                  |
| `MCP_SERVER_PORT`        | `8000`            | 监听端口                  |
| `MCP_TRANSPORT`          | `sse`             | 传输模式: `sse` / `stdio` |
| `MCP_LOG_LEVEL`          | `INFO`            | 日志级别                  |
| `ENABLE_WEATHER_TOOL`    | `true`            | 启用天气工具              |
| `ENABLE_CALCULATOR_TOOL` | `true`            | 启用计算器工具            |
| `ENABLE_TEXT_TOOL`       | `true`            | 启用文本处理工具          |
| `ENABLE_WEB_SEARCH_TOOL` | `true`            | 启用网页搜索工具          |
| `WORKERS`                | `4`               | Worker 数量 (生产环境)    |

---

## 常见问题

### Q: SSE 和 stdio 该选哪个？

- **stdio**: 本地使用，Claude Desktop / Cursor 集成，低延迟
- **SSE**: 网络部署，多客户端共享，云服务器

### Q: 天气工具需要 API Key 吗？

不需要。使用的是免费开放的 Open-Meteo API。

### Q: 如何增加并发处理能力？

- Docker 部署时增加副本数：`docker compose up -d --scale mcp-server=3`
- Nginx 会自动负载均衡到多个实例

### Q: 如何添加身份验证？

在 Nginx 配置中添加 Basic Auth 或 JWT 验证，或使用 API Gateway。

---

## License

MIT
