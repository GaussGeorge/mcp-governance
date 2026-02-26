"""
run_and_plot.py
主入口 - 运行 agent_test 测试并生成全部可视化图表

使用方式:
  # 模式 1: 运行 Go 测试 + 生成图表
  python run_and_plot.py

  # 模式 2: 使用样例数据预览图表 (无需运行 Go 测试)
  python run_and_plot.py --sample

  # 模式 3: 从已有的测试输出文件解析 + 生成图表
  python run_and_plot.py --input test_output.txt

  # 模式 4: 只运行某类测试
  python run_and_plot.py --filter "TestCompetition"
  python run_and_plot.py --filter "TestBudget"
  python run_and_plot.py --filter "TestChain"

  # 指定输出目录
  python run_and_plot.py --output ./my_charts

  # 保存原始测试输出
  python run_and_plot.py --save-output
"""

import argparse
import os
import sys
import time

# 确保当前目录在 path 中
sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))

from parse_test_output import ParsedOutput, parse_go_test_output, run_go_tests_and_parse, get_sample_data
from plot_competition import plot_all_competition
from plot_budget import plot_all_budget
from plot_reasoning_chain import plot_all_chain
from plot_dashboard import plot_dashboard


def main():
    parser = argparse.ArgumentParser(
        description='Agent 测试数据可视化工具 - 解析 agent_test/ 测试输出并生成 Matplotlib 图表',
        formatter_class=argparse.RawDescriptionHelpFormatter,
        epilog="""
示例:
  python run_and_plot.py --sample              # 使用样例数据预览
  python run_and_plot.py                       # 运行测试并生成图表
  python run_and_plot.py --input output.txt    # 从文件解析
  python run_and_plot.py --filter TestBudget   # 只测试预算场景
        """)

    parser.add_argument('--sample', action='store_true',
                        help='使用内置样例数据生成图表 (无需运行 Go 测试)')
    parser.add_argument('--input', '-i', type=str, default=None,
                        help='从指定文件读取 go test 输出 (而非实时运行)')
    parser.add_argument('--output', '-o', type=str, default='output',
                        help='图表输出目录 (默认: output/)')
    parser.add_argument('--filter', '-f', type=str, default='',
                        help='测试过滤器, 传递给 go test -run (如 TestCompetition)')
    parser.add_argument('--timeout', '-t', type=str, default='2m',
                        help='Go 测试超时时间 (默认: 2m)')
    parser.add_argument('--test-dir', type=str, default='./agent_test/',
                        help='Go 测试目录 (默认: ./agent_test/)')
    parser.add_argument('--save-output', action='store_true',
                        help='保存原始测试输出到文件')
    parser.add_argument('--no-dashboard', action='store_true',
                        help='不生成综合仪表板')
    parser.add_argument('--only', type=str, choices=['competition', 'budget', 'chain', 'dashboard'],
                        help='只生成指定类别的图表')

    args = parser.parse_args()

    print("=" * 60)
    print("  Agent 测试数据可视化工具")
    print("  MCP Governance - agent_test 测试结果图表生成器")
    print("=" * 60)

    start_time = time.time()

    # ---- 步骤 1: 获取数据 ----
    data: ParsedOutput

    if args.sample:
        print("\n[1/2] 使用内置样例数据...")
        data = get_sample_data()
        print(f"  加载了 {len(data.all_tests)} 个测试的样例数据")

    elif args.input:
        print(f"\n[1/2] 从文件读取测试输出: {args.input}")
        if not os.path.exists(args.input):
            print(f"  错误: 文件不存在 - {args.input}")
            sys.exit(1)
        with open(args.input, 'r', encoding='utf-8') as f:
            text = f.read()
        data = parse_go_test_output(text)
        print(f"  解析了 {len(data.all_tests)} 个测试结果")

    else:
        print(f"\n[1/2] 运行 Go 测试: {args.test_dir}")
        if args.filter:
            print(f"  测试过滤器: {args.filter}")
        print(f"  超时: {args.timeout}")

        try:
            import subprocess
            # 先检查目录
            cmd = ["go", "test", args.test_dir, "-v", "-timeout", args.timeout, "-count=1"]
            if args.filter:
                cmd += ["-run", args.filter]

            print(f"  执行: {' '.join(cmd)}")
            proc = subprocess.run(cmd, capture_output=True, text=True, timeout=300)

            output = proc.stdout + "\n" + proc.stderr
            print(f"  退出码: {proc.returncode}")

            if args.save_output:
                output_file = os.path.join(args.output, 'test_output.txt')
                os.makedirs(args.output, exist_ok=True)
                with open(output_file, 'w', encoding='utf-8') as f:
                    f.write(output)
                print(f"  测试输出已保存到: {output_file}")

            data = parse_go_test_output(output)
            print(f"  解析了 {len(data.all_tests)} 个测试结果")

        except FileNotFoundError:
            print("  错误: 未找到 go 命令, 请确保 Go 已安装并在 PATH 中")
            print("  提示: 可使用 --sample 模式预览图表, 或用 --input 读取已有输出")
            sys.exit(1)
        except subprocess.TimeoutExpired:
            print("  错误: 测试超时, 请增加 --timeout 参数")
            sys.exit(1)

    # ---- 步骤 2: 生成图表 ----
    print(f"\n[2/2] 生成图表到: {args.output}/")
    os.makedirs(args.output, exist_ok=True)

    if args.only:
        if args.only == 'competition':
            plot_all_competition(data, args.output)
        elif args.only == 'budget':
            plot_all_budget(data, args.output)
        elif args.only == 'chain':
            plot_all_chain(data, args.output)
        elif args.only == 'dashboard':
            plot_dashboard(data, args.output)
    else:
        plot_all_competition(data, args.output)
        plot_all_budget(data, args.output)
        plot_all_chain(data, args.output)
        if not args.no_dashboard:
            plot_dashboard(data, args.output)

    # ---- 完成 ----
    elapsed = time.time() - start_time
    chart_count = len([f for f in os.listdir(args.output) if f.endswith('.png')])
    print(f"\n{'=' * 60}")
    print(f"  完成! 共生成 {chart_count} 张图表")
    print(f"  输出目录: {os.path.abspath(args.output)}")
    print(f"  耗时: {elapsed:.1f}s")
    print(f"{'=' * 60}")


if __name__ == '__main__':
    main()
