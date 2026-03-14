"""
文本处理工具
====================
提供文本分析、转换、编码等功能。
纯 Python 实现，无需外部 API。

该工具演示了 MCP 工具如何实现本地计算密集型功能。
"""

import hashlib
import base64
import json
import re
import logging
from collections import Counter

logger = logging.getLogger("mcp-server.tools.text")

# 从 server.app 导入 mcp 实例（避免 __main__ 双重导入问题）
from server.app import mcp


@mcp.tool()
async def text_analyze(text: str) -> str:
    """
    分析文本的基本特征：字数、词数、句数、段落数、字符频率等。

    Args:
        text: 要分析的文本内容
    """
    logger.info(f"文本分析: {len(text)} 字符")

    if not text.strip():
        return "❌ 请提供非空文本"

    # 基本统计
    char_count = len(text)
    char_count_no_space = len(text.replace(" ", "").replace("\n", ""))

    # 中文字符计数
    chinese_chars = len(re.findall(r"[\u4e00-\u9fff]", text))

    # 英文单词计数
    words = re.findall(r"[a-zA-Z]+", text)
    word_count = len(words)

    # 句子计数（中英文句号、问号、感叹号）
    sentences = re.split(r"[。！？.!?]+", text)
    sentence_count = len([s for s in sentences if s.strip()])

    # 段落计数
    paragraphs = text.split("\n")
    paragraph_count = len([p for p in paragraphs if p.strip()])

    # 字符频率 Top 10
    filtered = re.sub(r"\s+", "", text)
    freq = Counter(filtered)
    top_chars = freq.most_common(10)
    freq_str = ", ".join(f"'{c}'={n}" for c, n in top_chars)

    # 可读性估算
    avg_sentence_len = char_count_no_space / max(sentence_count, 1)

    return (
        f"📝 文本分析报告\n"
        f"{'=' * 35}\n"
        f"📏 总字符数: {char_count}\n"
        f"📏 非空字符数: {char_count_no_space}\n"
        f"🀄 中文字符数: {chinese_chars}\n"
        f"🔤 英文单词数: {word_count}\n"
        f"📜 句子数: {sentence_count}\n"
        f"📄 段落数: {paragraph_count}\n"
        f"📊 平均句长: {avg_sentence_len:.1f} 字符/句\n"
        f"{'=' * 35}\n"
        f"🔢 字符频率 Top10: {freq_str}"
    )


@mcp.tool()
async def text_transform(
    text: str,
    operation: str,
) -> str:
    """
    文本转换操作。

    Args:
        text: 输入文本
        operation: 转换操作，支持:
            - "uppercase": 全部大写
            - "lowercase": 全部小写
            - "title": 标题格式
            - "reverse": 反转文本
            - "remove_spaces": 去除空格
            - "remove_punctuation": 去除标点符号
            - "trim": 去除首尾空白
            - "slug": URL 友好格式 (用连字符替换空格)
            - "camel_case": 驼峰命名
            - "snake_case": 蛇形命名
    """
    logger.info(f"文本转换: {operation}")

    operations = {
        "uppercase": lambda t: t.upper(),
        "lowercase": lambda t: t.lower(),
        "title": lambda t: t.title(),
        "reverse": lambda t: t[::-1],
        "remove_spaces": lambda t: t.replace(" ", ""),
        "remove_punctuation": lambda t: re.sub(r"[^\w\s]", "", t),
        "trim": lambda t: t.strip(),
        "slug": lambda t: re.sub(r"\s+", "-", t.strip().lower()),
        "camel_case": lambda t: _to_camel_case(t),
        "snake_case": lambda t: _to_snake_case(t),
    }

    op = operation.lower().strip()
    if op not in operations:
        supported = ", ".join(operations.keys())
        return f"❌ 不支持的操作: {operation}\n💡 支持的操作: {supported}"

    result = operations[op](text)

    return (
        f"🔄 文本转换\n"
        f"{'=' * 30}\n"
        f"📝 操作: {operation}\n"
        f"📥 原文: {text[:100]}{'...' if len(text) > 100 else ''}\n"
        f"📤 结果: {result[:500]}{'...' if len(result) > 500 else ''}"
    )


def _to_camel_case(text: str) -> str:
    words = re.split(r"[\s_\-]+", text)
    if not words:
        return text
    return words[0].lower() + "".join(w.capitalize() for w in words[1:])


def _to_snake_case(text: str) -> str:
    # 先在大写字母前插入下划线
    s = re.sub(r"([A-Z])", r"_\1", text)
    # 替换空格和连字符
    s = re.sub(r"[\s\-]+", "_", s)
    # 去除连续下划线和首尾下划线
    s = re.sub(r"_+", "_", s).strip("_").lower()
    return s


@mcp.tool()
async def text_encode(
    text: str,
    encoding: str = "base64",
) -> str:
    """
    文本编码/哈希工具。

    Args:
        text: 输入文本
        encoding: 编码方式，支持:
            - "base64": Base64 编码
            - "base64_decode": Base64 解码
            - "url_encode": URL 编码
            - "md5": MD5 哈希
            - "sha256": SHA-256 哈希
            - "sha512": SHA-512 哈希
    """
    logger.info(f"文本编码: {encoding}")

    try:
        if encoding == "base64":
            result = base64.b64encode(text.encode("utf-8")).decode("ascii")
        elif encoding == "base64_decode":
            result = base64.b64decode(text.encode("ascii")).decode("utf-8")
        elif encoding == "url_encode":
            from urllib.parse import quote

            result = quote(text, safe="")
        elif encoding == "md5":
            result = hashlib.md5(text.encode("utf-8")).hexdigest()
        elif encoding == "sha256":
            result = hashlib.sha256(text.encode("utf-8")).hexdigest()
        elif encoding == "sha512":
            result = hashlib.sha512(text.encode("utf-8")).hexdigest()
        else:
            return f"❌ 不支持的编码: {encoding}\n💡 支持: base64, base64_decode, url_encode, md5, sha256, sha512"

        return (
            f"🔐 文本编码\n"
            f"{'=' * 30}\n"
            f"📝 方式: {encoding}\n"
            f"📥 输入: {text[:80]}{'...' if len(text) > 80 else ''}\n"
            f"📤 输出: {result}"
        )

    except Exception as e:
        return f"❌ 编码失败: {e}"


@mcp.tool()
async def json_format(json_text: str, indent: int = 2) -> str:
    """
    格式化 JSON 文本，使其更易读。

    Args:
        json_text: JSON 字符串
        indent: 缩进空格数 (默认2)
    """
    logger.info("JSON 格式化")

    try:
        parsed = json.loads(json_text)
        formatted = json.dumps(
            parsed, indent=indent, ensure_ascii=False, sort_keys=True
        )
        return f"📋 JSON 格式化结果\n" f"{'=' * 30}\n" f"{formatted}"
    except json.JSONDecodeError as e:
        return f"❌ JSON 解析失败: {e}"


@mcp.tool()
async def regex_match(text: str, pattern: str) -> str:
    """
    使用正则表达式匹配文本。

    Args:
        text: 输入文本
        pattern: 正则表达式模式
    """
    logger.info(f"正则匹配: {pattern}")

    try:
        matches = list(re.finditer(pattern, text))

        if not matches:
            return f"🔍 未找到匹配\n📝 模式: {pattern}"

        lines = [
            f"🔍 正则匹配结果",
            f"{'=' * 30}",
            f"📝 模式: {pattern}",
            f"✅ 匹配数: {len(matches)}",
            "",
        ]

        for i, m in enumerate(matches[:20]):  # 最多显示20个
            lines.append(f'  [{i + 1}] 位置 {m.start()}-{m.end()}: "{m.group()}"')
            if m.groups():
                for j, g in enumerate(m.groups()):
                    lines.append(f'       组{j + 1}: "{g}"')

        if len(matches) > 20:
            lines.append(f"\n  ... 还有 {len(matches) - 20} 个匹配未显示")

        return "\n".join(lines)

    except re.error as e:
        return f"❌ 正则表达式错误: {e}"
