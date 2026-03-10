"""
集成测试
====================
端到端测试：启动服务器 → 客户端连接 → 调用工具 → 验证结果

这些测试需要完整的运行环境，CI 中可能需要跳过。
"""

import asyncio
import pytest
import subprocess
import time
import sys

import httpx

# 服务器进程
SERVER_URL = "http://localhost:18765"
SSE_URL = f"{SERVER_URL}/sse"


@pytest.fixture(scope="module")
async def running_server():
    """
    启动 MCP 服务器作为子进程，测试完成后关闭。
    使用非标准端口避免冲突。
    """
    import os

    env = os.environ.copy()
    env["MCP_SERVER_PORT"] = "18765"
    env["MCP_SERVER_HOST"] = "127.0.0.1"
    env["MCP_TRANSPORT"] = "sse"
    env["MCP_LOG_LEVEL"] = "WARNING"

    proc = subprocess.Popen(
        [sys.executable, "-m", "server.main"],
        env=env,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
    )

    # 等待服务器就绪
    max_retries = 30
    for i in range(max_retries):
        try:
            async with httpx.AsyncClient() as client:
                resp = await client.get(f"{SERVER_URL}/sse", timeout=1.0)
                if resp.status_code in (200, 405):
                    break
        except (httpx.ConnectError, httpx.ReadTimeout, httpx.ConnectTimeout):
            pass
        await asyncio.sleep(0.5)

    yield proc

    # 关闭服务器
    proc.terminate()
    try:
        proc.wait(timeout=5)
    except subprocess.TimeoutExpired:
        proc.kill()


@pytest.mark.skipif(
    True,
    reason="集成测试需要手动启动，CI 中跳过。使用 pytest -k integration --override 运行",
)
class TestIntegration:
    """端到端集成测试"""

    @pytest.mark.asyncio
    async def test_full_workflow(self, running_server):
        """完整工作流测试"""
        from client.main import MCPClient

        client = MCPClient()
        try:
            await client.connect_sse(SSE_URL)

            # 列出工具
            tools = await client.list_tools()
            assert len(tools) > 0
            tool_names = [t["name"] for t in tools]
            assert "get_weather" in tool_names
            assert "calculate" in tool_names

            # 调用计算器
            result = await client.call_tool("calculate", {"expression": "2 + 2"})
            assert "4" in result

            # 调用文本分析
            result = await client.call_tool("text_analyze", {"text": "Hello World"})
            assert "英文单词数" in result

        finally:
            await client.close()

    @pytest.mark.asyncio
    async def test_concurrent_calls(self, running_server):
        """并发调用测试"""
        from client.main import MCPClient

        client = MCPClient()
        try:
            await client.connect_sse(SSE_URL)

            # 并发调用 10 次计算器
            tasks = [
                client.call_tool("calculate", {"expression": f"{i} + {i}"})
                for i in range(10)
            ]
            results = await asyncio.gather(*tasks)

            for i, result in enumerate(results):
                assert str(i * 2) in result

        finally:
            await client.close()

    @pytest.mark.asyncio
    async def test_error_handling(self, running_server):
        """错误处理测试"""
        from client.main import MCPClient

        client = MCPClient()
        try:
            await client.connect_sse(SSE_URL)

            # 调用不存在的工具
            with pytest.raises(Exception):
                await client.call_tool("nonexistent_tool", {})

        finally:
            await client.close()
