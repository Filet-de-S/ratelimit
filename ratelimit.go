package ratelimit

import (
	"errors"
	"fmt"
	"sync/atomic"

	"time"
)

type Opts struct {
	BurstRate       int64  // max concurrent commands
	CMDLimitPerTime uint64 // max commands per {time}
	Commands        <-chan Command
	ReturnChan      chan<- Command

	// per sec/min/...
	RateLimitTime *time.Duration
	// how often check for new tokens
	NewTokenIssueFreq *time.Duration
}

type Command interface {
	Do()
	Cancel()
}

type rateLimit struct {
	burstRate         int64
	cmdsLimitPerTime  uint64
	rateLimitTime     time.Duration
	newTokenIssueFreq uint64
	tokenPerTime      uint64
	commands          <-chan Command

	returnChanBuf  chan Command
	returnChanReal chan<- Command
}

type state struct {
	tokens   uint64
	leftover uint64
	lastUpd  time.Time
}

// Rate limit implementation of leaky token algorithm
// By default limits per minute and "checks" every ms for new tokens
//
// NB: LEAKS IF NOT CLOSE Commands chan
func Run(o *Opts) error {
	name := errors.New("rate limiter")
	switch {
	case o == nil:
		return fmt.Errorf("%s: Opts is nil", name)
	case o.Commands == nil:
		return fmt.Errorf("%s: Commands is nil chan", name)
	case o.BurstRate < 1:
		return fmt.Errorf("%s: BurstRate must be > 0", name)
	case o.CMDLimitPerTime < 1:
		return fmt.Errorf("%s: CMDLimitPerTime must be > 0", name)
	case o.NewTokenIssueFreq != nil && *o.NewTokenIssueFreq < 1:
		return fmt.Errorf("%s: NewTokenIssueFreq must be > 0", name)
	case o.RateLimitTime != nil && *o.RateLimitTime < 1:
		return fmt.Errorf("%s: RateLimitTime must be > 0", name)
	case o.RateLimitTime != nil && o.NewTokenIssueFreq != nil &&
		*o.RateLimitTime < *o.NewTokenIssueFreq:
		return fmt.Errorf("%s: NewTokenIssueFreq must be < RateLimitTime", name)
	}

	rt := initRates(*o)

	go rt.run()

	return nil
}

func initRates(o Opts) *rateLimit {
	rt := &rateLimit{
		burstRate:        o.BurstRate,
		cmdsLimitPerTime: o.CMDLimitPerTime,
		commands:         o.Commands,
		returnChanReal:   o.ReturnChan,
	}

	if o.ReturnChan != nil {
		rt.returnChanBuf = make(chan Command, 1024)
		go returning(rt.returnChanBuf, rt.returnChanReal)
	}

	rt.rateLimitTime = time.Minute
	if o.RateLimitTime != nil {
		rt.rateLimitTime = *o.RateLimitTime
	}

	rt.newTokenIssueFreq = uint64(time.Millisecond)
	if o.NewTokenIssueFreq != nil {
		rt.newTokenIssueFreq = uint64(*o.NewTokenIssueFreq)
	}

	rt.tokenPerTime = uint64(rt.rateLimitTime) / rt.cmdsLimitPerTime

	return rt
}

func (rl *rateLimit) run() {
	var nGOing int64
	state := state{
		tokens:   rl.cmdsLimitPerTime,
		lastUpd:  time.Now(),
		leftover: 0,
	}

	for cmd := range rl.commands {
		if atomic.LoadInt64(&nGOing) >= rl.burstRate {
			rl.returnCase(cmd)
			continue
		}

		state = rl.updState(state)

		if state.tokens > 0 {
			state.tokens--
			atomic.AddInt64(&nGOing, 1)

			go func(f Command) {
				f.Do()
				atomic.AddInt64(&nGOing, -1)
			}(cmd)

		} else {
			rl.returnCase(cmd)
		}
	} //end for

	if rl.returnChanBuf != nil {
		close(rl.returnChanBuf)
	}
}

func (rl *rateLimit) updState(s state) state {
	if s.tokens == rl.cmdsLimitPerTime {
		return s
	}

	now := time.Now()

	elapsed := uint64(now.Sub(s.lastUpd)) + s.leftover
	// count elapsed time with fraction of newTokenIssueFreq,
	// ie do we must upd tokens now or not
	if (elapsed / rl.newTokenIssueFreq) < 1 {
		return s
	}

	newTokens := elapsed / rl.tokenPerTime
	s.leftover = elapsed - (newTokens * rl.tokenPerTime)
	s.tokens += newTokens

	if s.tokens >= rl.cmdsLimitPerTime {
		s.tokens = rl.cmdsLimitPerTime
		s.leftover = 0
	}

	s.lastUpd = now
	return s
}

func (rl *rateLimit) returnCase(cmd Command) {
	switch {
	case rl.returnChanReal != nil:
		rl.returnChanBuf <- cmd
	default:
		cmd.Cancel()
	}
}

func returning(buf <-chan Command, real chan<- Command) {
	for cmd := range buf {
		real <- cmd
	}
	close(real)
}
