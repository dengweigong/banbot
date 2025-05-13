package strat

import (
	"fmt"
	"github.com/banbox/banbot/btime"
	"github.com/banbox/banbot/config"
	"github.com/banbox/banbot/core"
	"github.com/banbox/banbot/orm"
	"github.com/banbox/banbot/orm/ormo"
	"github.com/banbox/banbot/utils"
	"github.com/banbox/banexg/errs"
	"github.com/banbox/banexg/log"
	"go.uber.org/zap"
	"math"
	"slices"
	"sort"
	"strconv"
	"strings"
)

type FuncMakeStrat = func(pol *config.RunPolicyConfig) *TradeStrat

var (
	StratMake = make(map[string]FuncMakeStrat) // 已加载的策略缓存
)

func New(pol *config.RunPolicyConfig) *TradeStrat {
	polID := pol.ID()
	makeFn, ok := StratMake[pol.Name]
	var stgy *TradeStrat
	if ok {
		stgy = makeFn(pol)
	} else {
		panic("strategy not found: " + pol.Name)
		// stgy = loadNative(pol.Name)
	}
	stgy.Name = polID
	if stgy.MinTfScore == 0 {
		stgy.MinTfScore = 0.75
	}
	stgy.Policy = pol
	if pol.StakeRate > 0 {
		stgy.StakeRate = pol.StakeRate
	}
	if pol.OrderBarMax > 0 {
		stgy.OdBarMax = pol.OrderBarMax
	}
	if pol.StopLoss != nil {
		slRate, isFlt := pol.StopLoss.(float64)
		if isFlt {
			if slRate > 0 {
				stgy.StopLoss = slRate
			} else if slRate < 0 {
				log.Error("stop_loss should > 0", zap.String("policy", pol.Name))
			}
		} else if slStr, ok := pol.StopLoss.(string); ok {
			if strings.TrimSpace(slStr) != "" {
				var err error
				slStr = strings.TrimSpace(slStr)
				if strings.HasSuffix(slStr, "%") {
					slRate, err = strconv.ParseFloat(slStr[:len(slStr)-1], 64)
					slRate /= 100
				} else {
					slRate, err = strconv.ParseFloat(slStr, 64)
				}
				if err != nil {
					log.Error("invalid stop_loss", zap.String("policy", pol.Name), zap.Error(err))
				} else if slRate > 0 {
					stgy.StopLoss = slRate
				}
			}
		} else if slInt, ok := pol.StopLoss.(int); ok {
			if slInt != 0 {
				log.Error("stop_loss format error, expect to be 5% or 0.05", zap.String("policy", pol.Name))
			}
		} else {
			log.Error("invalid stop_loss type, expect e.g.: 5% or 0.05", zap.String("policy", pol.Name),
				zap.String("type", fmt.Sprintf("%T", pol.StopLoss)))
		}
	}
	return stgy
}

func Get(pair, stratID string) *TradeStrat {
	data, _ := PairStrats[pair]
	if len(data) == 0 {
		return nil
	}
	res, _ := data[stratID]
	return res
}

func GetStratPerf(pair, strat string) *config.StratPerfConfig {
	stg := Get(pair, strat)
	if stg == nil {
		return nil
	}
	if stg.Policy.StratPerf != nil {
		return stg.Policy.StratPerf
	}
	return config.Data.StratPerf
}

//func loadNative(stratName string) *TradeStrat {
//	filePath := path.Join(config.GetStratDir(), stratName+".o")
//	_, err := os.Stat(filePath)
//	nameVar := zap.String("name", stratName)
//	if err != nil {
//		log.Error("strategy not found", zap.String("path", filePath), zap.Error(err))
//		return nil
//	}
//	linker, err := goloader.ReadObj(filePath, stratName)
//	if err != nil {
//		log.Error("strategy load fail, package is `main`?", nameVar, zap.Error(err))
//		return nil
//	}
//	symPtr := make(map[string]uintptr)
//	err = goloader.RegSymbol(symPtr)
//	if err != nil {
//		log.Error("strategy read symbol fail", nameVar, zap.Error(err))
//		return nil
//	}
//	regLoaderTypes(symPtr)
//	module, err := goloader.Load(linker, symPtr)
//	if err != nil {
//		log.Error("strategy load module fail", nameVar, zap.Error(err))
//		return nil
//	}
//	keys := zap.String("keys", strings.Join(utils.KeysOfMap(module.Syms), ","))
//	prefix := stratName + "."
//	// 加载Main
//	mainPath := prefix + "Main"
//	mainPtr := GetModuleItem(module, mainPath)
//	if mainPtr == nil {
//		log.Error("module item not found", zap.String("p", mainPath), keys)
//		return nil
//	}
//	runFunc := *(*func() *TradeStrat)(mainPtr)
//	stagy := runFunc()
//	stagy.Name = stratName
//	// 这里不能卸载，卸载后结构体的嵌入函数无法调用
//	// module.Unload()
//	return stagy
//}
//
//func GetModuleItem(module *goloader.CodeModule, itemPath string) unsafe.Pointer {
//	main, ok := module.Syms[itemPath]
//	if !ok || main == 0 {
//		return nil
//	}
//	mainPtr := (uintptr)(unsafe.Pointer(&main))
//	return unsafe.Pointer(&mainPtr)
//}
//
//func regLoaderTypes(symPtr map[string]uintptr) {
//	goloader.RegTypes(symPtr, &ta.BarEnv{}, &ta.Series{}, &ta.CrossLog{}, &ta.XState{}, ta.Cross, ta.Sum, ta.SMA,
//		ta.EMA, ta.EMABy, ta.RMA, ta.RMABy, ta.TR, ta.ATR, ta.MACD, ta.MACDBy, ta.RSI, ta.Highest, ta.Lowest, ta.KDJ,
//		ta.KDJBy, ta.StdDev, ta.StdDevBy, ta.BBANDS, ta.TD, &ta.AdxState{}, ta.ADX, ta.ROC, ta.HeikinAshi)
//	stgy := &TradeStrat{}
//	goloader.RegTypes(symPtr, stgy, stgy.OnPairInfos, stgy.OnStartUp, stgy.OnBar, stgy.OnInfoBar, stgy.OnTrades,
//		stgy.OnCheckExit, stgy.GetDrawDownExitRate, stgy.PickTimeFrame, stgy.OnShutDown)
//	job := &StratJob{}
//	goloader.RegTypes(symPtr, job, job.OpenOrder)
//	goloader.RegTypes(symPtr, &PairSub{}, &EnterReq{}, &ExitReq{})
//}

func (q *ExitReq) Clone() *ExitReq {
	res := &ExitReq{
		Tag:        q.Tag,
		StratName:  q.StratName,
		EnterTag:   q.EnterTag,
		Dirt:       q.Dirt,
		OrderType:  q.OrderType,
		Limit:      q.Limit,
		ExitRate:   q.ExitRate,
		Amount:     q.Amount,
		OrderID:    q.OrderID,
		UnFillOnly: q.UnFillOnly,
		Force:      q.Force,
	}
	return res
}

func (s *StratJob) InitBar(curOrders []*ormo.InOutOrder) {
	s.CheckMS = btime.TimeMS()
	s.LastBarMS = s.Env.TimeStop
	if s.IsWarmUp {
		s.LongOrders = nil
		s.ShortOrders = nil
	} else if s.OrderNum > 0 {
		s.LongOrders = nil
		s.ShortOrders = nil
		enteredNum := 0
		for _, od := range curOrders {
			if od.Symbol == s.Symbol.Symbol && od.Timeframe == s.TimeFrame && od.Strategy == s.Strat.Name {
				if od.Status >= ormo.InOutStatusPartEnter && od.Status <= ormo.InOutStatusPartExit {
					enteredNum += 1
				}
				if od.Short {
					s.ShortOrders = append(s.ShortOrders, od)
				} else {
					s.LongOrders = append(s.LongOrders, od)
				}
			}
		}
		s.OrderNum = len(s.LongOrders) + len(s.ShortOrders)
		s.EnteredNum = enteredNum
	}
	s.Entrys = nil
	s.Exits = nil
}

func CheckCustomExits(job *StratJob) *errs.Error {
	orders := job.GetOrders(0)
	for _, od := range orders {
		if od.Status >= ormo.InOutStatusFullEnter && od.CanClose() {
			_, err := job.customExit(od)
			if err != nil {
				return err
			}
		}
	}
	return nil
}

/*
GetJobs 返回：pair_tf: [stratID]StratJob
*/
func GetJobs(account string) map[string]map[string]*StratJob {
	if !core.EnvReal {
		account = config.DefAcc
	}
	jobs, ok := AccJobs[account]
	if !ok {
		jobs = map[string]map[string]*StratJob{}
		AccJobs[account] = jobs
	}
	return jobs
}

func GetInfoJobs(account string) map[string]map[string]*StratJob {
	if !core.EnvReal {
		account = config.DefAcc
	}
	lockInfoJobs.Lock()
	jobs, ok := AccInfoJobs[account]
	if !ok {
		jobs = map[string]map[string]*StratJob{}
		AccInfoJobs[account] = jobs
	}
	lockInfoJobs.Unlock()
	return jobs
}

func CalcJobScores(pair, tf, stgy string) *errs.Error {
	var orders []*ormo.InOutOrder
	cfg := GetStratPerf(pair, stgy)
	if core.LiveMode {
		// Retrieve recent orders from the database
		// 从数据库查询最近订单
		sess, conn, err := ormo.Conn(orm.DbTrades, false)
		if err != nil {
			return err
		}
		defer conn.Close()
		taskId := ormo.GetTaskID(config.DefAcc)
		orders, err = sess.GetOrders(ormo.GetOrdersArgs{
			TaskID:    taskId,
			Strategy:  stgy,
			TimeFrame: tf,
			Pairs:     []string{pair},
			Status:    2,
			Limit:     cfg.MaxOdNum,
		})
		if err != nil {
			return err
		}
	} else {
		// 从HistODs查询
		for _, od := range ormo.HistODs {
			if od.Symbol != pair || od.Timeframe != tf || od.Strategy != stgy {
				continue
			}
			orders = append(orders, od)
		}
		if len(orders) > cfg.MaxOdNum {
			orders = orders[len(orders)-cfg.MaxOdNum:]
		}
	}
	sta := core.GetPerfSta(stgy)
	sta.OdNum += 1
	if len(orders) < cfg.MinOdNum {
		return nil
	}
	totalPft := 0.0
	for _, od := range orders {
		totalPft += od.ProfitRate
	}
	var prefKey = core.KeyStratPairTf(stgy, pair, tf)
	perf, _ := core.JobPerfs[prefKey]
	if perf == nil {
		perf = &core.JobPerf{
			Num:       len(orders),
			TotProfit: totalPft,
			Score:     1,
		}
		core.JobPerfs[prefKey] = perf
	} else {
		perf.Num = len(orders)
	}
	// collect all trade jobs for this strategy & calculate stake rate
	// 收集此策略所有任务，计算开单倍率
	var prefs []*core.JobPerf
	for key, p := range core.JobPerfs {
		if strings.HasPrefix(key, stgy) {
			prefs = append(prefs, p)
		}
	}
	perf.Score = defaultCalcJobScore(cfg, sta, perf, prefs)
	if core.LiveMode {
		// Real disk mode, immediately save to data directory
		// 实盘模式，立刻保存到数据目录
		core.DumpPerfs(config.GetDataDir())
	}
	return nil
}

func defaultCalcJobScore(cfg *config.StratPerfConfig, sta *core.PerfSta, p *core.JobPerf, perfs []*core.JobPerf) float64 {
	if len(perfs) < cfg.MinJobNum {
		return 1
	}
	// 按Job总利润分组5档
	if sta.Splits == nil || sta.OdNum-sta.LastGpAt >= len(perfs) {
		// 按总收益率KMeans分组
		perfs = append(perfs, p)
		CalcJobPerfs(cfg, sta, perfs)
		return p.Score
	}
	// 聚类结果依然有效，查找最接近的pref，使用相同的Score
	var near *core.JobPerf
	var minDist = 0.0
	for _, pf := range perfs {
		dist := math.Abs(p.TotProfit - pf.TotProfit)
		if near == nil || dist < minDist {
			near = pf
			minDist = dist
		}
	}
	p.Score = near.Score
	return p.Score
}

func CalcJobPerfs(cfg *config.StratPerfConfig, p *core.PerfSta, perfs []*core.JobPerf) {
	sumProfit := 0.0
	var profits = make([]float64, 0, len(perfs))
	for _, pf := range perfs {
		profits = append(profits, pf.TotProfit)
		sumProfit += math.Abs(pf.TotProfit)
	}
	//Logarithmic processing of returns to make clustering more accurate
	//Map the average positive rate of return to x=9 in log2, ensuring that the logarithmic processing results in an appropriate distribution divergence
	// 对收益率对数处理，使聚类更准确
	// 将平均正收益率固定映射为log2的x=9，确保对数处理后能在合适的分布散度
	avgProfit := sumProfit / float64(len(profits))
	p.Delta = 9 / avgProfit
	for i, val := range profits {
		profits[i] = p.Log2(val)
	}
	res := utils.KMeansVals(profits, 5)
	groups := res.Clusters
	sort.Slice(groups, func(i, j int) bool {
		return groups[i].Center < groups[j].Center
	})
	var maxList = make([]float64, 0, len(groups))
	for _, gp := range groups {
		maxList = append(maxList, slices.Max(gp.Items))
	}
	// Calculate the upper and lower bounds of the rate of return for each group
	// 计算每组的收益率上下界
	p.Splits = &[4]float64{maxList[0], maxList[1], maxList[2], maxList[3]}
	p.LastGpAt = p.OdNum
	// Recalculate the weight of each job
	// 重新计算每个job的权重
	idxList := make([]int, 0, len(perfs))
	var totalAdd, goodNum, bestNum = 0.0, 0, 0
	for i, profit := range profits {
		pf := perfs[i]
		gid := p.FindGID(profit)
		if gid == 0 {
			pf.Score = core.PrefMinRate
			totalAdd += 1
		} else if gid == 1 {
			pf.Score = cfg.BadWeight
			totalAdd += 1 - cfg.BadWeight
		} else if gid == 2 {
			pf.Score = cfg.MidWeight
			totalAdd += 1 - cfg.MidWeight
		} else if gid == 3 {
			pf.Score = 1
			goodNum += 1
		} else if gid == 4 {
			pf.Score = 1
			bestNum += 1
		}
		idxList = append(idxList, gid)
	}
	if totalAdd > 0 {
		// Overlay the weight of losses onto profitable jobs
		// 将亏损的权重，叠加到盈利的job上
		totalWeight := float64(goodNum) + float64(bestNum)*2
		unitAdd := totalAdd / totalWeight
		for i, gid := range idxList {
			if gid < 3 {
				continue
			}
			pf := perfs[i]
			pf.Score += unitAdd * (float64(gid) - 2)
		}
	}
}

func AddOdSub(acc string, cb FnOdChange) {
	lockOdSub.Lock()
	subs, _ := accOdSubs[acc]
	accOdSubs[acc] = append(subs, cb)
	lockOdSub.Unlock()
}

func FireOdChange(acc string, od *ormo.InOutOrder, evt int) {
	lockOdSub.Lock()
	subs, _ := accOdSubs[acc]
	subs2, _ := accOdSubs["*"]
	lockOdSub.Unlock()
	subs = append(subs, subs2...)
	// 将模拟时间置为事件触发时间，并备份当前时间
	evtTime := int64(0)
	if evt == OdChgEnter {
		evtTime = od.Enter.CreateAt
	} else if evt == OdChgEnterFill {
		evtTime = od.Enter.UpdateAt
	} else if evt == OdChgExit {
		evtTime = od.Exit.CreateAt
	} else if evt == OdChgExitFill && od.Exit != nil {
		evtTime = od.Exit.UpdateAt
	}
	backMS := int64(0)
	if evtTime > 0 {
		backMS = btime.TimeMS()
		btime.CurTimeMS = evtTime
	}
	for _, cb := range subs {
		cb(acc, od, evt)
	}
	// 恢复原始时间
	if evtTime > 0 {
		btime.CurTimeMS = backMS
	}
}

func AddStratGroup(group string, items map[string]FuncMakeStrat) {
	for k, v := range items {
		StratMake[group+":"+k] = v
	}
}

func (w Warms) Update(pair, tf string, num int) {
	if warms, ok := w[pair]; ok {
		if oldNum, ok := warms[tf]; ok {
			warms[tf] = max(oldNum, num)
		} else {
			warms[tf] = num
		}
	} else {
		w[pair] = map[string]int{tf: num}
	}
}

/*
JobForbidType 0 allow; 1 forbid; 2 forbid & occupy a slot
*/
func JobForbidType(pair, tf, stratID string) int {
	if jobs, ok := ForbidJobs[fmt.Sprintf("%s_%s", pair, tf)]; ok {
		hold, ok2 := jobs[stratID]
		if ok2 {
			if hold {
				return 2
			}
			return 1
		}
	}
	return 0
}

/*
GetJobKeys 获取已订阅的所有策略任务记录
return map[pair_tf][stratID]bool
*/
func GetJobKeys() map[string]map[string]bool {
	jobs := make(map[string]map[string]bool)
	for _, jobsMap := range AccJobs {
		for pairTF, stgMap := range jobsMap {
			idMap := make(map[string]bool)
			for polID := range stgMap {
				idMap[polID] = true
			}
			jobs[pairTF] = idMap
		}
		break
	}
	return jobs
}

func AddAccFailOpen(acc, tag string) {
	AddAccFailOpens(acc, tag, 1)
}

func AddAccFailOpens(acc, tag string, num int) {
	lockAccFailOpen.Lock()
	tagMap, ok1 := accFailOpens[acc]
	if !ok1 {
		tagMap = make(map[string]int)
		accFailOpens[acc] = tagMap
	}
	count, _ := tagMap[tag]
	tagMap[tag] = count + num
	lockAccFailOpen.Unlock()
}

func DumpAccFailOpens() string {
	lockAccFailOpen.Lock()
	var b strings.Builder
	isFirst := true
	for k, v := range accFailOpens {
		if isFirst {
			isFirst = false
		} else {
			b.WriteString("\n")
		}
		b.WriteString(k)
		b.WriteString(": ")
		b.WriteString(utils.MapToStr(v, true, 0))
	}
	lockAccFailOpen.Unlock()
	return b.String()
}

func newAccStratLimits() (accStratLimits, int) {
	res := make(accStratLimits)
	maxJobNum := 1
	for acc, cfg := range config.Accounts {
		res[acc] = &stgLimits{
			limit:  cfg.MaxPair,
			strats: make(map[string]int),
		}
		if cfg.MaxPair == 0 {
			maxJobNum = 0
		} else if maxJobNum > 0 && cfg.MaxPair > maxJobNum {
			maxJobNum = cfg.MaxPair
		}
	}
	if maxJobNum == 0 {
		maxJobNum = 999
	}
	return res, maxJobNum
}

/*
尝试对某个策略增加计数
*/
func (l *stgLimits) tryAdd(name string) bool {
	num, _ := l.strats[name]
	if l.limit > 0 && num >= l.limit {
		return false
	}
	l.strats[name] = num + 1
	return true
}

/*
尝试对某个账户的某个策略增加计数
*/
func (a accStratLimits) tryAdd(acc, name string) bool {
	li, ok := a[acc]
	if ok {
		return li.tryAdd(name)
	}
	return true
}

/*
PrintStratGroups
print strategy+timeframe from `core.StgPairTfs`
从core.StgPairTfs输出策略+时间周期的币种信息到控制台
*/
func PrintStratGroups() {
	text := core.GroupByPairQuotes(map[string][]string{"Pairs": core.Pairs}, false)
	log.Info("global pairs", zap.String("res", "\n"+text))
	for acc, jobMap := range AccJobs {
		allows := make(map[string][]string)
		disables := make(map[string][]string)
		for pairTF, stratMap := range jobMap {
			arrP := strings.Split(pairTF, "_")
			pair, tf := arrP[0], arrP[1]
			for stratID := range stratMap {
				key := fmt.Sprintf("%s_%s", stratID, tf)
				if ok, _ := core.PairsMap[pair]; ok {
					arr, _ := allows[key]
					allows[key] = append(arr, pair)
				} else {
					arr, _ := disables[key]
					disables[key] = append(arr, pair)
				}
			}
		}
		if len(allows) > 0 {
			text := core.GroupByPairQuotes(allows, true)
			log.Info("group jobs by strat_tf", zap.String("acc", acc), zap.String("res", "\n"+text))
		}
		if len(disables) > 0 {
			text := core.GroupByPairQuotes(disables, true)
			log.Info("group disable jobs by strat_tf", zap.String("acc", acc), zap.String("res", "\n"+text))
		}
	}
}
