package ratelimit

import (
	"errors"
	"fmt"
	"sync/atomic"

	"time"
)

type Opts struct {
	BurstRate       int64  // max parallel commands
	CMDLimitPerTime uint64 // max commands per {time}
	Commands        <-chan func()
	ReturnChan      chan<- func()

	// per sec/min/...
	RateLimitTime *time.Duration
	// how often check for new tokens
	NewTokenIssueFreq *time.Duration
}

type rateLimit struct {
	burstRate       int64
	cmdLimitPerTime uint64
	commands        <-chan func()
	returnChanBuf   chan func()
	returnChanReal  chan<- func()
}

type state struct {
	tokens   uint64
	leftover uint64
	lastUpd  time.Time
}

var (
	rateLimitTime     time.Duration
	newTokenIssueFreq uint64
	tokenPerMS        uint64
	returnChan        bool
)

// Rate limit implementation of leaky token algorithm
// By default limits per minute and "checks" every ms for new tokens
//
// NB: LEAKS IF NOT CLOSE Commands chan
func RunRateLimiter(o *Opts) error {
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
		burstRate:       o.BurstRate,
		cmdLimitPerTime: o.CMDLimitPerTime,
		commands:        o.Commands,
		returnChanReal:  o.ReturnChan,
	}

	returnChan = false
	if o.ReturnChan != nil {
		returnChan = true
		rt.returnChanBuf = make(chan func(), 1024)
		go returning(rt.returnChanBuf, rt.returnChanReal)
	}

	rateLimitTime = time.Minute
	if o.RateLimitTime != nil {
		rateLimitTime = *o.RateLimitTime
	}

	newTokenIssueFreq = uint64(time.Millisecond)
	if o.NewTokenIssueFreq != nil {
		newTokenIssueFreq = uint64(*o.NewTokenIssueFreq)
	}

	tokenPerMS = uint64(rateLimitTime) / rt.cmdLimitPerTime

	return rt
}

func (rl *rateLimit) run() {
	var nGOing int64
	state := state{
		tokens:   rl.cmdLimitPerTime,
		lastUpd:  time.Now(),
		leftover: 0,
	}

	for cmd := range rl.commands {
		if atomic.LoadInt64(&nGOing) >= rl.burstRate {
			if returnChan {
				rl.returnChanBuf <- cmd
			}
			continue
		}

		state = updState(state, rl.cmdLimitPerTime)

		if state.tokens > 0 {
			state.tokens--
			atomic.AddInt64(&nGOing, 1)

			go func(f func()) {
				f()
				atomic.AddInt64(&nGOing, -1)
			}(cmd)

		} else if returnChan {
			rl.returnChanBuf <- cmd
		}
	} //end for

	if returnChan {
		close(rl.returnChanBuf)
	}
}

func updState(s state, limitPerTime uint64) state {
	if s.tokens == limitPerTime {
		return s
	}

	now := time.Now()

	elapsed := uint64(now.Sub(s.lastUpd)) + s.leftover
	// count elapsed time with fraction of newTokenIssueFreq,
	// ie do we must upd tokens now or not
	if (elapsed / newTokenIssueFreq) < 1 {
		return s
	}

	newTokens := elapsed / tokenPerMS
	s.leftover = elapsed - (newTokens * tokenPerMS)
	s.tokens += newTokens

	if s.tokens >= limitPerTime {
		s.tokens = limitPerTime
		s.leftover = 0
	}

	s.lastUpd = now
	return s
}

func returning(buf <-chan func(), real chan<- func()) {
	for cmd := range buf {
		real <- cmd
	}
	close(real)
}
