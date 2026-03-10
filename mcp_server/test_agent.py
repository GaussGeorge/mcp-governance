#!/usr/bin/env python3
"""Agent 端到端测试"""
import asyncio, subprocess, sys, os, time, socket

PYTHON = sys.executable
PORT = 18777
PROJECT_DIR = os.path.dirname(os.path.abspath(__file__))


async def test_agent():
    sys.path.insert(0, PROJECT_DIR)
    from client.main import MCPClient
    from client.agent import SimpleAgent

    client = MCPClient()
    await client.connect_sse(f"http://127.0.0.1:{PORT}/sse")
    agent = SimpleAgent(client)

    tests = [
        ("北京天气", "get_weather"),
        ("计算 sqrt(144)", "calculate"),
        ("转换 100km 到 mile", "unit_convert"),
        ("统计 10, 20, 30, 40, 50", "statistics"),
        ("用base64编码 Hello World", "text_encode"),
    ]

    passed = 0
    for query, expected_tool in tests:
        print(f"--- Agent: '{query}' ---")
        result = await agent.process(query)
        if expected_tool in result:
            print(f"  ✅ 使用了 [{expected_tool}]")
            passed += 1
        else:
            print(f"  ❌ 未使用 [{expected_tool}]")
            print(f"  结果: {result[:200]}")
        print()

    await client.close()
    return passed, len(tests)


def main():
    env = os.environ.copy()
    env.update(
        {
            "MCP_SERVER_PORT": str(PORT),
            "MCP_SERVER_HOST": "127.0.0.1",
            "MCP_TRANSPORT": "sse",
            "MCP_LOG_LEVEL": "WARNING",
        }
    )

    proc = subprocess.Popen(
        [PYTHON, "-m", "server.main"],
        cwd=PROJECT_DIR,
        env=env,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
    )

    for _ in range(30):
        try:
            s = socket.create_connection(("127.0.0.1", PORT), timeout=0.5)
            s.close()
            break
        except:
            time.sleep(0.5)

    print("🤖 Agent 端到端测试\n")
    try:
        passed, total = asyncio.run(test_agent())
        print(f"{'='*50}")
        print(f"{'🎉' if passed == total else '⚠️'} Agent: {passed}/{total} passed")
    except Exception as e:
        import traceback

        traceback.print_exc()
    finally:
        proc.terminate()
        proc.wait(timeout=5)


if __name__ == "__main__":
    main()
