package biz

import (
	"archive/zip"
	"context"
	"encoding/csv"
	"encoding/gob"
	"errors"
	"flag"
	"fmt"
	"github.com/sasha-s/go-deadlock"
	"math"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/banbox/banbot/btime"
	"github.com/banbox/banbot/config"
	"github.com/banbox/banbot/core"
	"github.com/banbox/banbot/data"
	"github.com/banbox/banbot/exg"
	"github.com/banbox/banbot/goods"
	"github.com/banbox/banbot/orm"
	"github.com/banbox/banbot/utils"
	"github.com/banbox/banexg"
	"github.com/banbox/banexg/errs"
	"github.com/banbox/banexg/log"
	utils2 "github.com/banbox/banexg/utils"
	"go.uber.org/zap"
)

func LoadZipKline(inPath string, fid int, file *zip.File, arg interface{}) *errs.Error {
	cleanName := strings.Split(filepath.Base(file.Name), ".")[0]
	exArgs := arg.([]string)
	exgName, market := exArgs[0], exArgs[1]
	exchange, err := exg.GetWith(exgName, market, exArgs[2])
	if err != nil {
		return err
	}
	yearStr := strings.Split(filepath.Base(inPath), ".")[0]
	year, _ := strconv.Atoi(yearStr)
	mar, err := exchange.MapMarket(cleanName, year)
	if err != nil {
		log.Warn("symbol invalid", zap.String("id", cleanName), zap.String("err", err.Short()))
		return nil
	}
	exs := &orm.ExSymbol{Symbol: mar.Symbol, Exchange: exgName, ExgReal: mar.ExgReal, Market: market}
	err = orm.EnsureSymbols([]*orm.ExSymbol{exs})
	if err != nil {
		return err
	}
	fReader, err_ := file.Open()
	if err_ != nil {
		return errs.New(errs.CodeIOReadFail, err_)
	}
	rows, err_ := csv.NewReader(fReader).ReadAll()
	if err_ != nil {
		return errs.New(errs.CodeIOReadFail, err_)
	}
	if len(rows) <= 1 {
		return nil
	}
	klines := make([]*banexg.Kline, 0, len(rows))
	lastMS := int64(0)
	tfMSecs := int64(math.MaxInt64)
	for _, r := range rows {
		barTime, _ := strconv.ParseInt(r[0], 10, 64)
		o, _ := strconv.ParseFloat(r[1], 64)
		h, _ := strconv.ParseFloat(r[2], 64)
		l, _ := strconv.ParseFloat(r[3], 64)
		c, _ := strconv.ParseFloat(r[4], 64)
		v, _ := strconv.ParseFloat(r[5], 64)
		var d float64
		if len(r) > 6 {
			d, _ = strconv.ParseFloat(r[6], 64)
		}
		if barTime == 0 {
			continue
		}
		timeDiff := barTime - lastMS
		lastMS = barTime
		if timeDiff > 0 && timeDiff < tfMSecs {
			tfMSecs = timeDiff
		}
		klines = append(klines, &banexg.Kline{
			Time:   barTime,
			Open:   o,
			High:   h,
			Low:    l,
			Close:  c,
			Volume: v,
			Info:   d,
		})
	}
	sort.Slice(klines, func(i, j int) bool {
		return klines[i].Time < klines[j].Time
	})
	startMS, endMS := klines[0].Time, klines[len(klines)-1].Time
	timeFrame := utils2.SecsToTF(int(tfMSecs / 1000))
	timeFrame, err = orm.GetDownTF(timeFrame)
	if err != nil {
		log.Warn("get down tf fail", zap.Int64("ms", tfMSecs), zap.String("id", exs.Symbol),
			zap.String("path", inPath), zap.String("err", err.Short()))
		return nil
	}
	tfMSecs = int64(utils2.TFToSecs(timeFrame) * 1000)
	ctx := context.Background()
	sess, conn, err := orm.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Release()
	// Filter non-trading time periods and trading volumes of 0
	// Since some trading days are not recorded in the historical data, the trading calendar is not applicable to filter K-lines
	// 过滤非交易时间段，成交量为0的
	// 由于历史数据中部分交易日未录入，故不适用交易日历过滤K线
	holes, err := sess.GetExSHoles(exchange, exs, startMS, endMS, true)
	if err != nil {
		return err
	}
	holeNum := len(holes)
	if holeNum > 0 {
		newKlines := make([]*banexg.Kline, 0, len(klines))
		hi := 0
		var half *banexg.Kline
		unExpNum := 0
		dayMSecs := int64(utils2.TFToSecs("1d") * 1000)
		for i, k := range klines {
			for hi < holeNum && holes[hi][1] <= k.Time {
				if unExpNum > 0 {
					h := holes[hi]
					if h[1]-h[0] >= dayMSecs {
						// Non-trading period exceeds 1 day
						// 非交易时间段超过1天
						expNum := int((h[1] - h[0]) / tfMSecs)
						if unExpNum*20 > expNum {
							// During non-trading hours, if the number of valid bars is at least 5%, a warning will be output
							// 非交易时间内，有效bar数量至少5%，则输出警告
							startStr := btime.ToDateStr(h[0], "")
							endStr := btime.ToDateStr(h[1], "")
							log.Warn("find klines in non-trade time", zap.Int32("sid", exs.ID),
								zap.Int("num", unExpNum), zap.String("start", startStr),
								zap.String("end", endStr))
						} else if unExpNum > 20 {
							half = nil
						}
					}
					unExpNum = 0
				}
				hi += 1
			}
			if hi >= holeNum {
				newKlines = append(newKlines, klines[i:]...)
				break
			}
			if half != nil {
				// There are extra ones in front, merge them together.
				// 有前面额外的，合并到一起
				if k.High < half.High {
					k.High = half.High
				}
				if k.Low > half.Low {
					k.Low = half.Low
				}
				k.Open = half.Open
				k.Volume += half.Volume
				half = nil
			}
			h := holes[hi]
			if k.Time < h[0] {
				// 有效时间内
				newKlines = append(newKlines, k)
			} else if k.Volume > 0 {
				// During non-trading hours, but there is trading volume, it will be merged into the most recent valid bar.
				// 非交易时间段内，但有成交量，合并到最近有效bar
				unExpNum += 1
				if h[1]-k.Time < k.Time-h[0] {
					//离后面更近，合并到下一个
					if h[1]-k.Time < tfMSecs*10 {
						half = k
					}
				} else if len(newKlines) > 0 {
					// 离前面更近，合并到上一个（最多10个）
					p := newKlines[len(newKlines)-1]
					if k.Time-p.Time < tfMSecs*10 {
						if p.High < k.High {
							p.High = k.High
						}
						if p.Low > k.Low {
							p.Low = k.Low
						}
						p.Close = k.Close
						p.Volume += k.Volume
						p.Info = k.Info
					}
				}
			}
		}
		if len(newKlines) == 0 {
			return nil
		}
		klines = newKlines
	}
	oldStart, oldEnd := sess.GetKlineRange(exs.ID, timeFrame)
	if oldStart <= startMS && endMS <= oldEnd {
		// 都已存在，无需写入
		return nil
	}
	if oldStart > 0 {
		newKlines := make([]*banexg.Kline, 0, len(klines))
		for _, k := range klines {
			if k.Time < oldStart || k.Time >= oldEnd {
				newKlines = append(newKlines, k)
			}
		}
		if len(newKlines) == 0 {
			return nil
		}
		klines = newKlines
	}
	startMS, endMS = klines[0].Time, klines[len(klines)-1].Time
	startDt := btime.ToDateStr(startMS, "")
	endDt := btime.ToDateStr(endMS, "")
	log.Info("insert", zap.String("symbol", exs.Symbol), zap.Int32("sid", exs.ID),
		zap.Int("num", len(klines)), zap.String("start", startDt), zap.String("end", endDt))
	// 这里不可使用数据库默认的归集策略，因有些bar成交量为0；应调用BuildOHLCVOff归集
	// the database default aggregation strategy cannot be used here, because some bar volumes are 0; BuildOHLCVOff aggregation should be called
	num, err := sess.InsertKLinesAuto(timeFrame, exs, klines, false)
	if err == nil && num > 0 {
		// insert data for big timeframes 插入更大周期
		return aggBigKlines(sess, klines, tfMSecs, exs)
	}
	return err
}

func aggBigKlines(sess *orm.Queries, klines []*banexg.Kline, tfMSecs int64, exs *orm.ExSymbol) *errs.Error {
	if len(klines) == 0 {
		return nil
	}
	aggList := orm.GetKlineAggs()
	klines1m := klines
	var err *errs.Error
	var num int64
	infoBy := exs.InfoBy()
	for _, agg := range aggList[1:] {
		if agg.MSecs <= tfMSecs {
			continue
		}
		offMS := int64(exg.GetAlignOff(exs.Exchange, int(agg.MSecs/1000)) * 1000)
		klines, _ = utils.BuildOHLCV(klines1m, agg.MSecs, 0, nil, tfMSecs, offMS, infoBy)
		if len(klines) == 0 {
			continue
		}
		endMs := klines[len(klines)-1].Time + agg.MSecs
		err = sess.DelKLines(exs.ID, agg.TimeFrame, klines[0].Time, endMs)
		if err != nil {
			return err
		}
		num, err = sess.InsertKLinesAuto(agg.TimeFrame, exs, klines, false)
		if err != nil {
			return err
		}
		if num == 0 {
			break
		}
	}
	return nil
}

func AggBigKlines(args *config.CmdArgs) *errs.Error {
	minTF := "1m"
	if len(args.TimeFrames) > 0 {
		minTF = args.TimeFrames[0]
	}
	log.Info("try agg timeFrames above " + minTF)
	pairs := args.Pairs
	if len(pairs) == 0 && len(config.Pairs) > 0 {
		pairs = config.Pairs
	}
	exsMap := orm.GetExSymbolMap(core.ExgName, core.Market)
	if len(pairs) == 0 {
		if !utils.ReadConfirm([]string{
			fmt.Sprintf("agg for %v symbols, input `y` to continue", len(exsMap)),
		}, "y", "n", true) {
			return nil
		}
	} else {
		keeps := make(map[string]*orm.ExSymbol)
		for _, pair := range pairs {
			exs, ok := exsMap[pair]
			if !ok {
				return errs.NewMsg(errs.CodeParamInvalid, "%v is invalid", pair)
			}
			keeps[pair] = exs
		}
		exsMap = keeps
	}
	ctx := context.Background()
	sess, conn, err := orm.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Release()
	minMSecs := int64(utils2.TFToSecs(minTF) * 1000)
	yearMSecs := int64(utils2.TFToSecs("1y") * 1000)
	startMS, endMS := config.TimeRange.StartMS, config.TimeRange.EndMS
	firstEndMS := utils2.AlignTfMSecs(startMS+yearMSecs, yearMSecs)
	pBar := utils.NewPrgBar(len(exsMap), "aggTf")
	defer pBar.Close()
	for _, exs := range exsMap {
		pBar.Add(1)
		curStartMS, curEndMS := startMS, firstEndMS
		for curStartMS < endMS {
			bars, err := sess.QueryOHLCV(exs, minTF, curStartMS, curEndMS, 0, false)
			if err != nil {
				return err
			}
			if len(bars) > 0 {
				err = aggBigKlines(sess, bars, minMSecs, exs)
				if err != nil {
					return err
				}
			}
			curStartMS = curEndMS
			curEndMS += yearMSecs
		}
	}
	return nil
}

func LoadCalendars(args *config.CmdArgs) *errs.Error {
	err := SetupComs(args)
	if err != nil {
		return err
	}
	if args.InPath == "" {
		return errs.NewMsg(errs.CodeParamRequired, "--in is required")
	}
	rows, err := utils.ReadCSV(args.InPath)
	if err != nil {
		return err
	}
	ctx := context.Background()
	sess, conn, err := orm.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Release()
	lastExg := ""
	dateList := make([][2]int64, 0)
	dtLay := "2006-01-02"
	for _, row := range rows {
		startMS, err_ := btime.ParseTimeMSBy(dtLay, row[1])
		if err_ != nil {
			return errs.New(errs.CodeRunTime, err_)
		}
		stopMS, err_ := btime.ParseTimeMSBy(dtLay, row[2])
		if err_ != nil {
			return errs.New(errs.CodeRunTime, err_)
		}
		if lastExg == "" {
			lastExg = row[0]
		}
		if lastExg != row[0] {
			if len(dateList) > 0 {
				err = sess.SetCalendars(lastExg, dateList)
				if err != nil {
					log.Error("save calendars fail", zap.String("exg", lastExg), zap.Error(err))
				}
				dateList = make([][2]int64, 0)
			}
			lastExg = row[0]
		}
		dateList = append(dateList, [2]int64{startMS, stopMS})
	}
	if len(dateList) > 0 {
		err = sess.SetCalendars(lastExg, dateList)
		if err != nil {
			log.Error("save calendars fail", zap.String("exg", lastExg), zap.Error(err))
		}
	}
	log.Info("load calendars success", zap.Int("num", len(rows)))
	return nil
}

var adjMap = map[string]int{
	"pre":  core.AdjFront,
	"post": core.AdjBehind,
	"none": core.AdjNone,
	"":     0,
}

func ExportKlines(args *config.CmdArgs, prg utils.PrgCB) *errs.Error {
	if args.OutPath == "" {
		return errs.NewMsg(errs.CodeParamRequired, "--out is required")
	}
	if len(args.Pairs) == 0 {
		// No target is provided, export all current market
		// 未提供标的，导出当前市场所有
		exsList := orm.GetAllExSymbols()
		for _, exs := range exsList {
			if exs.Exchange != core.ExgName || exs.Market != core.Market {
				continue
			}
			args.Pairs = append(args.Pairs, exs.Symbol)
		}
		if len(args.Pairs) == 0 {
			return errs.NewMsg(errs.CodeParamRequired, "--pairs is required")
		}
		sort.Strings(args.Pairs)
	}
	if len(args.TimeFrames) == 0 {
		return errs.NewMsg(errs.CodeParamRequired, "--timeframes is required")
	}
	adjVal, adjValid := adjMap[args.AdjType]
	if !adjValid {
		return errs.NewMsg(errs.CodeParamRequired, "--adj should be pre/post/none")
	}
	ctx := context.Background()
	sess, conn, err := orm.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Release()
	err_ := utils.EnsureDir(args.OutPath, 0755)
	if err_ != nil {
		return errs.New(errs.CodeIOWriteFail, err_)
	}
	start, stop := config.TimeRange.StartMS, config.TimeRange.EndMS
	startStr := btime.ToDateStrLoc(start, core.DefaultDateFmt)
	endStr := btime.ToDateStrLoc(stop, core.DefaultDateFmt)
	log.Info("export kline", zap.Strings("tf", args.TimeFrames), zap.String("dt", startStr+" - "+endStr),
		zap.String("adj", args.AdjType), zap.Int("num", len(args.Pairs)))
	names, err := data.FindPathNames(args.OutPath, ".zip")
	if err != nil {
		return err
	}
	handles := make(map[string]bool)
	for _, n := range names {
		parts := strings.Split(strings.ReplaceAll(n, ".zip", ""), "_")
		handles[strings.Join(parts[:len(parts)-1], "_")] = true
	}
	tfNum := len(args.TimeFrames)
	pBar := utils.NewPrgBar(len(args.Pairs)*tfNum, "Export")
	if prg != nil {
		pBar.PrgCbs = append(pBar.PrgCbs, prg)
	}
	defer pBar.Close()
	for _, symbol := range args.Pairs {
		clean := strings.ReplaceAll(strings.ReplaceAll(symbol, "/", "_"), ":", "_")
		if _, ok := handles[clean]; ok {
			pBar.Add(tfNum)
			log.Info("skip exist", zap.String("symbol", symbol))
			continue
		}
		log.Info("handle", zap.String("symbol", symbol))
		exs, err := orm.GetExSymbolCur(symbol)
		if err != nil {
			pBar.Add(tfNum)
			log.Warn("export fail", zap.String("symbol", symbol), zap.Error(err))
			continue
		}
		for _, tf := range args.TimeFrames {
			adjs, klines, err := sess.GetOHLCV(exs, tf, start, stop, 0, false)
			if err != nil {
				return err
			}
			klines = orm.ApplyAdj(adjs, klines, adjVal, 0, 0)
			rows := utils.KlineToStr(klines, btime.LocShow)
			path := filepath.Join(args.OutPath, fmt.Sprintf("%s_%s.csv", clean, tf))
			err = utils.WriteCsvFile(path, rows, true)
			if err != nil {
				return err
			}
			pBar.Add(1)
		}
	}
	log.Info("export kline complete")
	return nil
}

func PurgeKlines(args *config.CmdArgs) *errs.Error {
	sess, conn, err := orm.Conn(nil)
	if err != nil {
		return err
	}
	defer conn.Release()
	exchange := exg.Default
	// 搜索需要删除的标的
	// Search for the target to be deleted
	exsList := make([]*orm.ExSymbol, 0)
	if len(config.Pairs) > 0 {
		for _, symbol := range config.Pairs {
			exs, err := orm.GetExSymbol(exchange, symbol)
			if err != nil {
				return err
			}
			exsList = append(exsList, exs)
		}
	} else {
		exInfo := exchange.Info()
		exMap := orm.GetExSymbols(exInfo.ID, exInfo.MarketType)
		for _, exs := range exMap {
			exsList = append(exsList, exs)
		}
	}
	if args.ExgReal != "" {
		filtered := make([]*orm.ExSymbol, 0, len(exsList))
		for _, exs := range exsList {
			if exs.ExgReal == args.ExgReal {
				filtered = append(filtered, exs)
			}
		}
		exsList = filtered
	}
	if len(exsList) == 0 {
		return errs.NewMsg(errs.CodeRunTime, "pairs is required")
	}
	// 输出信息要求确认
	// Output information requires confirmation
	pairs := make([]string, 0, len(exsList))
	for _, exs := range exsList {
		pairs = append(pairs, exs.Symbol)
	}
	tfList := args.TimeFrames
	if len(tfList) == 0 {
		aggs := orm.GetKlineAggs()
		for _, a := range aggs {
			tfList = append(tfList, a.TimeFrame)
		}
	}
	isOk := utils.ReadConfirm([]string{
		fmt.Sprintf("exchange: %s, exg_real: %s", config.Exchange.Name, args.ExgReal),
		fmt.Sprintf("date range: all"),
		fmt.Sprintf("timeFrames: %s", strings.Join(tfList, ", ")),
		fmt.Sprintf("symbols(%v): %s", len(exsList), strings.Join(pairs, ", ")),
		"input `y` to delete, `n` to cancel:",
	}, "y", "n", true)
	if !isOk {
		return nil
	}
	// 删除符合要求的数据
	// Delete the data that meets the requirements
	pBar := utils.NewPrgBar(len(exsList), "purge")
	defer pBar.Close()
	for _, exs := range exsList {
		pBar.Add(1)
		err := sess.DelKData(exs, tfList, 0, 0)
		if err != nil {
			return err
		}
	}
	log.Info("all purge complete")
	return nil
}

func ExportAdjFactors(args *config.CmdArgs) *errs.Error {
	err := SetupComsExg(args)
	if err != nil {
		return err
	}
	if args.OutPath == "" {
		return errs.NewMsg(errs.CodeParamRequired, "--out is required")
	}
	if len(args.Pairs) == 0 {
		return errs.NewMsg(errs.CodeParamRequired, "--pairs is required")
	}
	ctx := context.Background()
	sess, conn, err := orm.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Release()
	err_ := utils.EnsureDir(args.OutPath, 0755)
	if err_ != nil {
		return errs.New(errs.CodeIOWriteFail, err_)
	}
	for _, symbol := range args.Pairs {
		log.Info("handle", zap.String("symbol", symbol))
		exs, err := orm.GetExSymbolCur(symbol)
		if err != nil {
			return err
		}
		facs, err_ := sess.GetAdjFactors(ctx, exs.ID)
		if err_ != nil {
			return orm.NewDbErr(core.ErrDbReadFail, err_)
		}
		sort.Slice(facs, func(i, j int) bool {
			return facs[i].StartMs < facs[j].StartMs
		})
		rows := make([][]string, 0, len(facs))
		for _, f := range facs {
			dateStr := btime.ToDateStrLoc(f.StartMs, core.DefaultDateFmt)
			subCode := ""
			if f.SubID > 0 {
				it := orm.GetSymbolByID(f.SubID)
				if it != nil {
					subCode = it.Symbol
				}
			}
			row := []string{
				subCode,
				dateStr,
				strconv.FormatFloat(f.Factor, 'f', -1, 64),
			}
			rows = append(rows, row)
		}
		path := filepath.Join(args.OutPath, symbol+"_adj.csv")
		err = utils.WriteCsvFile(path, rows, false)
		if err != nil {
			return err
		}
	}
	return nil
}

/*
CalcCorrelation calculate correlation for pairs; generate csv or images
*/
func CalcCorrelation(args *config.CmdArgs) *errs.Error {
	err := SetupComsExg(args)
	if err != nil {
		return err
	}
	if len(args.TimeFrames) == 0 {
		return errs.NewMsg(errs.CodeParamRequired, "--timeframes is required")
	}
	if args.BatchSize <= 1 {
		return errs.NewMsg(errs.CodeParamRequired, "--batch-size is required")
	}
	if args.RunEveryTF == "" {
		return errs.NewMsg(errs.CodeParamRequired, "--run-every is required")
	}
	if args.OutPath == "" {
		return errs.NewMsg(errs.CodeParamRequired, "--out is required")
	}
	pairs, err := goods.RefreshPairList(btime.TimeMS())
	if err != nil {
		return err
	}
	slices.Sort(pairs)
	exsList := make([]*orm.ExSymbol, 0, len(pairs))
	for _, pair := range pairs {
		exs, err := orm.GetExSymbolCur(pair)
		if err != nil {
			log.Warn("get exs fail, skip", zap.String("code", pair), zap.Error(err))
			continue
		}
		exsList = append(exsList, exs)
	}
	tf := args.TimeFrames[0]
	tfMSecs := int64(utils2.TFToSecs(tf) * 1000)
	gapTFMSecs := int64(utils2.TFToSecs(args.RunEveryTF) * 1000)
	if int(gapTFMSecs/tfMSecs) < args.BatchSize {
		log.Error("run-every is too small for current batch-size and timeframe")
		return nil
	}
	startMs := config.TimeRange.StartMS
	klineNum := args.BatchSize + 1
	pBar := utils.NewPrgBar(int((config.TimeRange.EndMS-startMs)/gapTFMSecs)+1, "Corr")
	defer pBar.Close()
	var csvRows [][]string
	codes := make([]string, 0, len(pairs))
	for _, pair := range pairs {
		id, _, _, _ := core.SplitSymbol(pair)
		codes = append(codes, id)
	}
	emptyRow := make(map[string]string)
	// make csv head; first avg corr for each code
	var head []string
	head = append(head, "date")
	emptyRow["date"] = "-"
	for _, id := range codes {
		emptyRow[id] = "-"
		head = append(head, id)
	}
	// detail corr for each pair
	for i, id := range codes {
		for j := i + 1; j < len(codes); j++ {
			key := fmt.Sprintf("%s/%s", id, codes[j])
			emptyRow[key] = "-"
			head = append(head, key)
		}
	}
	csvRows = append(csvRows, head)
	for {
		if startMs >= config.TimeRange.EndMS {
			break
		}
		pBar.Add(1)
		// Calculate logarithmic rate of return
		names := make([]string, 0, len(exsList))
		dataArr := make([][]float64, 0, len(exsList))
		var lacks []string
		for i, exs := range exsList {
			_, klines, err := orm.GetOHLCV(exs, tf, startMs, startMs+gapTFMSecs, klineNum, false)
			if err != nil {
				log.Warn("get kline fail, skip", zap.String("code", exs.Symbol), zap.Error(err))
				continue
			}
			if len(klines) >= klineNum {
				prices := make([]float64, 0, len(klines))
				for _, b := range klines {
					prices = append(prices, b.Close)
				}
				names = append(names, codes[i])
				dataArr = append(dataArr, prices[:klineNum])
			} else {
				lacks = append(lacks, exs.Symbol)
			}
		}
		dateStr := btime.ToDateStr(startMs, "20060102")
		if len(lacks) > 0 {
			log.Warn("skip no enough kline", zap.String("dt", dateStr), zap.Strings("codes", lacks))
		}
		startMs += gapTFMSecs
		if len(names) == 0 {
			continue
		}
		// Calculate the Pearson correlation matrix
		corrMat, avgs, err_ := utils.CalcCorrMat(klineNum, dataArr, true)
		if err_ != nil {
			return errs.New(errs.CodeRunTime, err_)
		}
		if args.OutType == "csv" {
			row := make(map[string]string)
			for k, v := range emptyRow {
				row[k] = v
			}
			row["date"] = btime.ToDateStr(startMs, "2006-01-02 15:04")
			for i, id := range names {
				for j := i + 1; j < len(names); j++ {
					val := corrMat.At(i, j)
					key := fmt.Sprintf("%s/%s", id, names[j])
					row[key] = strconv.FormatFloat(val, 'f', 3, 64)
				}
				row[id] = strconv.FormatFloat(avgs[i], 'f', 3, 64)
			}
			arr := make([]string, 0, len(head))
			for _, name := range head {
				arr = append(arr, row[name])
			}
			csvRows = append(csvRows, arr)
		} else {
			imgData, err_ := utils.GenCorrImg(corrMat, dateStr, names, "", 0)
			if err_ != nil {
				return errs.New(errs.CodeRunTime, err_)
			}
			err = utils.WriteFile(filepath.Join(args.OutPath, dateStr+".png"), imgData)
			if err != nil {
				return err
			}
		}
	}
	if args.OutType == "csv" {
		outName := fmt.Sprintf("corr_%s_every_%s.csv", tf, args.RunEveryTF)
		return utils.WriteCsvFile(filepath.Join(args.OutPath, outName), csvRows, false)
	}
	return nil
}

type RunHistArgs struct {
	ExsList     []*orm.ExSymbol
	Start       int64
	End         int64
	ViewNextNum int // number of future bars to get
	TfWarms     map[string]int
	OnEnvEnd    func(bar *banexg.PairTFKline, adj *orm.AdjInfo)
	VerCh       chan int // write -1 to exit;
	OnBar       func(bar *orm.InfoKline, nexts []*orm.InfoKline)
}

type tfFuts struct {
	TF    string
	MSecs int64
	Futs  []*orm.InfoKline
}

/*
RunHistKline
RePlay of K-lines within a specified time range for multiple symbols, supporting multiple timeFrames and returning n min-timeFrame bars.
对多个品种回放指定时间范围的K线，支持多周期，支持返回未来n个最小周期bar。
*/
func RunHistKline(args *RunHistArgs) *errs.Error {
	if args.VerCh == nil {
		args.VerCh = make(chan int, 5)
	}
	if cap(args.VerCh) == 0 {
		return errs.NewMsg(errs.CodeRunTime, "cap of VerCh should > 0")
	}
	var tfItems = make([]*tfFuts, 0, len(args.TfWarms))
	for tf := range args.TfWarms {
		tfItems = append(tfItems, &tfFuts{
			TF:    tf,
			MSecs: int64(utils2.TFToSecs(tf) * 1000),
		})
	}
	slices.SortFunc(tfItems, func(a, b *tfFuts) int {
		return int(a.MSecs - b.MSecs)
	})
	var tfIdxs = make(map[string]int)
	for i, it := range tfItems {
		tfIdxs[it.TF] = i
	}
	var futures = make(map[string][]*tfFuts)
	var lock deadlock.Mutex
	onItemBar := func(b *orm.InfoKline) {
		if args.ViewNextNum <= 0 || b.IsWarmUp {
			args.OnBar(b, nil)
			return
		}
		lock.Lock()
		lv := tfIdxs[b.TimeFrame]
		tfArr := futures[b.Symbol]
		lock.Unlock()
		state := tfArr[lv]
		state.Futs = append(state.Futs, b)
		if lv == 0 && len(state.Futs) > args.ViewNextNum {
			old := state.Futs[0]
			state.Futs = state.Futs[1:]
			barEndMs := old.Time + state.MSecs
			for i := len(tfIdxs) - 1; i > 0; i-- {
				big := tfArr[i]
				if len(big.Futs) > 0 && big.Futs[0].Time+big.MSecs <= barEndMs {
					oldBig := big.Futs[0]
					big.Futs = big.Futs[1:]
					args.OnBar(oldBig, big.Futs)
				}
			}
			args.OnBar(old, state.Futs)
		}
	}
	var holds = make([]data.IHistKlineFeeder, 0, len(args.ExsList))
	var skipWarms = make(map[string][2]int)
	var skips map[string][2]int
	for i, exs := range args.ExsList {
		tfList := make([]*tfFuts, 0, len(tfItems))
		for _, it := range tfItems {
			tfList = append(tfList, &tfFuts{
				TF:    it.TF,
				MSecs: it.MSecs,
				Futs:  make([]*orm.InfoKline, 0, args.ViewNextNum),
			})
		}
		futures[exs.Symbol] = tfList
		feeder, err := data.NewDBKlineFeeder(exs, onItemBar, true)
		if err != nil {
			return err
		}
		holds = append(holds, feeder)
		feeder.SetEndMS(args.End)
		feeder.OnEnvEnd = func(bar *banexg.PairTFKline, adj *orm.AdjInfo) {
			if args.OnEnvEnd != nil {
				args.OnEnvEnd(bar, adj)
			}
		}
		feeder.SubTfs(utils.KeysOfMap(args.TfWarms), true)
		exchange, err := exg.GetWith(exs.Exchange, exs.Market, "")
		if err != nil {
			return err
		}
		err = feeder.DownIfNeed(nil, exchange, nil)
		if err != nil {
			log.Error("down kline fail", zap.String("code", exs.Symbol), zap.Error(err))
		}
		_, skips, err = feeder.WarmTfs(args.Start, args.TfWarms, nil)
		if err != nil {
			return err
		}
		for k, v := range skips {
			skipWarms[k] = v
		}
		feeder.SetSeek(args.Start)
		if i%10 == 0 {
			log.Info("warm done", zap.Int("total", len(args.ExsList)), zap.Int("cur", i+1))
		}
	}
	if len(skipWarms) > 0 {
		log.Warn("warm lacks", zap.String("items", data.StrWarmLacks(skipWarms)))
	}
	makeFeeders := func() []data.IHistKlineFeeder {
		return holds
	}
	err := data.RunHistFeeders(makeFeeders, args.VerCh, nil)
	if args.OnEnvEnd != nil {
		args.OnEnvEnd(nil, nil)
	}
	return err
}

func DownExgOrders(args []string) error {
	var exchange, market, account, pairs string
	var timeStart, timeEnd string
	var force bool
	var configPaths config.ArrString
	var sub = flag.NewFlagSet("cmp", flag.ExitOnError)
	sub.Var(&configPaths, "config", "config path to use, Multiple -config options may be used")
	sub.StringVar(&account, "account", "", "account for api-key to fetch exchange orders")
	sub.StringVar(&exchange, "exchange", "", "exchange id")
	sub.StringVar(&market, "market", "", "spot/linear/inverse/option")
	sub.StringVar(&timeStart, "timestart", "", "set start time, allow multiple formats")
	sub.StringVar(&timeEnd, "timeend", "", "set start time, allow multiple formats")
	sub.StringVar(&pairs, "pairs", "", "symbols, comma separated")
	sub.BoolVar(&force, "force", false, "force check from order stamp")
	err_ := sub.Parse(args)
	if err_ != nil {
		return err_
	}
	core.SetRunMode(core.RunModeLive)
	err := SetupComs(&config.CmdArgs{Configs: configPaths})
	if err != nil {
		return err
	}
	if exchange == "" {
		exchange = core.ExgName
	}
	if market == "" {
		market = core.Market
	}
	if pairs == "" {
		pairs = strings.Join(config.Pairs, ",")
	}
	if timeStart == "" || timeEnd == "" {
		return errors.New("timestart or timeend is required")
	}
	if account == "" || pairs == "" {
		return errors.New("`account` or `pairs` is required")
	}
	save, err := GetExgOrderSet(account, exchange, market)
	if err != nil {
		return err
	}
	startMS, err_ := btime.ParseTimeMS(timeStart)
	if err_ != nil {
		return err_
	}
	endMS, err_ := btime.ParseTimeMS(timeEnd)
	if err_ != nil {
		return err_
	}
	pairArr := strings.Split(pairs, ",")
	err = save.Download(startMS, endMS, pairArr, force)
	if err != nil {
		return err
	}
	return nil
}

type PairOrders struct {
	Symbol  string
	Orders  []*banexg.Order
	StartMS int64
	EndMS   int64
}

type ExgOrderSet struct {
	Account  string
	Name     string
	Market   string
	Data     map[string]*PairOrders
	path     string
	exchange banexg.BanExchange
}

func GetExgOrderSet(account, exgName, market string) (*ExgOrderSet, *errs.Error) {
	acc := config.Accounts[account]
	accHash := acc.GetApiSecret().APIKey[:5]
	fname := fmt.Sprintf("%s_%s_%s_%s.gob", exgName, market, account, accHash)
	orderDir := filepath.Join(config.GetDataDir(), "exgOrders")
	err := utils.EnsureDir(orderDir, 0755)
	if err != nil {
		return nil, errs.New(errs.CodeIOWriteFail, err)
	}
	path := filepath.Join(orderDir, fname)
	var save = ExgOrderSet{
		Account: account,
		Name:    exgName,
		Market:  market,
		Data:    make(map[string]*PairOrders),
		path:    path,
	}
	_ = utils.DecodeGobFile(path, &save)
	exchange, err2 := exg.GetWith(exgName, market, "")
	if err2 != nil {
		return nil, err2
	}
	save.exchange = exchange
	return &save, nil
}

// Download 下载指定时间范围内的订单记录
func (s *ExgOrderSet) Download(startMS, endMS int64, pairs []string, force bool) *errs.Error {
	if len(pairs) == 0 {
		return errs.NewMsg(errs.CodeParamRequired, "pairs is required")
	}
	if startMS >= endMS || startMS <= 0 {
		return errs.NewMsg(errs.CodeParamInvalid, "startMS must < endMS")
	}
	curMS := btime.UTCStamp()
	startMS = max(startMS, curMS-int64(utils2.TFToSecs("1y")*1000))
	endMS = min(curMS-60000, endMS)

	// 检查每个交易对的本地数据范围
	var needSave = false
	for _, pair := range pairs {
		old, _ := s.Data[pair]
		if old == nil {
			old = &PairOrders{}
			s.Data[pair] = old
		}
		var oldStart, oldEnd = old.StartMS, old.EndMS
		if force && len(old.Orders) > 0 {
			oldStart = old.Orders[0].Timestamp
			oldEnd = old.Orders[len(old.Orders)-1].Timestamp + 1
		}

		var downloadRanges [][2]int64
		if oldStart == 0 {
			// 本地无数据，下载整个区间
			downloadRanges = append(downloadRanges, [2]int64{startMS, endMS})
		} else {
			// 检查是否需要下载前部分
			if startMS < oldStart {
				downloadRanges = append(downloadRanges, [2]int64{startMS, oldStart})
			}
			// 检查是否需要下载后部分
			if endMS > oldEnd {
				downloadRanges = append(downloadRanges, [2]int64{oldEnd, endMS})
			}
		}
		if len(downloadRanges) == 0 {
			continue
		}

		// 执行下载
		var limit = 500
		for _, r := range downloadRanges {
			start, end := r[0], r[1]
			offsetMS := start
			for offsetMS < end {
				newOrders, err := s.exchange.FetchOrders(pair, offsetMS, limit, map[string]interface{}{
					banexg.ParamAccount: s.Account,
				})
				if err != nil {
					return err
				}
				if len(newOrders) == 0 {
					break
				}

				// 合并新订单到本地缓存
				old.Orders = append(old.Orders, newOrders...)
				offsetMS = newOrders[len(newOrders)-1].Timestamp + 1
				needSave = true

				startStr := btime.ToDateStr(start, "")
				endStr := btime.ToDateStr(end, "")
				log.Info("download orders",
					zap.String("pair", pair),
					zap.String("range", fmt.Sprintf("%s - %s", startStr, endStr)),
					zap.Int("num", len(newOrders)))
				if len(newOrders) < limit {
					break
				}
			}
		}

		// 确保订单按时间排序
		if len(old.Orders) > 0 {
			sort.Slice(old.Orders, func(i, j int) bool {
				return old.Orders[i].Timestamp < old.Orders[j].Timestamp
			})
		}
		if old.StartMS == 0 || old.StartMS > startMS {
			old.StartMS = startMS
			needSave = true
		}
		if old.EndMS < endMS {
			old.EndMS = endMS
			needSave = true
		}
	}

	// 保存到本地文件
	if needSave {
		gob.Register(map[string]interface{}{})
		return utils.EncodeGob(s.path, s)
	}
	return nil
}

// Get 获取指定时间范围和交易对的订单记录
func (s *ExgOrderSet) Get(startMS, endMS int64, pairs []string, botName string) (map[string][]*banexg.Order, *errs.Error) {
	if startMS >= endMS {
		return nil, errs.NewMsg(errs.CodeParamInvalid, "startMS must < endMS")
	}
	result := make(map[string][]*banexg.Order)

	// 如果未指定交易对，使用所有已缓存的交易对
	if len(pairs) == 0 {
		pairs = make([]string, 0, len(s.Data))
		for pair := range s.Data {
			pairs = append(pairs, pair)
		}
	}

	// 遍历每个交易对，提取符合时间范围的订单
	for _, pair := range pairs {
		obj, exists := s.Data[pair]
		if !exists {
			continue
		}
		orders := obj.Orders

		// 二分查找开始位置
		startIdx := sort.Search(len(orders), func(i int) bool {
			return orders[i].Timestamp >= startMS
		})

		// 二分查找结束位置
		endIdx := sort.Search(len(orders), func(i int) bool {
			return orders[i].Timestamp > endMS
		})

		if startIdx < endIdx {
			var exgOrders []*banexg.Order
			if botName != "" {
				for _, od := range orders[startIdx:endIdx] {
					if strings.HasPrefix(od.ClientOrderID, botName) {
						exgOrders = append(exgOrders, od)
					}
				}
			} else {
				exgOrders = orders[startIdx:endIdx]
			}
			result[pair] = exgOrders
		}
	}

	return result, nil
}
