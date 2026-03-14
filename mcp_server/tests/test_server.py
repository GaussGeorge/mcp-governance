"""
MCP Server 测试
====================
测试 FastMCP 服务器的基本功能：工具注册、资源、提示模板等。
"""

import pytest
from mcp.server.fastmcp import FastMCP


@pytest.fixture
def test_server():
    """创建一个测试用 FastMCP 服务器"""
    server = FastMCP(name="test-server", version="0.0.1")

    @server.tool()
    async def echo(message: str) -> str:
        """回显消息"""
        return f"echo: {message}"

    @server.resource("test://info")
    def test_info() -> str:
        return "test server info"

    @server.prompt("test_prompt")
    def test_prompt(topic: str) -> str:
        return f"Please analyze: {topic}"

    return server


class TestServerCreation:
    """测试服务器创建和配置"""

    def test_create_server(self):
        """测试创建 FastMCP 实例"""
        server = FastMCP(name="test")
        assert server is not None

    def test_server_with_config(self):
        """测试通过配置创建服务器"""
        from server.config import ServerConfig

        config = ServerConfig(
            MCP_SERVER_NAME="test-config-server",
            MCP_SERVER_PORT=9999,
            MCP_TRANSPORT="sse",
        )
        assert config.server_name == "test-config-server"
        assert config.port == 9999
        assert config.transport == "sse"


class TestServerConfig:
    """测试服务器配置"""

    def test_default_config(self):
        """测试默认配置值"""
        from server.config import ServerConfig

        config = ServerConfig()
        assert config.server_name == "mcp-real-server"
        assert config.host == "0.0.0.0"
        assert config.port == 8000
        assert config.transport == "sse"
        assert config.enable_weather is True
        assert config.enable_calculator is True

    def test_config_override(self):
        """测试配置覆盖"""
        from server.config import ServerConfig

        config = ServerConfig(
            MCP_SERVER_NAME="custom-server",
            MCP_SERVER_PORT=3000,
            MCP_TRANSPORT="stdio",
            ENABLE_WEATHER_TOOL=False,
        )
        assert config.server_name == "custom-server"
        assert config.port == 3000
        assert config.transport == "stdio"
        assert config.enable_weather is False
