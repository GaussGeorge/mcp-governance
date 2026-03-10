"""
MCP Client 测试
====================
测试客户端的基本功能。
"""

import pytest
from client.main import MCPClient


class TestMCPClient:
    """测试 MCP 客户端"""

    def test_create_client(self):
        """测试创建客户端实例"""
        client = MCPClient()
        assert client.session is None
        assert client._tools == []

    @pytest.mark.asyncio
    async def test_call_without_connect(self):
        """测试未连接时调用工具应抛出异常"""
        client = MCPClient()
        with pytest.raises(RuntimeError, match="未连接到服务器"):
            await client.call_tool("test_tool")

    @pytest.mark.asyncio
    async def test_list_tools_without_connect(self):
        """测试未连接时列出工具"""
        client = MCPClient()
        # list_tools 返回缓存的空列表
        tools = await client.list_tools()
        assert tools == []


class TestAgentRouting:
    """测试 Agent 的工具路由逻辑"""

    def test_extract_city(self):
        """测试城市名提取"""
        from client.agent import _extract_city

        assert _extract_city("北京天气") == "北京"
        assert _extract_city("上海今天天气") in ["上海今天", "上海"]

    def test_extract_expression(self):
        """测试数学表达式提取"""
        from client.agent import _extract_expression

        result = _extract_expression("计算 2+3")
        assert "2+3" in result

    def test_extract_numbers(self):
        """测试数字列表提取"""
        from client.agent import _extract_numbers

        numbers = _extract_numbers("统计 1, 2, 3, 4, 5")
        assert len(numbers) == 5
        assert 1.0 in numbers
        assert 5.0 in numbers

    def test_extract_url(self):
        """测试 URL 提取"""
        from client.agent import _extract_url

        url = _extract_url("抓取 https://example.com 的内容")
        assert url == "https://example.com"

    def test_extract_url_no_match(self):
        """测试无 URL 时的默认值"""
        from client.agent import _extract_url

        url = _extract_url("没有链接的文本")
        assert url == "https://example.com"

    def test_extract_unit_conversion(self):
        """测试单位转换参数提取"""
        from client.agent import _extract_unit_conversion

        result = _extract_unit_conversion("转换 100 km 到 mile")
        assert result["value"] == 100.0
        assert result["from_unit"] == "km"
        assert result["to_unit"] == "mile"
