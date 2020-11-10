package ratelimit

import (
	"fmt"
	"log"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func someCmd(dur time.Duration) {
	time.Sleep(dur)
}

func TestFunctional(t *testing.T) {
	testTokenBudget(t, state{
		tokens:   0,
		leftover: 0,
		lastUpd:  time.Now().Add(-6501 * time.Millisecond),
	}, 1, 501, 10, true, "easyMath, minutes:")

	testTokenBudget(t, state{
		tokens:   9,
		leftover: 0,
		lastUpd:  time.Now().Add(-12300 * time.Millisecond),
	}, 10, 0, 10, true, "overfill, minutes:")

	testTokenBudget(t, state{
		tokens:   0,
		leftover: uint64(5 * time.Second),
		lastUpd:  time.Now().Add(-time.Second),
	}, 1, 0, 10, true, "leftover, minutes:")

	var limitPerTime uint64 = 25
	rateLimitTime = time.Second
	newTokenIssueFreq = uint64(time.Second / 2)
	tokenPerMS = uint64(rateLimitTime) / limitPerTime
	testTokenBudget(t, state{
		tokens:   0,
		leftover: 0,
		lastUpd:  time.Now().Add(-500 * time.Millisecond),
	}, 12, 20, limitPerTime, false, "seconds:")

	//wait a bit for finish of prev runtime.test goroutines
	time.Sleep(30 * time.Millisecond)

	testLimitPerTime(t)
	time.Sleep(100 * time.Millisecond)

	testBurst(t)
	time.Sleep(100 * time.Millisecond)

	testBurst2(t)
	time.Sleep(100 * time.Millisecond)

	testReturnChan(t)
}

func testTokenBudget(t *testing.T, st state,
	expT, expL, limPerTime uint64, init bool, name string) {
	if init {
		_ = initRates(Opts{CMDLimitPerTime: limPerTime})
	}
	s := updState(st, limPerTime)

	left := time.Duration(s.leftover).Milliseconds()
	if s.tokens != expT || left != int64(expL) {
		t.Fatal(name, "token budget differ", s.tokens, expT, "\n\tleft:", s.leftover, left, expL)
	}
}

func testLimitPerTime(t *testing.T) {
	sleepTime_ := time.Second / 2
	testFunc(t, &Opts{
		BurstRate:       100,
		CMDLimitPerTime: 5,
		RateLimitTime:   &sleepTime_,
	}, 5, 10, sleepTime_, 590*time.Millisecond, "testLimitPerTime")
}

func testBurst(t *testing.T) {
	sleepTime_ := time.Second / 2
	testFunc(t, &Opts{
		BurstRate:       50,
		CMDLimitPerTime: 100,
		RateLimitTime:   &sleepTime_,
	}, 50, 100, sleepTime_, 590*time.Millisecond, "testBurst")
}

func testBurst2(t *testing.T) {
	sleepTime_ := time.Second
	testFunc(t, &Opts{
		BurstRate:       100,
		CMDLimitPerTime: 100,
		RateLimitTime:   &sleepTime_,
	}, 100, 100, sleepTime_, 1090*time.Millisecond, "testBurst2")
}

func testReturnChan(t *testing.T) {
	backChan := make(chan func(), 50)
	sleepTime_ := time.Second / 2
	testFunc(t, &Opts{
		BurstRate:       50,
		CMDLimitPerTime: 100,
		RateLimitTime:   &sleepTime_,
		ReturnChan:      backChan,
	}, 50, 100, sleepTime_, 590*time.Millisecond, "testReturnChan")

	i := 0
	for range backChan {
		i++
	}
	if i != 50 {
		t.Fatal("testReturnChan expects 50 return cmds, have", i)
	}
}

func testFunc(t *testing.T, o *Opts, needToBeDone, nLoop int,
	sleepTime, gt time.Duration, name string) {
	name += ":"

	ch := make(chan func(), needToBeDone)
	o.Commands = ch

	startgn := runtime.NumGoroutine()

	err := RunRateLimiter(o)
	if err != nil {
		t.Fatal(name, err)
	}

	wg := sync.WaitGroup{}
	wg.Add(needToBeDone)
	var completed int32

	done := make(chan bool)
	time.AfterFunc(gt, func() {
		if _, ok := <-done; ok {
			t.Fatal(name, "expected to finish")
		}
	})
	now := time.Now()

	for i := 0; i < nLoop; i++ {
		ch <- func() {
			someCmd(sleepTime)
			wg.Done()
			atomic.AddInt32(&completed, 1)
		}
	}
	close(ch)

	wg.Wait()
	since := time.Since(now)
	gn := runtime.NumGoroutine()

	switch {
	case since < sleepTime:
		t.Fatal(name, "wrong timing", since)
	case needToBeDone != int(completed):
		t.Fatal(name, "needToBeDone and completed differ:", needToBeDone, completed)
	case gn-startgn-1 > 0: // -1: time.AfterFunc (NB: could be additional G in test.runtime)
		t.Fatal(name, "too much goroutines ", gn)
	}
	close(done)
}

func BenchmarkRateLimiter(b *testing.B) {
	for n := 1e5; n < 1e8; n *= 10 {
		ch := make(chan func(), int32(1e5))
		o := Opts{
			BurstRate:       int64(n / 10 / 10),
			CMDLimitPerTime: uint64(n),
			Commands:        ch,
		}
		rateLimTime := time.Second
		o.RateLimitTime = &rateLimTime

		startgn := runtime.NumGoroutine()

		err := RunRateLimiter(&o)
		if err != nil {
			b.Fatal(err)
		}

		l := int(n)
		now := time.Now()

		var completed int64
		for i := 0; i < l; i++ {
			ch <- func() {
				time.Sleep(time.Millisecond)
				atomic.AddInt64(&completed, 1)
			}
		}
		close(ch)

		time.Sleep(time.Millisecond)
		for i := 0; i < 2; i++ {
			log.Println(float64(completed), "completed with burstRate", n/10/10, "from", n, "in", time.Since(now),
				"\n\t~active goroutines:", runtime.NumGoroutine()-startgn)
			if completed == int64(n) {
				break
			}
			if i+1 < 2 {
				fmt.Println("\tSleeping for", rateLimTime)
				time.Sleep(rateLimTime)
			}
		}
	}
}
