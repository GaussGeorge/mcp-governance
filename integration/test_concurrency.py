#!/usr/bin/env python3
"""快速验证 Bridge ConcurrencyLimiter 是否生效"""
import asyncio
import aiohttp
import json
import time


async def test_concurrency():
    url = "http://localhost:9000/mcp"
    payload = json.dumps(
        {
            "jsonrpc": "2.0",
            "id": 1,
            "method": "tools/call",
            "params": {"name": "calculate", "arguments": {"expression": "1+1"}},
        }
    )

    async with aiohttp.ClientSession() as session:

        async def send_one(i):
            start = time.monotonic()
            async with session.post(
                url, data=payload, headers={"Content-Type": "application/json"}
            ) as resp:
                data = await resp.json()
                elapsed = (time.monotonic() - start) * 1000
                has_error = "error" in data
                msg = (
                    data.get("error", {}).get("message", "")[:80] if has_error else "ok"
                )
                return (i, elapsed, has_error, msg)

        # Send 100 concurrent requests
        tasks = [send_one(i) for i in range(100)]
        results = await asyncio.gather(*tasks)

        errors = sum(1 for _, _, e, _ in results if e)
        print(
            f"Total: {len(results)}, Errors(rejected): {errors}, Success: {len(results) - errors}"
        )

        latencies = [e for _, e, _, _ in results]
        print(
            f"Latency: min={min(latencies):.0f}ms, max={max(latencies):.0f}ms, avg={sum(latencies)/len(latencies):.0f}ms"
        )

        if errors:
            print(f"\nRejected requests:")
            for i, elapsed, has_err, msg in sorted(results, key=lambda x: x[1]):
                if has_err:
                    print(f"  [{i}] {elapsed:.0f}ms: {msg}")


if __name__ == "__main__":
    asyncio.run(test_concurrency())
