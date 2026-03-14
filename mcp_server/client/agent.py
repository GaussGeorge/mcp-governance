"""
简单 Agent 示例
====================
演示如何将 MCP 客户端集成到一个简易 AI Agent 流程中。
Agent 接收用户自然语言输入，自动选择合适的工具并调用。

这只是一个规则匹配的演示 Agent，实际生产环境中应接入 LLM (如 GPT-4, Claude)
来实现智能的工具选择。

使用方式:
  python -m client.agent
"""

import asyncio
import re
import logging
import json

from client.main import MCPClient

logger = logging.getLogger("mcp-agent")
logging.basicConfig(level=logging.INFO)


class SimpleAgent:
    """
    简易 MCP Agent

    接收自然语言查询，通过关键词匹配选择合适的 MCP 工具进行调用。
    在真实场景中，工具选择应由 LLM 完成。
    """

    def __init__(self, client: MCPClient):
        self.client = client

    # 工具路由规则（关键词 → 工具名 + 参数提取）
    TOOL_ROUTES = [
        {
            "keywords": ["天气", "weather", "温度", "气温"],
            "tool": "get_weather",
            "extract_args": lambda q: {"city": _extract_city(q)},
        },
        {
            "keywords": ["预报", "forecast", "未来几天"],
            "tool": "get_weather_forecast",
            "extract_args": lambda q: {
                "city": _extract_city(q),
                "days": _extract_number(q, default=3),
            },
        },
        {
            "keywords": [
                "计算",
                "calculate",
                "等于",
                "=",
                "加",
                "减",
                "乘",
                "除",
                "sqrt",
                "sin",
                "cos",
            ],
            "tool": "calculate",
            "extract_args": lambda q: {"expression": _extract_expression(q)},
        },
        {
            "keywords": ["转换", "convert", "单位"],
            "tool": "unit_convert",
            "extract_args": lambda q: _extract_unit_conversion(q),
        },
        {
            "keywords": ["统计", "statistics", "平均", "标准差"],
            "tool": "statistics",
            "extract_args": lambda q: {"numbers": _extract_numbers(q)},
        },
        {
            "keywords": ["分析文本", "text_analyze", "字数"],
            "tool": "text_analyze",
            "extract_args": lambda q: {"text": q},
        },
        {
            "keywords": ["网页", "抓取", "fetch", "http://", "https://"],
            "tool": "fetch_webpage",
            "extract_args": lambda q: {"url": _extract_url(q)},
        },
        {
            "keywords": ["编码", "encode", "base64", "md5", "sha"],
            "tool": "text_encode",
            "extract_args": lambda q: _extract_encode_args(q),
        },
    ]

    async def process(self, query: str) -> str:
        """
        处理用户查询

        Args:
            query: 用户自然语言输入

        Returns:
            处理结果文本
        """
        logger.info(f"🤖 Agent 收到查询: {query}")

        # 匹配工具
        for route in self.TOOL_ROUTES:
            if any(kw in query.lower() for kw in route["keywords"]):
                tool_name = route["tool"]
                try:
                    arguments = route["extract_args"](query)
                    logger.info(f"🎯 匹配工具: {tool_name}, 参数: {arguments}")
                    result = await self.client.call_tool(tool_name, arguments)
                    return f"🤖 Agent 使用了工具 [{tool_name}]\n\n{result}"
                except Exception as e:
                    return f"❌ Agent 调用工具 [{tool_name}] 失败: {e}"

        return (
            "🤖 抱歉，我无法理解您的请求。\n\n"
            "我可以帮您:\n"
            "  🌤️ 查询天气 (例如: '北京天气')\n"
            "  🧮 数学计算 (例如: '计算 sqrt(144)')\n"
            "  🔄 单位转换 (例如: '转换 100km 到 mile')\n"
            "  📊 统计分析 (例如: '统计 1,2,3,4,5')\n"
            "  🌐 抓取网页 (例如: '抓取 https://example.com')\n"
            "  🔐 文本编码 (例如: '用base64编码 Hello')\n"
        )


# ==================== 参数提取辅助函数 ====================


def _extract_city(query: str) -> str:
    """从查询中提取城市名"""
    # 移除常见关键词
    cleaned = query
    for word in [
        "天气",
        "预报",
        "温度",
        "气温",
        "查询",
        "的",
        "今天",
        "明天",
        "weather",
        "forecast",
    ]:
        cleaned = cleaned.replace(word, "")
    city = cleaned.strip()
    return city if city else "北京"


def _extract_expression(query: str) -> str:
    """从查询中提取数学表达式"""
    # 尝试提取引号中的表达式
    match = re.search(r'["\'](.+?)["\']', query)
    if match:
        return match.group(1)
    # 移除"计算"等关键词
    for word in ["计算", "calculate", "求", "等于多少", "是多少"]:
        query = query.replace(word, "")
    return query.strip()


def _extract_number(query: str, default: int = 3) -> int:
    """从查询中提取数字"""
    match = re.search(r"(\d+)", query)
    return int(match.group(1)) if match else default


def _extract_numbers(query: str) -> list[float]:
    """从查询中提取数字列表"""
    numbers = re.findall(r"[-+]?\d*\.?\d+", query)
    return [float(n) for n in numbers] if numbers else [1, 2, 3, 4, 5]


def _extract_url(query: str) -> str:
    """从查询中提取 URL"""
    match = re.search(r"(https?://\S+)", query)
    return match.group(1) if match else "https://example.com"


def _extract_unit_conversion(query: str) -> dict:
    """从查询中提取单位转换参数"""
    # 尝试匹配 "100 km to mile" 或 "100km转换为mile"
    match = re.search(
        r"([\d.]+)\s*(\w+)\s*(?:to|到|转换为?|→)\s*(\w+)", query, re.IGNORECASE
    )
    if match:
        return {
            "value": float(match.group(1)),
            "from_unit": match.group(2),
            "to_unit": match.group(3),
        }
    return {"value": 1, "from_unit": "km", "to_unit": "mile"}


def _extract_encode_args(query: str) -> dict:
    """从查询中提取编码参数"""
    encoding = "base64"
    for enc in ["md5", "sha256", "sha512", "base64"]:
        if enc in query.lower():
            encoding = enc
            break

    # 提取要编码的文本
    text = query
    for word in ["编码", "encode", "base64", "md5", "sha256", "sha512", "用"]:
        text = text.replace(word, "")
    text = text.strip()

    return {"text": text or "Hello MCP!", "encoding": encoding}


# ==================== 主入口 ====================


async def main():
    print("\n" + "=" * 50)
    print("🤖 MCP 简易 Agent")
    print("=" * 50)
    print("输入自然语言查询，Agent 会自动选择工具处理。")
    print("输入 'quit' 退出。")
    print("=" * 50 + "\n")

    client = MCPClient()

    try:
        await client.connect_sse("http://localhost:8000/sse")
        agent = SimpleAgent(client)

        while True:
            try:
                query = input("🧑 您: ").strip()
            except (EOFError, KeyboardInterrupt):
                print("\n👋 再见！")
                break

            if not query:
                continue
            if query.lower() in ("quit", "exit", "q"):
                print("👋 再见！")
                break

            result = await agent.process(query)
            print(f"\n{result}\n")

    finally:
        await client.close()


if __name__ == "__main__":
    asyncio.run(main())
