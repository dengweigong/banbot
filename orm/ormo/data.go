package ormo

import "github.com/sasha-s/go-deadlock"

var (
	accOpenODs     = map[string]map[int64]*InOutOrder{}            // all open orders: accName:orderId:order 全部打开的订单
	accTriggerODs  = map[string]map[string]map[int64]*InOutOrder{} // Waiting for the limit order to be triggered, only for real trading . accName:pair:orders 等待触发限价单的订单，仅实盘使用；
	lockOpenMap    = map[string]*deadlock.Mutex{}                  // Access to the lock of accOpenODs 访问accOpenODs的锁
	lockTriggerMap = map[string]*deadlock.Mutex{}                  // Access to the lock of accTriggerODs 访问accTriggerODs的锁
	lockOds        = map[string]*deadlock.Mutex{}                  // Modify the lock of the order to prevent concurrent modification 修改订单的锁，防止并发修改
	mLockOds       deadlock.Mutex
	mOpenLock      deadlock.Mutex
	HistODs        []*InOutOrder             // Historical orders, used as storage for backtesting. 历史订单，回测时作为存储用
	doneODs        = make(map[int64]bool)    // 用于防止重复添加到HistODs
	FakeOdId       = int64(1)                // Virtual order ID, used for temporary maintenance during backtesting. 虚拟订单ID，用于回测时临时维护
	OdEditListener func(*InOutOrder, string) // 订单修改的监听
)

const (
	InOutStatusInit = iota
	InOutStatusPartEnter
	InOutStatusFullEnter
	InOutStatusPartExit
	InOutStatusFullExit
	InOutStatusDelete
)

const (
	OdStatusInit = iota
	OdStatusPartOK
	OdStatusClosed
)

const (
	KeyStatusMsg = "status_msg"
)

const (
	OdInfoLegalCost  = "LegalCost"
	OdInfoStopAfter  = "StopAfter"
	OdInfoStopLoss   = "StopLoss"
	OdInfoTakeProfit = "TakeProfit"
	OdInfoClientID   = "ClientID"
)

const (
	OdActionEnter      = "Enter"
	OdActionExit       = "Exit"
	OdActionLimitEnter = "LimitEnter"
	OdActionLimitExit  = "LimitExit"
	OdActionStopLoss   = "StopLoss"
	OdActionTakeProfit = "TakeProfit"
)
