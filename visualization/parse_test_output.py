"""
parse_test_output.py
解析 `go test ./agent_test/ -v` 的标准输出，提取结构化指标数据。

支持的测试输出格式 (通过正则匹配 t.Logf 输出的行):
  - Agent 指标行: [agent-name] 调用=N 成功=N 拒绝=N ...
  - 摘要行: 总请求: N, 成功: N, 拒绝: N
  - 阶段行: 阶段N: 价格=N 调用=N 成功=N ...
  - 策略效率行: [策略名] 成功=N 消耗=N 效率=N
  - Jain 公平性行: Jain 公平性指数: N.NNNN
  - 链完成行: 完成=N/N 链完成=true/false
  - 批量实验行: 链完成率: N.N% (N/N)
  等
"""

import re
import subprocess
import sys
from dataclasses import dataclass, field
from typing import Optional


# ==================== 数据结构 ====================

@dataclass
class AgentResult:
    """单个 Agent 的执行结果"""
    name: str = ""
    total_calls: int = 0
    success_calls: int = 0
    rejected_calls: int = 0
    retried_calls: int = 0
    budget_spent: int = 0
    chain_completed: bool = False
    steps_completed: int = 0
    initial_budget: int = 0
    budget_left: int = 0
    efficiency: float = 0.0  # 成功/千令牌


@dataclass
class TestResult:
    """单个测试用例的解析结果"""
    test_name: str = ""
    agents: dict = field(default_factory=dict)  # name -> AgentResult
    total_requests: int = 0
    total_success: int = 0
    total_rejected: int = 0
    jain_fairness: float = 0.0
    reject_rate: float = 0.0
    success_rate: float = 0.0
    # 阶段数据 (用于 Escalation / PriceErosion / Lifecycle 等)
    phases: list = field(default_factory=list)  # [{phase, price, calls, success, rejected, budget_left, ...}]
    # 策略对比数据
    strategies: list = field(default_factory=list)  # [{name, success, spent, efficiency}]
    # 链数据
    chain_completion_rate: float = 0.0
    chain_completed_count: int = 0
    chain_total_count: int = 0
    avg_steps: float = 0.0
    avg_budget_spent: float = 0.0
    # 预算轨迹
    budget_trajectory: list = field(default_factory=list)
    # 原始日志
    raw_lines: list = field(default_factory=list)


@dataclass
class ParsedOutput:
    """整体解析结果"""
    competition: dict = field(default_factory=dict)   # test_name -> TestResult
    budget: dict = field(default_factory=dict)         # test_name -> TestResult
    chain: dict = field(default_factory=dict)          # test_name -> TestResult
    all_tests: dict = field(default_factory=dict)      # test_name -> TestResult


# ==================== 正则表达式 ====================

# Agent 指标行: [agent-1] 调用=40 成功=35 拒绝=5 重试=2 预算消耗=800 链完成=true 步骤=3
RE_AGENT_METRICS = re.compile(
    r'\[([^\]]+)\]\s+调用=(\d+)\s+成功=(\d+)\s+拒绝=(\d+)\s+重试=(\d+)\s+预算消耗=(\d+)\s+链完成=(\w+)\s+步骤=(\d+)'
)

# 摘要行: 总请求: 200, 成功: 150, 拒绝: 50
RE_SUMMARY = re.compile(r'总请求:\s*(\d+),\s*成功:\s*(\d+),\s*拒绝:\s*(\d+)')

# 成功率行: 成功率: 75.0%
RE_SUCCESS_RATE = re.compile(r'成功率:\s*([\d.]+)%')

# Jain 公平性: Jain 公平性指数: 0.9521
RE_JAIN = re.compile(r'Jain\s+公平性指数:\s*([\d.]+)')

# 拒绝率行: 拒绝率: 25.0%  or  拒绝率=25.0%
RE_REJECT_RATE = re.compile(r'拒绝率[=:]\s*([\d.]+)%')

# 阶段行: 阶段1: 价格=10 调用=10 成功=10 拒绝=0 剩余预算=2700
RE_PHASE = re.compile(
    r'阶段\s*(\d+):\s+(?:价格=(\d+)\s+)?(?:预算\s+(\d+)→(\d+)\s+\(Δ=(\d+)\),\s+)?'
    r'(?:调用=(\d+)\s+)?(?:成功=(\d+)(?:/(\d+))?\s*)?(?:拒绝=(\d+)\s*)?(?:剩余预算=(-?\d+))?'
)

# 策略效率行: [Fixed] 成功=30 消耗=1200 效率=25.00成功/千令牌
RE_STRATEGY = re.compile(
    r'\[([^\]]+)\]\s+成功=(\d+)\s+消耗=(\d+)\s+效率=([\d.]+)'
)

# 链完成行: 完成=3/5 链完成=true
RE_CHAIN = re.compile(
    r'完成=(\d+)/(\d+)\s+链完成=(\w+)'
)

# 链完成率行: 链完成率: 3/5 (60%)
RE_CHAIN_RATE = re.compile(r'链完成率:\s*(\d+)/(\d+)')

# Agent 数竞争行: Agent数=10: 总请求=200 成功=150 拒绝=50 拒绝率=25.0%
RE_ESCALATION = re.compile(
    r'Agent数=(\d+):\s+总请求=(\d+)\s+成功=(\d+)\s+拒绝=(\d+)\s+拒绝率=([\d.]+)%'
)

# 预算层级行: [低预算] 预算=100 调用=5 成功=3 成功率=60.0% 预算消耗=100
RE_BUDGET_TIER = re.compile(
    r'\[([^\]]+)\]\s+预算=(\d+)\s+调用=(\d+)\s+成功=(\d+)\s+成功率=([\d.]+)%\s+预算消耗=(\d+)'
)

# 突发竞争完成行: 突发竞争完成: 总请求=500, 成功=400, 有效吞吐率=80.0%
RE_BURST = re.compile(
    r'突发竞争完成:\s+总请求=(\d+),\s+成功=(\d+),\s+有效吞吐率=([\d.]+)%'
)

# 高/低预算平均行: 高预算平均成功: 40.0, 低预算平均成功: 20.0
RE_BUDGET_AVG = re.compile(
    r'高预算平均成功:\s*([\d.]+),\s*低预算平均成功:\s*([\d.]+)'
)

# 预算轨迹行: 预算轨迹: [5000 4500 4000 ...]
RE_BUDGET_TRAJECTORY = re.compile(r'预算轨迹:\s*\[([^\]]+)\]')

# 批量实验行
RE_BATCH_COMPLETION = re.compile(r'链完成率:\s*([\d.]+)%\s*\((\d+)/(\d+)\)')
RE_BATCH_AVG_STEPS = re.compile(r'平均步骤完成:\s*([\d.]+)/(\d+)')
RE_BATCH_AVG_SPENT = re.compile(r'平均预算消耗:\s*([\d.]+)/(\d+)')

# 竞争环境下链完成率: 竞争环境下链完成率: 2/3
RE_COMPETITION_CHAIN = re.compile(r'竞争环境下链完成率:\s*(\d+)/(\d+)')

# Alpha / Beta 成功率
RE_GROUP_SUCCESS = re.compile(r'(Alpha|Beta)\s+成功率:\s*([\d.]+)%')

# 零预算/最小预算/超大预算行: 调用=N 成功=N
RE_EDGE = re.compile(r'(零预算|最小预算|超大预算):\s+调用=(\d+)\s+成功=(\d+)')

# 初始预算等信息行
RE_INIT_BUDGET = re.compile(r'初始预算=(\d+)')
RE_FINAL_BUDGET = re.compile(r'最终预算=(-?\d+)')


# ==================== 解析函数 ====================

def parse_go_test_output(text: str) -> ParsedOutput:
    """解析 go test -v 输出文本，返回结构化结果"""
    result = ParsedOutput()
    current_test = None
    current_result = None

    for line in text.splitlines():
        line = line.strip()

        # 检测测试开始
        m = re.match(r'=== RUN\s+(Test\w+)', line)
        if m:
            test_name = m.group(1)
            current_test = test_name
            current_result = TestResult(test_name=test_name)
            result.all_tests[test_name] = current_result
            # 按类别分类
            if 'Competition' in test_name:
                result.competition[test_name] = current_result
            elif 'Budget' in test_name:
                result.budget[test_name] = current_result
            elif 'Chain' in test_name:
                result.chain[test_name] = current_result
            continue

        # 检测子测试
        m = re.match(r'=== RUN\s+(Test\w+)/(.+)', line)
        if m:
            parent_test = m.group(1)
            sub_name = m.group(2)
            # 子测试归属到父测试
            if parent_test in result.all_tests:
                current_test = parent_test
                current_result = result.all_tests[parent_test]
            continue

        if current_result is None:
            continue

        current_result.raw_lines.append(line)

        # ---- 解析各种指标行 ----

        # Agent 指标行
        m = RE_AGENT_METRICS.search(line)
        if m:
            ar = AgentResult(
                name=m.group(1),
                total_calls=int(m.group(2)),
                success_calls=int(m.group(3)),
                rejected_calls=int(m.group(4)),
                retried_calls=int(m.group(5)),
                budget_spent=int(m.group(6)),
                chain_completed=m.group(7).lower() == 'true',
                steps_completed=int(m.group(8)),
            )
            current_result.agents[ar.name] = ar
            continue

        # 摘要行
        m = RE_SUMMARY.search(line)
        if m:
            current_result.total_requests = int(m.group(1))
            current_result.total_success = int(m.group(2))
            current_result.total_rejected = int(m.group(3))
            continue

        # Jain 公平性
        m = RE_JAIN.search(line)
        if m:
            current_result.jain_fairness = float(m.group(1))
            continue

        # 拒绝率
        m = RE_REJECT_RATE.search(line)
        if m:
            current_result.reject_rate = float(m.group(1))
            continue

        # 成功率
        m = RE_SUCCESS_RATE.search(line)
        if m:
            current_result.success_rate = float(m.group(1))
            continue

        # 竞争升级行
        m = RE_ESCALATION.search(line)
        if m:
            current_result.phases.append({
                'agent_count': int(m.group(1)),
                'total_requests': int(m.group(2)),
                'success': int(m.group(3)),
                'rejected': int(m.group(4)),
                'reject_rate': float(m.group(5)),
            })
            continue

        # 阶段行 (价格阶梯/生命周期)
        m = RE_PHASE.search(line)
        if m:
            phase_data = {'phase': int(m.group(1))}
            if m.group(2):
                phase_data['price'] = int(m.group(2))
            if m.group(3) and m.group(4):
                phase_data['budget_before'] = int(m.group(3))
                phase_data['budget_after'] = int(m.group(4))
            if m.group(5):
                phase_data['budget_delta'] = int(m.group(5))
            if m.group(6):
                phase_data['calls'] = int(m.group(6))
            if m.group(7):
                phase_data['success'] = int(m.group(7))
            if m.group(8):
                phase_data['total'] = int(m.group(8))
            if m.group(9):
                phase_data['rejected'] = int(m.group(9))
            if m.group(10):
                phase_data['budget_left'] = int(m.group(10))
            current_result.phases.append(phase_data)
            continue

        # 策略效率行
        m = RE_STRATEGY.search(line)
        if m:
            current_result.strategies.append({
                'name': m.group(1),
                'success': int(m.group(2)),
                'spent': int(m.group(3)),
                'efficiency': float(m.group(4)),
            })
            continue

        # 预算层级行
        m = RE_BUDGET_TIER.search(line)
        if m:
            current_result.strategies.append({
                'name': m.group(1),
                'budget': int(m.group(2)),
                'calls': int(m.group(3)),
                'success': int(m.group(4)),
                'success_rate': float(m.group(5)),
                'spent': int(m.group(6)),
            })
            continue

        # 链完成
        m = RE_CHAIN.search(line)
        if m:
            steps_done = int(m.group(1))
            steps_total = int(m.group(2))
            chain_done = m.group(3).lower() == 'true'
            # 如果还没设置 chain_completion_rate, 设为当前值
            current_result.avg_steps = steps_done
            if chain_done:
                current_result.chain_completed_count += 1
            current_result.chain_total_count = max(current_result.chain_total_count, 1)
            continue

        # 链完成率
        m = RE_CHAIN_RATE.search(line)
        if m:
            current_result.chain_completed_count = int(m.group(1))
            current_result.chain_total_count = int(m.group(2))
            if current_result.chain_total_count > 0:
                current_result.chain_completion_rate = (
                    current_result.chain_completed_count / current_result.chain_total_count * 100
                )
            continue

        # 竞争环境链完成率
        m = RE_COMPETITION_CHAIN.search(line)
        if m:
            current_result.chain_completed_count = int(m.group(1))
            current_result.chain_total_count = int(m.group(2))
            continue

        # 批量实验
        m = RE_BATCH_COMPLETION.search(line)
        if m:
            current_result.chain_completion_rate = float(m.group(1))
            current_result.chain_completed_count = int(m.group(2))
            current_result.chain_total_count = int(m.group(3))
            continue

        m = RE_BATCH_AVG_STEPS.search(line)
        if m:
            current_result.avg_steps = float(m.group(1))
            continue

        m = RE_BATCH_AVG_SPENT.search(line)
        if m:
            current_result.avg_budget_spent = float(m.group(1))
            continue

        # 高低预算平均
        m = RE_BUDGET_AVG.search(line)
        if m:
            current_result.strategies.append({
                'name': '高预算',
                'avg_success': float(m.group(1)),
            })
            current_result.strategies.append({
                'name': '低预算',
                'avg_success': float(m.group(2)),
            })
            continue

        # 突发竞争结果
        m = RE_BURST.search(line)
        if m:
            current_result.total_requests = int(m.group(1))
            current_result.total_success = int(m.group(2))
            current_result.success_rate = float(m.group(3))
            continue

        # Alpha / Beta 成功率
        m = RE_GROUP_SUCCESS.search(line)
        if m:
            current_result.strategies.append({
                'name': m.group(1),
                'success_rate': float(m.group(2)),
            })
            continue

        # 预算轨迹
        m = RE_BUDGET_TRAJECTORY.search(line)
        if m:
            try:
                current_result.budget_trajectory = [int(x) for x in m.group(1).split()]
            except ValueError:
                pass
            continue

        # 边界条件
        m = RE_EDGE.search(line)
        if m:
            current_result.strategies.append({
                'name': m.group(1),
                'calls': int(m.group(2)),
                'success': int(m.group(3)),
            })
            continue

    # 后处理: 计算衍生指标
    for tr in result.all_tests.values():
        if tr.total_requests > 0 and tr.success_rate == 0:
            tr.success_rate = tr.total_success / tr.total_requests * 100
        if tr.total_requests > 0 and tr.reject_rate == 0:
            tr.reject_rate = tr.total_rejected / tr.total_requests * 100

    return result


def run_go_tests_and_parse(test_dir: str = "./agent_test/",
                           timeout: str = "2m",
                           test_filter: str = "") -> ParsedOutput:
    """运行 Go 测试并解析输出"""
    cmd = ["go", "test", test_dir, "-v", "-timeout", timeout, "-count=1"]
    if test_filter:
        cmd += ["-run", test_filter]

    print(f"运行命令: {' '.join(cmd)}")
    proc = subprocess.run(cmd, capture_output=True, text=True, timeout=180)

    output = proc.stdout + "\n" + proc.stderr
    print(f"测试退出码: {proc.returncode}")

    return parse_go_test_output(output)


# ==================== 样例数据生成 ====================

def get_sample_data() -> ParsedOutput:
    """返回样例数据，用于在不运行测试时预览图表"""
    result = ParsedOutput()

    # --- 竞争: 公平性测试 ---
    fair = TestResult(test_name="TestCompetition_EqualBudget_Fairness")
    fair.jain_fairness = 0.95
    for i in range(5):
        ar = AgentResult(name=f"agent-{i+1}", total_calls=40, success_calls=32 + i % 3,
                         rejected_calls=8 - i % 3, budget_spent=700 + i * 20)
        fair.agents[ar.name] = ar
    fair.total_requests = 200
    fair.total_success = 165
    fair.total_rejected = 35
    result.competition[fair.test_name] = fair
    result.all_tests[fair.test_name] = fair

    # --- 竞争: 不等预算 ---
    unequal = TestResult(test_name="TestCompetition_UnequalBudget_HighBudgetAdvantage")
    for name, succ in [("rich-1", 45), ("rich-2", 43), ("poor-1", 25), ("poor-2", 22), ("poor-3", 20)]:
        ar = AgentResult(name=name, success_calls=succ, total_calls=50, rejected_calls=50 - succ)
        unequal.agents[ar.name] = ar
    unequal.strategies = [
        {'name': '高预算', 'avg_success': 44.0},
        {'name': '低预算', 'avg_success': 22.3},
    ]
    result.competition[unequal.test_name] = unequal
    result.all_tests[unequal.test_name] = unequal

    # --- 竞争: 升级 ---
    escalation = TestResult(test_name="TestCompetition_Escalation")
    escalation.phases = [
        {'agent_count': 2, 'total_requests': 40, 'success': 38, 'rejected': 2, 'reject_rate': 5.0},
        {'agent_count': 5, 'total_requests': 100, 'success': 85, 'rejected': 15, 'reject_rate': 15.0},
        {'agent_count': 10, 'total_requests': 200, 'success': 140, 'rejected': 60, 'reject_rate': 30.0},
        {'agent_count': 20, 'total_requests': 400, 'success': 220, 'rejected': 180, 'reject_rate': 45.0},
    ]
    result.competition[escalation.test_name] = escalation
    result.all_tests[escalation.test_name] = escalation

    # --- 竞争: 多工具隔离 ---
    isolation = TestResult(test_name="TestCompetition_MultiTool_ResourceIsolation")
    isolation.strategies = [
        {'name': 'Alpha', 'success_rate': 92.0},
        {'name': 'Beta', 'success_rate': 68.0},
    ]
    result.competition[isolation.test_name] = isolation
    result.all_tests[isolation.test_name] = isolation

    # --- 竞争: 突发 ---
    burst = TestResult(test_name="TestCompetition_BurstArrival")
    burst.total_requests = 500
    burst.total_success = 380
    burst.success_rate = 76.0
    result.competition[burst.test_name] = burst
    result.all_tests[burst.test_name] = burst

    # --- 竞争: 策略对比 ---
    strat_comp = TestResult(test_name="TestCompetition_StrategyComparison")
    strat_comp.strategies = [
        {'name': 'fixed-agent', 'success': 28, 'spent': 1400, 'efficiency': 20.0},
        {'name': 'split-agent', 'success': 25, 'spent': 1200, 'efficiency': 20.83},
        {'name': 'front-agent', 'success': 22, 'spent': 1350, 'efficiency': 16.30},
        {'name': 'adaptive-agent', 'success': 30, 'spent': 1100, 'efficiency': 27.27},
    ]
    result.competition[strat_comp.test_name] = strat_comp
    result.all_tests[strat_comp.test_name] = strat_comp

    # --- 预算: 耗尽 ---
    exhaust = TestResult(test_name="TestBudget_Exhaustion_GracefulStop")
    exhaust.agents['budget-agent'] = AgentResult(
        name='budget-agent', total_calls=8, success_calls=6, rejected_calls=2,
        budget_spent=190, initial_budget=200
    )
    result.budget[exhaust.test_name] = exhaust
    result.all_tests[exhaust.test_name] = exhaust

    # --- 预算: 分层 ---
    tiers = TestResult(test_name="TestBudget_Tiers_CompletionRate")
    tiers.strategies = [
        {'name': '低预算', 'budget': 100, 'calls': 5, 'success': 3, 'success_rate': 60.0, 'spent': 100},
        {'name': '中预算', 'budget': 500, 'calls': 20, 'success': 16, 'success_rate': 80.0, 'spent': 480},
        {'name': '高预算', 'budget': 2000, 'calls': 50, 'success': 48, 'success_rate': 96.0, 'spent': 1440},
    ]
    result.budget[tiers.test_name] = tiers
    result.all_tests[tiers.test_name] = tiers

    # --- 预算: 策略效率基准 ---
    bench = TestResult(test_name="TestBudget_StrategyEfficiency_Benchmark")
    bench.strategies = [
        {'name': 'Fixed', 'success': 25, 'spent': 1000, 'efficiency': 25.0},
        {'name': 'EqualSplit', 'success': 23, 'spent': 920, 'efficiency': 25.0},
        {'name': 'FrontLoaded', 'success': 20, 'spent': 980, 'efficiency': 20.41},
        {'name': 'Adaptive', 'success': 28, 'spent': 900, 'efficiency': 31.11},
    ]
    result.budget[bench.test_name] = bench
    result.all_tests[bench.test_name] = bench

    # --- 预算: 价格阶梯侵蚀 ---
    erosion = TestResult(test_name="TestBudget_PriceIncrease_BudgetErosion")
    erosion.phases = [
        {'phase': 1, 'price': 10, 'calls': 10, 'success': 10, 'rejected': 0, 'budget_left': 2400},
        {'phase': 2, 'price': 30, 'calls': 10, 'success': 10, 'rejected': 0, 'budget_left': 1800},
        {'phase': 3, 'price': 50, 'calls': 10, 'success': 8, 'rejected': 2, 'budget_left': 1320},
        {'phase': 4, 'price': 70, 'calls': 10, 'success': 5, 'rejected': 5, 'budget_left': 1020},
        {'phase': 5, 'price': 100, 'calls': 10, 'success': 2, 'rejected': 8, 'budget_left': 900},
    ]
    result.budget[erosion.test_name] = erosion
    result.all_tests[erosion.test_name] = erosion

    # --- 预算: 自适应 vs 固定 ---
    adapt = TestResult(test_name="TestBudget_Adaptive_vs_Fixed_UnderPriceFluctuation")
    adapt.strategies = [
        {'name': 'Fixed-40', 'success': 30, 'spent': 1600},
        {'name': 'Fixed-80', 'success': 35, 'spent': 1900},
        {'name': 'Adaptive', 'success': 34, 'spent': 1400},
    ]
    result.budget[adapt.test_name] = adapt
    result.all_tests[adapt.test_name] = adapt

    # --- 预算: 生命周期 ---
    lifecycle = TestResult(test_name="TestBudget_Lifecycle_LongRunning")
    lifecycle.budget_trajectory = [5000, 4500, 4000, 3500, 3000, 2500, 2000, 1500, 1000, 500]
    lifecycle.phases = [
        {'phase': i + 1, 'budget_before': 5000 - i * 500, 'budget_after': 4500 - i * 500,
         'budget_delta': 500, 'success': 18, 'total': 20}
        for i in range(10)
    ]
    result.budget[lifecycle.test_name] = lifecycle
    result.all_tests[lifecycle.test_name] = lifecycle

    # --- 预算: 边界 ---
    edge = TestResult(test_name="TestBudget_EdgeCases")
    edge.strategies = [
        {'name': '零预算', 'calls': 0, 'success': 0},
        {'name': '最小预算', 'calls': 1, 'success': 1},
        {'name': '超大预算', 'calls': 20, 'success': 20},
    ]
    result.budget[edge.test_name] = edge
    result.all_tests[edge.test_name] = edge

    # --- 预算: 守恒 ---
    conserve = TestResult(test_name="TestBudget_Conservation_TotalTokens")
    conserve.strategies = [
        {'name': '全局', 'initial': 5000, 'final': 2800, 'spent': 2200},
    ]
    result.budget[conserve.test_name] = conserve
    result.all_tests[conserve.test_name] = conserve

    # --- 推理链: 基础 ---
    linear = TestResult(test_name="TestChain_Linear_Basic")
    linear.avg_steps = 3
    linear.chain_completed_count = 1
    linear.chain_total_count = 1
    linear.chain_completion_rate = 100.0
    result.chain[linear.test_name] = linear
    result.all_tests[linear.test_name] = linear

    # --- 推理链: 依赖断裂 ---
    dep_break = TestResult(test_name="TestChain_DependencyBreak")
    dep_break.avg_steps = 0
    dep_break.chain_completed_count = 0
    dep_break.chain_total_count = 1
    result.chain[dep_break.test_name] = dep_break
    result.all_tests[dep_break.test_name] = dep_break

    # --- 推理链: 重试 ---
    retry = TestResult(test_name="TestChain_WithRetries")
    retry.strategies = [
        {'name': '无重试', 'steps': 2, 'chain_completed': False, 'calls': 3},
        {'name': '有重试', 'steps': 3, 'chain_completed': True, 'calls': 5},
    ]
    result.chain[retry.test_name] = retry
    result.all_tests[retry.test_name] = retry

    # --- 推理链: 策略对比 ---
    chain_strat = TestResult(test_name="TestChain_BudgetStrategy_Comparison")
    chain_strat.strategies = [
        {'name': 'Fixed', 'success': 5, 'spent': 250, 'efficiency': 20.0},
        {'name': 'EqualSplit', 'success': 5, 'spent': 220, 'efficiency': 22.73},
        {'name': 'FrontLoaded', 'success': 4, 'spent': 280, 'efficiency': 14.29},
        {'name': 'Adaptive', 'success': 5, 'spent': 200, 'efficiency': 25.0},
    ]
    result.chain[chain_strat.test_name] = chain_strat
    result.all_tests[chain_strat.test_name] = chain_strat

    # --- 推理链: 批量实验 ---
    batch = TestResult(test_name="TestChain_BatchExperiment")
    batch.chain_completion_rate = 85.0
    batch.chain_completed_count = 17
    batch.chain_total_count = 20
    batch.avg_steps = 4.6
    batch.avg_budget_spent = 320.0
    result.chain[batch.test_name] = batch
    result.all_tests[batch.test_name] = batch

    # --- 推理链: 多 Agent 并行链 ---
    parallel = TestResult(test_name="TestChain_MultiAgent_ParallelChains")
    parallel.chain_completed_count = 4
    parallel.chain_total_count = 5
    parallel.chain_completion_rate = 80.0
    for i in range(5):
        ar = AgentResult(name=f"chain-agent-{i+1}", steps_completed=3 if i < 4 else 2,
                         chain_completed=i < 4, total_calls=3 + (1 if i >= 4 else 0),
                         success_calls=3 if i < 4 else 2, budget_spent=150 + i * 10)
        parallel.agents[ar.name] = ar
    result.chain[parallel.test_name] = parallel
    result.all_tests[parallel.test_name] = parallel

    # --- 推理链: 竞争环境 ---
    under_comp = TestResult(test_name="TestChain_UnderCompetition")
    under_comp.chain_completed_count = 2
    under_comp.chain_total_count = 3
    result.chain[under_comp.test_name] = under_comp
    result.all_tests[under_comp.test_name] = under_comp

    return result


if __name__ == '__main__':
    if len(sys.argv) > 1 and sys.argv[1] == '--sample':
        data = get_sample_data()
        print(f"样例数据: {len(data.all_tests)} 个测试")
        for name, tr in data.all_tests.items():
            print(f"  {name}: agents={len(tr.agents)} strategies={len(tr.strategies)} phases={len(tr.phases)}")
    elif len(sys.argv) > 1:
        # 从文件读取
        with open(sys.argv[1], 'r', encoding='utf-8') as f:
            text = f.read()
        data = parse_go_test_output(text)
        print(f"解析完成: {len(data.all_tests)} 个测试")
        for name in data.all_tests:
            print(f"  {name}")
    else:
        # 运行测试
        data = run_go_tests_and_parse()
        print(f"解析完成: {len(data.all_tests)} 个测试")
