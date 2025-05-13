package strat

import (
	"github.com/banbox/banbot/config"
	"github.com/banbox/banbot/core"
	"github.com/banbox/banbot/orm"
	"github.com/banbox/banbot/orm/ormo"
	"github.com/banbox/banexg"
	ta "github.com/banbox/banta"
)

type CalcDDExitRate func(s *StratJob, od *ormo.InOutOrder, maxChg float64) float64
type PickTimeFrameFunc func(symbol string, tfScores []*core.TfScore) string
type FnOdChange func(acc string, od *ormo.InOutOrder, evt int)
type FnOnPostApi func(client *core.ApiClient, msg map[string]interface{}, jobs map[string]map[string]*StratJob) error

type Warms map[string]map[string]int

type TradeStrat struct {
	Name          string
	Version       int
	WarmupNum     int
	OdBarMax      int     // default: 500. Estimated maximum bar hold number for orders (used to find unfinished positions for backtesting) 预计订单持仓最大bar数量（用于查找回测未完成持仓）
	MinTfScore    float64 // Minimum time cycle quality, default 0.8 最小时间周期质量，默认0.8
	WatchBook     bool
	DrawDownExit  bool
	BatchInOut    bool    // Whether to batch execute entry/exit 是否批量执行入场/出场
	BatchInfo     bool    // whether to perform batch processing after OninfoBar 是否对OnInfoBar后执行批量处理
	StakeRate     float64 // Relative basic amount billing rate 相对基础金额开单倍率
	StopLoss      float64 // Default stoploss without leverage 此策略默认止损比率，不带杠杆
	StopEnterBars int
	EachMaxLong   int      // max number of long open orders for one pair, -1 for disable
	EachMaxShort  int      // max number of short open orders for one pair, -1 for disable
	RunTimeFrames []string // Allow running time period, use global configuration when not provided 允许运行的时间周期，不提供时使用全局配置
	Outputs       []string // The content of the text file output by the strategy, where each string is one line 策略输出的文本文件内容，每个字符串是一行
	Policy        *config.RunPolicyConfig

	OnPairInfos         func(s *StratJob) []*PairSub
	OnSymbols           func(items []string) []string // return modified pairs
	OnStartUp           func(s *StratJob)
	OnBar               func(s *StratJob)
	OnInfoBar           func(s *StratJob, e *ta.BarEnv, pair, tf string)    // Other dependent bar data 其他依赖的bar数据
	OnTrades            func(s *StratJob, trades []*banexg.Trade)           // Transaction by transaction data 逐笔交易数据
	OnBatchJobs         func(jobs []*StratJob)                              // All target jobs at the current time, used for bulk opening/closing of orders 当前时间所有标的job，用于批量开单/平仓
	OnBatchInfos        func(tf string, jobs map[string]*JobEnv)            // All info marked jobs at the current time, used for batch processing 当前时间所有info标的job，用于批量处理
	OnCheckExit         func(s *StratJob, od *ormo.InOutOrder) *ExitReq     // Custom order exit logic 自定义订单退出逻辑
	OnOrderChange       func(s *StratJob, od *ormo.InOutOrder, chgType int) // Order update callback 订单更新回调
	GetDrawDownExitRate CalcDDExitRate                                      // Calculate the ratio of tracking profit taking, drawdown, and exit 计算跟踪止盈回撤退出的比率
	PickTimeFrame       PickTimeFrameFunc                                   // Choose a suitable trading cycle for the specified currency 为指定币选择适合的交易周期
	OnPostApi           FnOnPostApi                                         // callback for post api PostAPI时的策略回调
	OnShutDown          func(s *StratJob)                                   // Callback when the robot stops 机器人停止时回调
}

const (
	OdChgNew       = iota // New order 新订单
	OdChgEnter            // Create an entry order 创建入场订单
	OdChgEnterFill        // Order entry completed 订单入场完成
	OdChgExit             // Order request to exit  订单请求退出
	OdChgExitFill         // Order exit completed 订单退出完成
)

type JobEnv struct {
	Job    *StratJob
	Env    *ta.BarEnv
	Symbol string
}

/*
BatchMap
Batch execution task pool for all targets in the current exchange market time cycle
当前交易所-市场-时间周期下，所有标的的批量执行任务池
*/
type BatchMap struct {
	Map     map[string]*JobEnv
	TFMSecs int64
	ExecMS  int64 // The timestamp for executing batch tasks is delayed by a few seconds upon receiving a new target; Delay exceeded and BatchMS did not receive, start execution 执行批量任务的时间戳，每收到新的标的，推迟几秒；超过DelayBatchMS未收到，开始执行
}

type PairSub struct {
	Pair      string
	TimeFrame string
	WarmupNum int
}

type StratJob struct {
	Strat         *TradeStrat
	Env           *ta.BarEnv
	Entrys        []*EnterReq
	Exits         []*ExitReq
	LongOrders    []*ormo.InOutOrder
	ShortOrders   []*ormo.InOutOrder
	Symbol        *orm.ExSymbol     // The currently running currency 当前运行的币种
	TimeFrame     string            // The current running time cycle 当前运行的时间周期
	Account       string            // The account to which the current task belongs 当前任务所属账号
	TPMaxs        map[int64]float64 // Price at maximum profit of the order 订单最大盈利时价格
	OrderNum      int               // All unfinished order quantities 所有未完成订单数量
	EnteredNum    int               // The number of fully/part entered orders 已完全/部分入场的订单数量
	CheckMS       int64             // Last timestamp of signal processing, 13 milliseconds 上次处理信号的时间戳，13位毫秒
	LastBarMS     int64             // End timestamp of the previous candlestick 上个K线的结束时间戳，13位毫秒
	MaxOpenLong   int               // Max open number for long position, 0 for any, -1 for disabled 最大开多数量，0不限制，-1禁止开多
	MaxOpenShort  int               // Max open number for short position, 0 for any, -1 for disabled 最大开空数量，0不限制，-1禁止开空
	CloseLong     bool              // whether to allow close long position 是否允许平多
	CloseShort    bool              // whether to allow close short position 是否允许平空
	ExgStopLoss   bool              // whether to allow stop losses in exchange side 是否允许交易所止损
	LongSLPrice   float64           // Default long stop loss price when opening a position 开仓时默认做多止损价格
	ShortSLPrice  float64           // Default short stop price when opening a position 开仓时默认做空止损价格
	ExgTakeProfit bool              // whether to allow take profit in exchange side  是否允许交易所止盈
	LongTPPrice   float64           // Default long take profit price when opening a position 开仓时默认做多止盈价格
	ShortTPPrice  float64           // Default short take profit price when opening a position 开仓时默认做空止盈价格
	IsWarmUp      bool              // whether in a preheating state 当前是否处于预热状态
	More          interface{}       // Additional information for policy customization 策略自定义的额外信息
}

/*
EnterReq
打开一个订单。默认开多。如需开空short=False
*/
type EnterReq struct {
	Tag             string  // Entry signal 入场信号
	StratName       string  // Strategy Name 策略名称
	Short           bool    // Whether to short sell or not 是否做空
	OrderType       int     // 订单类型, core.OrderType*
	Limit           float64 // The entry price of a limit order will be submitted as a limit order when specified 限价单入场价格，指定时订单将作为限价单提交
	CostRate        float64 // The opening ratio is set to 1 times by default according to the configuration. Used for calculating LegalList 开仓倍率、默认按配置1倍。用于计算LegalCost
	LegalCost       float64 // Spend the amount in fiat currency. Ignore CostRate when specified 花费法币金额。指定时忽略CostRate
	Leverage        float64 // Leverage ratio 杠杆倍数
	Amount          float64 // The number of admission targets is calculated by LegalList and price 入场标的数量，由LegalCost和price计算
	StopLossVal     float64 // The distance from the entry price to the stop loss price is used to calculate StopLoss 入场价格到止损价格的距离，用于计算StopLoss
	StopLoss        float64 // Stop loss trigger price, submit a stop loss order on the exchange when it is not empty 止损触发价格，不为空时在交易所提交一个止损单
	StopLossLimit   float64 // Stop loss limit price, does not provide the use of StopLoss 止损限制价格，不提供使用StopLoss
	StopLossRate    float64 // Stop loss exit ratio, 0 means all exits, needs to be between (0,1) 止损退出比例，0表示全部退出，需介于(0,1]之间
	StopLossTag     string  // Reason for Stop Loss 止损原因
	TakeProfitVal   float64 // The distance from the entry price to the take profit price is used to calculate TakeProfit 入场价格到止盈价格的距离，用于计算TakeProfit
	TakeProfit      float64 // When the take profit trigger price is not empty, submit a take profit order on the exchange. 止盈触发价格，不为空时在交易所提交一个止盈单。
	TakeProfitLimit float64 // Profit taking limit price, TakeProfit is not available for use 止盈限制价格，不提供使用TakeProfit
	TakeProfitRate  float64 // Take profit exit ratio, 0 indicates full exit, needs to be between (0,1) 止盈退出比率，0表示全部退出，需介于(0,1]之间
	TakeProfitTag   string  // Reason for profit taking 止盈原因
	StopBars        int     // If the entry limit order exceeds how many bars and is not executed, it will be cancelled 入场限价单超过多少个bar未成交则取消
	ClientID        string  // used as suffix of ClientOrderID to exchange
}

/*
ExitReq
请求平仓
*/
type ExitReq struct {
	Tag        string  // Exit signal 退出信号
	StratName  string  // Strategy Name 策略名称
	EnterTag   string  // Only exit orders with EnterTag as the entry signal 只退出入场信号为EnterTag的订单
	Dirt       int     // core.OdDirt* long/short/both
	OrderType  int     // 订单类型, core.OrderType*
	Limit      float64 // Limit order exit price, the order will be submitted as a limit order when specified 限价单退出价格，指定时订单将作为限价单提交
	ExitRate   float64 // Exit rate, default is 100%, which means all orders are exited 退出比率，默认100%即所有订单全部退出
	Amount     float64 // The number of targets to be exited. ExitRate is invalid when specified 要退出的标的数量。指定时ExitRate无效
	OrderID    int64   // Only exit specified orders 只退出指定订单
	UnFillOnly bool    // When True, exit orders which hasn't been filled only. True时只退出尚未入场的部分
	FilledOnly bool    // Only exit orders that have already entered when True True时只退出已入场的订单
	Force      bool    // Whether to force exit 是否强制退出
}

type accStratLimits map[string]*stgLimits

type stgLimits struct {
	limit  int
	strats map[string]int
}
