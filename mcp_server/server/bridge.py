"""
MCP Server HTTP Bridge
=======================
为 Go 治理代理提供 JSON-RPC 2.0 HTTP 端点。

架构说明:
  Go 负载生成器 → Go 治理代理 (Rajomon/限流/无治理) → 本 Bridge → Python MCP 工具函数

本模块创建一个独立的 HTTP 服务器，镜像了 Go MCPServer (mcp_transport.go) 的 JSON-RPC 接口，
使 Go 代理可以通过标准 HTTP POST 调用 Python MCP 工具。

协议格式 (与 Go MCPServer 完全一致):
  POST /mcp
  Content-Type: application/json

  请求: {"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": {"name": "calculate", "arguments": {"expression": "2+3"}}}
  成功响应: {"jsonrpc": "2.0", "id": 1, "result": {"content": [{"type": "text", "text": "5"}]}}
  错误响应: {"jsonrpc": "2.0", "id": 1, "error": {"code": -32603, "message": "..."}}

启动方式:
  cd mcp_server
  python -m server.bridge                     # 默认端口 9000
  python -m server.bridge --port 9001         # 自定义端口
  BRIDGE_PORT=9001 python -m server.bridge    # 环境变量方式
"""

import asyncio
import inspect
import json
import logging
import math
import os
import sys
import time
import traceback
import threading
from typing import Any

import uvicorn
from starlette.applications import Starlette
from starlette.requests import Request
from starlette.responses import JSONResponse
from starlette.routing import Route

# ==================== 日志配置 ====================
logging.basicConfig(
    level=logging.INFO,
    format="%(asctime)s [%(levelname)s] %(name)s: %(message)s",
    handlers=[logging.StreamHandler(sys.stderr)],
)
logger = logging.getLogger("mcp-bridge")


# ==================== 工具注册表 ====================
# 存储工具名 → (函数, 参数信息) 的映射
TOOL_REGISTRY: dict[str, dict[str, Any]] = {}


def register_tool(
    name: str, func: Any, description: str = "", input_schema: dict | None = None
):
    """注册一个工具到 Bridge 服务器"""
    TOOL_REGISTRY[name] = {
        "func": func,
        "description": description,
        "input_schema": input_schema or {"type": "object"},
        "is_async": inspect.iscoroutinefunction(func),
    }
    logger.info(f"  ✅ 已注册工具: {name}")


def _load_tools():
    """
    从 MCP 服务器加载所有已注册工具。
    通过导入 server.app 来触发 @mcp.tool() 装饰器注册,
    然后从 FastMCP 实例中提取工具函数。
    """
    logger.info("正在加载 MCP 工具...")

    # 导入 app 模块, 这会触发所有工具的注册
    from server.app import mcp as mcp_instance

    # 从 FastMCP 实例获取所有已注册工具
    # FastMCP 内部维护了工具列表, 我们需要提取工具函数
    # 由于 FastMCP 的内部 API 可能变化, 我们直接导入工具模块并手动注册

    # --- 计算器工具 ---
    from server.tools.calculator import calculate, unit_convert, statistics

    register_tool(
        "calculate",
        calculate,
        "计算数学表达式",
        {
            "type": "object",
            "properties": {
                "expression": {"type": "string", "description": "数学表达式"}
            },
            "required": ["expression"],
        },
    )
    register_tool(
        "unit_convert",
        unit_convert,
        "单位转换",
        {
            "type": "object",
            "properties": {
                "value": {"type": "number"},
                "from_unit": {"type": "string"},
                "to_unit": {"type": "string"},
            },
            "required": ["value", "from_unit", "to_unit"],
        },
    )
    register_tool(
        "statistics",
        statistics,
        "统计计算",
        {
            "type": "object",
            "properties": {
                "numbers": {
                    "type": "array",
                    "items": {"type": "number"},
                    "description": "数字列表",
                }
            },
            "required": ["numbers"],
        },
    )

    # --- 文本处理工具 ---
    from server.tools.text_processor import (
        text_analyze,
        text_transform,
        text_encode,
        json_format,
        regex_match,
    )

    register_tool(
        "text_analyze",
        text_analyze,
        "文本分析",
        {
            "type": "object",
            "properties": {"text": {"type": "string"}},
            "required": ["text"],
        },
    )
    register_tool(
        "text_transform",
        text_transform,
        "文本转换",
        {
            "type": "object",
            "properties": {"text": {"type": "string"}, "operation": {"type": "string"}},
            "required": ["text", "operation"],
        },
    )
    register_tool(
        "text_encode",
        text_encode,
        "文本编码",
        {
            "type": "object",
            "properties": {"text": {"type": "string"}, "encoding": {"type": "string"}},
            "required": ["text", "encoding"],
        },
    )
    register_tool(
        "json_format",
        json_format,
        "JSON 格式化",
        {
            "type": "object",
            "properties": {
                "json_text": {"type": "string"},
                "indent": {"type": "integer", "default": 2},
            },
            "required": ["json_text"],
        },
    )
    register_tool(
        "regex_match",
        regex_match,
        "正则表达式匹配",
        {
            "type": "object",
            "properties": {
                "text": {"type": "string"},
                "pattern": {"type": "string"},
            },
            "required": ["text", "pattern"],
        },
    )

    # --- 天气查询工具 (涉及外部 API, 负载测试时可选) ---
    try:
        from server.tools.weather import get_weather, get_weather_forecast

        register_tool(
            "get_weather",
            get_weather,
            "查询天气",
            {
                "type": "object",
                "properties": {"city": {"type": "string"}},
                "required": ["city"],
            },
        )
        register_tool(
            "get_weather_forecast",
            get_weather_forecast,
            "天气预报",
            {
                "type": "object",
                "properties": {"city": {"type": "string"}, "days": {"type": "integer"}},
                "required": ["city"],
            },
        )
    except Exception as e:
        logger.warning(f"天气工具加载失败 (可忽略): {e}")

    # --- 网页搜索工具 (涉及外部 API, 负载测试时可选) ---
    try:
        from server.tools.web_search import fetch_webpage, extract_links, url_info

        register_tool(
            "fetch_webpage",
            fetch_webpage,
            "获取网页内容",
            {
                "type": "object",
                "properties": {
                    "url": {"type": "string"},
                    "max_length": {"type": "integer", "default": 5000},
                },
                "required": ["url"],
            },
        )
        register_tool(
            "extract_links",
            extract_links,
            "提取网页链接",
            {
                "type": "object",
                "properties": {
                    "url": {"type": "string"},
                    "max_links": {"type": "integer", "default": 50},
                },
                "required": ["url"],
            },
        )
        register_tool(
            "url_info",
            url_info,
            "获取URL信息",
            {
                "type": "object",
                "properties": {"url": {"type": "string"}},
                "required": ["url"],
            },
        )
    except Exception as e:
        logger.warning(f"网页搜索工具加载失败 (可忽略): {e}")

    logger.info(f"共加载 {len(TOOL_REGISTRY)} 个工具")


# ==================== JSON-RPC 2.0 协议处理 ====================

JSONRPC_VERSION = "2.0"

# 标准 JSON-RPC 错误码
CODE_PARSE_ERROR = -32700
CODE_INVALID_REQUEST = -32600
CODE_METHOD_NOT_FOUND = -32601
CODE_INVALID_PARAMS = -32602
CODE_INTERNAL_ERROR = -32603


def jsonrpc_success(req_id: Any, result: Any) -> dict:
    """构造 JSON-RPC 成功响应"""
    return {"jsonrpc": JSONRPC_VERSION, "id": req_id, "result": result}


def jsonrpc_error(req_id: Any, code: int, message: str, data: Any = None) -> dict:
    """构造 JSON-RPC 错误响应"""
    error = {"code": code, "message": message}
    if data is not None:
        error["data"] = data
    return {"jsonrpc": JSONRPC_VERSION, "id": req_id, "error": error}


# ==================== 请求统计 ====================
class RequestStats:
    """简单的请求统计计数器"""

    def __init__(self):
        self.total = 0
        self.success = 0
        self.errors = 0
        self.start_time = time.time()

    def record_success(self):
        self.total += 1
        self.success += 1

    def record_error(self):
        self.total += 1
        self.errors += 1

    def get_stats(self) -> dict:
        uptime = time.time() - self.start_time
        rps = self.total / max(uptime, 1)
        return {
            "total_requests": self.total,
            "success": self.success,
            "errors": self.errors,
            "uptime_seconds": round(uptime, 1),
            "rps": round(rps, 2),
        }


stats = RequestStats()


# ==================== 并发控制与过载模拟 ====================
class ConcurrencyLimiter:
    """
    模拟真实后端的资源限制行为。
    当并发数超过阈值时，逐步增加处理延迟（模拟 CPU/内存竞争），
    超过最大容量时直接拒绝（模拟 OOM/线程池耗尽）。

    这使得三种治理策略的对比实验有意义：
    - 无治理：高并发时延迟飙升，最终大量错误
    - 静态限流：超过 QPS 阈值后一刀切拒绝
    - Rajomon：动态感知后端压力，按价格优先保护高预算请求
    """

    def __init__(
        self,
        max_concurrent: int = 50,
        base_delay_ms: float = 15.0,
        delay_jitter_ms: float = 10.0,
        overload_scale: float = 8.0,
    ):
        self.max_concurrent = max_concurrent
        self.base_delay_ms = base_delay_ms
        self.delay_jitter_ms = delay_jitter_ms
        self.overload_scale = overload_scale
        self._active = 0
        self._lock = threading.Lock()

    def acquire(self) -> tuple[bool, float]:
        """
        尝试获取处理槽位。
        返回 (允许处理, 额外延迟秒数)
        """
        import random

        with self._lock:
            self._active += 1
            current = self._active

        # 超过最大容量：拒绝 (模拟资源耗尽)
        if current > self.max_concurrent:
            return False, 0.0

        # 计算过载额外延迟
        load_ratio = current / self.max_concurrent
        extra_delay_ms = 0.0
        if load_ratio > 0.7:
            # 超过70%容量后延迟指数增长 (与 loadtest/server.go 一致)
            overload_factor = ((load_ratio - 0.7) / 0.3) ** 2 * self.overload_scale
            extra_delay_ms = self.base_delay_ms * overload_factor

        # 基础延迟 + 随机波动 + 过载延迟
        total_delay_ms = (
            self.base_delay_ms
            + random.uniform(0, self.delay_jitter_ms)
            + extra_delay_ms
        )
        return True, total_delay_ms / 1000.0  # 转为秒

    def release(self):
        with self._lock:
            self._active -= 1

    @property
    def active_count(self) -> int:
        return self._active


# 全局并发限制器 (参数可通过环境变量调整)
# 默认 max_concurrent=20 配合 base_delay=50ms:
#   - 30 并发 → in-flight ≈ 25, 开始过载延迟
#   - 60 并发 → in-flight ≈ 50, 超过容量被拒绝
#   - 120 并发 → 大量拒绝
concurrency_limiter = ConcurrencyLimiter(
    max_concurrent=int(os.environ.get("BRIDGE_MAX_CONCURRENT", "20")),
    base_delay_ms=float(os.environ.get("BRIDGE_BASE_DELAY_MS", "50")),
    delay_jitter_ms=float(os.environ.get("BRIDGE_DELAY_JITTER_MS", "15")),
    overload_scale=float(os.environ.get("BRIDGE_OVERLOAD_SCALE", "8.0")),
)


# ==================== HTTP 请求处理器 ====================


async def handle_mcp(request: Request) -> JSONResponse:
    """
    处理 /mcp 端点的 JSON-RPC 2.0 请求。
    完整实现了 MCP 协议的四个核心方法:
    - initialize: 协议握手
    - tools/list: 列出可用工具
    - tools/call: 调用工具
    - ping: 健康检查
    """
    try:
        body = await request.json()
    except json.JSONDecodeError as e:
        return JSONResponse(
            jsonrpc_error(None, CODE_PARSE_ERROR, f"JSON 解析错误: {e}")
        )

    req_id = body.get("id")
    method = body.get("method", "")
    params = body.get("params", {})

    # 校验 JSON-RPC 版本
    if body.get("jsonrpc") != JSONRPC_VERSION:
        return JSONResponse(
            jsonrpc_error(req_id, CODE_INVALID_REQUEST, "jsonrpc 版本必须为 2.0")
        )

    # 方法路由
    if method == "initialize":
        return JSONResponse(
            jsonrpc_success(
                req_id,
                {
                    "protocolVersion": "2024-11-05",
                    "serverInfo": {"name": "mcp-bridge-server", "version": "1.0.0"},
                    "capabilities": {"tools": {"listChanged": False}},
                },
            )
        )

    elif method == "tools/list":
        tools = [
            {
                "name": name,
                "description": info["description"],
                "inputSchema": info["input_schema"],
            }
            for name, info in TOOL_REGISTRY.items()
        ]
        return JSONResponse(jsonrpc_success(req_id, {"tools": tools}))

    elif method == "tools/call":
        return await _handle_tools_call(req_id, params)

    elif method == "ping":
        return JSONResponse(jsonrpc_success(req_id, {}))

    else:
        return JSONResponse(
            jsonrpc_error(req_id, CODE_METHOD_NOT_FOUND, f"MCP 方法 '{method}' 未找到")
        )


async def _handle_tools_call(req_id: Any, params: dict) -> JSONResponse:
    """处理 tools/call 请求 — 核心工具调用逻辑

    包含并发控制和过载模拟:
    - 并发 < 70% 容量: 正常处理 (基础延迟 + 随机波动)
    - 并发 70%~100%: 延迟指数增长 (模拟 CPU/内存竞争)
    - 并发 > 100%: 直接返回错误 (模拟资源耗尽)
    """
    tool_name = params.get("name", "")
    arguments = params.get("arguments", {})

    # 查找工具
    if tool_name not in TOOL_REGISTRY:
        stats.record_error()
        return JSONResponse(
            jsonrpc_error(req_id, CODE_METHOD_NOT_FOUND, f"工具 '{tool_name}' 未注册")
        )

    # === 并发控制: 模拟真实后端资源限制 ===
    allowed, delay_sec = concurrency_limiter.acquire()
    if not allowed:
        concurrency_limiter.release()
        stats.record_error()
        return JSONResponse(
            jsonrpc_error(
                req_id,
                CODE_INTERNAL_ERROR,
                f"服务器过载: 当前并发 {concurrency_limiter.active_count} "
                f"超过容量 {concurrency_limiter.max_concurrent}",
            )
        )

    tool_info = TOOL_REGISTRY[tool_name]
    func = tool_info["func"]

    try:
        # 模拟处理延迟 (基础延迟 + 随机波动 + 过载额外延迟)
        if delay_sec > 0:
            await asyncio.sleep(delay_sec)

        # 调用工具函数
        start = time.monotonic()
        if tool_info["is_async"]:
            result_text = await func(**arguments)
        else:
            # 在线程池中运行同步函数, 避免阻塞事件循环
            loop = asyncio.get_event_loop()
            result_text = await loop.run_in_executor(None, lambda: func(**arguments))
        elapsed_ms = (time.monotonic() - start) * 1000

        stats.record_success()

        # 构造 MCP 标准响应 (与 Go MCPServer 格式一致)
        result = {
            "content": [{"type": "text", "text": str(result_text)}],
            "isError": False,
        }

        if logger.isEnabledFor(logging.DEBUG):
            logger.debug(f"工具 {tool_name} 执行成功, 耗时 {elapsed_ms:.1f}ms")

        return JSONResponse(jsonrpc_success(req_id, result))

    except TypeError as e:
        stats.record_error()
        return JSONResponse(
            jsonrpc_error(
                req_id, CODE_INVALID_PARAMS, f"工具 '{tool_name}' 参数错误: {e}"
            )
        )
    except Exception as e:
        stats.record_error()
        logger.error(f"工具 {tool_name} 执行失败: {e}\n{traceback.format_exc()}")
        return JSONResponse(
            jsonrpc_error(req_id, CODE_INTERNAL_ERROR, f"工具执行失败: {e}")
        )
    finally:
        concurrency_limiter.release()


async def handle_health(request: Request) -> JSONResponse:
    """健康检查端点"""
    return JSONResponse(
        {
            "status": "ok",
            "tools": list(TOOL_REGISTRY.keys()),
            "stats": stats.get_stats(),
            "concurrency": {
                "active": concurrency_limiter.active_count,
                "max": concurrency_limiter.max_concurrent,
                "base_delay_ms": concurrency_limiter.base_delay_ms,
                "overload_scale": concurrency_limiter.overload_scale,
            },
        }
    )


async def handle_stats(request: Request) -> JSONResponse:
    """统计信息端点"""
    return JSONResponse(stats.get_stats())


# ==================== Starlette 应用 ====================


def create_app() -> Starlette:
    """创建 Starlette 应用实例"""
    _load_tools()

    routes = [
        Route("/mcp", handle_mcp, methods=["POST"]),
        Route("/health", handle_health, methods=["GET"]),
        Route("/stats", handle_stats, methods=["GET"]),
    ]

    app = Starlette(routes=routes)
    logger.info("Bridge 应用已创建")
    return app


# ==================== 主入口 ====================


def main():
    """启动 Bridge HTTP 服务器"""
    import argparse

    parser = argparse.ArgumentParser(description="MCP Server HTTP Bridge")
    parser.add_argument("--host", default="0.0.0.0", help="监听地址 (默认: 0.0.0.0)")
    parser.add_argument(
        "--port",
        type=int,
        default=int(os.environ.get("BRIDGE_PORT", "9000")),
        help="监听端口 (默认: 9000)",
    )
    parser.add_argument(
        "--workers", type=int, default=4, help="Worker 进程数 (默认: 4)"
    )
    parser.add_argument("--log-level", default="info", help="日志级别 (默认: info)")
    args = parser.parse_args()

    logger.info(f"🚀 MCP Bridge Server 启动中...")
    logger.info(f"   监听地址: {args.host}:{args.port}")
    logger.info(f"   Worker 数: {args.workers}")
    logger.info(f"   端点: POST /mcp (JSON-RPC 2.0)")
    logger.info(f"   健康检查: GET /health")

    uvicorn.run(
        "server.bridge:create_app",
        host=args.host,
        port=args.port,
        workers=args.workers,
        log_level=args.log_level,
        factory=True,
    )


if __name__ == "__main__":
    main()
