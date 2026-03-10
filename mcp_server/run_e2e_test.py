#!/usr/bin/env python3
"""
端到端测试脚本
启动 MCP Server 子进程 → 等待就绪 → 客户端连接并调用工具 → 关闭
"""

import asyncio
import subprocess
import sys
import os
import time
import signal

PYTHON = sys.executable
PROJECT_DIR = os.path.dirname(os.path.abspath(__file__))
SERVER_PORT = 18888  # 使用非标准端口避免冲突


async def run_client_tests():
    """运行客户端测试"""
    sys.path.insert(0, PROJECT_DIR)
    os.chdir(PROJECT_DIR)

    # 需要设置环境变量让 server 用正确端口
    from client.main import MCPClient

    client = MCPClient()
    passed = 0
    failed = 0

    try:
        print("🔌 连接 SSE 服务器...")
        await client.connect_sse(f"http://127.0.0.1:{SERVER_PORT}/sse")
        print("✅ SSE 连接成功\n")

        # === 测试 1: 列出工具 ===
        print("--- 测试 1: 列出工具 ---")
        tools = await client.list_tools()
        print(f"  📋 发现 {len(tools)} 个工具:")
        for t in tools:
            print(f"    🔧 {t['name']}")
        assert len(tools) >= 10, f"期望至少 10 个工具, 实际 {len(tools)}"
        passed += 1
        print("  ✅ PASS\n")

        # === 测试 2: 计算器 ===
        print("--- 测试 2: calculate ---")
        result = await client.call_tool("calculate", {"expression": "2 + 3 * 4"})
        print(f"  {result[:100]}")
        assert "14" in result, "计算结果应包含 14"
        passed += 1
        print("  ✅ PASS\n")

        # === 测试 3: 平方根 ===
        print("--- 测试 3: sqrt ---")
        result = await client.call_tool("calculate", {"expression": "sqrt(144)"})
        print(f"  {result[:100]}")
        assert "12" in result, "sqrt(144) 应为 12"
        passed += 1
        print("  ✅ PASS\n")

        # === 测试 4: 单位转换 ===
        print("--- 测试 4: unit_convert ---")
        result = await client.call_tool(
            "unit_convert", {"value": 100, "from_unit": "km", "to_unit": "mile"}
        )
        print(f"  {result[:100]}")
        assert "62" in result, "100km ≈ 62 miles"
        passed += 1
        print("  ✅ PASS\n")

        # === 测试 5: 统计 ===
        print("--- 测试 5: statistics ---")
        result = await client.call_tool("statistics", {"numbers": [10, 20, 30, 40, 50]})
        print(f"  {result[:150]}")
        assert "30" in result, "均值应为 30"
        passed += 1
        print("  ✅ PASS\n")

        # === 测试 6: 文本分析 ===
        print("--- 测试 6: text_analyze ---")
        result = await client.call_tool(
            "text_analyze", {"text": "Hello MCP! 你好世界！这是测试。"}
        )
        print(f"  {result[:200]}")
        assert "中文字符" in result
        passed += 1
        print("  ✅ PASS\n")

        # === 测试 7: 文本转换 ===
        print("--- 测试 7: text_transform ---")
        result = await client.call_tool(
            "text_transform", {"text": "hello world", "operation": "uppercase"}
        )
        print(f"  {result[:100]}")
        assert "HELLO WORLD" in result
        passed += 1
        print("  ✅ PASS\n")

        # === 测试 8: Base64 编码 ===
        print("--- 测试 8: text_encode ---")
        result = await client.call_tool(
            "text_encode", {"text": "Hello", "encoding": "base64"}
        )
        print(f"  {result[:100]}")
        assert "SGVsbG8=" in result
        passed += 1
        print("  ✅ PASS\n")

        # === 测试 9: JSON 格式化 ===
        print("--- 测试 9: json_format ---")
        result = await client.call_tool("json_format", {"json_text": '{"a":1,"b":2}'})
        print(f"  {result[:100]}")
        assert '"a": 1' in result
        passed += 1
        print("  ✅ PASS\n")

        # === 测试 10: 正则匹配 ===
        print("--- 测试 10: regex_match ---")
        result = await client.call_tool(
            "regex_match", {"text": "hello 123 world 456", "pattern": "\\d+"}
        )
        print(f"  {result[:150]}")
        assert "123" in result and "456" in result
        passed += 1
        print("  ✅ PASS\n")

        # === 测试 11: 资源列表 ===
        print("--- 测试 11: resources ---")
        resources = await client.list_resources()
        print(f"  📦 发现 {len(resources)} 个资源:")
        for r in resources:
            print(f"    📄 {r['uri']}")
            content = await client.read_resource(str(r["uri"]))
            print(f"       {content[:80]}")
        assert len(resources) >= 1
        passed += 1
        print("  ✅ PASS\n")

        # === 测试 12: 提示模板 ===
        print("--- 测试 12: prompts ---")
        prompts = await client.list_prompts()
        print(f"  📝 发现 {len(prompts)} 个提示模板:")
        for p in prompts:
            print(f"    📌 {p['name']}")
        assert len(prompts) >= 1
        passed += 1
        print("  ✅ PASS\n")

    except Exception as e:
        import traceback

        print(f"  ❌ FAIL: {e}")
        traceback.print_exc()
        failed += 1
    finally:
        await client.close()

    return passed, failed


def main():
    print("=" * 60)
    print("🧪 MCP Server 端到端测试")
    print("=" * 60)

    # 启动服务器子进程
    env = os.environ.copy()
    env["MCP_SERVER_PORT"] = str(SERVER_PORT)
    env["MCP_SERVER_HOST"] = "127.0.0.1"
    env["MCP_TRANSPORT"] = "sse"
    env["MCP_LOG_LEVEL"] = "WARNING"
    env["PYTHONPATH"] = PROJECT_DIR

    print(f"\n🚀 启动 MCP Server (port={SERVER_PORT})...")
    server_proc = subprocess.Popen(
        [PYTHON, "-m", "server.main"],
        cwd=PROJECT_DIR,
        env=env,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
    )

    # 等待服务器就绪（用 socket 探测端口，避免 SSE 流式响应阻塞）
    import socket

    ready = False
    for i in range(30):
        time.sleep(0.5)
        # 检查进程是否还活着
        if server_proc.poll() is not None:
            stderr = server_proc.stderr.read().decode() if server_proc.stderr else ""
            print(f"❌ 服务器进程已退出 (code={server_proc.returncode})")
            print(f"Server stderr:\n{stderr[:1000]}")
            sys.exit(1)
        # TCP 端口探测
        try:
            sock = socket.create_connection(("127.0.0.1", SERVER_PORT), timeout=1)
            sock.close()
            ready = True
            break
        except (ConnectionRefusedError, OSError):
            pass

    if not ready:
        print("❌ 服务器启动超时!")
        server_proc.kill()
        sys.exit(1)

    print("✅ 服务器就绪\n")

    try:
        # 运行客户端测试
        passed, failed = asyncio.run(run_client_tests())

        print("=" * 60)
        if failed == 0:
            print(f"🎉 全部通过! {passed} passed, {failed} failed")
        else:
            print(f"⚠️ 部分失败: {passed} passed, {failed} failed")
        print("=" * 60)

    finally:
        # 关闭服务器
        print("\n🛑 关闭服务器...")
        server_proc.terminate()
        try:
            server_proc.wait(timeout=5)
        except subprocess.TimeoutExpired:
            server_proc.kill()
        print("✅ 服务器已关闭")

    sys.exit(1 if failed > 0 else 0)


if __name__ == "__main__":
    main()
