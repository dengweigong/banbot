package strat

import (
	"flag"
	"fmt"
	"github.com/banbox/banbot/btime"
	"github.com/banbox/banbot/config"
	"github.com/banbox/banbot/core"
	"github.com/banbox/banbot/exg"
	"github.com/banbox/banbot/goods"
	"github.com/banbox/banbot/orm"
	"github.com/banbox/banbot/orm/ormo"
	"github.com/banbox/banbot/utils"
	"github.com/banbox/banexg/errs"
	"github.com/banbox/banexg/log"
	utils2 "github.com/banbox/banexg/utils"
	ta "github.com/banbox/banta"
	"go.uber.org/zap"
	"sort"
	"strings"
)

/*
LoadStratJobs Loading strategies and trading pairs 加载策略和交易对

更新以下全局变量：
Update the following global variables:
core.TFSecs
core.StgPairTfs
core.BookPairs
strat.Versions
strat.Envs
strat.PairStrats
strat.AccJobs
strat.AccInfoJobs

	return：pair:timeframe:warmNum, acc:exit orders, error
*/
func LoadStratJobs(pairs []string, tfScores map[string]map[string]float64) (map[string]map[string]int, map[string][]*ormo.InOutOrder, *errs.Error) {
	if len(pairs) == 0 || len(tfScores) == 0 {
		return nil, nil, errs.NewMsg(errs.CodeParamRequired, "`pairs` and `tfScores` are required for LoadStratJobs")
	}
	// Set the global variables involved to null, as will be updated below
	// 将涉及的全局变量置为空，下面会更新
	core.TFSecs = make(map[string]int)
	core.BookPairs = make(map[string]bool)
	core.StgPairTfs = make(map[string]map[string]string)
	resetJobs()
	pairTfWarms := make(Warms)
	// 记录每个账户下，每个策略的任务数量，防止超过账户要求数量
	accLimits, maxJobNum := newAccStratLimits()
	for _, pol := range config.RunPolicy {
		stgy := New(pol)
		polID := pol.ID()
		if stgy == nil {
			return nil, nil, errs.NewMsg(core.ErrRunTime, "strategy %s load fail", polID)
		}
		Versions[stgy.Name] = stgy.Version
		stgyMaxNum := pol.MaxPair
		if stgyMaxNum == 0 {
			stgyMaxNum = maxJobNum
		}
		holdNum := 0
		failTfScores := make(map[string]map[string]float64)
		var curPairs, err = getPolicyPairs(pol, pairs)
		if err != nil {
			return nil, nil, err
		}
		exsList, err := CallStratSymbols(stgy, curPairs, tfScores)
		if err != nil {
			return nil, nil, err
		}
		dirt := pol.OdDirt()
		for _, exs := range exsList {
			if holdNum >= stgyMaxNum {
				break
			}
			curStgy := stgy
			scores, _ := tfScores[exs.Symbol]
			tf := curStgy.pickTimeFrame(exs.Symbol, scores)
			if tf == "" {
				failTfScores[exs.Symbol] = scores
				continue
			}
			jobType := JobForbidType(exs.Symbol, tf, polID)
			if jobType > 0 {
				if jobType > 1 {
					// 任务禁止，但增加占位
					holdNum += 1
					for acc := range config.Accounts {
						accLimits.tryAdd(acc, polID)
					}
				}
				continue
			}
			items, ok := PairStrats[exs.Symbol]
			if !ok {
				items = make(map[string]*TradeStrat)
				PairStrats[exs.Symbol] = items
			}
			if _, ok = items[polID]; ok {
				// 当前pair+stratID已有任务，跳过
				err = markStratJob(tf, polID, exs, dirt, accLimits)
				if err != nil {
					return nil, nil, err
				}
				holdNum += 1
				continue
			}
			// Check for proprietary parameters of the current target and reinitialize the strategy
			// 检查有当前标的专有参数，重新初始化策略
			if curPol, isDiff := pol.PairDup(exs.Symbol); isDiff {
				curStgy = New(curPol)
			}
			items[polID] = curStgy
			holdNum += 1
			// 初始化BarEnv
			env := initBarEnv(exs, tf)
			// Record the data that needs to be preheated; Record subscription information
			// 记录需要预热的数据；记录订阅信息
			pairTfWarms.Update(exs.Symbol, tf, curStgy.WarmupNum)
			ensureStratJob(curStgy, tf, exs, env, dirt, pairTfWarms.Update, accLimits)
		}
		printFailTfScores(polID, failTfScores)
	}
	var envKeys = make(map[string]bool)
	// 对AccJobs中，当前禁止开单的job，如果无入场订单，则删除job
	accExitOds := make(map[string][]*ormo.InOutOrder)
	exitJobs := make(map[*StratJob]bool)
	exitPairs := make(map[string]bool) // 不再监听的品种
	newPairs := make(map[string]bool)  // 继续监听的品种
	pairTfs := make(Warms)
	holdPosition := config.PairMgr.PosOnRotation != "close"
	for acc, jobs := range AccJobs {
		exitOds := make([]*ormo.InOutOrder, 0, 4)
		for envKey, envJobs := range jobs {
			resJobs := make(map[string]*StratJob)
			for name, job := range envJobs {
				if job.MaxOpenLong == -1 && job.MaxOpenShort == -1 {
					// disable open order
					if job.EnteredNum > 0 && holdPosition {
						// 有未平仓订单，继续跟踪
						resJobs[name] = job
					} else {
						// 立刻平仓
						if job.Strat.OnShutDown != nil {
							job.Strat.OnShutDown(job)
						}
						exitJobs[job] = true
						exitPairs[job.Symbol.Symbol] = true
						if job.EnteredNum > 0 {
							exitOds = append(exitOds, job.LongOrders...)
							exitOds = append(exitOds, job.ShortOrders...)
						}
					}
				} else {
					// 可以继续开单
					resJobs[name] = job
					newPairs[job.Symbol.Symbol] = true
				}
			}
			if len(resJobs) > 0 {
				jobs[envKey] = resJobs
				arr := strings.Split(envKey, "_")
				pair, tf := arr[0], arr[1]
				if _, ok := core.TFSecs[tf]; !ok {
					core.TFSecs[tf] = utils2.TFToSecs(tf)
				}
				for _, j := range resJobs {
					subMap, ok := core.StgPairTfs[j.Strat.Name]
					if !ok {
						subMap = make(map[string]string)
						core.StgPairTfs[j.Strat.Name] = subMap
					}
					subMap[pair] = tf
					if j.Strat.WatchBook {
						core.BookPairs[pair] = true
					}
				}
				envKeys[envKey] = true
				pairTfs.Update(pair, tf, 0)
			} else {
				delete(jobs, envKey)
			}
		}
		if len(exitOds) > 0 {
			accExitOds[acc] = exitOds
		}
	}
	for p := range exitPairs {
		if _, ok := newPairs[p]; ok {
			delete(exitPairs, p)
		}
	}
	if len(exitPairs) > 0 {
		keys := utils2.KeysOfMap(exitPairs)
		log.Info("exit pairs", zap.Int("num", len(keys)), zap.Strings("arr", keys))
	}
	for k := range core.PairsMap {
		_, ok := newPairs[k]
		core.PairsMap[k] = ok
	}
	// 从AccInfoJobs中移除已取消的项
	lockInfoJobs.Lock()
	for acc, jobMap := range AccInfoJobs {
		newJobMap := make(map[string]map[string]*StratJob)
		for pairTf, stgMap := range jobMap {
			newStgMap := make(map[string]*StratJob)
			for name, job := range stgMap {
				if _, ok := exitJobs[job]; !ok {
					newStgMap[name] = job
				}
			}
			if len(newStgMap) > 0 {
				newJobMap[pairTf] = newStgMap
				envKeys[pairTf] = true
				arr := strings.Split(pairTf, "_")
				pair, tf := arr[0], arr[1]
				if _, ok := core.TFSecs[tf]; !ok {
					core.TFSecs[tf] = utils2.TFToSecs(tf)
				}
				// 确保添加到pairTfWarms中
				pairTfs.Update(pair, tf, 0)
			}
		}
		AccInfoJobs[acc] = newJobMap
	}
	lockInfoJobs.Unlock()
	// Ensure that all pairs and TFs are recorded in the returned data to prevent them from being removed by the data subscriber
	// 确保所有pair、tf都在返回的中有记录，防止被数据订阅端移除
	for _, pairMap := range core.StgPairTfs {
		for pair, tf := range pairMap {
			pairTfs.Update(pair, tf, 0)
		}
	}
	// Remove useless items from PairStrats
	// 从PairStrats中删除无用的项
	for pair, stgMap := range PairStrats {
		for name := range stgMap {
			if pairMap, ok := core.StgPairTfs[name]; ok {
				if _, ok = pairMap[pair]; ok {
					continue
				}
			}
			delete(stgMap, name)
		}
	}
	// Remove useless items from Envs
	// 从Envs中删除无用的项
	for envKey := range Envs {
		if _, ok := envKeys[envKey]; !ok {
			delete(Envs, envKey)
		}
	}
	// 从pairTfs中确认哪些要恢复
	for pair, tfMap := range pairTfs {
		rawTfMap, ok := pairTfWarms[pair]
		if !ok {
			continue
		}
		for tf := range tfMap {
			rawNum, ok2 := rawTfMap[tf]
			if ok2 {
				tfMap[tf] = rawNum
			}
		}
	}
	return pairTfs, accExitOds, nil
}

func ExitStratJobs() {
	for _, jobs := range AccJobs {
		for _, items := range jobs {
			for _, job := range items {
				if job.Strat.OnShutDown != nil {
					job.Strat.OnShutDown(job)
				}
			}
		}
	}
}

func CallStratSymbols(stgy *TradeStrat, curPairs []string, tfScores map[string]map[string]float64) ([]*orm.ExSymbol, *errs.Error) {
	var exsMap = make(map[string]*orm.ExSymbol)
	for _, pair := range curPairs {
		exs, err := orm.GetExSymbolCur(pair)
		if err != nil {
			return nil, err
		}
		exsMap[pair] = exs
	}
	if stgy.OnSymbols == nil {
		return utils2.ValsOfMapBy(exsMap, curPairs), nil
	}
	modified := stgy.OnSymbols(curPairs)
	adds, removes := utils.GetAddsRemoves(modified, curPairs)
	if len(adds) > 0 || len(removes) > 0 {
		log.Info("strategy change symbols", zap.String("strat", stgy.Name),
			zap.Int("add", len(adds)), zap.Int("remove", len(removes)))
		if len(adds) > 0 {
			newPairs := make([]string, 0, len(adds))
			for _, pair := range adds {
				if _, ok := exsMap[pair]; !ok {
					exs, err := orm.GetExSymbolCur(pair)
					if err != nil {
						return nil, err
					}
					exsMap[pair] = exs
					if _, ok = tfScores[pair]; !ok {
						newPairs = append(newPairs, pair)
						if _, ok = core.PairsMap[pair]; !ok {
							core.PairsMap[pair] = true
							core.Pairs = append(core.Pairs, pair)
						}
					}
				}
			}
			if len(newPairs) > 0 {
				pairTfScores, err := CalcPairTfScores(exg.Default, newPairs)
				if err != nil {
					log.Error("CalcPairTfScores fail", zap.Error(err))
				} else {
					for pair, scores := range pairTfScores {
						tfScores[pair] = scores
					}
				}
			}
		}
		if len(removes) > 0 {
			for _, it := range removes {
				if _, ok := exsMap[it]; ok {
					delete(exsMap, it)
				}
			}
		}
	}
	return utils2.ValsOfMapBy(exsMap, modified), nil
}

func printFailTfScores(stratName string, pairTfScores map[string]map[string]float64) {
	if len(pairTfScores) == 0 {
		return
	}
	lines := make([]string, 0, len(pairTfScores))
	for pair, tfScores := range pairTfScores {
		if len(tfScores) == 0 {
			lines = append(lines, fmt.Sprintf("%v: ", pair))
			continue
		}
		scoreStrs := make([]string, 0, len(pairTfScores))
		for tf_, score := range tfScores {
			scoreStrs = append(scoreStrs, fmt.Sprintf("%v: %.3f", tf_, score))
		}
		lines = append(lines, fmt.Sprintf("%v: %v", pair, strings.Join(scoreStrs, ", ")))
	}
	log.Info(fmt.Sprintf("%v filter pairs by tfScore: \n%v", stratName, strings.Join(lines, "\n")))
}

func initBarEnv(exs *orm.ExSymbol, tf string) *ta.BarEnv {
	envKey := strings.Join([]string{exs.Symbol, tf}, "_")
	env, ok := Envs[envKey]
	if !ok {
		tfMSecs := int64(utils2.TFToSecs(tf) * 1000)
		env = &ta.BarEnv{
			Exchange:   core.ExgName,
			MarketType: core.Market,
			Symbol:     exs.Symbol,
			TimeFrame:  tf,
			TFMSecs:    tfMSecs,
			MaxCache:   core.NumTaCache,
			Data:       map[string]interface{}{"sid": int64(exs.ID)},
		}
		Envs[envKey] = env
	}
	return env
}

func markStratJob(tf, polID string, exs *orm.ExSymbol, dirt int, accLimits accStratLimits) *errs.Error {
	envKey := strings.Join([]string{exs.Symbol, tf}, "_")
	for acc, jobs := range AccJobs {
		envJobs, ok := jobs[envKey]
		if !ok {
			return errs.NewMsg(errs.CodeRunTime, "`envKey` for StratJob not found: %s", envKey)
		}
		job, ok := envJobs[polID]
		if !ok {
			return errs.NewMsg(errs.CodeRunTime, "`name` for StratJob not found: %s %s", polID, envKey)
		}
		if accLimits.tryAdd(acc, polID) {
			job.MaxOpenShort = job.Strat.EachMaxShort
			job.MaxOpenLong = job.Strat.EachMaxLong
			if dirt == core.OdDirtShort {
				job.MaxOpenLong = -1
			} else if dirt == core.OdDirtLong {
				job.MaxOpenShort = -1
			}
		}
	}
	return nil
}

func ensureStratJob(stgy *TradeStrat, tf string, exs *orm.ExSymbol, env *ta.BarEnv, dirt int,
	logWarm func(pair, tf string, num int), accLimits accStratLimits) {
	envKey := strings.Join([]string{exs.Symbol, tf}, "_")
	for account, jobs := range AccJobs {
		envJobs, ok := jobs[envKey]
		if !ok {
			envJobs = make(map[string]*StratJob)
			jobs[envKey] = envJobs
		}
		allowOpen := accLimits.tryAdd(account, stgy.Name)
		job, ok := envJobs[stgy.Name]
		if !ok {
			if !allowOpen {
				continue
			}
			job = &StratJob{
				Strat:         stgy,
				Env:           env,
				Symbol:        exs,
				TimeFrame:     tf,
				Account:       account,
				TPMaxs:        make(map[int64]float64),
				CloseLong:     true,
				CloseShort:    true,
				ExgStopLoss:   true,
				ExgTakeProfit: true,
			}
			if stgy.OnStartUp != nil {
				stgy.OnStartUp(job)
			}
			envJobs[stgy.Name] = job
		}
		if allowOpen {
			job.MaxOpenShort = stgy.EachMaxShort
			job.MaxOpenLong = stgy.EachMaxLong
			if dirt == core.OdDirtShort {
				job.MaxOpenLong = -1
			} else if dirt == core.OdDirtLong {
				job.MaxOpenShort = -1
			}
		}
		// Load subscription information for other targets
		// 加载订阅其他标的信息
		if stgy.OnPairInfos != nil {
			infoJobs := GetInfoJobs(account)
			hasInfoSubs := false
			for _, s := range stgy.OnPairInfos(job) {
				pair := s.Pair
				if pair == "_cur_" {
					pair = exs.Symbol
					initBarEnv(exs, s.TimeFrame)
				} else {
					curExs, err := orm.GetExSymbolCur(pair)
					if err != nil {
						log.Warn("skip invalid pair", zap.String("strat", job.Strat.Name),
							zap.String("pair", pair))
						continue
					}
					initBarEnv(curExs, s.TimeFrame)
				}
				hasInfoSubs = true
				logWarm(pair, s.TimeFrame, s.WarmupNum)
				jobKey := strings.Join([]string{pair, s.TimeFrame}, "_")
				items, ok := infoJobs[jobKey]
				if !ok {
					items = make(map[string]*StratJob)
					infoJobs[jobKey] = items
				}
				// 这里需要stratID+pair作为键，否则多个品种订阅同一个额外品种数据时，只记录了最后一个
				items[strings.Join([]string{stgy.Name, exs.Symbol}, "_")] = job
			}
			if hasInfoSubs && stgy.OnInfoBar == nil {
				panic(fmt.Sprintf("%s: `OnInfoBar` is required for OnPairInfos", stgy.Name))
			}
		}
	}
}

/*
将jobs的MaxOpenLong,MacOpenShort都置为-1，禁止开单，并更新附加订单
*/
func resetJobs() {
	for account := range config.Accounts {
		openOds, lock := ormo.GetOpenODs(account)
		lock.Lock()
		odList := make([]*ormo.InOutOrder, 0, len(openOds))
		for _, od := range openOds {
			odList = append(odList, od)
		}
		lock.Unlock()
		accJobs := GetJobs(account)
		for _, jobs := range accJobs {
			for _, job := range jobs {
				job.InitBar(odList)
				job.MaxOpenLong = -1
				job.MaxOpenShort = -1
			}
		}
	}
}

var polFilters = make(map[string][]goods.IFilter)

func getPolicyPairs(pol *config.RunPolicyConfig, pairs []string) ([]string, *errs.Error) {
	// According to pol Pair determines the subject of the transaction
	// 根据pol.Pairs确定交易的标的
	if len(pol.Pairs) > 0 {
		pairs = pol.Pairs
	}
	if len(pairs) == 0 {
		return pairs, nil
	}
	if len(pol.Filters) > 0 {
		// Filter based on filters
		// 根据filters过滤筛选
		polID := pol.ID()
		filters, ok := polFilters[polID]
		var err *errs.Error
		if !ok {
			filters, err = goods.GetPairFilters(pol.Filters, false)
			if err != nil {
				return nil, err
			}
			polFilters[polID] = filters
		}
		curMS := btime.TimeMS()
		for _, flt := range filters {
			pairs, err = flt.Filter(pairs, curMS)
			if err != nil {
				return nil, err
			}
		}
	}
	return pairs, nil
}

func ListStrats(args []string) error {
	var prefix string
	var sub = flag.NewFlagSet("cmp", flag.ExitOnError)
	sub.StringVar(&prefix, "prefix", "", "prefix to filter")
	err_ := sub.Parse(args)
	if err_ != nil {
		return err_
	}
	arr := utils.KeysOfMap(StratMake)
	if prefix != "" {
		filtered := make([]string, 0, len(arr))
		for _, code := range arr {
			if strings.HasPrefix(code, prefix) {
				filtered = append(filtered, code)
			}
		}
		arr = filtered
	}
	sort.Strings(arr)
	fmt.Println(strings.Join(arr, "\n"))
	return nil
}
