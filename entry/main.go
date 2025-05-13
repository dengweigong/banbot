package entry

import (
	"errors"
	"flag"
	"fmt"
	"github.com/banbox/banbot/utils"
	"github.com/sasha-s/go-deadlock"
	"net/http"
	_ "net/http/pprof"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/banbox/banbot/config"
	"github.com/banbox/banbot/core"
	"github.com/banbox/banbot/web"
	"github.com/banbox/banexg/errs"
	"github.com/banbox/banexg/log"
	"go.uber.org/zap"
)

func RunCmd() {
	defer func() {
		if r := recover(); r != nil {
			if err, ok := r.(*errs.Error); ok {
				log.Error("banbot panic", zap.Any("error", err))
			} else {
				log.Error("banbot panic", zap.Any("error", r), zap.Stack("stack"))
			}
			core.RunExitCalls()
			os.Exit(1)
		} else {
			core.RunExitCalls()
		}
	}()

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	// 在goroutine中等待信号
	go func() {
		<-sigChan
		if core.StopAll != nil {
			core.StopAll()
		}
		core.RunExitCalls()
		os.Exit(0)
	}()

	// disable deadlock by default
	deadlock.Opts.Disable = true

	args := os.Args[1:]
	if len(args) == 0 {
		runWeb(args)
		return
	}

	name := args[0]
	if strings.HasPrefix(name, "-") {
		if name == "-h" || name == "--help" {
			printMainHelp()
		} else {
			runWeb(args)
		}
		return
	}

	// 检查是否是子命令组
	if group := GetGroup(name); group != nil {
		if len(args) < 2 {
			printGroupHelp(name)
			return
		}
		// 处理子命令
		subName := args[1]
		if job := GetCmdJob(subName, name); job != nil {
			runJobCmd(args[1:], job, func(e error) {
				if e != nil {
					log.Error("parse command args fail", zap.String("cmd", args[1]), zap.Error(e))
				} else {
					log.Warn("unknown subcommand: " + args[1])
					printGroupHelp(name)
				}
			})
			return
		}
		printGroupHelp(name)
		return
	}

	// 处理根命令
	if job := GetCmdJob(name, ""); job != nil {
		runJobCmd(args, job, func(e error) {
			if e != nil {
				log.Error("parse command args fail", zap.String("cmd", args[0]), zap.Error(e))
			} else {
				log.Warn("unknown command: " + args[0])
				printMainHelp()
			}
		})
		return
	}

	printMainHelp()
}

func printMainHelp() {
	tpl := `
args: %s
banbot %v
please run command:
`
	var b strings.Builder
	b.WriteString(fmt.Sprintf(tpl, strings.Join(os.Args, " "), core.Version))

	if group := GetGroup(""); group != nil {
		for name, job := range group.Jobs {
			b.WriteString(fmt.Sprintf("    %-12s%s\n", name+":", job.Help))
		}
	}

	b.WriteString("\nor choose a subcommand:\n")
	for key, gp := range groupMap {
		if key == "" {
			continue
		}
		b.WriteString(fmt.Sprintf("    %-12s%s\n", gp.Name+":", gp.Help))
	}
	log.Warn(b.String())
}

func printGroupHelp(groupName string) {
	group := GetGroup(groupName)
	if group == nil {
		return
	}

	var b strings.Builder
	b.WriteString(fmt.Sprintf("\nbanbot %s:\n", groupName))
	for name, job := range group.Jobs {
		b.WriteString(fmt.Sprintf("    %-12s%s\n", name+":", job.Help))
	}
	b.WriteString("please choose a valid action")
	log.Warn(b.String())
}

func runWeb(args []string) {
	err_ := web.RunDev(args)
	if err_ != nil {
		panic(err_)
	}
}

func runJobCmd(sysArgs []string, job *CmdJob, fallback func(e error)) {
	core.SetRunMode(core.RunModeOther)
	if job.RunRaw != nil {
		err_ := job.RunRaw(sysArgs[1:])
		if err_ != nil {
			panic(err_)
		}
		return
	}
	name, subArgs := sysArgs[0], sysArgs[1:]
	var args config.CmdArgs
	var sub = flag.NewFlagSet(name, flag.ExitOnError)
	err_ := bindSubFlags(&args, sub, job.Options...)
	if err_ == nil {
		err_ = sub.Parse(subArgs)
	}
	if err_ != nil {
		fallback(err_)
		return
	}
	args.Init()
	if core.MemProfile {
		go func() {
			log.Info("mem profile serve http at :6060 ...")
			err_ = http.ListenAndServe(":6060", nil)
			if err_ != nil {
				log.Error("run mem profile http fail", zap.Error(err_))
			}
		}()
	}
	if core.CPUProfile {
		wd, err_ := os.Getwd()
		if err_ != nil {
			panic(err_)
		}
		outPath := filepath.Join(wd, "cpu.profile")
		err := utils.StartCpuProfile(outPath, 6060)
		if err != nil {
			panic(err)
		}
		log.Info("start cpu profile", zap.String("to", outPath))
	}
	err := job.Run(&args)
	if err != nil {
		panic(err)
	}
}

func bindSubFlags(args *config.CmdArgs, cmd *flag.FlagSet, opts ...string) error {
	args.BindToFlag(cmd)
	cmd.BoolVar(&core.CPUProfile, "cpu-profile", false, "enable cpu profile")
	cmd.BoolVar(&core.MemProfile, "mem-profile", false, "enable memory profile")

	for _, key := range opts {
		switch key {
		case "stake_amount":
			cmd.Float64Var(&args.StakeAmount, "stake-amount", 0.0, "Override `stake_amount` in config")
		case "stake_pct":
			cmd.Float64Var(&args.StakePct, "stake-pct", 0.0, "Override `stake_pct` in config")
		case "pairs":
			cmd.StringVar(&args.RawPairs, "pairs", "", "comma-separated pairs")
		case "with_spider":
			cmd.BoolVar(&args.WithSpider, "spider", false, "start spider if not running")
		case "timerange":
			cmd.StringVar(&args.TimeRange, "timerange", "", "set timerange")
		case "timestart":
			cmd.StringVar(&args.TimeStart, "timestart", "", "set start time, allow multiple formats")
		case "timeend":
			cmd.StringVar(&args.TimeEnd, "timeend", "", "set end time, timestart is required")
		case "timeframes":
			cmd.StringVar(&args.RawTimeFrames, "timeframes", "", "comma-seperated timeframes to use")
		case "medium":
			cmd.StringVar(&args.Medium, "medium", "", "data medium:db,file")
		case "tables":
			cmd.StringVar(&args.RawTables, "tables", "", "db tables, comma-separated")
		case "force":
			cmd.BoolVar(&args.Force, "force", false, "skip confirm")
		case "prg":
			cmd.StringVar(&args.PrgOut, "prg", "", "prefix for progress in stdout")
		case "in":
			cmd.StringVar(&args.InPath, "in", "", "input file or directory")
		case "in_type":
			cmd.StringVar(&args.InType, "in-type", "", "input data type")
		case "out":
			cmd.StringVar(&args.OutPath, "out", "", "output file or directory")
		case "adj":
			cmd.StringVar(&args.AdjType, "adj", "", "pre/post/none for kline")
		case "tz":
			cmd.StringVar(&args.TimeZone, "tz", "", "timeZone, default: utc")
		case "exg_real":
			cmd.StringVar(&args.ExgReal, "exg-real", "", "real exchange")
		case "opt_rounds":
			cmd.IntVar(&args.OptRounds, "opt-rounds", 30, "rounds num for single optimize job")
		case "sampler":
			cmd.StringVar(&args.Sampler, "sampler", "bayes", "hyper optimize method, tpe/bayes/random/cmaes/ipop-cmaes/bipop-cmaes")
		case "picker":
			cmd.StringVar(&args.Picker, "picker", "good3", "Method for selecting targets from multiple hyperparameter optimization results")
		case "alpha":
			cmd.Float64Var(&args.Alpha, "alpha", 1, "ma alpha for calculating ema in hyperOpt")
		case "pair_picker":
			cmd.StringVar(&args.PairPicker, "pair-picker", "", "min sharpe val for pairs in bt_opt mode")
		case "each_pairs":
			cmd.BoolVar(&args.EachPairs, "each-pairs", false, "run for each pairs")
		case "concur":
			cmd.IntVar(&args.Concur, "concur", 1, "Concurrent Number")
		case "review_period":
			cmd.StringVar(&args.ReviewPeriod, "review-period", "3y", "review period, default: 3 years")
		case "run_period":
			cmd.StringVar(&args.RunPeriod, "run-period", "6M", "run period, default: 6 months")
		case "batch_size":
			cmd.IntVar(&args.BatchSize, "batch-size", 0, "batch size for task")
		case "run_every":
			cmd.StringVar(&args.RunEveryTF, "run-every", "", "run every ? timerange")
		case "out_type":
			cmd.StringVar(&args.OutType, "out-type", "", "output data type")
		case "separate":
			cmd.BoolVar(&args.Separate, "separate", false, "run policy separately for backtest")
		default:
			return errors.New(fmt.Sprintf("unknown argument: %s", key))
		}
	}
	return nil
}
