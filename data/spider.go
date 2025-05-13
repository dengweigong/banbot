package data

import (
	"fmt"
	"github.com/banbox/banbot/btime"
	"github.com/banbox/banbot/core"
	"github.com/banbox/banbot/exg"
	"github.com/banbox/banbot/orm"
	"github.com/banbox/banbot/utils"
	"github.com/banbox/banexg"
	"github.com/banbox/banexg/errs"
	"github.com/banbox/banexg/log"
	utils2 "github.com/banbox/banexg/utils"
	"github.com/sasha-s/go-deadlock"
	"go.uber.org/zap"
	"strings"
	"time"
)

var (
	Spider     *LiveSpider
	retryWaits = btime.NewRetryWaits(0, nil)
)

type NotifyKLines struct {
	TFSecs   int
	Interval int // 推送更新间隔, <= TFSecs
	Arr      []*banexg.Kline
}

type KLineMsg struct {
	NotifyKLines
	ExgName string // The name of the exchange 交易所名称
	Market  string // market 市场
	Pair    string // symbol  币种
}

/*
getCheckInterval
Based on the trading pair and timeframe being monitored. Calculate the minimum check interval.

< 60s to fetch data through WebSocket, check the update interval can be relatively small.

	If the data is 1 m or more and obtained through the second-level interface of the API, it will be updated once every 3s

根据监听的交易对和时间帧。计算最小检查间隔。

	<60s的通过WebSocket获取数据，检查更新间隔可以比较小。
	1m及以上的通过API的秒级接口获取数据，3s更新一次
*/
func getCheckInterval(tfSecs int) float64 {
	var checkIntv float64

	switch {
	case tfSecs <= 3:
		checkIntv = 0.5
	case tfSecs <= 10:
		checkIntv = float64(tfSecs) * 0.35
	case tfSecs <= 60:
		checkIntv = float64(tfSecs) * 0.2
	case tfSecs <= 300:
		checkIntv = float64(tfSecs) * 0.15
	case tfSecs <= 900:
		checkIntv = float64(tfSecs) * 0.1
	case tfSecs <= 3600:
		checkIntv = float64(tfSecs) * 0.07
	default:
		// 超过1小时维度的，10分钟刷新一次
		checkIntv = 600
	}
	return checkIntv
}

/** *******************************  Spider 爬虫部分   ****************************
 */
var (
	writeQ = make(chan *SaveKline, 1000)
)

type SaveKline struct {
	Sid       int32
	TimeFrame string
	Arr       []*banexg.Kline
	MsgAction string
}

func consumeWriteQ(workNum int) {
	guard := make(chan struct{}, workNum)
	defer close(guard)
	setOne := func() {
		logged := false
		for {
			select {
			case guard <- struct{}{}:
				return
			case <-time.After(20 * time.Second):
				if !logged {
					log.Error("wait save in spider timeout")
					logged = true
				}
			}
		}
	}
	mntSta := newPeriodSta("1m")
	hourSta := newPeriodSta("1h")
	for save := range writeQ {
		setOne()
		go func(job *SaveKline) {
			defer func() {
				<-guard
			}()
			tfSecs := utils2.TFToSecs(job.TimeFrame)
			trySaveKlines(job, tfSecs, mntSta, hourSta)
			// After the K-line is written to the database, a message will be sent to notify the robot to avoid repeated insertion of K-line
			// 写入K线到数据库后，才发消息通知机器人，避免重复插入K线
			err := Spider.Broadcast(&utils.IOMsg{
				Action: job.MsgAction,
				Data: NotifyKLines{
					TFSecs:   tfSecs,
					Interval: tfSecs,
					Arr:      job.Arr,
				},
			})
			if err != nil {
				log.Error("broadCast kline fail", zap.String("action", job.MsgAction), zap.Error(err))
			}
		}(save)
	}
}

type FetchJob struct {
	PairTFCache
	Pair      string
	CheckSecs int
	Since     int64
	NextRun   int64
}

type Miner struct {
	spider       *LiveSpider
	ExgName      string
	Market       string
	exchange     banexg.BanExchange
	Fetchs       map[string]*FetchJob
	KLines       *PairSubs
	Trades       *PairSubs
	Depths       *PairSubs
	IsWatchPrice bool
	klineStates  map[string]*SubKLineState
}

type PairSubs struct {
	pairs  map[string]bool
	m      deadlock.Mutex
	Status int // 0 not subscribed, 1 subscribing, 2 subscribed
}

func NewPairSubs() *PairSubs {
	return &PairSubs{
		pairs: make(map[string]bool),
	}
}

// GetNewSubs get pairs need to be subscribed
func (s *PairSubs) GetNewSubs(pairs []string) []string {
	if s.Status == 0 {
		// 未订阅，返回全部品种
		if len(pairs) > 0 {
			s.Set(pairs...)
		}
		pairs = s.Keys()
		if len(pairs) > 0 {
			s.Status = 1
		}
		return pairs
	} else {
		// 正在订阅，返回新的尚未订阅品种
		return s.Set(pairs...)
	}
}

func (s *PairSubs) Set(pairs ...string) []string {
	s.m.Lock()
	res := make([]string, 0, len(pairs))
	for _, p := range pairs {
		if _, ok := s.pairs[p]; !ok {
			s.pairs[p] = true
			res = append(res, p)
		}
	}
	s.m.Unlock()
	return res
}

func (s *PairSubs) Remove(pairs ...string) []string {
	s.m.Lock()
	res := make([]string, 0, len(pairs))
	for _, p := range pairs {
		if _, ok := s.pairs[p]; ok {
			delete(s.pairs, p)
			res = append(res, p)
		}
	}
	s.m.Unlock()
	return res
}

func (s *PairSubs) Len() int {
	s.m.Lock()
	l := len(s.pairs)
	s.m.Unlock()
	return l
}

func (s *PairSubs) Keys() []string {
	s.m.Lock()
	keys := utils.KeysOfMap(s.pairs)
	s.m.Unlock()
	return keys
}

type LiveSpider struct {
	*utils.ServerIO
	miners map[string]*Miner
}

// monitorSubscriptions periodically checks all miners for failed subscriptions and restarts them
func (s *LiveSpider) monitorSubscriptions() {
	log.Info("Starting subscription monitor")
	for {
		time.Sleep(time.Second * 1)

		// Check all miners for failed subscriptions
		for key, miner := range s.miners {
			curMS := btime.UTCStamp()
			// Check KLine subscriptions
			klineNum := miner.KLines.Len()
			if klineNum > 0 && miner.KLines.Status == 0 && curMS > retryWaits.NextRetry("watchKLines") {
				log.Info("Recovering KLine subscription",
					zap.String("miner", key),
					zap.Int("pairs", klineNum))
				miner.watchKLines(nil)
			}

			// Check Trade subscriptions
			tradeNum := miner.Trades.Len()
			if tradeNum > 0 && miner.Trades.Status == 0 && curMS > retryWaits.NextRetry("watchTrades") {
				log.Info("Recovering Trade subscription",
					zap.String("miner", key),
					zap.Int("pairs", tradeNum))
				miner.watchTrades(nil)
			}

			// Check OrderBook subscriptions
			bookNum := miner.Depths.Len()
			if bookNum > 0 && miner.Depths.Status == 0 && curMS > retryWaits.NextRetry("watchOdBooks") {
				log.Info("Recovering OrderBook subscription",
					zap.String("miner", key),
					zap.Int("pairs", bookNum))
				miner.watchOdBooks(nil)
			}

			// Check Price subscriptions for contract markets
			if miner.exchange.IsContract(miner.Market) && !miner.IsWatchPrice && curMS > retryWaits.NextRetry("watchPrices") {
				log.Info("Recovering Price subscription", zap.String("miner", key))
				miner.watchPrices()
			}
		}
	}
}

func newMiner(spider *LiveSpider, exgName, market string) (*Miner, *errs.Error) {
	exchange, err := exg.GetWith(exgName, market, "")
	if err != nil {
		return nil, err
	}
	return &Miner{
		spider:      spider,
		ExgName:     exgName,
		Market:      market,
		exchange:    exchange,
		Fetchs:      map[string]*FetchJob{},
		KLines:      NewPairSubs(),
		Trades:      NewPairSubs(),
		Depths:      NewPairSubs(),
		klineStates: map[string]*SubKLineState{},
	}, nil
}

func (m *Miner) init() {
	_, err := orm.LoadMarkets(m.exchange, false)
	if err != nil {
		log.Error("load markets for miner fail", zap.String("exg", m.ExgName), zap.Error(err))
	}
}

func (m *Miner) SubPairs(jobType string, pairs ...string) *errs.Error {
	valids, _ := m.exchange.CheckSymbols(pairs...)
	if len(valids) == 0 {
		if len(pairs) > 0 {
			return nil
		}
		// If the incoming is empty, take all the underlying of the current exchange + market
		// 传入为空，取当前交易所+市场的所有标的
		markets := m.exchange.GetCurMarkets()
		valids = make([]string, 0, len(markets))
		for _, mar := range markets {
			valids = append(valids, mar.Symbol)
		}
	}
	ensures := make([]*orm.ExSymbol, 0, len(valids))
	for _, p := range pairs {
		ensures = append(ensures, &orm.ExSymbol{
			Exchange: m.ExgName,
			Market:   m.Market,
			Symbol:   p,
		})
	}
	err := orm.EnsureSymbols(ensures, m.ExgName)
	if err != nil {
		return err
	}
	if jobType == "ws" || jobType == "book" {
		m.watchOdBooks(valids)
	} else if jobType == "ohlcv" || jobType == "uohlcv" {
		m.watchKLines(valids)
	} else if jobType == "price" {
		m.watchPrices()
	} else if jobType == "trade" {
		m.watchTrades(valids)
	} else {
		log.Error("unknown sub type", zap.String("val", jobType))
	}
	return nil
}

func (m *Miner) UnSubPairs(jobType string, pairs ...string) *errs.Error {
	if jobType == "ws" || jobType == "book" {
		removes := m.Depths.Remove(pairs...)
		return m.exchange.UnWatchOrderBooks(removes, nil)
	} else if jobType == "ohlcv" || jobType == "uohlcv" {
		timeFrame := "1s"
		if banexg.IsContract(m.Market) {
			timeFrame = "1m"
		}
		items := m.KLines.Remove(pairs...)
		jobs := make([][2]string, len(items))
		for _, p := range items {
			jobs = append(jobs, [2]string{p, timeFrame})
		}
		return m.exchange.UnWatchOHLCVs(jobs, nil)
	} else if jobType == "price" {
		return m.exchange.UnWatchMarkPrices(nil, nil)
	} else if jobType == "trade" {
		items := m.Trades.Remove(pairs...)
		return m.exchange.UnWatchTrades(items, nil)
	} else {
		log.Error("unknown unsub type", zap.String("val", jobType))
	}
	return nil
}

func (m *Miner) watchTrades(pairs []string) {
	pairs = m.Trades.GetNewSubs(pairs)
	if len(pairs) == 0 {
		return
	}
	out, err := m.exchange.WatchTrades(pairs, nil)
	if err != nil {
		m.Trades.Status = 0
		retryWaits.SetFail("watchTrades")
		log.Error("watch trades fail", zap.String("exg", m.ExgName), zap.Error(err))
		return
	}
	retryWaits.Reset("watchTrades")
	if m.Trades.Status == 2 {
		return
	}
	m.Trades.Status = 2
	log.Info("start watch trades", zap.String("exg", m.ExgName), zap.Int("num", m.Trades.Len()))
	prefix := fmt.Sprintf("trade_%s_%s_", m.ExgName, m.Market)

	go func() {
		defer func() {
			m.Trades.Status = 0
			retryWaits.SetFail("watchTrades")
			log.Info("watch trades stopped", zap.String("exg", m.ExgName))
		}()
		for item := range out {
			err = m.spider.Broadcast(&utils.IOMsg{
				Action: prefix,
				Data:   item,
			})
			if err != nil {
				log.Error("broadCast trade fail", zap.String("key", prefix), zap.Error(err))
			}
		}
	}()
}

func (m *Miner) watchPrices() {
	if m.IsWatchPrice || !m.exchange.IsContract(m.Market) {
		return
	}
	out, err := m.exchange.WatchMarkPrices(nil, map[string]interface{}{
		banexg.ParamInterval: "1s",
	})
	if err != nil {
		m.IsWatchPrice = false
		retryWaits.SetFail("watchPrices")
		log.Error("watch prices fail", zap.String("exg", m.ExgName), zap.Error(err))
		return
	}
	retryWaits.Reset("watchPrices")
	m.IsWatchPrice = true
	log.Info("start watch prices", zap.String("exg", m.ExgName))
	prefix := fmt.Sprintf("price_%s_%s", m.ExgName, m.Market)

	go func() {
		defer func() {
			m.IsWatchPrice = false
			retryWaits.SetFail("watchPrices")
			log.Info("watch prices stopped", zap.String("exg", m.ExgName))
		}()
		for item := range out {
			err = m.spider.Broadcast(&utils.IOMsg{
				Action: prefix,
				Data:   item,
			})
			if err != nil {
				log.Error("broadCast price fail", zap.String("key", prefix), zap.Error(err))
			}
		}
	}()
}

func (m *Miner) watchOdBooks(pairs []string) {
	pairs = m.Depths.GetNewSubs(pairs)
	if len(pairs) == 0 {
		return
	}
	out, err := m.exchange.WatchOrderBooks(pairs, 0, nil)
	if err != nil {
		m.Depths.Status = 0
		retryWaits.SetFail("watchOdBooks")
		log.Error("watch odBook fail", zap.String("exg", m.ExgName), zap.Error(err))
		return
	}
	retryWaits.Reset("watchOdBooks")
	if m.Depths.Status == 2 {
		return
	}
	m.Depths.Status = 2
	log.Info("start watch odBooks", zap.String("exg", m.ExgName), zap.Int("num", m.Depths.Len()))
	prefix := fmt.Sprintf("book_%s_%s_", m.ExgName, m.Market)

	go func() {
		defer func() {
			m.Depths.Status = 0
			retryWaits.SetFail("watchOdBooks")
			log.Info("watch odBook stopped", zap.String("exg", m.ExgName))
		}()
		for book := range out {
			err = m.spider.Broadcast(&utils.IOMsg{
				Action: prefix + book.Symbol,
				Data:   book,
			})
			if err != nil {
				log.Error("broadCast odBook fail", zap.String("pair", book.Symbol), zap.Error(err))
			}
		}
	}()
}

type SubKLineState struct {
	Sid        int32
	LastNotify int64
	ExpectMS   int64 // next bar start time
	PrevBar    *banexg.Kline
}

/*
这里将订阅此市场的最小周期(1s/1m)；1h/1d等大周期已在writeQ消费端判断并fetch
*/
func (m *Miner) watchKLines(pairs []string) {
	pairs = m.KLines.GetNewSubs(pairs)
	if len(pairs) == 0 {
		return
	}
	jobs := make([][2]string, 0, len(pairs))
	timeFrame := "1s"
	if banexg.IsContract(m.Market) {
		timeFrame = "1m"
	}
	tfSecs := utils2.TFToSecs(timeFrame)
	tfMSecs := int64(tfSecs * 1000)
	expectMS := utils2.AlignTfMSecs(btime.TimeMS(), tfMSecs)
	for _, p := range pairs {
		jobs = append(jobs, [2]string{p, timeFrame})
		if _, ok := m.klineStates[p]; !ok {
			exs, err := orm.GetExSymbol(m.exchange, p)
			if err != nil {
				code := fmt.Sprintf("%s.%s.%s", m.ExgName, m.Market, p)
				log.Error("invalid watchKLines", zap.String("pair", code), zap.Error(err))
				continue
			}
			res := &SubKLineState{
				Sid:      exs.ID,
				ExpectMS: expectMS,
			}
			m.klineStates[p] = res
		}
	}
	out, err := m.exchange.WatchOHLCVs(jobs, nil)
	if err != nil {
		m.KLines.Status = 0
		retryWaits.SetFail("watchKLines")
		log.Error("watch kline fail", zap.String("exg", m.ExgName),
			zap.Strings("pairs", pairs), zap.Error(err))
		return
	}
	retryWaits.Reset("watchKLines")
	if m.KLines.Status == 2 {
		return
	}
	m.KLines.Status = 2
	log.Info("start watch kline", zap.String("exg", m.ExgName), zap.Int("num", m.KLines.Len()))
	prefix := fmt.Sprintf("ohlcv_%s_%s_", m.ExgName, m.Market)
	unPrefix := "u" + prefix
	intvMa := core.NewEMA(0.1)
	// 5s统计更新一次K线全品种平均间隔到intvMa
	ns := core.NewNumSet(5000, func(stamp int64, data map[string]float64) {
		var sum float64
		for _, v := range data {
			sum += v
		}
		intvMa.Update(sum / float64(len(data)))
	})

	// The candlestick is received, sent to the robot, and saved to the database
	// 收到K线，发送到机器人，保存到数据库
	handleSubKLines := func(key string, arr []*banexg.Kline) {
		parts := strings.Split(key, "_")
		pair, curTF := parts[0], parts[1]
		state, ok := m.klineStates[pair]
		if !ok {
			code := fmt.Sprintf("%s.%s.%s", m.ExgName, m.Market, pair)
			log.Warn("no pair state: " + code)
			return
		}
		curTS := btime.UTCStamp()
		var err_ *errs.Error
		var intvMS float64
		// Send uohlcv subscription messages
		// 发送uohlcv订阅消息
		if curTS > state.LastNotify+900 {
			// at most 1 K-line message can be sent in 1s
			// 1s最多发送1次k线消息
			if state.LastNotify > 0 {
				intvMS = float64(curTS - state.LastNotify)
				ns.Update(curTS, key, intvMS)
			}
			err_ = m.spider.Broadcast(&utils.IOMsg{
				Action: unPrefix + pair,
				Data: NotifyKLines{
					TFSecs:   tfSecs,
					Interval: 1,
					Arr:      arr,
				},
			})
			state.LastNotify = curTS
		}
		if intvMa.Age > 3 && intvMS > intvMa.Val*5 {
			// 间隔超过平均间隔的5倍，认为有缺失（也有可能是交易所数据无变化未推送）
			log.Warn("ohlcv interval too big, may lost data", zap.String("k", key),
				zap.Float64("intv", intvMS), zap.Float64("avgIntv", intvMa.Val))
			state.PrevBar = nil
		}
		// Check the completed candlesticks
		// 检查已完成的k线
		finishes := arr[:len(arr)-1]
		last := arr[len(arr)-1]
		if state.PrevBar != nil && last.Time > state.PrevBar.Time {
			if len(finishes) == 0 || state.PrevBar.Time < finishes[0].Time {
				finishes = append([]*banexg.Kline{state.PrevBar}, finishes...)
			}
		}
		if state.PrevBar == nil || last.Time >= state.PrevBar.Time {
			state.PrevBar = last
			state.ExpectMS = last.Time
		}
		if len(finishes) > 0 {
			// There are completed k-lines, written to the database, and only then the message is broadcast
			// 有已完成的k线，写入到数据库，然后才广播消息
			writeQ <- &SaveKline{
				Sid:       state.Sid,
				TimeFrame: curTF,
				Arr:       finishes,
				MsgAction: prefix + pair,
			}
		}
		if err_ != nil {
			log.Error("broadCast kline fail", zap.String("pair", pair), zap.Error(err_))
		}
	}

	// 处理ws推送的K线数据
	pricePrefix := fmt.Sprintf("price_%s_%s", m.ExgName, m.Market)
	go func() {
		defer func() {
			m.KLines.Status = 0
			retryWaits.SetFail("watchKLines")
			log.Info("watch kline stopped", zap.String("exg", m.ExgName))
		}()
		for {
			first := <-out
			if first == nil {
				continue
			}
			cache := map[string][]*banexg.Kline{}
			curKey := fmt.Sprintf("%s_%s", first.Symbol, first.TimeFrame)
			cache[curKey] = []*banexg.Kline{&first.Kline}
			prices := map[string]float64{first.Symbol: first.Close}
		readCache:
			for {
				select {
				case val := <-out:
					prices[val.Symbol] = val.Close
					valKey := fmt.Sprintf("%s_%s", val.Symbol, val.TimeFrame)
					arr, _ := cache[valKey]
					if len(arr) > 0 && arr[len(arr)-1].Time == val.Time {
						arr[len(arr)-1] = &val.Kline
					} else {
						cache[valKey] = append(arr, &val.Kline)
					}
				default:
					break readCache
				}
			}
			if tfSecs < 60 {
				// 对于现货，无全市场价格推送，从kline获取发送
				err = m.spider.Broadcast(&utils.IOMsg{
					Action: pricePrefix,
					Data:   prices,
				})
				if err != nil {
					log.Error("broadCast price fail", zap.String("key", pricePrefix), zap.Error(err))
				}
			}
			for key, arr := range cache {
				handleSubKLines(key, arr)
			}
		}
	}()

	// 交易所ws可能偶发故障，这里定期检查，通过rest获取k线(约1m一次)
	delayMS := int64(3000)
	minMSecs := int64(60000)
	go func() {
		for {
			time.Sleep(time.Second * 2)
			curMS := btime.TimeMS()
			curMinuteMS := utils2.AlignTfMSecs(curMS, minMSecs)
			if curMS-curMinuteMS < delayMS {
				continue
			}
			alignMS := utils2.AlignTfMSecs(curMS, tfMSecs)
			adds := make(map[string]int)
			for pair, sta := range m.klineStates {
				if curMinuteMS > sta.ExpectMS+delayMS {
					bars, err := m.exchange.FetchOHLCV(pair, timeFrame, sta.ExpectMS, 0, nil)
					if err != nil {
						log.Warn("FetchOHLCV fail", zap.String("pair", pair), zap.Error(err))
						continue
					}
					if len(bars) > 0 {
						adds[pair] = len(bars)
						key := fmt.Sprintf("%s_%s", pair, timeFrame)
						last := bars[len(bars)-1]
						if last.Time < alignMS {
							// 交易所未返回未完成bar，添加最后一个未完成bar
							bars = append(bars, &banexg.Kline{
								Time:  last.Time + tfMSecs,
								Open:  last.Close,
								High:  last.Close,
								Low:   last.Close,
								Close: last.Close,
							})
						}
						handleSubKLines(key, bars)
					}
				}
			}
			if len(adds) > 0 {
				log.Warn("FetchOHLCV as ws timeout", zap.String("items", utils.MapToStr(adds, true, 0)))
			}
		}
	}()
}

func RunSpider(addr string) *errs.Error {
	server := utils.NewBanServer(addr, "spider")
	Spider = &LiveSpider{
		ServerIO: server,
		miners:   map[string]*Miner{},
	}
	server.InitConn = makeInitConn(Spider)
	go consumeWriteQ(5)
	sess, conn, err := orm.Conn(nil)
	if err != nil {
		return err
	}
	err = sess.PurgeKlineUn()
	if err != nil {
		conn.Release()
		return err
	}
	conn.Release()

	// Start the subscription monitor goroutine
	go Spider.monitorSubscriptions()

	return Spider.RunForever()
}

func (s *LiveSpider) getMiner(exgName, market string) *Miner {
	key := fmt.Sprintf("%s:%s", exgName, market)
	miner, ok := s.miners[key]
	var err *errs.Error
	if !ok {
		miner, err = newMiner(s, exgName, market)
		if err != nil {
			panic(err)
		}
		miner.init()
		err = miner.SubPairs("price")
		if err != nil {
			log.Error("sub prices fail", zap.Error(err))
		}
		s.miners[key] = miner
		log.Info("start miner for", zap.String("e", exgName), zap.String("m", market))
	}
	return miner
}

func makeInitConn(s *LiveSpider) func(*utils.BanConn) {
	return func(c *utils.BanConn) {
		handlePairs := func(data []byte, name string) (*Miner, []string) {
			var arr = make([]string, 0, 8)
			err := utils2.Unmarshal(data, &arr, utils2.JsonNumDefault)
			if err != nil {
				log.Warn("receive invalid pairs", zap.String("n", name),
					zap.String("in", string(data)), zap.Error(err))
				return nil, nil
			}
			if len(arr) < 4 {
				log.Error(name+" receive invalid", zap.Strings("msg", arr))
				return nil, nil
			}
			miner := s.getMiner(arr[0], arr[1])
			return miner, arr[2:]
		}
		c.Listens["watch_pairs"] = func(_ string, data []byte) {
			miner, arr := handlePairs(data, "watch_pairs")
			err := miner.SubPairs(arr[0], arr[1:]...)
			if err != nil {
				log.Error("spider.sub_pairs fail", zap.Error(err))
			}
		}
		c.Listens["unwatch_pairs"] = func(_ string, data []byte) {
			miner, arr := handlePairs(data, "unwatch_pairs")
			err := miner.UnSubPairs(arr[0], arr[1:]...)
			if err != nil {
				log.Error("spider.unsub_pairs fail", zap.Error(err))
			}
		}
	}
}
