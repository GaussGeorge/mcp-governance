"""
MCP 服务器应用实例
====================
将 FastMCP 实例和工具注册集中管理在独立模块中，
避免 python -m server.main 导致的 __main__ vs server.main 双重导入问题。

所有工具模块应从此处导入 mcp 实例：
    from server.app import mcp
"""

import logging
import sys

from mcp.server.fastmcp import FastMCP

from server.config import get_config

# ==================== 初始化配置 ====================
config = get_config()

# 配置日志
logging.basicConfig(
    level=getattr(logging, config.log_level.upper(), logging.INFO),
    format="%(asctime)s [%(levelname)s] %(name)s: %(message)s",
    handlers=[logging.StreamHandler(sys.stderr)],
)
logger = logging.getLogger("mcp-server")

# ==================== 创建 FastMCP 服务器实例 ====================
mcp = FastMCP(
    name=config.server_name,
    instructions="基于 MCP 协议的真实工具服务器，提供天气查询、计算器、文本处理、网页搜索等工具",
    host=config.host,
    port=config.port,
)

# ==================== 注册工具 ====================
# 通过导入工具模块来注册工具到 FastMCP 实例
# 每个工具模块内部使用 @mcp.tool() 装饰器注册

if config.enable_weather:
    from server.tools import weather  # noqa: F401

    logger.info("✅ 天气查询工具已注册")

if config.enable_calculator:
    from server.tools import calculator  # noqa: F401

    logger.info("✅ 计算器工具已注册")

if config.enable_text:
    from server.tools import text_processor  # noqa: F401

    logger.info("✅ 文本处理工具已注册")

if config.enable_web_search:
    from server.tools import web_search  # noqa: F401

    logger.info("✅ 网页搜索工具已注册")

# ==================== 注册资源和提示模板 ====================


@mcp.resource("server://info")
def get_server_info() -> str:
    """获取 MCP 服务器信息"""
    return (
        f"服务器名称: {config.server_name}\n"
        f"版本: 1.0.0\n"
        f"传输模式: {config.transport}\n"
        f"已启用工具: weather={config.enable_weather}, "
        f"calculator={config.enable_calculator}, "
        f"text={config.enable_text}, "
        f"web_search={config.enable_web_search}"
    )


@mcp.resource("server://health")
def health_check() -> str:
    """健康检查端点"""
    return "OK"


@mcp.prompt("analyze")
def analyze_prompt(topic: str) -> str:
    """生成分析类提示模板"""
    return f"请对以下主题进行深入分析，包括背景、现状、趋势和建议：\n\n主题：{topic}"


@mcp.prompt("summarize")
def summarize_prompt(content: str, max_words: int = 200) -> str:
    """生成摘要类提示模板"""
    return f"请将以下内容总结为不超过 {max_words} 字的摘要：\n\n{content}"
