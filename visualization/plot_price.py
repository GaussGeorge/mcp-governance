import pandas as pd
import matplotlib.pyplot as plt
import numpy as np

# 1. 加载 CSV 数据集
# 请将文件路径替换为你本地真实的 CSV 路径
file_path = r"C:\Users\86158\Desktop\go_workspace\ra-annotion-demo\loadtest\output\test_price_dynamics\rajomon_step_run1_20260228_113249.csv"
df = pd.read_csv(file_path)


# 全局字体设置：优先使用中文字体
plt.rcParams.update({
    "font.sans-serif": ["Microsoft YaHei", "SimHei", "DejaVu Sans"],
    "axes.unicode_minus": False,
    "figure.dpi": 150,
    "savefig.dpi": 150,
})

# 确保时间戳有序并计算相对时间（秒）
df = df.sort_values("timestamp")
start_time = df['timestamp'].min()
df['rel_time_sec'] = (df['timestamp'] - start_time) / 1000.0

# 2. 将数据按 1 秒为窗口进行聚合 (Binning)
df['time_bin'] = np.floor(df['rel_time_sec']).astype(int)
agg_df = df.groupby('time_bin').agg(
    request_count=('request_id', 'count'),  # 每秒请求数 (RPS)
    max_price=('price', 'max'),             # 每秒最高价格
    phase=('phase', lambda x: x.iloc[0])    # 记录当前所处阶段
).reset_index()

# 3. 开始绘图
fig, ax1 = plt.subplots(figsize=(12, 6))

# 为不同的压测阶段设置背景颜色，增强可读性
phase_colors = {
    'warmup': '#E8F5E9',   # 浅绿
    'low': '#FFF3E0',      # 浅橙
    'overload': '#FFCDD2', # 浅红 (拥塞期)
    'recovery': '#E3F2FD'  # 浅蓝 (恢复期)
}

phases = df['phase'].unique()
for p in phases:
    p_data = df[df['phase'] == p]
    start_x = p_data['rel_time_sec'].min()
    end_x = p_data['rel_time_sec'].max()
    color = phase_colors.get(p, '#FFFFFF')
    ax1.axvspan(start_x, end_x, facecolor=color, alpha=0.5, label=f'阶段: {p}')

# 清理图例中的重复项
handles, labels = ax1.get_legend_handles_labels()
by_label = dict(zip(labels, handles))
ax1.legend(by_label.values(), by_label.keys(), loc='upper left')

# 绘制左 Y 轴：并发请求量 (柱状图)
color_req = '#4A90E2'
ax1.set_xlabel('相对时间 (秒)', fontsize=12)
ax1.set_ylabel('每秒请求数 (RPS)', color=color_req, fontsize=12, fontweight='bold')
ax1.bar(agg_df['time_bin'], agg_df['request_count'], color=color_req, alpha=0.7, label='RPS (流量负载)')
ax1.tick_params(axis='y', labelcolor=color_req)

# 绘制右 Y 轴：实时价格 (折线图)
ax2 = ax1.twinx()
color_price = '#D0021B'
ax2.set_ylabel('RAJOMON 实时动态价格', color=color_price, fontsize=12, fontweight='bold')
ax2.plot(agg_df['time_bin'], agg_df['max_price'], color=color_price, linewidth=3, marker='.', label='最高价格/秒')
ax2.tick_params(axis='y', labelcolor=color_price)

# 添加标题与布局优化
plt.title('RAJOMON 模型验证：动态价格曲线 vs 并发流量洪峰', fontsize=15, fontweight='bold')
fig.tight_layout()

# 保存并展示图表
plt.savefig('rajomon_price_dynamics.png', dpi=300)
print("图表已生成: rajomon_price_dynamics.png")