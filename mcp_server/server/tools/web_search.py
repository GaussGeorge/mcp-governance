"""
网页搜索与抓取工具
====================
提供网页内容抓取、URL 信息提取等功能。
使用 httpx 进行异步 HTTP 请求，BeautifulSoup 进行 HTML 解析。

该工具演示了 MCP 工具如何实现网络 I/O 密集型功能。
"""

import logging
from datetime import datetime
from urllib.parse import urlparse

import httpx
from bs4 import BeautifulSoup

logger = logging.getLogger("mcp-server.tools.web_search")

# 从 server.app 导入 mcp 实例（避免 __main__ 双重导入问题）
from server.app import mcp

# 默认 HTTP 请求头（模拟正常浏览器）
DEFAULT_HEADERS = {
    "User-Agent": (
        "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) "
        "AppleWebKit/537.36 (KHTML, like Gecko) "
        "Chrome/120.0.0.0 Safari/537.36"
    ),
    "Accept": "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8",
    "Accept-Language": "zh-CN,zh;q=0.9,en;q=0.8",
}


@mcp.tool()
async def fetch_webpage(url: str, max_length: int = 5000) -> str:
    """
    抓取网页内容，提取主要文本信息。

    Args:
        url: 网页 URL (必须以 http:// 或 https:// 开头)
        max_length: 返回文本的最大字符数 (默认5000)
    """
    logger.info(f"抓取网页: {url}")

    # 验证 URL
    parsed = urlparse(url)
    if parsed.scheme not in ("http", "https"):
        return f"❌ 无效的 URL: {url}\n💡 URL 必须以 http:// 或 https:// 开头"

    try:
        async with httpx.AsyncClient(
            timeout=15.0,
            follow_redirects=True,
            headers=DEFAULT_HEADERS,
        ) as client:
            response = await client.get(url)
            response.raise_for_status()

        content_type = response.headers.get("content-type", "")

        # 非 HTML 内容
        if "html" not in content_type.lower() and "text" not in content_type.lower():
            return (
                f"📄 URL 响应信息\n"
                f"{'=' * 30}\n"
                f"🔗 URL: {url}\n"
                f"📊 状态码: {response.status_code}\n"
                f"📝 Content-Type: {content_type}\n"
                f"📏 内容大小: {len(response.content)} bytes\n"
                f"ℹ️ 该 URL 返回的不是 HTML 内容，无法提取文本。"
            )

        # 解析 HTML
        soup = BeautifulSoup(response.text, "html.parser")

        # 移除脚本和样式
        for element in soup(["script", "style", "nav", "footer", "header", "aside"]):
            element.decompose()

        # 提取标题
        title = (
            soup.title.string.strip() if soup.title and soup.title.string else "无标题"
        )

        # 提取 meta description
        meta_desc = ""
        meta_tag = soup.find("meta", attrs={"name": "description"})
        if meta_tag and meta_tag.get("content"):
            meta_desc = meta_tag["content"]

        # 提取主要文本
        text = soup.get_text(separator="\n", strip=True)

        # 清理空行
        lines = [line.strip() for line in text.splitlines() if line.strip()]
        clean_text = "\n".join(lines)

        # 截断
        if len(clean_text) > max_length:
            clean_text = clean_text[:max_length] + "\n\n... [内容已截断]"

        return (
            f"🌐 网页内容\n"
            f"{'=' * 40}\n"
            f"🔗 URL: {url}\n"
            f"📰 标题: {title}\n"
            f"📝 描述: {meta_desc[:200] if meta_desc else 'N/A'}\n"
            f"📊 状态码: {response.status_code}\n"
            f"🕐 抓取时间: {datetime.now().strftime('%Y-%m-%d %H:%M:%S')}\n"
            f"{'=' * 40}\n\n"
            f"{clean_text}"
        )

    except httpx.HTTPStatusError as e:
        return f"❌ HTTP 错误: {e.response.status_code} {e.response.reason_phrase}"
    except httpx.TimeoutException:
        return f"❌ 请求超时: {url} (超时限制: 15秒)"
    except httpx.RequestError as e:
        return f"❌ 网络请求失败: {e}"
    except Exception as e:
        logger.error(f"网页抓取异常: {e}")
        return f"❌ 抓取失败: {e}"


@mcp.tool()
async def extract_links(url: str, max_links: int = 50) -> str:
    """
    提取网页中的所有链接。

    Args:
        url: 网页 URL
        max_links: 最大返回链接数 (默认50)
    """
    logger.info(f"提取链接: {url}")

    parsed_base = urlparse(url)
    if parsed_base.scheme not in ("http", "https"):
        return f"❌ 无效的 URL: {url}"

    try:
        async with httpx.AsyncClient(
            timeout=15.0,
            follow_redirects=True,
            headers=DEFAULT_HEADERS,
        ) as client:
            response = await client.get(url)
            response.raise_for_status()

        soup = BeautifulSoup(response.text, "html.parser")
        links = []

        for a_tag in soup.find_all("a", href=True):
            href = a_tag["href"].strip()
            text = a_tag.get_text(strip=True)[:80]

            # 补全相对链接
            if href.startswith("/"):
                href = f"{parsed_base.scheme}://{parsed_base.netloc}{href}"
            elif not href.startswith(
                ("http://", "https://", "mailto:", "tel:", "#", "javascript:")
            ):
                href = f"{parsed_base.scheme}://{parsed_base.netloc}/{href}"

            if href.startswith(("http://", "https://")):
                links.append({"url": href, "text": text or "(无文字)"})

        # 去重
        seen = set()
        unique_links = []
        for link in links:
            if link["url"] not in seen:
                seen.add(link["url"])
                unique_links.append(link)

        lines = [
            f"🔗 链接提取结果",
            f"{'=' * 40}",
            f"📍 源 URL: {url}",
            f"📊 链接总数: {len(unique_links)}",
            f"{'=' * 40}",
        ]

        for i, link in enumerate(unique_links[:max_links]):
            lines.append(f"\n[{i + 1}] {link['text']}")
            lines.append(f"    {link['url']}")

        if len(unique_links) > max_links:
            lines.append(f"\n... 还有 {len(unique_links) - max_links} 个链接未显示")

        return "\n".join(lines)

    except Exception as e:
        logger.error(f"链接提取异常: {e}")
        return f"❌ 链接提取失败: {e}"


@mcp.tool()
async def url_info(url: str) -> str:
    """
    获取 URL 的详细信息（HTTP 头、重定向、SSL 证书等），不下载完整内容。

    Args:
        url: 要检查的 URL
    """
    logger.info(f"检查 URL: {url}")

    parsed = urlparse(url)
    if parsed.scheme not in ("http", "https"):
        return f"❌ 无效的 URL: {url}"

    try:
        async with httpx.AsyncClient(
            timeout=10.0,
            follow_redirects=False,
            headers=DEFAULT_HEADERS,
        ) as client:
            response = await client.head(url)

        headers = dict(response.headers)

        lines = [
            f"🔍 URL 信息",
            f"{'=' * 40}",
            f"🔗 URL: {url}",
            f"📊 状态码: {response.status_code}",
            f"📝 协议: {parsed.scheme.upper()}",
            f"🏠 域名: {parsed.netloc}",
            f"📂 路径: {parsed.path or '/'}",
        ]

        # 重要的 HTTP 头
        important_headers = [
            "content-type",
            "content-length",
            "server",
            "last-modified",
            "cache-control",
            "x-powered-by",
            "location",
        ]

        lines.append(f"\n📋 HTTP 响应头:")
        for h in important_headers:
            if h in headers:
                lines.append(f"   {h}: {headers[h]}")

        # 重定向
        if 300 <= response.status_code < 400:
            location = headers.get("location", "未知")
            lines.append(f"\n🔀 重定向目标: {location}")

        lines.append(f"\n🕐 检查时间: {datetime.now().strftime('%Y-%m-%d %H:%M:%S')}")
        return "\n".join(lines)

    except Exception as e:
        return f"❌ URL 检查失败: {e}"
