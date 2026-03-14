"""
天气查询工具
====================
提供全球城市天气查询功能，使用免费的 Open-Meteo API (无需 API Key)。
同时提供天气预报功能。

Open-Meteo API 文档: https://open-meteo.com/en/docs

该工具演示了 MCP 工具如何与外部 API 集成。
"""

import httpx
import logging
from datetime import datetime

logger = logging.getLogger("mcp-server.tools.weather")

# 从 server.app 导入 mcp 实例（避免 __main__ 双重导入问题）
from server.app import mcp as _mcp


# ==================== 城市坐标数据库 ====================
# 常用城市的经纬度映射（用于 Open-Meteo API）
CITY_COORDINATES: dict[str, dict[str, float]] = {
    # 中国城市
    "北京": {"lat": 39.9042, "lon": 116.4074},
    "上海": {"lat": 31.2304, "lon": 121.4737},
    "广州": {"lat": 23.1291, "lon": 113.2644},
    "深圳": {"lat": 22.5431, "lon": 114.0579},
    "杭州": {"lat": 30.2741, "lon": 120.1551},
    "成都": {"lat": 30.5728, "lon": 104.0668},
    "武汉": {"lat": 30.5928, "lon": 114.3055},
    "南京": {"lat": 32.0603, "lon": 118.7969},
    "西安": {"lat": 34.3416, "lon": 108.9398},
    "重庆": {"lat": 29.4316, "lon": 106.9123},
    "香港": {"lat": 22.3193, "lon": 114.1694},
    "台北": {"lat": 25.0330, "lon": 121.5654},
    # 国际城市
    "tokyo": {"lat": 35.6762, "lon": 139.6503},
    "new york": {"lat": 40.7128, "lon": -74.0060},
    "london": {"lat": 51.5074, "lon": -0.1278},
    "paris": {"lat": 48.8566, "lon": 2.3522},
    "sydney": {"lat": -33.8688, "lon": 151.2093},
    "singapore": {"lat": 1.3521, "lon": 103.8198},
    "seoul": {"lat": 37.5665, "lon": 126.9780},
    "berlin": {"lat": 52.5200, "lon": 13.4050},
    "moscow": {"lat": 55.7558, "lon": 37.6173},
    "dubai": {"lat": 25.2048, "lon": 55.2708},
    "los angeles": {"lat": 34.0522, "lon": -118.2437},
    "san francisco": {"lat": 37.7749, "lon": -122.4194},
}

# WMO 天气代码映射
WMO_WEATHER_CODES: dict[int, str] = {
    0: "晴天 ☀️",
    1: "大部分晴 🌤️",
    2: "局部多云 ⛅",
    3: "多云 ☁️",
    45: "雾 🌫️",
    48: "冻雾 🌫️❄️",
    51: "小毛毛雨 🌦️",
    53: "中毛毛雨 🌦️",
    55: "大毛毛雨 🌧️",
    61: "小雨 🌧️",
    63: "中雨 🌧️",
    65: "大雨 🌧️",
    71: "小雪 🌨️",
    73: "中雪 🌨️",
    75: "大雪 ❄️",
    80: "小阵雨 🌦️",
    81: "中阵雨 🌧️",
    82: "大阵雨 ⛈️",
    95: "雷暴 ⛈️",
    96: "雷暴伴小冰雹 ⛈️",
    99: "雷暴伴大冰雹 ⛈️",
}


def _get_weather_description(code: int) -> str:
    """将 WMO 天气代码转换为可读描述"""
    return WMO_WEATHER_CODES.get(code, f"未知天气 (代码: {code})")


def _resolve_city(city: str) -> dict[str, float] | None:
    """解析城市名称为坐标"""
    city_lower = city.lower().strip()
    # 直接匹配
    if city_lower in CITY_COORDINATES:
        return CITY_COORDINATES[city_lower]
    if city in CITY_COORDINATES:
        return CITY_COORDINATES[city]
    # 模糊匹配
    for name, coords in CITY_COORDINATES.items():
        if city_lower in name.lower() or name.lower() in city_lower:
            return coords
    return None


# ==================== MCP 工具注册 ====================
# 使用 FastMCP 的装饰器语法注册工具

mcp = _mcp


@mcp.tool()
async def get_weather(city: str) -> str:
    """
    查询指定城市的当前天气信息。
    返回温度、湿度、风速、天气状况等实时数据。

    Args:
        city: 城市名称，支持中文（如"北京"）和英文（如"tokyo"）
    """
    logger.info(f"查询天气: {city}")

    coords = _resolve_city(city)
    if coords is None:
        # 尝试使用 Open-Meteo Geocoding API 解析未知城市
        coords = await _geocode_city(city)
        if coords is None:
            return (
                f"❌ 无法识别城市「{city}」\n\n"
                f"支持的中文城市: {', '.join(k for k in CITY_COORDINATES if not k.isascii())}\n"
                f"支持的英文城市: {', '.join(k for k in CITY_COORDINATES if k.isascii())}\n\n"
                f"您也可以尝试使用英文城市名。"
            )

    try:
        async with httpx.AsyncClient(timeout=10.0) as client:
            response = await client.get(
                "https://api.open-meteo.com/v1/forecast",
                params={
                    "latitude": coords["lat"],
                    "longitude": coords["lon"],
                    "current": "temperature_2m,relative_humidity_2m,apparent_temperature,"
                    "wind_speed_10m,wind_direction_10m,weather_code,pressure_msl",
                    "timezone": "auto",
                },
            )
            response.raise_for_status()
            data = response.json()

        current = data.get("current", {})
        units = data.get("current_units", {})

        weather_code = current.get("weather_code", -1)
        weather_desc = _get_weather_description(weather_code)

        result = (
            f"🌍 {city} 当前天气\n"
            f"{'=' * 30}\n"
            f"🌡️ 温度: {current.get('temperature_2m', 'N/A')}{units.get('temperature_2m', '°C')}\n"
            f"🤒 体感温度: {current.get('apparent_temperature', 'N/A')}{units.get('apparent_temperature', '°C')}\n"
            f"💧 相对湿度: {current.get('relative_humidity_2m', 'N/A')}{units.get('relative_humidity_2m', '%')}\n"
            f"💨 风速: {current.get('wind_speed_10m', 'N/A')}{units.get('wind_speed_10m', 'km/h')}\n"
            f"🧭 风向: {current.get('wind_direction_10m', 'N/A')}°\n"
            f"🔽 气压: {current.get('pressure_msl', 'N/A')}{units.get('pressure_msl', 'hPa')}\n"
            f"🌤️ 天气: {weather_desc}\n"
            f"{'=' * 30}\n"
            f"📍 坐标: ({coords['lat']}, {coords['lon']})\n"
            f"🕐 查询时间: {datetime.now().strftime('%Y-%m-%d %H:%M:%S')}"
        )
        logger.info(f"天气查询成功: {city}")
        return result

    except httpx.HTTPStatusError as e:
        logger.error(f"天气 API 请求失败: {e}")
        return f"❌ 天气查询失败: HTTP {e.response.status_code}"
    except httpx.RequestError as e:
        logger.error(f"天气 API 网络错误: {e}")
        return f"❌ 网络请求失败: {e}"
    except Exception as e:
        logger.error(f"天气查询异常: {e}")
        return f"❌ 天气查询发生错误: {e}"


@mcp.tool()
async def get_weather_forecast(city: str, days: int = 3) -> str:
    """
    查询指定城市的天气预报。

    Args:
        city: 城市名称
        days: 预报天数 (1-7天，默认3天)
    """
    logger.info(f"查询天气预报: {city}, {days}天")

    days = max(1, min(7, days))
    coords = _resolve_city(city)
    if coords is None:
        coords = await _geocode_city(city)
        if coords is None:
            return f"❌ 无法识别城市「{city}」"

    try:
        async with httpx.AsyncClient(timeout=10.0) as client:
            response = await client.get(
                "https://api.open-meteo.com/v1/forecast",
                params={
                    "latitude": coords["lat"],
                    "longitude": coords["lon"],
                    "daily": "temperature_2m_max,temperature_2m_min,weather_code,"
                    "precipitation_sum,wind_speed_10m_max",
                    "timezone": "auto",
                    "forecast_days": days,
                },
            )
            response.raise_for_status()
            data = response.json()

        daily = data.get("daily", {})
        dates = daily.get("time", [])
        max_temps = daily.get("temperature_2m_max", [])
        min_temps = daily.get("temperature_2m_min", [])
        weather_codes = daily.get("weather_code", [])
        precip = daily.get("precipitation_sum", [])
        wind = daily.get("wind_speed_10m_max", [])

        lines = [f"📅 {city} 未来 {days} 天天气预报", "=" * 40]

        for i in range(len(dates)):
            weather_desc = _get_weather_description(
                weather_codes[i] if i < len(weather_codes) else -1
            )
            lines.append(
                f"\n📆 {dates[i]}\n"
                f"   {weather_desc}\n"
                f"   🌡️ {min_temps[i] if i < len(min_temps) else 'N/A'}°C ~ "
                f"{max_temps[i] if i < len(max_temps) else 'N/A'}°C\n"
                f"   🌧️ 降水: {precip[i] if i < len(precip) else 'N/A'}mm\n"
                f"   💨 最大风速: {wind[i] if i < len(wind) else 'N/A'}km/h"
            )

        lines.append(f"\n{'=' * 40}")
        lines.append(f"🕐 查询时间: {datetime.now().strftime('%Y-%m-%d %H:%M:%S')}")
        return "\n".join(lines)

    except Exception as e:
        logger.error(f"天气预报查询异常: {e}")
        return f"❌ 天气预报查询失败: {e}"


async def _geocode_city(city: str) -> dict[str, float] | None:
    """使用 Open-Meteo Geocoding API 解析城市名称"""
    try:
        async with httpx.AsyncClient(timeout=5.0) as client:
            response = await client.get(
                "https://geocoding-api.open-meteo.com/v1/search",
                params={"name": city, "count": 1, "language": "zh"},
            )
            response.raise_for_status()
            data = response.json()

        results = data.get("results", [])
        if results:
            return {"lat": results[0]["latitude"], "lon": results[0]["longitude"]}
        return None
    except Exception:
        return None
