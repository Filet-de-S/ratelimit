package ratelimit

import (
	"sync"
	"testing"
	"time"
)

func someCmd(dur time.Duration) {
	time.Sleep(dur)
}

func TestCase(t *testing.T) {
	ch := make(chan func())

	err := New(100, 1, ch, &Opts{RateLimitTime: time.Second})
	if err != nil {
		t.Fatal(err)
	}

	wg := sync.WaitGroup{}
	now := time.Now()

	for i := 0; i < 5; i++ {
		wg.Add(1)
		ch <- func() {
			someCmd(time.Millisecond)
			wg.Done()
		}
	}

	wg.Wait()
	since := time.Since(now)

	if since > 6 * time.Millisecond || since < 5 * time.Millisecond {
		t.Fatal("wrong timing", since)
	}
}

