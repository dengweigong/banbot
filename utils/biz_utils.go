package utils

import (
	"fmt"
	"github.com/sasha-s/go-deadlock"

	"github.com/banbox/banbot/btime"
	"github.com/banbox/banexg"
	"gonum.org/v1/gonum/floats"

	"github.com/banbox/banbot/core"
	"github.com/banbox/banexg/log"
	"github.com/schollz/progressbar/v3"
	"go.uber.org/zap"
)

type PrgCB = func(done int, total int)
type FnTaskPrg = func(task string, rate float64)

type PrgBar struct {
	bar      *progressbar.ProgressBar
	m        *deadlock.Mutex
	title    string
	DoneNum  int
	TotalNum int
	Last     int64 // for outer usage
	PrgCbs   []PrgCB
}

type StagedPrg struct {
	taskMap      map[string]*PrgTask
	tasks        []string
	lock         deadlock.Mutex
	triggers     map[string]FnTaskPrg
	active       int     // index for tasks
	minIntvMS    int64   // default 100
	lastNotifyMS int64   // 13 digit timestamp
	Progress     float64 // [0,1]
}

type PrgTask struct {
	ID       int
	Name     string
	Progress float64
	Weight   float64
}

func NewPrgBar(totalNum int, title string) *PrgBar {
	var pBar *progressbar.ProgressBar
	if totalNum > 0 {
		pBar = progressbar.Default(int64(totalNum), title)
	}
	return &PrgBar{
		bar:      pBar,
		m:        &deadlock.Mutex{},
		TotalNum: totalNum,
		title:    title,
	}
}

func (p *PrgBar) Add(num int) {
	if p.bar == nil {
		return
	}
	p.m.Lock()
	defer p.m.Unlock()
	p.DoneNum += num
	if p.DoneNum > p.TotalNum {
		log.Warn("pBar progress exceed", zap.String("title", p.title), zap.Int("max", p.TotalNum),
			zap.Int("cur", p.DoneNum))
		return
	}
	if len(p.PrgCbs) > 0 {
		for _, cb := range p.PrgCbs {
			cb(p.DoneNum, p.TotalNum)
		}
	}
	err_ := p.bar.Add(num)
	if err_ != nil {
		log.Error("add pBar fail", zap.String("title", p.title), zap.Error(err_))
	}
}

func (p *PrgBar) NewJob(num int) *PrgBarJob {
	return &PrgBarJob{PrgBar: p, jobTotal: num}
}

func (p *PrgBar) Close() {
	if p.bar == nil || p.TotalNum == 0 {
		return
	}
	for _, cb := range p.PrgCbs {
		cb(p.TotalNum, p.TotalNum)
	}
	if p.DoneNum < p.TotalNum {
		p.Add(p.TotalNum - p.DoneNum)
	}
	err := p.bar.Close()
	if err != nil {
		log.Error("close progressBar error", zap.Error(err))
	}
	p.bar = nil
}

type PrgBarJob struct {
	*PrgBar
	jobTotal  int // 当前子任务总数量
	jobDone   int // 当前子任务已完成数量
	jobPrgNum int // 对应StepTotal的进度值
}

func (j *PrgBarJob) Add(num int) {
	if j.PrgBar == nil || num <= 0 || j.jobDone >= j.jobTotal {
		return
	}
	j.jobDone += num
	curProgress := j.jobDone * core.StepTotal / j.jobTotal
	addNum := curProgress - j.jobPrgNum
	if addNum > 0 {
		j.PrgBar.Add(addNum)
		j.jobPrgNum = curProgress
	}
}

func (j *PrgBarJob) Done() {
	if j.jobPrgNum < core.StepTotal {
		j.jobDone = j.jobTotal
		j.Add(core.StepTotal - j.jobPrgNum)
		j.jobPrgNum = core.StepTotal
	}
}

/*
NewStagedPrg 创建多任务复合进度提示器
tasks: 子任务代码列表，按执行顺序，不可重复
weights: 各个子任务权重，>0，内部会自动归一化
*/
func NewStagedPrg(tasks []string, weights []float64) *StagedPrg {
	res := &StagedPrg{
		taskMap:   make(map[string]*PrgTask),
		tasks:     tasks,
		triggers:  make(map[string]FnTaskPrg),
		minIntvMS: 100,
	}
	if len(tasks) != len(weights) {
		panic(fmt.Sprintf("NewStagedPrg: tasks(%v) len differs from weights(%v)", len(tasks), len(weights)))
	}
	totalWei := floats.Sum(weights)
	for i, task := range tasks {
		wei := weights[i]
		if wei <= 0 {
			panic(fmt.Sprintf("NewStagedPrg: weight should > 0, task: %s ", task))
		}
		if _, ok := res.taskMap[task]; ok {
			panic(fmt.Sprintf("NewStagedPrg: duplicate task: %s ", task))
		}
		res.taskMap[task] = &PrgTask{
			ID:     i,
			Name:   task,
			Weight: wei / totalWei,
		}
	}
	return res
}

func (p *StagedPrg) SetMinInterval(intvMSecs int) {
	if intvMSecs > 0 {
		p.minIntvMS = int64(intvMSecs)
	}
}

func (p *StagedPrg) AddTrigger(name string, cb FnTaskPrg) {
	p.lock.Lock()
	if _, ok := p.triggers[name]; !ok {
		p.triggers[name] = cb
	}
	p.lock.Unlock()
}

func (p *StagedPrg) DelTrigger(name string) {
	p.lock.Lock()
	if _, ok := p.triggers[name]; ok {
		delete(p.triggers, name)
	}
	p.lock.Unlock()
}

func (p *StagedPrg) SetProgress(task string, progress float64) {
	if progress < 0 || progress > 1 {
		log.Warn("progress should be in [0,1]", zap.String("task", task), zap.Float64("prg", progress))
	}
	p.lock.Lock()
	t, ok := p.taskMap[task]
	if !ok {
		panic(fmt.Sprintf("task: %v not registered in StagedPrg", task))
	}
	if progress > t.Progress && p.active <= t.ID {
		p.active = t.ID
		t.Progress = progress
		totalPrg := float64(0)
		for i := 0; i < t.ID; i++ {
			totalPrg += p.taskMap[p.tasks[i]].Weight
		}
		p.Progress = totalPrg + t.Progress*t.Weight
		curTime := btime.UTCStamp()
		if curTime-p.lastNotifyMS > p.minIntvMS {
			p.lastNotifyMS = curTime
			for _, cb := range p.triggers {
				cb(task, p.Progress)
			}
		}
	}
	p.lock.Unlock()
}

func FillOHLCVLacks(bars []*banexg.Kline, startMS, endMS, tfMSecs int64) ([]*banexg.Kline, int) {
	if len(bars) == 0 || startMS >= endMS || tfMSecs <= 0 {
		return nil, 0
	}
	addNum := 0
	arrLen := int((endMS - startMS) / tfMSecs)
	if bars[0].Time > startMS {
		preLack := int((bars[0].Time - startMS) / tfMSecs)
		if preLack > 0 {
			// 补全头部缺失的k线
			firstOpen := bars[0].Open
			preKs := make([]*banexg.Kline, preLack)
			for i := 0; i < preLack; i++ {
				preKs[i] = &banexg.Kline{
					Time:   startMS + int64(i)*tfMSecs,
					Open:   firstOpen,
					High:   firstOpen,
					Low:    firstOpen,
					Close:  firstOpen,
					Volume: 0,
				}
			}
			addNum += preLack
			bars = append(preKs, bars...)
		}
	}

	// 检查并补全中间缺失的k线
	fullBars := make([]*banexg.Kline, 0, arrLen)
	fullBars = append(fullBars, bars[0])

	for i := 1; i < len(bars); i++ {
		prev := fullBars[len(fullBars)-1]
		expectedTime := prev.Time + tfMSecs
		if bars[i].Time == expectedTime {
			fullBars = append(fullBars, bars[i])
		} else if bars[i].Time > expectedTime {
			// 有缺失，需要补全
			lastClose := prev.Close
			for t := expectedTime; t < bars[i].Time; t += tfMSecs {
				addNum += 1
				fullBars = append(fullBars, &banexg.Kline{
					Time:   t,
					Open:   lastClose,
					High:   lastClose,
					Low:    lastClose,
					Close:  lastClose,
					Volume: 0,
				})
			}
			fullBars = append(fullBars, bars[i])
		}
		// 如果bars[i].Time < expectedTime，忽略这个bar
	}

	// 补全尾部缺失的K线
	curMS := fullBars[len(fullBars)-1].Time + tfMSecs
	lastClose := fullBars[len(fullBars)-1].Close
	for curMS < endMS {
		addNum += 1
		fullBars = append(fullBars, &banexg.Kline{
			Time:   curMS,
			Open:   lastClose,
			High:   lastClose,
			Low:    lastClose,
			Close:  lastClose,
			Volume: 0,
		})
		curMS += tfMSecs
	}
	return fullBars, addNum
}
