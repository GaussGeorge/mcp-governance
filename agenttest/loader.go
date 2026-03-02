// loader.go
// Agent 场景负载生成器
// 支持三种负载模式：突发（Burst）、泊松（Poisson）、正弦（Sine）
// 按照泊松分布生成任务，每个任务由随机选择的 Agent 顺序执行多步骤
package main

import (
	"context"
	"fmt"
	"math"
	"math/rand"
	"sync"
	"sync/atomic"
	"time"
)

// AgentLoadGenerator Agent 场景的负载生成器
type AgentLoadGenerator struct {
	cfg       *AgentTestConfig
	serverURL string
	agents    []*Agent
	executor  *TaskExecutor
	resultsCh chan StepResult
	results   []StepResult
	resultsMu sync.Mutex
	taskIDGen int64
	rng       *rand.Rand
}

// NewAgentLoadGenerator 创建 Agent 负载生成器
func NewAgentLoadGenerator(cfg *AgentTestConfig, serverURL string) *AgentLoadGenerator {
	resultsCh := make(chan StepResult, 50000)

	// 初始化 Agents
	agents := make([]*Agent, cfg.NumAgents)
	for i := 0; i < cfg.NumAgents; i++ {
		budget := pickWeighted(cfg.Budgets, cfg.BudgetWeights, rand.Float64())
		agents[i] = NewAgent(fmt.Sprintf("agent-%d", i), budget)
	}

	gen := &AgentLoadGenerator{
		cfg:       cfg,
		serverURL: serverURL,
		agents:    agents,
		resultsCh: resultsCh,
		results:   make([]StepResult, 0, 50000),
		rng:       rand.New(rand.NewSource(cfg.RandomSeed)),
	}

	gen.executor = NewTaskExecutor(cfg, serverURL, resultsCh)

	return gen
}

// Run 运行负载测试，返回所有步骤结果
func (g *AgentLoadGenerator) Run() []StepResult {
	fmt.Printf("[Agent负载生成器] 开始运行，策略=%s，模式=%s，目标=%s\n",
		g.cfg.Strategy, g.cfg.LoadPattern, g.serverURL)
	fmt.Printf("[Agent负载生成器] %d 个Agent，预算分布=%v，权重=%v\n",
		g.cfg.NumAgents, g.cfg.Budgets, g.cfg.BudgetWeights)

	// 启动结果收集协程
	done := make(chan struct{})
	go func() {
		for r := range g.resultsCh {
			g.resultsMu.Lock()
			g.results = append(g.results, r)
			g.resultsMu.Unlock()
		}
		close(done)
	}()

	switch g.cfg.LoadPattern {
	case PatternBurst:
		g.runBurstPattern()
	case PatternPoisson:
		g.runPoissonPattern()
	case PatternSine:
		g.runSinePattern()
	default:
		fmt.Printf("[Agent负载生成器] 未知负载模式: %s，使用突发模式\n", g.cfg.LoadPattern)
		g.runBurstPattern()
	}

	// 等待所有结果收集完成
	close(g.resultsCh)
	<-done

	g.resultsMu.Lock()
	defer g.resultsMu.Unlock()
	return g.results
}

// ==================== 突发负载 ====================

func (g *AgentLoadGenerator) runBurstPattern() {
	for _, phase := range g.cfg.BurstPhases {
		fmt.Printf("[阶段: %s] 任务率=%.1f tasks/s, 持续=%s\n", phase.Name, phase.TaskRate, phase.Duration)

		ctx, cancel := context.WithTimeout(context.Background(), phase.Duration)
		var wg sync.WaitGroup

		// 按泊松过程生成任务
		go func(phaseName string, taskRate float64) {
			for {
				select {
				case <-ctx.Done():
					return
				default:
					if taskRate <= 0 {
						time.Sleep(100 * time.Millisecond)
						continue
					}
					// 泊松间隔
					interval := time.Duration(rand.ExpFloat64()/taskRate*1e9) * time.Nanosecond
					time.Sleep(interval)

					select {
					case <-ctx.Done():
						return
					default:
					}

					// 生成并执行任务
					task := g.generateTask()
					agent := g.pickAgent()
					agent.ResetBudget() // 每个新任务重置预算

					wg.Add(1)
					go func(a *Agent, t *Task, ph string) {
						defer wg.Done()
						g.executor.ExecuteTask(a, t, ph)
					}(agent, task, phaseName)
				}
			}
		}(phase.Name, phase.TaskRate)

		<-ctx.Done()
		cancel()
		wg.Wait()

		fmt.Printf("[阶段: %s] 完成\n", phase.Name)
	}
}

// ==================== 泊松负载 ====================

func (g *AgentLoadGenerator) runPoissonPattern() {
	taskRate := g.cfg.PoissonTaskRate
	duration := g.cfg.Duration

	fmt.Printf("[泊松负载] 平均任务率=%.1f tasks/s, 总时长=%s\n", taskRate, duration)

	ctx, cancel := context.WithTimeout(context.Background(), duration)
	defer cancel()

	var wg sync.WaitGroup

	for {
		select {
		case <-ctx.Done():
			wg.Wait()
			return
		default:
			interval := time.Duration(rand.ExpFloat64()/taskRate*1e9) * time.Nanosecond
			time.Sleep(interval)

			select {
			case <-ctx.Done():
				wg.Wait()
				return
			default:
			}

			task := g.generateTask()
			agent := g.pickAgent()
			agent.ResetBudget()

			wg.Add(1)
			go func(a *Agent, t *Task) {
				defer wg.Done()
				g.executor.ExecuteTask(a, t, "poisson")
			}(agent, task)
		}
	}
}

// ==================== 正弦波负载 ====================

func (g *AgentLoadGenerator) runSinePattern() {
	duration := g.cfg.Duration
	baseRate := g.cfg.SineBaseRate
	amplitude := g.cfg.SineAmplitude
	period := g.cfg.SinePeriod

	fmt.Printf("[正弦负载] 基础率=%.1f, 振幅=%.1f, 周期=%s, 总时长=%s\n",
		baseRate, amplitude, period, duration)

	ctx, cancel := context.WithTimeout(context.Background(), duration)
	defer cancel()

	var wg sync.WaitGroup
	startTime := time.Now()

	for {
		select {
		case <-ctx.Done():
			wg.Wait()
			return
		default:
			// 根据正弦函数计算当前任务到达率
			elapsed := time.Since(startTime).Seconds()
			currentRate := baseRate + amplitude*math.Sin(2*math.Pi*elapsed/period.Seconds())
			if currentRate < 0.5 {
				currentRate = 0.5
			}

			interval := time.Duration(rand.ExpFloat64()/currentRate*1e9) * time.Nanosecond
			time.Sleep(interval)

			select {
			case <-ctx.Done():
				wg.Wait()
				return
			default:
			}

			task := g.generateTask()
			agent := g.pickAgent()
			agent.ResetBudget()

			phaseName := fmt.Sprintf("sine_r%.0f", currentRate)
			wg.Add(1)
			go func(a *Agent, t *Task, ph string) {
				defer wg.Done()
				g.executor.ExecuteTask(a, t, ph)
			}(agent, task, phaseName)
		}
	}
}

// ==================== 任务/Agent 生成辅助 ====================

// generateTask 生成一个随机任务
func (g *AgentLoadGenerator) generateTask() *Task {
	taskID := fmt.Sprintf("task-%d", atomic.AddInt64(&g.taskIDGen, 1))

	// 随机步骤数
	stepCount := g.cfg.MinSteps + rand.Intn(g.cfg.MaxSteps-g.cfg.MinSteps+1)

	// 随机选择步骤的工具类型
	steps := make([]TaskStep, stepCount)
	toolNames := make([]string, len(g.cfg.ToolTypes))
	for i, t := range g.cfg.ToolTypes {
		toolNames[i] = t.Name
	}
	for i := 0; i < stepCount; i++ {
		steps[i] = TaskStep{
			ToolType: toolNames[rand.Intn(len(toolNames))],
		}
	}

	// 随机优先级
	priorityIdx := pickWeightedIdx(g.cfg.PriorityWeights, rand.Float64())
	priority := g.cfg.Priorities[priorityIdx]

	return &Task{
		ID:       taskID,
		Priority: priority,
		Steps:    steps,
	}
}

// pickAgent 随机选择一个 Agent
func (g *AgentLoadGenerator) pickAgent() *Agent {
	return g.agents[rand.Intn(len(g.agents))]
}

// pickWeighted 按权重选择一个整数值
func pickWeighted(values []int, weights []float64, r float64) int {
	if len(weights) == 0 || len(weights) != len(values) {
		return values[rand.Intn(len(values))]
	}
	cumulative := 0.0
	for i, w := range weights {
		cumulative += w
		if r < cumulative {
			return values[i]
		}
	}
	return values[len(values)-1]
}

// pickWeightedIdx 按权重返回索引
func pickWeightedIdx(weights []float64, r float64) int {
	cumulative := 0.0
	for i, w := range weights {
		cumulative += w
		if r < cumulative {
			return i
		}
	}
	return len(weights) - 1
}
