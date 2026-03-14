"""
MCP Server 配置管理
使用 pydantic-settings 从环境变量和 .env 文件加载配置
"""

from pydantic_settings import BaseSettings
from pydantic import Field
from typing import Literal


class ServerConfig(BaseSettings):
    """MCP 服务器配置"""

    # 服务器基本配置
    server_name: str = Field(default="mcp-real-server", alias="MCP_SERVER_NAME")
    host: str = Field(default="0.0.0.0", alias="MCP_SERVER_HOST")
    port: int = Field(default=8000, alias="MCP_SERVER_PORT")
    transport: Literal["stdio", "sse"] = Field(default="sse", alias="MCP_TRANSPORT")
    log_level: str = Field(default="INFO", alias="MCP_LOG_LEVEL")

    # 工具开关
    enable_weather: bool = Field(default=True, alias="ENABLE_WEATHER_TOOL")
    enable_calculator: bool = Field(default=True, alias="ENABLE_CALCULATOR_TOOL")
    enable_text: bool = Field(default=True, alias="ENABLE_TEXT_TOOL")
    enable_web_search: bool = Field(default=True, alias="ENABLE_WEB_SEARCH_TOOL")

    # API Keys (可选)
    weather_api_key: str | None = Field(default=None, alias="WEATHER_API_KEY")

    # 部署配置
    workers: int = Field(default=4, alias="WORKERS")

    model_config = {
        "env_file": ".env",
        "env_file_encoding": "utf-8",
        "populate_by_name": True,
    }


def get_config() -> ServerConfig:
    """获取服务器配置单例"""
    return ServerConfig()
