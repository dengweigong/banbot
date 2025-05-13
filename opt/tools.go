package opt

import (
	"encoding/csv"
	"errors"
	"flag"
	"fmt"
	"github.com/banbox/banbot/utils"
	"math"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/banbox/banbot/core"

	"github.com/banbox/banbot/biz"
	"github.com/banbox/banbot/btime"
	"github.com/banbox/banbot/config"
	"github.com/banbox/banbot/exg"
	"github.com/banbox/banbot/orm"
	"github.com/banbox/banbot/orm/ormo"
	"github.com/banbox/banexg"
	"github.com/banbox/banexg/errs"
	"github.com/banbox/banexg/log"
	utils2 "github.com/banbox/banexg/utils"
	"go.uber.org/zap"
)

/*
CompareExgBTOrders
Compare the exchange export order records with the backtest order records.
对比交易所导出订单记录和回测订单记录。
*/
func CompareExgBTOrders(args []string) error {
	var btPath, botName, account string
	var amtRate float64
	var skipUnHitBt bool
	var configPaths config.ArrString
	var sub = flag.NewFlagSet("cmp", flag.ExitOnError)
	sub.Var(&configPaths, "config", "config path to use, Multiple -config options may be used")
	sub.StringVar(&botName, "bot-name", "", "botName for live trade")
	sub.StringVar(&account, "account", "", "account for api-key to fetch exchange orders")
	sub.StringVar(&btPath, "bt-path", "", "backTest order file")
	sub.Float64Var(&amtRate, "amt-rate", 0.1, "amt rate threshold, 0~1")
	sub.BoolVar(&skipUnHitBt, "skip-unhit", true, "skip unhit pairs for backtest order")
	err_ := sub.Parse(args)
	if err_ != nil {
		return err_
	}
	if account == "" || btPath == "" || botName == "" {
		return errors.New("`exg-path/account` `bt-path` bot-name is required")
	}
	core.SetRunMode(core.RunModeLive)
	err := biz.SetupComs(&config.CmdArgs{Configs: configPaths})
	if err != nil {
		return err
	}
	btOrders, pairNums, startMS, endMS, err_ := readBackTestOrders(btPath)
	if err_ != nil {
		return err_
	}
	if len(btOrders) == 0 {
		return errors.New("no batcktest orders found")
	}
	exs := orm.GetSymbolByID(int32(btOrders[0].Sid))
	exchange, err := exg.GetWith(exs.Exchange, exs.Market, "")
	if err != nil {
		return err
	}
	_, err = orm.LoadMarkets(exchange, false)
	if err != nil {
		return err
	}
	outDir := filepath.Join(config.GetDataDir(), "exgOrders")
	exgOrders := make([]*banexg.Order, 0)
	exgOrders, err = loadExgOrders(account, exs.Exchange, exs.Market, startMS, endMS, pairNums)
	if err != nil {
		return err
	}
	if len(exgOrders) == 0 {
		return errors.New("no exchange orders to compare")
	}
	log.Info("loaded exchange orders", zap.Int("num", len(exgOrders)))
	pairExgOds := buildExgOrders(exgOrders, botName)
	exgOdList := make([]*ormo.InOutOrder, 0)
	for _, odList := range pairExgOds {
		exgOdList = append(exgOdList, odList...)
	}
	outPath := fmt.Sprintf("%s/cmp_orders.csv", outDir)
	file, err_ := os.Create(outPath)
	if err_ != nil {
		return err_
	}
	defer file.Close()
	writer := csv.NewWriter(file)
	defer writer.Flush()
	heads := []string{"tag", "symbol", "timeFrame", "dirt", "entAt", "exitAt", "entPrice", "exitPrice", "Amount",
		"Fee", "Profit", "entDelay", "exitDelay", "priceDiff %", "amtDiff %", "feeDiff %",
		"profitDiff %", "profitDf", "reason"}
	if err_ = writer.Write(heads); err_ != nil {
		return err_
	}
	for _, iod := range btOrders {
		tfMSecs := int64(utils2.TFToSecs(iod.Timeframe) * 1000)
		tfMSecsFlt := float64(tfMSecs)
		entFixMS := utils2.AlignTfMSecs(iod.RealEnterMS(), tfMSecs)
		exgOds, _ := pairExgOds[iod.Symbol]
		if skipUnHitBt && len(exgOds) == 0 {
			continue
		}
		dirt := "long"
		if iod.Short {
			dirt = "short"
		}
		// Find out if there are matching exchange orders
		// 查找是否有匹配的交易所订单
		var matches []*ormo.InOutOrder
		for _, exod := range exgOds {
			if exod.Short == iod.Short && math.Abs(float64(exod.RealEnterMS()-entFixMS)) < tfMSecsFlt {
				amtRate2 := exod.Enter.Filled / iod.Enter.Filled
				if math.Abs(amtRate2-1) <= amtRate {
					matches = append(matches, exod)
				}
			}
		}
		var matOd *ormo.InOutOrder
		if len(matches) > 1 {
			slices.SortFunc(matches, func(a, b *ormo.InOutOrder) int {
				diffA := math.Abs(float64(a.RealExitMS()-iod.RealExitMS()) / 1000)
				diffB := math.Abs(float64(b.RealExitMS()-iod.RealExitMS()) / 1000)
				return int(diffA - diffB)
			})
		}
		if len(matches) > 0 {
			matOd = matches[0]
			unMatches := make([]*ormo.InOutOrder, 0, len(exgOds))
			for _, exod := range exgOds {
				if exod == matOd {
					continue
				}
				unMatches = append(unMatches, exod)
			}
			pairExgOds[iod.Symbol] = unMatches
		}
		if matOd == nil {
			// There is no corresponding record for backtesting orders
			// 回测订单没有对应记录
			entMSStr := btime.ToDateStrLoc(iod.RealEnterMS(), "")
			exitMSStr := btime.ToDateStrLoc(iod.RealExitMS(), "")
			entPriceStr := strconv.FormatFloat(iod.Enter.Price, 'f', 6, 64)
			amtStr := strconv.FormatFloat(iod.Enter.Filled+iod.Exit.Filled, 'f', 6, 64)
			feeStr := strconv.FormatFloat(iod.Enter.Fee+iod.Exit.Fee, 'f', 6, 64)
			exitPriceStr := strconv.FormatFloat(iod.Exit.Price, 'f', 6, 64)
			profitStr := strconv.FormatFloat(iod.Profit, 'f', 6, 64)
			err_ = writer.Write([]string{"bt", iod.Symbol, iod.Timeframe, dirt, entMSStr, exitMSStr, entPriceStr,
				exitPriceStr, amtStr, feeStr, profitStr, "0", "0", "", "", "", "", "", ""})
			if err_ != nil {
				log.Error("writer csv fail", zap.Error(err_))
			}
		} else {
			// 有匹配记录
			if matOd.Exit == nil {
				matOd.Exit = &ormo.ExOrder{}
			}
			entMSStr := btime.ToDateStrLoc(matOd.RealEnterMS(), "")
			exitMSStr := btime.ToDateStrLoc(matOd.RealExitMS(), "")
			entPriceStr := strconv.FormatFloat(matOd.Enter.Average, 'f', 6, 64)
			amtStr := strconv.FormatFloat(matOd.Enter.Filled+matOd.Exit.Filled, 'f', 6, 64)
			feeStr := strconv.FormatFloat(matOd.Enter.Fee+matOd.Exit.Fee, 'f', 6, 64)
			profitStr := strconv.FormatFloat(matOd.Profit, 'f', 6, 64)
			exitPriceStr := strconv.FormatFloat(matOd.Exit.Average, 'f', 6, 64)
			entDelay := matOd.RealEnterMS() - iod.RealEnterMS()
			exitDelay := matOd.RealExitMS() - iod.RealExitMS()
			entDelayStr := strconv.FormatInt(entDelay/1000, 10)
			exitDelayStr := strconv.FormatInt(exitDelay/1000, 10)
			priceDf := (matOd.Enter.Average - iod.Enter.Average) - (matOd.Exit.Average - iod.Exit.Average)
			priceDiff := strconv.FormatFloat(priceDf*100/iod.Enter.Average, 'f', 1, 64)
			amtDf := (matOd.Enter.Filled - iod.Enter.Filled) - (matOd.Exit.Filled - iod.Exit.Filled)
			amountDiff := strconv.FormatFloat(amtDf*100/iod.Enter.Filled, 'f', 1, 64)
			feeDf := (matOd.Enter.Fee - iod.Enter.Fee) + (matOd.Exit.Fee - iod.Exit.Fee)
			feeDiff := strconv.FormatFloat(feeDf*50/iod.Enter.Fee, 'f', 1, 64)
			profitDf := matOd.Profit - iod.Profit
			profitDfPct := profitDf * 100 / iod.Profit
			profitDiff := strconv.FormatFloat(profitDfPct, 'f', 1, 64)
			profitDfStr := strconv.FormatFloat(profitDf, 'f', 6, 64)
			reason := "OK"
			if math.Abs(float64(entDelay)) < tfMSecsFlt && math.Abs(float64(exitDelay)) < tfMSecsFlt {
				// The time of entry and exit is matched
				// 入场和出场的时间匹配
				if math.Abs(profitDfPct) < 20 {
					reason = "OK"
				} else {
					reason = "Slop"
				}
			} else {
				reason = "Wrong"
			}
			err_ = writer.Write([]string{"same", iod.Symbol, iod.Timeframe, dirt, entMSStr, exitMSStr, entPriceStr,
				exitPriceStr, amtStr, feeStr, profitStr, entDelayStr, exitDelayStr, priceDiff, amountDiff,
				feeDiff, profitDiff, profitDfStr, reason})
			if err_ != nil {
				log.Error("writer csv fail", zap.Error(err_))
			}
		}
	}
	// append unmatch exchange orders
	for _, odList := range pairExgOds {
		for _, iod := range odList {
			dirt := "long"
			if iod.Short {
				dirt = "short"
			}
			if iod.Exit == nil {
				iod.Exit = &ormo.ExOrder{}
			}
			entMSStr := btime.ToDateStrLoc(iod.RealEnterMS(), "")
			exitMSStr := btime.ToDateStrLoc(iod.RealExitMS(), "")
			entPriceStr := strconv.FormatFloat(iod.Enter.Average, 'f', 6, 64)
			amtStr := strconv.FormatFloat(iod.Enter.Filled+iod.Exit.Filled, 'f', 6, 64)
			feeStr := strconv.FormatFloat(iod.Enter.Fee+iod.Exit.Fee, 'f', 6, 64)
			profitStr := strconv.FormatFloat(iod.Profit, 'f', 6, 64)
			exitPriceStr := strconv.FormatFloat(iod.Exit.Average, 'f', 6, 64)
			err_ = writer.Write([]string{"exg", iod.Symbol, iod.Timeframe, dirt, entMSStr, exitMSStr, entPriceStr,
				exitPriceStr, amtStr, feeStr, profitStr, "0", "0", "", "", "", "", "", ""})
			if err_ != nil {
				log.Error("writer csv fail", zap.Error(err_))
			}
		}
	}
	log.Info("dump compare result", zap.String("at", outPath))
	// write raw exchange orders
	outPath = fmt.Sprintf("%s/exg_orders_raw.csv", outDir)
	rows := make([][]string, 0, len(exgOrders)+1)
	rows = append(rows, []string{"symbol", "orderId", "dateTime", "status", "type", "timeInForce", "pos", "side",
		"price", "average", "amount", "filled", "cost", "reduceOnly", "fee"})
	for _, od := range exgOrders {
		price := strconv.FormatFloat(od.Price, 'f', -1, 64)
		average := strconv.FormatFloat(od.Average, 'f', -1, 64)
		amount := strconv.FormatFloat(od.Amount, 'f', -1, 64)
		filled := strconv.FormatFloat(od.Filled, 'f', -1, 64)
		cost := strconv.FormatFloat(od.Cost, 'f', -1, 64)
		reduceOnly := strconv.FormatBool(od.ReduceOnly)
		feeStr := ""
		if od.Fee != nil {
			feeStr = fmt.Sprintf("%s: %.2f", od.Fee.Currency, od.Fee.Cost)
		}
		rows = append(rows, []string{
			od.Symbol, od.ClientOrderID, od.Datetime, od.Status, od.Type,
			od.TimeInForce, od.PositionSide, od.Side,
			price, average, amount, filled, cost, reduceOnly, feeStr,
		})
	}
	err = utils.WriteCsvFile(outPath, rows, false)
	if err != nil {
		return err
	}
	log.Info("dump exchange raw orders", zap.String("at", outPath))
	outPath = fmt.Sprintf("%s/exg_orders.csv", outDir)
	log.Info("dump exchange orders", zap.String("at", outPath))
	return DumpOrdersCSV(exgOdList, outPath)
}

func loadExgOrders(account, exgName, market string, startMS, endMS int64, pairNums map[string]int) ([]*banexg.Order, *errs.Error) {
	save, err := biz.GetExgOrderSet(account, exgName, market)
	if err != nil {
		return nil, err
	}
	pairs := utils.KeysOfMap(pairNums)
	err = save.Download(startMS, endMS, pairs, true)
	if err != nil {
		return nil, err
	}
	var pairOrders map[string][]*banexg.Order
	pairOrders, err = save.Get(startMS, endMS, pairs, "")
	if err != nil {
		return nil, err
	}
	var exgOrders []*banexg.Order
	for _, odList := range pairOrders {
		exgOrders = append(exgOrders, odList...)
	}
	sort.Slice(exgOrders, func(i, j int) bool {
		return exgOrders[i].Timestamp < exgOrders[j].Timestamp
	})
	return exgOrders, nil
}

func readBackTestOrders(path string) ([]*ormo.InOutOrder, map[string]int, int64, int64, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, nil, 0, 0, err
	}
	if info.IsDir() {
		path = filepath.Join(path, "orders.gob")
	} else if !strings.HasSuffix(path, ".gob") {
		return nil, nil, 0, 0, errors.New("orders.gob path is required")
	}
	orders, err2 := ormo.LoadOrdersGob(path)
	if err2 != nil {
		return nil, nil, 0, 0, err2
	}
	var startMS, endMS int64
	var startTFSecs int
	var maxTfSecs int
	var pairNums = make(map[string]int)
	for _, od := range orders {
		tfSecs := utils2.TFToSecs(od.Timeframe)
		if tfSecs > maxTfSecs {
			maxTfSecs = tfSecs
		}
		curEntMS := od.RealEnterMS()
		if startMS == 0 || curEntMS < startMS {
			startMS = curEntMS
			startTFSecs = tfSecs
		}
		if od.Exit != nil && od.Exit.UpdateAt > endMS {
			endMS = od.Exit.UpdateAt
		}
		num, _ := pairNums[od.Symbol]
		pairNums[od.Symbol] = num + 1
	}
	// 初始时间向前移动半个周期，防止部分订单未记录
	startMS -= int64(startTFSecs * 500)
	// Move the end time back by 2 bars to prevent the exchange order section from being filtered
	// 将结束时间，往后推移2个bar，防止交易所订单部分被过滤
	endMS += int64(maxTfSecs*1000) * 2
	return orders, pairNums, startMS, endMS, nil
}

/*
handleExitOrders 处理平仓订单，返回更新后的订单列表和已使用的平仓数量
remainFilled: 剩余需要平仓的数量
od: 交易所订单
odList: 当前持仓订单列表
excludeOd: 需要排除的订单（已经通过ClientOrderID匹配的订单）
返回值: 更新后的订单列表, 已使用的平仓数量
*/
func handleExitOrders(remainFilled float64, od *banexg.Order, odList []*ormo.InOutOrder, excludeOd *ormo.InOutOrder) ([]*ormo.InOutOrder, float64) {
	newList := make([]*ormo.InOutOrder, 0, len(odList))
	usedAmount := float64(0)
	initAmt := remainFilled
	for _, iod := range odList {
		if iod == excludeOd || iod.Exit != nil || iod.Enter.Side == od.Side {
			newList = append(newList, iod)
			continue
		}
		part := iod
		curFee := od.Fee.Cost
		exitAmount := iod.Enter.Amount
		if remainFilled < iod.Enter.Amount*0.99 {
			exitAmount = remainFilled
			part = iod.CutPart(remainFilled, remainFilled)
			rate := remainFilled / od.Filled
			curFee = od.Fee.Cost * rate
		} else {
			rate := iod.Enter.Amount / od.Filled
			curFee = od.Fee.Cost * rate
		}
		part.ExitAt = od.LastUpdateTimestamp
		part.Exit = &ormo.ExOrder{
			Symbol:   part.Symbol,
			CreateAt: od.LastUpdateTimestamp,
			UpdateAt: od.LastUpdateTimestamp,
			Price:    od.Price,
			Average:  od.Average,
			Amount:   part.Enter.Amount,
			Filled:   part.Enter.Filled,
			Fee:      curFee,
		}
		part.Status = ormo.InOutStatusFullExit
		part.UpdateProfits(od.Average)
		remainFilled -= exitAmount
		usedAmount += exitAmount
		newList = append(newList, part)
		if part != iod {
			newList = append(newList, iod)
		}
		if remainFilled <= initAmt*0.01 {
			break
		}
	}
	return newList, usedAmount
}

/*
buildExgOrders
Construct an InOutOrder from an exchange order for comparison; It is not used for real/backtesting
从交易所订单构建InOutOrder用于对比；非实盘/回测时使用
*/
func buildExgOrders(ods []*banexg.Order, clientPrefix string) map[string][]*ormo.InOutOrder {
	// 按ClientOrderID分组订单
	orderMap := make(map[string][]*banexg.Order)
	for _, od := range ods {
		if od.Filled == 0 {
			continue
		}
		orderMap[od.ClientOrderID] = append(orderMap[od.ClientOrderID], od)
	}
	jobMap := make(map[string][]*ormo.InOutOrder)
	var temps []*banexg.Order

	for _, orders := range orderMap {
		if len(orders) != 2 {
			// 不是配对订单，加入temps
			temps = append(temps, orders...)
			continue
		}

		// 确保orders[0]是较早的订单
		if orders[0].LastTradeTimestamp > orders[1].LastTradeTimestamp {
			orders[0], orders[1] = orders[1], orders[0]
		}

		// 检查是否是一买一卖
		if orders[0].Side == orders[1].Side {
			temps = append(temps, orders...)
			continue
		}
		ent, exit := orders[0], orders[1]
		if !strings.HasPrefix(ent.ClientOrderID, clientPrefix) || !strings.HasPrefix(exit.ClientOrderID, clientPrefix) {
			temps = append(temps, orders...)
			continue
		}

		// 创建InOutOrder
		iod := &ormo.InOutOrder{
			IOrder: &ormo.IOrder{
				Symbol:  ent.Symbol,
				Short:   ent.Side == banexg.OdSideSell,
				EnterAt: ent.LastTradeTimestamp,
				ExitAt:  exit.LastTradeTimestamp,
				Status:  ormo.InOutStatusFullExit,
			},
			Enter: &ormo.ExOrder{
				Enter:    true,
				Symbol:   ent.Symbol,
				Side:     ent.Side,
				CreateAt: ent.LastTradeTimestamp,
				UpdateAt: ent.LastTradeTimestamp,
				Price:    ent.Price,
				Average:  ent.Average,
				Amount:   ent.Filled,
				Filled:   ent.Filled,
				Fee:      ent.Fee.Cost,
				OrderID:  ent.ClientOrderID,
			},
			Exit: &ormo.ExOrder{
				Symbol:   exit.Symbol,
				CreateAt: exit.LastTradeTimestamp,
				UpdateAt: exit.LastTradeTimestamp,
				Price:    exit.Price,
				Average:  exit.Average,
				Amount:   exit.Filled, // 使用入场订单的数量
				Filled:   exit.Filled,
				Fee:      exit.Fee.Cost,
			},
		}

		// 将InOutOrder添加到结果map
		jobMap[ent.Symbol] = append(jobMap[ent.Symbol], iod)

		inoutRate := exit.Filled / ent.Filled
		if inoutRate > 1.01 {
			// 平仓数量未耗尽，同时平仓其他订单
			remainOrder := *exit
			remainOrder.Filled = exit.Filled - ent.Filled
			remainOrder.Fee.Cost = exit.Fee.Cost * (remainOrder.Filled / exit.Filled)
			temps = append(temps, &remainOrder)
			iod.Exit.Amount = ent.Filled
			iod.Exit.Filled = ent.Filled
			iod.Exit.Fee = exit.Fee.Cost * (ent.Fee.Cost / exit.Fee.Cost)
		} else if inoutRate < 0.99 {
			// 未完全平仓
			remainOrder := *ent
			remainOrder.Filled = ent.Filled - exit.Filled
			remainOrder.Fee.Cost = ent.Fee.Cost * (remainOrder.Filled / ent.Filled)
			temps = append(temps, &remainOrder)
			iod.Enter.Amount = exit.Filled
			iod.Enter.Filled = exit.Filled
			iod.Enter.Fee = ent.Fee.Cost * (exit.Fee.Cost / ent.Fee.Cost)
		}
		iod.UpdateProfits(exit.Average)
	}

	// 处理未配对订单
	if len(temps) > 0 {
		// 按时间排序temps
		sort.Slice(temps, func(i, j int) bool {
			return temps[i].LastTradeTimestamp < temps[j].LastTradeTimestamp
		})

		// 对每个未配对订单执行原来的逻辑
		for _, od := range temps {
			odList, _ := jobMap[od.Symbol]
			newList := make([]*ormo.InOutOrder, 0, len(odList))
			remainFilled := od.Filled

			// 尝试平仓现有持仓
			var usedAmount float64
			newList, usedAmount = handleExitOrders(remainFilled, od, odList, nil)
			remainFilled -= usedAmount

			jobMap[od.Symbol] = newList
			if remainFilled <= od.Filled*0.01 || !strings.HasPrefix(od.ClientOrderID, clientPrefix) {
				continue
			}

			// 创建新的入场订单
			iod := &ormo.InOutOrder{
				IOrder: &ormo.IOrder{
					Symbol:  od.Symbol,
					Short:   od.Side == banexg.OdSideSell,
					EnterAt: od.LastTradeTimestamp,
					Status:  ormo.InOutStatusFullEnter,
				},
				Enter: &ormo.ExOrder{
					Enter:    true,
					Symbol:   od.Symbol,
					Side:     od.Side,
					CreateAt: od.LastTradeTimestamp,
					UpdateAt: od.LastTradeTimestamp,
					Price:    od.Price,
					Average:  od.Average,
					Amount:   remainFilled,
					Filled:   remainFilled,
					Fee:      od.Fee.Cost * (remainFilled / od.Filled),
					OrderID:  od.ClientOrderID,
				},
			}
			jobMap[od.Symbol] = append(newList, iod)
		}
	}

	return jobMap
}

type AssetData struct {
	Title    string     `json:"title"`
	Labels   []string   `json:"labels"`
	Datasets []*ChartDs `json:"datasets"`
	Times    []int64    // 存储解析后的时间戳
}

/*
MergeAssetsHtml 合并多个assets.html文件的曲线到一个html文件中

files assets.html文件路径Map，键是路径，值是代表此文件的字符串ID
outPath 输出文件路径
lines 需要提取的曲线名称列表，默认为["Real", "Available"]
*/
func MergeAssetsHtml(outPath string, files map[string]string, tags []string, useRate bool) *errs.Error {
	if len(files) <= 1 {
		return errs.NewMsg(errs.CodeParamRequired, "at least 2 files need to merge")
	}
	if len(tags) == 0 {
		tags = []string{"Real", "Available"}
	}
	linesMap := make(map[string]bool)
	for _, line := range tags {
		linesMap[line] = true
	}

	// 读取所有文件的数据
	var allData []*AssetData
	var minTime, maxTime int64 = math.MaxInt64, 0
	for file, prefix := range files {
		data, err := readAssetHtml(file, prefix, useRate)
		if err != nil {
			return err
		}
		allData = append(allData, data)
		// 更新整体时间范围
		if len(data.Times) > 0 {
			minTime = min(minTime, data.Times[0])
			maxTime = max(maxTime, data.Times[len(data.Times)-1])
		}
	}

	// 确定采样数量和间隔
	maxSamples := 0
	for _, data := range allData {
		maxSamples = max(maxSamples, len(data.Labels))
	}

	// 生成最终的时间戳和标签
	interval := (maxTime - minTime) / int64(maxSamples-1)
	dateLay := core.DefaultDateFmt
	if interval >= utils2.SecsDay*1000 {
		dateLay = core.DateFmt
	}
	var labels = make([]string, 0, maxSamples+3)
	var finalTimes = make([]int64, 0, maxSamples+3)
	for i := 0; i < maxSamples; i++ {
		t := minTime + interval*int64(i)
		finalTimes = append(finalTimes, t)
		labels = append(labels, btime.ToDateStr(t, dateLay))
	}
	finalTimes = append(finalTimes, maxTime)
	labels = append(labels, btime.ToDateStr(maxTime, dateLay))

	// 合并数据集
	var datasets []*ChartDs
	for _, data := range allData {
		for _, ds := range data.Datasets {
			if _, ok := linesMap[ds.Label]; !ok {
				continue
			}
			values := make([]float64, 0, len(finalTimes))
			curVal := math.NaN()
			idx := 0
			nextTime, nextVal := data.Times[0], ds.Data[0]
			for _, curTime := range finalTimes {
				for curTime >= nextTime {
					curVal = nextVal
					idx += 1
					if idx < len(data.Times) {
						nextTime, nextVal = data.Times[idx], ds.Data[idx]
					} else {
						nextTime = math.MaxInt64
					}
				}
				values = append(values, curVal)
			}

			// 添加到最终数据集
			label := fmt.Sprintf("%s_%s", data.Title, ds.Label)
			datasets = append(datasets, &ChartDs{
				Label: label,
				Data:  values,
			})
		}
	}

	// 生成最终的图表
	title := "Merged Assets Comparison"
	return DumpChart(outPath, title, labels, 5, nil, datasets)
}

// readAssetHtml 读取assets.html文件并解析数据
func readAssetHtml(file, prefix string, useRate bool) (*AssetData, *errs.Error) {
	content, err := os.ReadFile(file)
	if err != nil {
		return nil, errs.New(errs.CodeIOReadFail, err)
	}

	// 提取JSON数据
	start := strings.Index(string(content), "var chartData = ") + 15
	end := strings.Index(string(content)[start:], "\n")
	if start < 15 || end < 0 {
		return nil, errs.New(errs.CodeInvalidData, fmt.Errorf("invalid html format in file %s", file))
	}
	jsonStr := string(content)[start : start+end]

	var data AssetData
	if err = utils2.UnmarshalString(jsonStr, &data, utils2.JsonNumDefault); err != nil {
		return nil, errs.New(errs.CodeUnmarshalFail, err)
	}

	// 解析时间标签为时间戳
	data.Times = make([]int64, len(data.Labels))
	for i, label := range data.Labels {
		data.Times[i], err = btime.ParseTimeMS(label)
		if err != nil {
			return nil, errs.New(errs.CodeRunTime, err)
		}
	}
	if prefix == "" {
		prefix = filepath.Base(filepath.Dir(file))
	}
	data.Title = prefix
	if useRate {
		for _, ds := range data.Datasets {
			initVal := ds.Data[0]
			if initVal != 0 {
				resArr := make([]float64, 0, len(ds.Data))
				for _, curVal := range ds.Data {
					resArr = append(resArr, curVal/initVal)
				}
			}
		}
	}

	return &data, nil
}

type FacArgs struct {
	Pairs        []*PairStat
	AvgOrderCost float64 // Avg Enter Cost for all orders
	TimeFrame    string
	TFMSecs      int64
	StartMS      int64
	EndMS        int64
	MinBack      string // 最小回顾历史周期
	MaxBack      string // 最大回顾历史周期
	Interval     string // 重新轮动的间隔
	MinBackMS    int64
	MaxBackMS    int64
	IntervalMS   int64
}

var (
	FactorMap = make(map[string]func(FacArgs) ([]string, error))
)

/*
BtFactors 从全品种回测订单，对给定的截面因子进行滚动回测，输出回测结果到控制台和目录
*/
func BtFactors(args []string) error {
	var configPaths config.ArrString
	var factor, inPath, outDir, minBack, maxBack, runPeriod string
	var downKline bool
	var sub = flag.NewFlagSet("fac", flag.ExitOnError)
	sub.Var(&configPaths, "config", "config path to use, Multiple -config options may be used")
	sub.StringVar(&factor, "factor", "", "factor to test")
	sub.StringVar(&inPath, "in", "", "path for all pairs orders e.g.: orders.gob")
	sub.StringVar(&outDir, "out", "", "dump result to directory")
	sub.StringVar(&minBack, "min-back", "1y", "min look back period")
	sub.StringVar(&maxBack, "max-back", "2y", "max look back period")
	sub.StringVar(&runPeriod, "interval", "4M", "interval time range between refreshes")
	sub.BoolVar(&downKline, "down", false, "try download klines")
	err_ := sub.Parse(args)
	if err_ != nil {
		return err_
	}
	if outDir != "" {
		outDir = config.ParsePath(outDir)
		err_ = utils.EnsureDir(outDir, 0755)
		if err_ != nil {
			return err_
		}
		core.SetLogCap(filepath.Join(outDir, "out.log"))
	}
	core.SetRunMode(core.RunModeBackTest)
	err := biz.SetupComsExg(&config.CmdArgs{
		Configs: configPaths,
	})
	if err != nil {
		return err
	}
	var startMs = config.TimeRange.StartMS
	var endMS = config.TimeRange.EndMS
	if len(config.StakeCurrency) == 0 {
		return errors.New("`stake_currency` in yml is required")
	}
	var tfSecs int
	var orders []*ormo.InOutOrder
	if inPath == "" {
		return errors.New("-in is required")
	} else {
		inPath = config.ParsePath(inPath)
	}
	facFunc, ok := FactorMap[factor]
	if !ok || facFunc == nil {
		return errors.New("-factor invalid")
	}
	orders, err = ormo.LoadOrdersGob(inPath)
	if err != nil {
		return err
	}
	var exsMap = make(map[int32]*orm.ExSymbol)
	var pairMap = make(map[string]int32)
	var totalCost float64
	var validOdNum int
	if len(orders) > 0 {
		startMs = orders[0].RealEnterMS()
		endMS = orders[0].RealExitMS()
		for _, od := range orders {
			curStart := od.RealEnterMS()
			if curStart < startMs && curStart > 0 {
				startMs = curStart
			}
			curEnd := od.RealExitMS()
			if curEnd > endMS {
				endMS = curEnd
			}
			tfSecs = max(tfSecs, utils2.TFToSecs(od.Timeframe))
			curCost := od.EnterCost()
			if curCost > 0 {
				totalCost += curCost
				validOdNum += 1
			}
			if _, ok = pairMap[od.Symbol]; !ok {
				exs, err := orm.GetExSymbolCur(od.Symbol)
				if err != nil {
					return err
				}
				exsMap[exs.ID] = exs
				pairMap[od.Symbol] = exs.ID
			}
		}
	} else {
		return errors.New("orders is empty")
	}
	if validOdNum == 0 {
		return errors.New("no valid orders")
	}
	if tfSecs == 0 {
		tfSecs = utils2.TFToSecs("1d")
	}
	minBackMSecs := int64(utils2.TFToSecs(minBack) * 1000)
	maxBackMSecs := int64(utils2.TFToSecs(maxBack) * 1000)
	intvMSecs := int64(utils2.TFToSecs(runPeriod) * 1000)
	dayMSecs := int64(utils2.TFToSecs("1d") * 1000)
	if intvMSecs < dayMSecs {
		return errors.New("interval must >= 1d")
	}
	if minBackMSecs > maxBackMSecs {
		return errors.New("min-back must <= max-back")
	}
	if minBackMSecs < intvMSecs {
		return errors.New("min-back must >= interval")
	}
	// 按interval对齐开始截止时间
	totalRangeMs := endMS - startMs
	dayNum := totalRangeMs / dayMSecs
	tfMSecs := int64(tfSecs * 1000)
	if dayNum >= 90 {
		tfMSecs = dayMSecs
	}
	if totalRangeMs < minBackMSecs+intvMSecs {
		return errors.New("order time range must > min-back + interval")
	}
	tf := utils2.SecsToTF(int(tfMSecs / 1000))
	// 下载K线
	if downKline {
		prgTotal := 10000
		pBar := utils.NewPrgBar(prgTotal, "BulkDown")
		err = orm.BulkDownOHLCV(exg.Default, exsMap, tf, startMs, endMS, 0, func(done int, total int) {
			newProgress := int64(prgTotal) * int64(done) / int64(total)
			add := newProgress - pBar.Last
			if add > 0 {
				pBar.Last = newProgress
				pBar.Add(int(add))
			}
		})
		if err != nil {
			return err
		}
	}
	// 滚动测试因子
	avgCost := totalCost / float64(validOdNum)
	rangeStart := startMs
	rangeEnd := startMs + minBackMSecs
	var testOrders []*ormo.InOutOrder
	for rangeEnd+intvMSecs/5 < endMS {
		pairOrders, err := CutOrdersInRange(orders, rangeStart, rangeEnd)
		if err != nil {
			return err
		}
		pairInfos, err := CalcPairStats(pairOrders, rangeStart, rangeEnd, tf)
		if err != nil {
			return err
		}
		pairs, err_ := facFunc(FacArgs{
			Pairs:        pairInfos,
			AvgOrderCost: avgCost,
			TimeFrame:    tf,
			TFMSecs:      tfMSecs,
			StartMS:      rangeStart,
			EndMS:        rangeEnd,
			MinBack:      minBack,
			MinBackMS:    minBackMSecs,
			MaxBack:      maxBack,
			MaxBackMS:    maxBackMSecs,
			Interval:     runPeriod,
			IntervalMS:   intvMSecs,
		})
		if err_ != nil {
			return err_
		}
		startDate := btime.ToDateStr(rangeStart, core.DefaultDateFmt)
		endDate := btime.ToDateStr(rangeEnd, core.DefaultDateFmt)
		rangeStr := fmt.Sprintf("%s-%s", startDate, endDate)
		log.Info("select pairs", zap.String("range", rangeStr), zap.Strings("arr", pairs))
		// 使用选中品种，交易interval时间段
		pairOrders, err = CutOrdersInRange(orders, rangeEnd, rangeEnd+intvMSecs)
		if err != nil {
			return err
		}
		for _, s := range pairs {
			if v, ok := pairOrders[s]; ok {
				testOrders = append(testOrders, v...)
			}
		}
		rangeEnd += intvMSecs
		if rangeEnd-rangeStart > maxBackMSecs {
			rangeStart = rangeEnd - maxBackMSecs
		}
	}
	_, err = calcBtResult(testOrders, config.WalletAmounts, outDir)
	if err != nil {
		return err
	}
	return nil
}

func CutOrdersInRange(orders []*ormo.InOutOrder, startMS, endMS int64) (map[string][]*ormo.InOutOrder, *errs.Error) {
	pairOrders := make(map[string][]*ormo.InOutOrder)
	cloneIds := make(map[int64]bool)
	for _, od := range orders {
		enterMS := od.RealEnterMS()
		exitMS := od.RealExitMS()
		if enterMS >= endMS || exitMS > 0 && exitMS <= startMS {
			continue
		}
		var curOd *ormo.InOutOrder
		if enterMS >= startMS && exitMS <= endMS {
			curOd = od
		} else {
			curOd = od.Clone()
			cloneIds[curOd.ID] = true
		}
		items, _ := pairOrders[od.Symbol]
		pairOrders[od.Symbol] = append(items, curOd)
	}
	for pair, items := range pairOrders {
		tfMap := make(map[string]int)
		minTF, minTfSecs := "", 0
		for _, od := range items {
			if _, ok := tfMap[od.Timeframe]; !ok {
				secs := utils2.TFToSecs(od.Timeframe)
				tfMap[od.Timeframe] = secs
				if minTF == "" || secs < minTfSecs {
					minTfSecs = secs
					minTF = od.Timeframe
				}
			}
		}
		tfMSecs := int64(minTfSecs * 1000)
		exs := orm.GetExSymbol2(core.ExgName, core.Market, pair)
		_, bars, err := orm.GetOHLCV(exs, minTF, startMS, endMS, 1, false)
		if err != nil {
			return nil, err
		}
		if len(bars) == 0 {
			continue
		}
		priceOpen := bars[0].Open
		openMS := bars[0].Time
		_, bars, err = orm.GetOHLCV(exs, minTF, 0, endMS, 1, false)
		if err != nil {
			return nil, err
		}
		if len(bars) == 0 {
			return nil, errs.NewMsg(errs.CodeRunTime, "no kline before %v -%v", pair, endMS)
		}
		last := bars[len(bars)-1]
		priceClose := last.Close
		closeMS := last.Time + tfMSecs
		for _, od := range items {
			if _, ok := cloneIds[od.ID]; !ok {
				continue
			}
			// 订单持仓超过时间区间，使用首尾价格重新计算
			amtRate := float64(1)
			if od.RealEnterMS() < openMS {
				od.InitPrice = priceOpen
				if od.Enter != nil {
					curAmt := od.EnterCost() / priceOpen
					amtRate = curAmt / od.Enter.Filled
					od.Enter.Filled = curAmt
					od.Enter.Amount = curAmt
					od.Enter.CreateAt = openMS
					od.Enter.UpdateAt = openMS
					od.Enter.Price = priceOpen
					od.Enter.Average = priceOpen
				}
			}
			if od.Exit != nil {
				if amtRate != 1 {
					od.Exit.Amount *= amtRate
					od.Exit.Filled *= amtRate
				}
				if od.RealExitMS() > closeMS {
					od.Exit.CreateAt = closeMS
					od.Exit.UpdateAt = closeMS
					od.Exit.Price = priceClose
					od.Exit.Average = priceClose
				}
			}
			od.UpdateProfits(priceClose)
		}
	}
	return pairOrders, nil
}

func BuildBtResult(args *config.CmdArgs) *errs.Error {
	core.SetRunMode(core.RunModeBackTest)
	if args.InPath == "" {
		return errs.NewMsg(errs.CodeRunTime, "-in for orders.gob is required")
	}
	outDir := config.ParsePath(args.OutPath)
	if outDir != "" {
		err_ := utils.EnsureDir(outDir, 0755)
		if err_ != nil {
			return errs.New(errs.CodeIOWriteFail, err_)
		}
		core.SetLogCap(filepath.Join(outDir, "out.log"))
	}
	err := biz.SetupComsExg(args)
	if err != nil {
		return err
	}
	inPath := config.ParsePath(args.InPath)
	orders, err := ormo.LoadOrdersGob(inPath)
	if err != nil {
		return err
	}
	_, err = calcBtResult(orders, config.WalletAmounts, outDir)
	return err
}
