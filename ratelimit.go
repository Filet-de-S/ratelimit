package ratelimit

import (
	"fmt"
	"sync/atomic"

	"time"
)

type rateLimit struct {
	burstRate, nGOing                int32
	limitPerTime, curRoutinesPerTime int32
	cmds                             <-chan func()
}

type Opts struct {
	RateLimitTime time.Duration // per sec/min/...
	CheckTime     time.Duration // sleep before retrying to check is cmd can run
}

var o = Opts{
	RateLimitTime: time.Second,
	CheckTime:     100 * time.Microsecond, // 0.1 ms = 1e4 per sec
}

func New(burstRate, limitPerTime int32, cmds <-chan func(), opts *Opts) error {
	switch {
	case burstRate < 1:
		return fmt.Errorf("burst rate can't be less than 1")
	case limitPerTime < 1:
		return fmt.Errorf("limit per time can't be less than 1")
	case cmds == nil:
		return fmt.Errorf("cmds are nil chan")
	case opts != nil && (opts.CheckTime < 0 || opts.RateLimitTime < 0):
		return fmt.Errorf("check options format")
	}

	rt := &rateLimit{
		burstRate:    burstRate,
		limitPerTime: limitPerTime,
		cmds:         cmds,
	}

	if opts != nil {
		o = *opts
	}

	go rt.run()

	return nil
}

func (rl *rateLimit) run() {
	now, prevTime := time.Now(), time.Now()

	for cmd := range rl.cmds {
	retry:
		now = time.Now()

		if now.Sub(prevTime) > o.RateLimitTime {
			atomic.StoreInt32(&rl.curRoutinesPerTime, 1)
			prevTime = now
			rl.runCmd(cmd, false, now)
			continue
		}

		if atomic.LoadInt32(&rl.curRoutinesPerTime) >= rl.limitPerTime {
			time.Sleep(o.CheckTime)
			goto retry
		}
		rl.runCmd(cmd, true, now)
	}
}

func (rl *rateLimit) runCmd(cmd func(), deltaMinute bool, prevTime time.Time) {
	for atomic.LoadInt32(&rl.nGOing) >= rl.burstRate {
		time.Sleep(o.CheckTime)
	}

	atomic.AddInt32(&rl.nGOing, 1)

	if deltaMinute {
		atomic.AddInt32(&rl.curRoutinesPerTime, 1)
	}

	go func() {
		cmd()

		atomic.AddInt32(&rl.nGOing, -1)
		if time.Now().Sub(prevTime) < o.RateLimitTime {
			atomic.AddInt32(&rl.curRoutinesPerTime, -1)
		}
	}()
}
