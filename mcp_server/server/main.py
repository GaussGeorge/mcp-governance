"""
MCP Server 主入口
====================
基于官方 MCP Python SDK (FastMCP) 构建的真实 MCP 服务器。

支持两种传输模式:
  - stdio: 本地进程间通信 (适合 Claude Desktop / IDE 集成)
  - sse:   HTTP + Server-Sent Events (适合网络部署 / 云服务器)

启动方式:
  # SSE 模式 (默认, 用于网络访问)
  python -m server.main

  # stdio 模式 (用于 Claude Desktop 等本地客户端)
  MCP_TRANSPORT=stdio python -m server.main

  # 使用 MCP CLI 启动 (开发调试)
  mcp dev server/main.py
"""

import logging

# 从 server.app 导入 mcp 实例和配置
# 这样避免 python -m server.main 导致的 __main__ vs server.main 双重导入问题
from server.app import mcp, config  # noqa: F401

logger = logging.getLogger("mcp-server")


# ==================== 服务器启动入口 ====================


def main():
    """启动 MCP 服务器"""
    logger.info(f"🚀 MCP Server [{config.server_name}] 正在启动...")
    logger.info(f"   传输模式: {config.transport}")

    if config.transport == "sse":
        logger.info(f"   监听地址: {config.host}:{config.port}")
        mcp.run(transport="sse")
    else:
        logger.info("   使用 stdio 传输模式")
        mcp.run(transport="stdio")


if __name__ == "__main__":
    main()


if __name__ == "__main__":
    main()
