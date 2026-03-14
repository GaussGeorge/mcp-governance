"""
MCP 工具单元测试
====================
直接测试各个工具函数的逻辑，不需要启动 MCP 服务器。
"""

import pytest
import json


class TestCalculatorTools:
    """测试计算器工具"""

    @pytest.mark.asyncio
    async def test_basic_arithmetic(self):
        """测试基本算术运算"""
        from server.tools.calculator import calculate

        result = await calculate("2 + 3 * 4")
        assert "14" in result
        assert "✅" in result

    @pytest.mark.asyncio
    async def test_sqrt(self):
        """测试平方根"""
        from server.tools.calculator import calculate

        result = await calculate("sqrt(144)")
        assert "12" in result

    @pytest.mark.asyncio
    async def test_trigonometry(self):
        """测试三角函数"""
        from server.tools.calculator import calculate

        result = await calculate("sin(pi/6)")
        assert "0.5" in result or "1/2" in result

    @pytest.mark.asyncio
    async def test_invalid_expression(self):
        """测试无效表达式"""
        from server.tools.calculator import calculate

        result = await calculate("invalid_expr $$$ ")
        assert "❌" in result

    @pytest.mark.asyncio
    async def test_unit_convert_length(self):
        """测试长度单位转换"""
        from server.tools.calculator import unit_convert

        result = await unit_convert(1.0, "km", "m")
        assert "1000" in result

    @pytest.mark.asyncio
    async def test_unit_convert_temperature(self):
        """测试温度转换"""
        from server.tools.calculator import unit_convert

        result = await unit_convert(100.0, "celsius", "fahrenheit")
        assert "212" in result

    @pytest.mark.asyncio
    async def test_unit_convert_invalid(self):
        """测试无效单位转换"""
        from server.tools.calculator import unit_convert

        result = await unit_convert(1.0, "invalid_unit", "m")
        assert "❌" in result

    @pytest.mark.asyncio
    async def test_statistics_basic(self):
        """测试基本统计"""
        from server.tools.calculator import statistics

        result = await statistics([1, 2, 3, 4, 5])
        assert "均值" in result
        assert "3" in result  # 均值为 3

    @pytest.mark.asyncio
    async def test_statistics_empty(self):
        """测试空列表统计"""
        from server.tools.calculator import statistics

        result = await statistics([])
        assert "❌" in result


class TestTextTools:
    """测试文本处理工具"""

    @pytest.mark.asyncio
    async def test_text_analyze_chinese(self):
        """测试中文文本分析"""
        from server.tools.text_processor import text_analyze

        result = await text_analyze("你好世界，这是一个测试。")
        assert "中文字符数" in result
        assert "句子数" in result

    @pytest.mark.asyncio
    async def test_text_analyze_english(self):
        """测试英文文本分析"""
        from server.tools.text_processor import text_analyze

        result = await text_analyze("Hello World. This is a test.")
        assert "英文单词数" in result

    @pytest.mark.asyncio
    async def test_text_analyze_empty(self):
        """测试空文本"""
        from server.tools.text_processor import text_analyze

        result = await text_analyze("   ")
        assert "❌" in result

    @pytest.mark.asyncio
    async def test_text_transform_uppercase(self):
        """测试大写转换"""
        from server.tools.text_processor import text_transform

        result = await text_transform("hello world", "uppercase")
        assert "HELLO WORLD" in result

    @pytest.mark.asyncio
    async def test_text_transform_reverse(self):
        """测试文本反转"""
        from server.tools.text_processor import text_transform

        result = await text_transform("abc", "reverse")
        assert "cba" in result

    @pytest.mark.asyncio
    async def test_text_transform_camel_case(self):
        """测试驼峰命名"""
        from server.tools.text_processor import text_transform

        result = await text_transform("hello world test", "camel_case")
        assert "helloWorldTest" in result

    @pytest.mark.asyncio
    async def test_text_transform_snake_case(self):
        """测试蛇形命名"""
        from server.tools.text_processor import text_transform

        result = await text_transform("helloWorld", "snake_case")
        assert "hello_world" in result

    @pytest.mark.asyncio
    async def test_text_transform_invalid_op(self):
        """测试无效操作"""
        from server.tools.text_processor import text_transform

        result = await text_transform("test", "invalid")
        assert "❌" in result

    @pytest.mark.asyncio
    async def test_text_encode_base64(self):
        """测试 Base64 编码"""
        from server.tools.text_processor import text_encode

        result = await text_encode("Hello", "base64")
        assert "SGVsbG8=" in result

    @pytest.mark.asyncio
    async def test_text_encode_md5(self):
        """测试 MD5 哈希"""
        from server.tools.text_processor import text_encode

        result = await text_encode("Hello", "md5")
        assert "8b1a9953c4611296a827abf8c47804d7" in result

    @pytest.mark.asyncio
    async def test_text_encode_sha256(self):
        """测试 SHA-256 哈希"""
        from server.tools.text_processor import text_encode

        result = await text_encode("Hello", "sha256")
        # SHA-256 of "Hello"
        assert "185f8db32271fe25f561a6fc938b2e26" in result.lower()

    @pytest.mark.asyncio
    async def test_json_format(self):
        """测试 JSON 格式化"""
        from server.tools.text_processor import json_format

        result = await json_format('{"a":1,"b":2}')
        assert '"a": 1' in result
        assert '"b": 2' in result

    @pytest.mark.asyncio
    async def test_json_format_invalid(self):
        """测试无效 JSON"""
        from server.tools.text_processor import json_format

        result = await json_format("not a json")
        assert "❌" in result

    @pytest.mark.asyncio
    async def test_regex_match(self):
        """测试正则匹配"""
        from server.tools.text_processor import regex_match

        result = await regex_match("hello 123 world 456", r"\d+")
        assert "123" in result
        assert "456" in result
        assert "匹配数: 2" in result

    @pytest.mark.asyncio
    async def test_regex_no_match(self):
        """测试正则无匹配"""
        from server.tools.text_processor import regex_match

        result = await regex_match("hello world", r"\d+")
        assert "未找到" in result


class TestWeatherTools:
    """测试天气工具（使用真实 API，可能受网络影响）"""

    @pytest.mark.asyncio
    async def test_resolve_city_known(self):
        """测试已知城市解析"""
        from server.tools.weather import _resolve_city

        coords = _resolve_city("北京")
        assert coords is not None
        assert abs(coords["lat"] - 39.9042) < 0.01

    @pytest.mark.asyncio
    async def test_resolve_city_unknown(self):
        """测试未知城市解析"""
        from server.tools.weather import _resolve_city

        coords = _resolve_city("不存在的城市xxxyyy")
        assert coords is None

    @pytest.mark.asyncio
    async def test_weather_description(self):
        """测试天气代码转描述"""
        from server.tools.weather import _get_weather_description

        assert "晴" in _get_weather_description(0)
        assert "雷暴" in _get_weather_description(95)

    @pytest.mark.asyncio
    @pytest.mark.skipif(True, reason="需要网络访问，CI 环境中跳过")
    async def test_get_weather_real(self):
        """测试真实天气查询（需要网络）"""
        from server.tools.weather import get_weather

        result = await get_weather("北京")
        assert "温度" in result
        assert "湿度" in result


class TestWebSearchTools:
    """测试网页搜索工具"""

    @pytest.mark.asyncio
    async def test_invalid_url(self):
        """测试无效 URL"""
        from server.tools.web_search import fetch_webpage

        result = await fetch_webpage("not-a-url")
        assert "❌" in result

    @pytest.mark.asyncio
    async def test_ftp_url(self):
        """测试 FTP URL (不支持)"""
        from server.tools.web_search import fetch_webpage

        result = await fetch_webpage("ftp://example.com")
        assert "❌" in result

    @pytest.mark.asyncio
    async def test_url_info_invalid(self):
        """测试无效 URL 信息"""
        from server.tools.web_search import url_info

        result = await url_info("not-a-url")
        assert "❌" in result
