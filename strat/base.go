package strat

import (
	"fmt"
	"github.com/banbox/banbot/config"
	"github.com/banbox/banbot/core"
	"github.com/banbox/banbot/orm/ormo"
	"github.com/banbox/banbot/utils"
	"github.com/banbox/banexg"
	"github.com/banbox/banexg/errs"
	"github.com/banbox/banexg/log"
	"go.uber.org/zap"
	"math"
	"slices"
)

/*
******************************  Membership methods of TradeStrat 成员方法  ***********************************
 */

func (s *TradeStrat) GetStakeAmount(j *StratJob) float64 {
	amount := config.GetStakeAmount(j.Account)
	// 乘以策略倍率
	if s.StakeRate > 0 {
		amount *= s.StakeRate
	}
	// 乘以此任务的开单倍率
	key := core.KeyStratPairTf(j.Strat.Name, j.Symbol.Symbol, j.TimeFrame)
	pref, _ := core.JobPerfs[key]
	if pref != nil {
		amount = pref.GetAmount(amount)
	}
	return amount
}

/*
Select the time period to trade from several candidate time periods. This method is called by the system
从若干候选时间周期中选择要交易的时间周期。此方法由系统调用
*/
func (s *TradeStrat) pickTimeFrame(symbol string, tfScores map[string]float64) string {
	if len(tfScores) == 0 {
		return ""
	}
	// 过滤当前需要的时间周期
	useTfs := make(map[string]bool)
	tfList := s.RunTimeFrames
	if len(s.Policy.RunTimeframes) > 0 {
		tfList = s.Policy.RunTimeframes
	}
	for _, tf := range tfList {
		useTfs[tf] = true
	}
	curScores := make([]*core.TfScore, 0, len(tfScores))
	for tf, score := range tfScores {
		if _, ok := useTfs[tf]; ok {
			curScores = append(curScores, &core.TfScore{TF: tf, Score: score})
		}
	}
	slices.SortFunc(curScores, func(a, b *core.TfScore) int {
		return int((a.Score - b.Score) * 1000)
	})
	if s.PickTimeFrame != nil {
		return s.PickTimeFrame(symbol, curScores)
	}
	for _, tfs := range curScores {
		if tfs.Score >= s.MinTfScore {
			return tfs.TF
		}
	}
	return ""
}

func (s *TradeStrat) orderBarMax() int {
	if s.OdBarMax > 0 {
		return s.OdBarMax
	}
	return config.OrderBarMax
}

/*
*****************************  StratJob的成员方法   ****************************************
 */
func (s *StratJob) CanOpen(short bool) bool {
	disable := false
	if short {
		disable = s.MaxOpenShort < 0 || s.MaxOpenShort > 0 && len(s.ShortOrders) >= s.MaxOpenShort
		if core.Market == banexg.MarketSpot {
			disable = true
		}
	} else {
		disable = s.MaxOpenLong < 0 || s.MaxOpenLong > 0 && len(s.LongOrders) >= s.MaxOpenLong
	}
	return !disable
}

func (s *StratJob) OpenOrder(req *EnterReq) *errs.Error {
	if req.Tag == "" {
		return errs.NewMsg(errs.CodeParamRequired, "tag is Required")
	}
	if req.StratName == "" {
		req.StratName = s.Strat.Name
	}
	isLiveMode := core.LiveMode
	symbol := s.Symbol.Symbol
	var dirType = core.OdDirtLong
	if req.Short {
		dirType = core.OdDirtShort
	}
	if !s.CanOpen(req.Short) {
		if isLiveMode {
			log.Warn("open order disabled",
				zap.String("strategy", s.Strat.Name),
				zap.String("pair", symbol),
				zap.String("tag", req.Tag),
				zap.Int("dir", dirType))
		}
		AddAccFailOpen(s.Account, FailOpenBadDirtOrLimit)
		return errs.NewMsg(errs.CodeParamInvalid, "open order disabled")
	}
	if math.IsNaN(req.Limit+req.Amount+req.Leverage+req.CostRate+req.LegalCost) ||
		math.IsNaN(req.StopLoss+req.StopLossVal+req.StopLossLimit+req.StopLossRate) ||
		math.IsNaN(req.TakeProfit+req.TakeProfitVal+req.TakeProfitLimit+req.TakeProfitRate) {
		AddAccFailOpen(s.Account, FailOpenNanNum)
		return errs.NewMsg(errs.CodeParamInvalid, "nan in EnterReq")
	}
	// 检查价格是否有效
	dirFlag := 1.0
	if req.Short {
		dirFlag = -1.0
	}
	enterPrice := core.GetPrice(symbol)
	if req.Limit > 0 {
		if (req.Limit-enterPrice)*dirFlag < 0 {
			enterPrice = req.Limit
		}
	}
	if req.Amount == 0 && req.LegalCost == 0 {
		if req.CostRate == 0 {
			req.CostRate = 1
		}
		req.LegalCost = s.Strat.GetStakeAmount(s) * req.CostRate
		avgVol := s.avgVolume(5) // 最近5个蜡烛成交量
		reqAmt := req.LegalCost / enterPrice
		if avgVol > 0 && reqAmt/avgVol > config.OpenVolRate {
			req.LegalCost = avgVol * config.OpenVolRate * enterPrice
			if core.LiveMode {
				log.Info(fmt.Sprintf("%v open amt rate: %.1f > open_vol_rate(%.1f), cut to cost: %.1f",
					symbol, reqAmt/avgVol, config.OpenVolRate, req.LegalCost))
			}
		}
		minCost := core.MinStakeAmount
		if req.LegalCost < minCost {
			rate := req.LegalCost / minCost
			if config.LowCostAction == core.LowCostKeepBig && rate > 0.4 || config.LowCostAction == core.LowCostKeepAll {
				req.LegalCost = minCost * 1.1
			} else {
				AddAccFailOpen(s.Account, FailOpenCostTooLess)
				return errs.NewMsg(errs.CodeParamInvalid, "legal cost must >= %d", minCost)
			}
		}
	}
	// 检查止损
	curSLPrice := s.LongSLPrice
	if req.Short {
		curSLPrice = s.ShortSLPrice
	}
	if curSLPrice == 0 && s.Strat.StopLoss > 0 {
		curSLPrice = enterPrice * (1 - s.Strat.StopLoss*dirFlag)
	}
	if req.StopLossVal > 0 {
		curSLPrice = enterPrice - req.StopLossVal*dirFlag
	} else if req.StopLoss != 0 {
		curSLPrice = req.StopLoss
	}
	if curSLPrice < 0 {
		curSLPrice = enterPrice * 0.001
	}
	req.StopLossVal = 0
	req.StopLoss = 0
	if curSLPrice > 0 {
		if s.ExgStopLoss {
			if (curSLPrice-enterPrice)*dirFlag >= 0 {
				rel := "<"
				if req.Short {
					rel = ">"
				}
				AddAccFailOpen(s.Account, FailOpenBadStopLoss)
				return errs.NewMsg(errs.CodeParamInvalid, "%s stopLoss %f must %s %f for %v order",
					symbol, curSLPrice, rel, enterPrice, dirType)
			}
			req.StopLoss = curSLPrice
		} else if isLiveMode {
			log.Warn("stopLoss disabled",
				zap.String("strategy", s.Strat.Name),
				zap.String("pair", symbol))
		}
	}
	// 检查止盈
	curTPPrice := s.LongTPPrice
	if req.Short {
		curTPPrice = s.ShortTPPrice
	}
	if req.TakeProfitVal > 0 {
		curTPPrice = enterPrice + req.TakeProfitVal*dirFlag
	} else {
		curTPPrice = req.TakeProfit
	}
	if curTPPrice < 0 {
		curTPPrice = enterPrice * 0.001
	}
	req.TakeProfitVal = 0
	req.TakeProfit = 0
	if curTPPrice > 0 {
		if s.ExgTakeProfit {
			if (curTPPrice-enterPrice)*dirFlag <= 0 {
				rel := ">"
				if req.Short {
					rel = "<"
				}
				AddAccFailOpen(s.Account, FailOpenBadTakeProfit)
				return errs.NewMsg(errs.CodeParamInvalid, "%s takeProfit %f must %s %f for %v order",
					symbol, curTPPrice, rel, enterPrice, dirType)
			}
			req.TakeProfit = curTPPrice
		} else if isLiveMode {
			log.Warn("takeProfit disabled", zap.String("stagy", s.Strat.Name), zap.String("pair", symbol))
		}
	}
	if req.Limit > 0 && req.OrderType == 0 {
		req.OrderType = core.OrderTypeLimit
	}
	if req.Limit > 0 && core.IsLimitOrder(req.OrderType) {
		// 是限价入场单
		if req.StopBars == 0 {
			req.StopBars = s.Strat.StopEnterBars
		}
	}
	s.Entrys = append(s.Entrys, req)
	s.OrderNum += 1
	return nil
}

func (s *StratJob) CloseOrders(req *ExitReq) *errs.Error {
	if s.GetOrderNum(float64(req.Dirt)) == 0 {
		return nil
	}
	if req.Tag == "" {
		return errs.NewMsg(errs.CodeParamRequired, "tag is required")
	}
	if req.StratName == "" {
		req.StratName = s.Strat.Name
	}
	dirtBoth := req.Dirt == core.OdDirtBoth
	if !s.CloseShort && (dirtBoth || req.Dirt == core.OdDirtShort) || !s.CloseLong && (dirtBoth || req.Dirt == core.OdDirtLong) {
		log.Warn("close order disabled",
			zap.String("strategy", s.Strat.Name),
			zap.String("pair", s.Symbol.Symbol),
			zap.String("tag", req.Tag),
			zap.Int("dirt", req.Dirt))
		return errs.NewMsg(errs.CodeParamInvalid, "close order disabled")
	}
	if req.ExitRate > 1 {
		return errs.NewMsg(errs.CodeParamInvalid, "ExitRate shoud in (0, 1], current: %f", req.ExitRate)
	} else if req.ExitRate == 0 {
		req.ExitRate = 1
	}
	if req.Limit > 0 && req.OrderType == 0 {
		req.OrderType = core.OrderTypeLimit
	}
	if req.Limit > 0 && req.Dirt == core.OdDirtBoth {
		if !core.IsLimitOrder(req.OrderType) {
			return errs.NewMsg(errs.CodeParamInvalid, "`ExitReq.Limit` is invalid for Market order")
		}
		hasLong := len(s.LongOrders) > 0
		hasShort := len(s.ShortOrders) > 0
		if hasLong && hasShort {
			return errs.NewMsg(errs.CodeParamInvalid, "`ExitReq.Dirt` is required for Limit order")
		} else if hasLong {
			req.Dirt = core.OdDirtLong
		} else if hasShort {
			req.Dirt = core.OdDirtShort
		}
	}
	s.Exits = append(s.Exits, req)
	return nil
}

/*
avgVolume
Calculate the average trading volume of the latest num candlesticks
计算最近num个K线的平均成交量
*/
func (s *StratJob) avgVolume(num int) float64 {
	arr := s.Env.Volume.Range(0, num)
	if len(arr) == 0 {
		return 0
	}
	sumVal := float64(0)
	for _, val := range arr {
		sumVal += val
	}
	return sumVal / float64(len(arr))
}

/*
Calculate the current order and the drawdown distance from maximum profit
Return: Withdrawal ratio after profit, entry price, maximum profit margin
计算当前订单，距离最大盈利的回撤
返回：盈利后回撤比例，入场价格，最大利润率
*/
func (s *StratJob) getMaxTp(od *ormo.InOutOrder) (float64, float64, float64) {
	entPrice := od.Enter.Average
	if entPrice == 0 {
		entPrice = od.InitPrice
	}
	exmPrice, ok := s.TPMaxs[od.ID]
	if !ok {
		exmPrice = od.InitPrice
	}
	var cmp func(float64, float64) float64
	var price float64
	if od.Short {
		price = s.Env.Low.Get(0)
		cmp = func(f float64, f2 float64) float64 {
			return min(f, f2)
		}
	} else {
		price = s.Env.High.Get(0)
		cmp = func(f float64, f2 float64) float64 {
			return max(f, f2)
		}
	}
	exmPrice = cmp(exmPrice, price)
	s.TPMaxs[od.ID] = exmPrice
	maxTPVal := math.Abs(exmPrice - entPrice)
	maxChg := maxTPVal / entPrice
	if utils.EqualNearly(maxTPVal, 0.0) {
		return 0, entPrice, maxChg
	}
	backVal := math.Abs(exmPrice - s.Env.Close.Get(0))
	return backVal / maxTPVal, entPrice, maxChg
}

func CalcDrawDownExitRate(maxChg float64) float64 {
	var rate float64
	switch {
	case maxChg > 0.1:
		rate = 0.15
	case maxChg > 0.04:
		rate = 0.17
	case maxChg > 0.025:
		rate = 0.25
	case maxChg > 0.015:
		rate = 0.37
	case maxChg > 0.007:
		rate = 0.5
	default:
		rate = 0
	}
	return rate
}

func (s *StratJob) getDrawDownExitPrice(od *ormo.InOutOrder) float64 {
	_, entPrice, exmChg := s.getMaxTp(od)
	var stopRate = float64(-1)
	if s.Strat.GetDrawDownExitRate != nil {
		stopRate = s.Strat.GetDrawDownExitRate(s, od, exmChg)
	}
	if stopRate < 0 {
		// 如果策略返回负数，则表示使用默认算法
		stopRate = CalcDrawDownExitRate(exmChg)
	}
	if utils.EqualNearly(stopRate, 0) {
		return 0
	}
	odDirt := 1.0
	if od.Short {
		odDirt = -1
	}
	return entPrice * (1 + exmChg*(1-stopRate)*odDirt)
}

/*
drawDownExit
Check whether the tracking profit check has reached the drawdown threshold. If it exceeds the threshold, exit. This method is called by the system
按跟踪止盈检查是否达到回撤阈值，超出则退出，此方法由系统调用
*/
func (s *StratJob) drawDownExit(od *ormo.InOutOrder) *ExitReq {
	spVal := s.getDrawDownExitPrice(od)
	if spVal == 0 {
		return nil
	}
	curPrice := s.Env.Close.Get(0)
	odDirt := 1.0
	if od.Short {
		odDirt = -1
	}
	if (spVal-curPrice)*odDirt >= 0 {
		return &ExitReq{Tag: "take", OrderID: od.ID}
	}
	od.SetStopLoss(&ormo.ExitTrigger{
		Price: spVal,
		Tag:   core.ExitTagDrawDown,
	})
	return nil
}

/*
customExit
Check if the order needs to be exited, this method is called by the system
检查订单是否需要退出，此方法由系统调用
*/
func (s *StratJob) customExit(od *ormo.InOutOrder) (*ExitReq, *errs.Error) {
	var req *ExitReq
	if s.Strat.OnCheckExit != nil {
		req = s.Strat.OnCheckExit(s, od)
		if req != nil {
			req.OrderID = od.ID
		}
	}
	if req == nil && s.Strat.DrawDownExit && od.Status >= ormo.InOutStatusFullEnter {
		// 只对已完全入场的订单启用回撤平仓
		req = s.drawDownExit(od)
	}
	var err *errs.Error
	if req != nil {
		err = s.CloseOrders(req)
	}
	return req, err
}

/*
Position
Retrieve the position size and return a multiple based on the benchmark amount.
获取仓位大小，返回基于基准金额的倍数。
side long/short/空
enterTag 入场标签，可为空
*/
func (s *StratJob) Position(dirt float64, enterTag string) float64 {
	var totalCost float64
	orders := s.GetOrders(dirt)
	for _, od := range orders {
		if enterTag != "" && od.EnterTag != enterTag {
			continue
		}
		totalCost += od.HoldCost()
	}
	return totalCost / s.Strat.GetStakeAmount(s)
}

func (s *StratJob) GetOrders(dirt float64) []*ormo.InOutOrder {
	if dirt < 0 {
		return s.ShortOrders
	} else if dirt > 0 {
		return s.LongOrders
	} else {
		res := make([]*ormo.InOutOrder, 0, len(s.ShortOrders)+len(s.LongOrders))
		res = append(res, s.ShortOrders...)
		res = append(res, s.LongOrders...)
		return res
	}
}

func (s *StratJob) GetOrderNum(dirt float64) int {
	if dirt > 0 {
		return len(s.LongOrders)
	} else if dirt < 0 {
		return len(s.ShortOrders)
	} else {
		return len(s.LongOrders) + len(s.ShortOrders)
	}
}

func (s *StratJob) setAllExitTrigger(dirt float64, key string, args *ormo.ExitTrigger) {
	if s.GetOrderNum(dirt) == 0 {
		return
	}
	if dirt == 0 && len(s.LongOrders) > 0 && len(s.ShortOrders) > 0 {
		panic(fmt.Sprintf("%v SetAll%s.dirt should be 1/-1 when both long/short orders exists!", s.Strat.Name, key))
	}
	odList := s.GetOrders(dirt)
	if args == nil {
		// 取消所有止盈或止损
		for _, od := range odList {
			od.SetExitTrigger(key, nil)
		}
		return
	}
	var entOds = make([]*ormo.InOutOrder, 0, len(odList))
	var position float64
	setAll := args.Rate <= 0 || args.Rate >= 1
	for _, od := range odList {
		if od.Status >= ormo.InOutStatusPartEnter && od.Status <= ormo.InOutStatusPartExit {
			if setAll {
				od.SetExitTrigger(key, args)
			} else {
				entOds = append(entOds, od)
				position += od.HoldAmount()
			}
		}
	}
	if setAll || len(entOds) == 0 {
		return
	}
	setPos := position * args.Rate
	for _, od := range entOds {
		size := od.HoldAmount()
		if size < core.AmtDust {
			continue
		}
		if setPos < core.AmtDust {
			od.SetExitTrigger(key, nil)
		} else {
			if setPos >= size+core.AmtDust {
				od.SetExitTrigger(key, &ormo.ExitTrigger{
					Price: args.Price,
					Limit: args.Limit,
					Tag:   args.Tag,
				})
				setPos -= size
			} else {
				od.SetExitTrigger(key, &ormo.ExitTrigger{
					Price: args.Price,
					Limit: args.Limit,
					Rate:  setPos / size,
					Tag:   args.Tag,
				})
				setPos = 0
			}
		}
	}
}

func (s *StratJob) SetAllStopLoss(dirt float64, args *ormo.ExitTrigger) {
	s.setAllExitTrigger(dirt, ormo.OdInfoStopLoss, args)
}

func (s *StratJob) SetAllTakeProfit(dirt float64, args *ormo.ExitTrigger) {
	s.setAllExitTrigger(dirt, ormo.OdInfoTakeProfit, args)
}
