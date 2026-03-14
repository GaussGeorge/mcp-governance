"""
计算器工具
====================
提供数学表达式计算、单位转换、统计计算等功能。
使用 SymPy 进行安全的数学表达式求值（避免 eval 的安全风险）。

该工具演示了 MCP 工具如何实现纯计算类功能。
"""

import math
import logging
from typing import Annotated

import sympy
from sympy.parsing.sympy_parser import (
    parse_expr,
    standard_transformations,
    implicit_multiplication_application,
    convert_xor,
)

logger = logging.getLogger("mcp-server.tools.calculator")

# 从 server.app 导入 mcp 实例（避免 __main__ 双重导入问题）
from server.app import mcp

# SymPy 解析转换规则（更宽松的语法支持）
TRANSFORMATIONS = standard_transformations + (
    implicit_multiplication_application,
    convert_xor,
)


@mcp.tool()
async def calculate(expression: str) -> str:
    """
    计算数学表达式，支持基本运算、三角函数、对数、阶乘等。
    使用安全的符号计算引擎（SymPy），不会执行任意代码。

    Args:
        expression: 数学表达式，例如:
            - "2 + 3 * 4"
            - "sqrt(144)"
            - "sin(pi/6)"
            - "log(100, 10)"
            - "factorial(10)"
            - "integrate(x**2, x)"  (符号积分)
            - "diff(sin(x), x)"    (符号微分)
    """
    logger.info(f"计算表达式: {expression}")

    try:
        # 使用 SymPy 安全解析
        x, y, z = sympy.symbols("x y z")
        local_dict = {
            "x": x,
            "y": y,
            "z": z,
            "pi": sympy.pi,
            "e": sympy.E,
            "inf": sympy.oo,
            "sqrt": sympy.sqrt,
            "abs": sympy.Abs,
            "sin": sympy.sin,
            "cos": sympy.cos,
            "tan": sympy.tan,
            "log": sympy.log,
            "ln": sympy.ln,
            "exp": sympy.exp,
            "factorial": sympy.factorial,
            "integrate": sympy.integrate,
            "diff": sympy.diff,
            "limit": sympy.limit,
            "sum": sympy.summation,
        }

        parsed = parse_expr(
            expression,
            local_dict=local_dict,
            transformations=TRANSFORMATIONS,
        )

        # 尝试数值化
        result = parsed.evalf()

        # 如果结果是纯数值，格式化输出
        if result.is_number:
            # 如果是整数，去掉小数点
            float_val = float(result)
            if float_val == int(float_val) and abs(float_val) < 1e15:
                formatted = str(int(float_val))
            else:
                formatted = f"{float_val:.10g}"

            return (
                f"🧮 计算结果\n"
                f"{'=' * 30}\n"
                f"📝 表达式: {expression}\n"
                f"✅ 结果: {formatted}\n"
                f"🔣 精确值: {parsed}"
            )
        else:
            # 符号结果（如积分、微分结果）
            return (
                f"🧮 符号计算结果\n"
                f"{'=' * 30}\n"
                f"📝 表达式: {expression}\n"
                f"🔣 结果: {parsed}\n"
                f"📊 简化: {sympy.simplify(parsed)}"
            )

    except (sympy.SympifyError, SyntaxError, TypeError) as e:
        return f"❌ 表达式解析错误: {e}\n💡 请检查表达式语法是否正确"
    except Exception as e:
        logger.error(f"计算异常: {e}")
        return f"❌ 计算错误: {e}"


@mcp.tool()
async def unit_convert(
    value: float,
    from_unit: str,
    to_unit: str,
) -> str:
    """
    单位转换工具，支持温度、长度、重量、面积、体积等常用单位。

    Args:
        value: 数值
        from_unit: 原始单位 (如 "km", "celsius", "kg", "m2")
        to_unit: 目标单位 (如 "mile", "fahrenheit", "lb", "ft2")
    """
    logger.info(f"单位转换: {value} {from_unit} → {to_unit}")

    # 温度转换特殊处理
    temp_result = _convert_temperature(value, from_unit, to_unit)
    if temp_result is not None:
        return (
            f"🔄 单位转换\n"
            f"{'=' * 30}\n"
            f"📥 {value} {from_unit}\n"
            f"📤 {temp_result:.4g} {to_unit}"
        )

    # 其他单位转换（通过中间单位 SI）
    conversions: dict[str, dict[str, float]] = {
        # 长度 (基准: 米)
        "m": {"m": 1},
        "km": {"m": 1000},
        "cm": {"m": 0.01},
        "mm": {"m": 0.001},
        "mile": {"m": 1609.344},
        "yard": {"m": 0.9144},
        "foot": {"m": 0.3048},
        "ft": {"m": 0.3048},
        "inch": {"m": 0.0254},
        "in": {"m": 0.0254},
        "nm": {"m": 1852},  # 海里
        # 重量 (基准: 千克)
        "kg": {"kg": 1},
        "g": {"kg": 0.001},
        "mg": {"kg": 0.000001},
        "lb": {"kg": 0.453592},
        "oz": {"kg": 0.0283495},
        "ton": {"kg": 1000},
        # 面积 (基准: 平方米)
        "m2": {"m2": 1},
        "km2": {"m2": 1e6},
        "cm2": {"m2": 1e-4},
        "ft2": {"m2": 0.092903},
        "acre": {"m2": 4046.86},
        "hectare": {"m2": 10000},
        # 体积 (基准: 升)
        "l": {"l": 1},
        "ml": {"l": 0.001},
        "gallon": {"l": 3.78541},
        "cup": {"l": 0.236588},
        "m3": {"l": 1000},
        # 速度 (基准: m/s)
        "m/s": {"m/s": 1},
        "km/h": {"m/s": 1 / 3.6},
        "mph": {"m/s": 0.44704},
        "knot": {"m/s": 0.514444},
    }

    from_key = from_unit.lower().strip()
    to_key = to_unit.lower().strip()

    if from_key not in conversions or to_key not in conversions:
        supported = ", ".join(sorted(conversions.keys()))
        return f"❌ 不支持的单位\n💡 支持的单位: {supported}"

    # 找到共同的基准单位
    from_bases = conversions[from_key]
    to_bases = conversions[to_key]

    common_base = None
    for base in from_bases:
        if base in to_bases:
            common_base = base
            break

    if common_base is None:
        return f"❌ 无法在 {from_unit} 和 {to_unit} 之间转换（不同量纲）"

    result = value * from_bases[common_base] / to_bases[common_base]

    return (
        f"🔄 单位转换\n"
        f"{'=' * 30}\n"
        f"📥 {value} {from_unit}\n"
        f"📤 {result:.6g} {to_unit}"
    )


def _convert_temperature(value: float, from_unit: str, to_unit: str) -> float | None:
    """温度转换"""
    f = from_unit.lower().strip()
    t = to_unit.lower().strip()

    temp_aliases = {
        "c": "celsius",
        "celsius": "celsius",
        "摄氏": "celsius",
        "f": "fahrenheit",
        "fahrenheit": "fahrenheit",
        "华氏": "fahrenheit",
        "k": "kelvin",
        "kelvin": "kelvin",
        "开尔文": "kelvin",
    }

    f = temp_aliases.get(f)
    t = temp_aliases.get(t)

    if f is None or t is None:
        return None

    # 先转为摄氏
    if f == "celsius":
        c = value
    elif f == "fahrenheit":
        c = (value - 32) * 5 / 9
    elif f == "kelvin":
        c = value - 273.15
    else:
        return None

    # 再转为目标
    if t == "celsius":
        return c
    elif t == "fahrenheit":
        return c * 9 / 5 + 32
    elif t == "kelvin":
        return c + 273.15
    return None


@mcp.tool()
async def statistics(numbers: list[float]) -> str:
    """
    对一组数字进行统计分析，计算均值、中位数、标准差、方差等。

    Args:
        numbers: 数字列表，例如 [1, 2, 3, 4, 5]
    """
    logger.info(f"统计分析: {len(numbers)} 个数字")

    if not numbers:
        return "❌ 请提供至少一个数字"

    n = len(numbers)
    sorted_nums = sorted(numbers)
    mean = sum(numbers) / n
    variance = sum((x - mean) ** 2 for x in numbers) / n
    std_dev = math.sqrt(variance)
    median = (
        sorted_nums[n // 2]
        if n % 2 == 1
        else (sorted_nums[n // 2 - 1] + sorted_nums[n // 2]) / 2
    )

    return (
        f"📊 统计分析结果\n"
        f"{'=' * 30}\n"
        f"📏 样本数: {n}\n"
        f"📈 总和: {sum(numbers):.6g}\n"
        f"📊 均值: {mean:.6g}\n"
        f"📐 中位数: {median:.6g}\n"
        f"📉 最小值: {min(numbers):.6g}\n"
        f"📈 最大值: {max(numbers):.6g}\n"
        f"📏 极差: {max(numbers) - min(numbers):.6g}\n"
        f"📊 方差: {variance:.6g}\n"
        f"📐 标准差: {std_dev:.6g}"
    )
