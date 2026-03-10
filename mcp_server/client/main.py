"""
MCP 客户端
====================
基于官方 MCP Python SDK 的客户端实现。
支持通过 SSE 和 stdio 两种传输方式连接 MCP 服务器。

使用方式:
  # 连接 SSE 服务器
  python -m client.main --transport sse --url http://localhost:8000/sse

  # 连接 stdio 服务器
  python -m client.main --transport stdio --command "python -m server.main"

  # 交互模式
  python -m client.main --interactive
"""

import asyncio
import argparse
import json
import logging
import sys
from contextlib import AsyncExitStack

from mcp import ClientSession
from mcp.client.sse import sse_client
from mcp.client.stdio import StdioServerParameters, stdio_client

logging.basicConfig(
    level=logging.INFO,
    format="%(asctime)s [%(levelname)s] %(name)s: %(message)s",
)
logger = logging.getLogger("mcp-client")


class MCPClient:
    """
    MCP 客户端封装类

    提供统一接口连接 MCP 服务器（SSE 或 stdio），
    并调用服务器提供的工具、资源和提示模板。
    """

    def __init__(self):
        self.session: ClientSession | None = None
        self._exit_stack = AsyncExitStack()
        self._tools: list[dict] = []

    async def connect_sse(self, url: str) -> None:
        """
        通过 SSE (Server-Sent Events) 连接 MCP 服务器

        Args:
            url: SSE 端点 URL，例如 http://localhost:8000/sse
        """
        logger.info(f"正在通过 SSE 连接: {url}")

        sse_transport = await self._exit_stack.enter_async_context(sse_client(url))
        read_stream, write_stream = sse_transport
        self.session = await self._exit_stack.enter_async_context(
            ClientSession(read_stream, write_stream)
        )
        await self.session.initialize()
        logger.info("✅ SSE 连接成功")
        await self._load_tools()

    async def connect_stdio(self, command: str, args: list[str] | None = None) -> None:
        """
        通过 stdio 连接 MCP 服务器

        Args:
            command: 服务器启动命令
            args: 命令参数
        """
        logger.info(f"正在通过 stdio 连接: {command}")

        server_params = StdioServerParameters(
            command=command,
            args=args or [],
        )
        stdio_transport = await self._exit_stack.enter_async_context(
            stdio_client(server_params)
        )
        read_stream, write_stream = stdio_transport
        self.session = await self._exit_stack.enter_async_context(
            ClientSession(read_stream, write_stream)
        )
        await self.session.initialize()
        logger.info("✅ stdio 连接成功")
        await self._load_tools()

    async def _load_tools(self) -> None:
        """加载服务器提供的工具列表"""
        if not self.session:
            raise RuntimeError("未连接到服务器")

        result = await self.session.list_tools()
        self._tools = [
            {
                "name": tool.name,
                "description": tool.description,
                "inputSchema": tool.inputSchema,
            }
            for tool in result.tools
        ]
        logger.info(f"📋 已加载 {len(self._tools)} 个工具")
        for tool in self._tools:
            logger.info(f"   🔧 {tool['name']}: {tool['description'][:60]}")

    async def list_tools(self) -> list[dict]:
        """列出所有可用工具"""
        return self._tools

    async def call_tool(self, tool_name: str, arguments: dict | None = None) -> str:
        """
        调用 MCP 工具

        Args:
            tool_name: 工具名称
            arguments: 工具参数 (字典)

        Returns:
            工具执行结果的文本
        """
        if not self.session:
            raise RuntimeError("未连接到服务器")

        logger.info(
            f"🔧 调用工具: {tool_name}({json.dumps(arguments or {}, ensure_ascii=False)})"
        )

        result = await self.session.call_tool(tool_name, arguments or {})

        # 提取文本内容
        texts = []
        for content in result.content:
            if hasattr(content, "text"):
                texts.append(content.text)
            elif hasattr(content, "data"):
                texts.append(f"[二进制数据: {len(content.data)} bytes]")
            else:
                texts.append(str(content))

        return "\n".join(texts)

    async def list_resources(self) -> list[dict]:
        """列出所有可用资源"""
        if not self.session:
            raise RuntimeError("未连接到服务器")

        result = await self.session.list_resources()
        return [
            {
                "uri": res.uri,
                "name": res.name,
                "description": getattr(res, "description", ""),
            }
            for res in result.resources
        ]

    async def read_resource(self, uri: str) -> str:
        """读取资源内容"""
        if not self.session:
            raise RuntimeError("未连接到服务器")

        result = await self.session.read_resource(uri)
        texts = []
        for content in result.contents:
            if hasattr(content, "text"):
                texts.append(content.text)
        return "\n".join(texts)

    async def list_prompts(self) -> list[dict]:
        """列出所有可用提示模板"""
        if not self.session:
            raise RuntimeError("未连接到服务器")

        result = await self.session.list_prompts()
        return [
            {"name": p.name, "description": getattr(p, "description", "")}
            for p in result.prompts
        ]

    async def close(self) -> None:
        """关闭客户端连接"""
        await self._exit_stack.aclose()
        logger.info("🔌 连接已关闭")


# ==================== 交互式命令行客户端 ====================


async def interactive_loop(client: MCPClient) -> None:
    """交互式命令行循环"""
    print("\n" + "=" * 50)
    print("🤖 MCP 交互式客户端")
    print("=" * 50)
    print("命令:")
    print("  tools          - 列出所有工具")
    print("  call <工具名>  - 调用工具 (会提示输入参数)")
    print("  resources      - 列出资源")
    print("  read <uri>     - 读取资源")
    print("  prompts        - 列出提示模板")
    print("  help           - 显示帮助")
    print("  quit           - 退出")
    print("=" * 50 + "\n")

    while True:
        try:
            user_input = input("MCP> ").strip()
        except (EOFError, KeyboardInterrupt):
            print("\n👋 再见！")
            break

        if not user_input:
            continue

        parts = user_input.split(maxsplit=1)
        cmd = parts[0].lower()

        try:
            if cmd in ("quit", "exit", "q"):
                print("👋 再见！")
                break

            elif cmd == "tools":
                tools = await client.list_tools()
                print(f"\n📋 可用工具 ({len(tools)} 个):")
                for t in tools:
                    print(f"  🔧 {t['name']}")
                    print(f"     {t['description'][:80]}")
                print()

            elif cmd == "call":
                if len(parts) < 2:
                    print("❌ 用法: call <工具名>")
                    continue

                tool_name = parts[1]
                # 交互式输入参数
                print(f"请输入 {tool_name} 的参数 (JSON 格式，直接回车跳过):")
                args_input = input("参数> ").strip()

                arguments = {}
                if args_input:
                    try:
                        arguments = json.loads(args_input)
                    except json.JSONDecodeError:
                        # 尝试解析为简单键值对
                        print("❌ JSON 解析失败，请使用正确的 JSON 格式")
                        continue

                result = await client.call_tool(tool_name, arguments)
                print(f"\n{result}\n")

            elif cmd == "resources":
                resources = await client.list_resources()
                print(f"\n📦 可用资源 ({len(resources)} 个):")
                for r in resources:
                    print(f"  📄 {r['uri']}: {r.get('name', 'N/A')}")
                print()

            elif cmd == "read":
                if len(parts) < 2:
                    print("❌ 用法: read <资源URI>")
                    continue
                content = await client.read_resource(parts[1])
                print(f"\n{content}\n")

            elif cmd == "prompts":
                prompts = await client.list_prompts()
                print(f"\n📝 可用提示模板 ({len(prompts)} 个):")
                for p in prompts:
                    print(f"  📌 {p['name']}: {p.get('description', 'N/A')}")
                print()

            elif cmd == "help":
                print("\n可用命令: tools, call, resources, read, prompts, quit\n")

            else:
                print(f"❌ 未知命令: {cmd}，输入 help 查看帮助")

        except Exception as e:
            print(f"❌ 错误: {e}")


async def run_demo(client: MCPClient) -> None:
    """运行演示：依次调用所有工具"""
    print("\n" + "=" * 50)
    print("🎬 MCP 工具演示")
    print("=" * 50)

    # 列出工具
    tools = await client.list_tools()
    print(f"\n📋 发现 {len(tools)} 个工具:")
    for t in tools:
        print(f"  🔧 {t['name']}")

    # 演示调用
    demos = [
        ("get_weather", {"city": "北京"}),
        ("calculate", {"expression": "sqrt(144) + sin(pi/6)"}),
        ("unit_convert", {"value": 100, "from_unit": "km", "to_unit": "mile"}),
        ("statistics", {"numbers": [23, 45, 67, 12, 89, 34, 56, 78, 90, 11]}),
        (
            "text_analyze",
            {
                "text": "MCP (Model Context Protocol) 是一个开放协议，用于标准化 AI 应用与外部数据源和工具的连接方式。"
            },
        ),
        ("text_encode", {"text": "Hello MCP!", "encoding": "base64"}),
        (
            "json_format",
            {"json_text": '{"name":"MCP","version":"1.0","tools":["weather","calc"]}'},
        ),
    ]

    for tool_name, args in demos:
        try:
            print(f"\n{'─' * 50}")
            print(f"🔧 调用: {tool_name}({json.dumps(args, ensure_ascii=False)})")
            print(f"{'─' * 50}")
            result = await client.call_tool(tool_name, args)
            print(result)
        except Exception as e:
            print(f"❌ 调用 {tool_name} 失败: {e}")

    # 列出资源
    try:
        print(f"\n{'─' * 50}")
        print("📦 资源列表:")
        resources = await client.list_resources()
        for r in resources:
            print(f"  📄 {r['uri']}")
            content = await client.read_resource(str(r["uri"]))
            print(f"     {content[:100]}")
    except Exception as e:
        print(f"❌ 资源列表获取失败: {e}")

    print(f"\n{'=' * 50}")
    print("✅ 演示完成!")


async def main():
    parser = argparse.ArgumentParser(description="MCP 客户端")
    parser.add_argument(
        "--transport",
        choices=["sse", "stdio"],
        default="sse",
        help="传输模式 (默认: sse)",
    )
    parser.add_argument(
        "--url",
        default="http://localhost:8000/sse",
        help="SSE 服务器 URL (默认: http://localhost:8000/sse)",
    )
    parser.add_argument(
        "--command",
        default="python",
        help="stdio 服务器启动命令",
    )
    parser.add_argument(
        "--args",
        nargs="*",
        default=["-m", "server.main"],
        help="stdio 服务器命令参数",
    )
    parser.add_argument(
        "--interactive",
        "-i",
        action="store_true",
        help="启动交互模式",
    )
    parser.add_argument(
        "--demo",
        "-d",
        action="store_true",
        help="运行演示模式",
    )

    args = parser.parse_args()
    client = MCPClient()

    try:
        # 连接服务器
        if args.transport == "sse":
            await client.connect_sse(args.url)
        else:
            await client.connect_stdio(args.command, args.args)

        # 运行模式
        if args.interactive:
            await interactive_loop(client)
        elif args.demo:
            await run_demo(client)
        else:
            # 默认运行演示
            await run_demo(client)

    finally:
        await client.close()


if __name__ == "__main__":
    asyncio.run(main())
