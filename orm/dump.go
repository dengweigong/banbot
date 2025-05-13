package orm

import (
	"encoding/gob"
	"github.com/banbox/banbot/btime"
	"github.com/banbox/banexg"
	"github.com/banbox/banexg/log"
	"github.com/sasha-s/go-deadlock"
	"go.uber.org/zap"
	"os"
)

type DumpRow struct {
	Time int64
	Type string
	Key  string
	Val  interface{}
}

var (
	dumpRows    []*DumpRow
	dumpLock    deadlock.Mutex
	dumpEncoder *gob.Encoder
	dumpFile    *os.File
)

const (
	DumpKline   = "kline"
	DumpStartUp = "startup"
)

func SetDump(file *os.File) {
	gob.Register(banexg.Kline{})
	dumpLock.Lock()
	dumpFile = file
	dumpEncoder = gob.NewEncoder(file)
	dumpLock.Unlock()
	AddDumpRow(DumpStartUp, "", nil)
}

func AddDumpRow(src, key string, val interface{}) {
	if dumpEncoder == nil {
		return
	}
	dumpLock.Lock()
	dumpRows = append(dumpRows, &DumpRow{
		Time: btime.UTCStamp(),
		Type: src,
		Key:  key,
		Val:  val,
	})
	dumpLock.Unlock()
}

func FlushDumps() {
	if dumpEncoder == nil {
		return
	}
	dumpLock.Lock()
	if len(dumpRows) > 0 {
		err := dumpEncoder.Encode(dumpRows)
		if err != nil {
			log.Error("dump rows fail", zap.Error(err))
		}
		dumpRows = nil
	}
	dumpLock.Unlock()
}

func CloseDump() {
	dumpLock.Lock()
	err := dumpFile.Close()
	if err != nil {
		log.Error("close dump file fail", zap.Error(err))
	}
	dumpFile = nil
	dumpEncoder = nil
	dumpLock.Unlock()
}
