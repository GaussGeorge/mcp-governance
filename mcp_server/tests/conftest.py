"""
Pytest 配置和共享 Fixtures
"""

import asyncio
import pytest


@pytest.fixture(scope="session")
def event_loop():
    """创建全局事件循环"""
    loop = asyncio.new_event_loop()
    yield loop
    loop.close()
