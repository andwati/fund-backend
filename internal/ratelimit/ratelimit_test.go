package ratelimit

import (
	"testing"
	"time"
)

func clocked() (*Limiter, *time.Time) {
	now := time.Unix(1_000_000, 0)
	l := New()
	l.SetClock(func() time.Time { return now })
	return l, &now
}

func TestFixedWindow(t *testing.T) {
	l, now := clocked()
	for i := 0; i < 3; i++ {
		if ok, _ := l.Take("k", 3, time.Minute); !ok {
			t.Fatalf("call %d should pass", i)
		}
	}
	ok, retry := l.Take("k", 3, time.Minute)
	if ok || retry != 60 {
		t.Fatalf("4th call: ok=%v retry=%d", ok, retry)
	}
	*now = now.Add(30 * time.Second)
	if _, retry := l.Take("k", 3, time.Minute); retry != 30 {
		t.Fatalf("retry after 30s = %d", retry)
	}
	*now = now.Add(31 * time.Second)
	if ok, _ := l.Take("k", 3, time.Minute); !ok {
		t.Fatal("window should have reset")
	}
	if ok, _ := l.Take("other", 3, time.Minute); !ok {
		t.Fatal("keys are independent")
	}
}

func TestCooldownMarkOnlyWhenAsked(t *testing.T) {
	l, now := clocked()
	if ok, _ := l.Check("r", time.Hour); !ok {
		t.Fatal("unmarked key must pass")
	}
	l.Mark("r")
	ok, retry := l.Check("r", time.Hour)
	if ok || retry != 3600 {
		t.Fatalf("after mark: ok=%v retry=%d", ok, retry)
	}
	*now = now.Add(61 * time.Minute)
	if ok, _ := l.Check("r", time.Hour); !ok {
		t.Fatal("cooldown should have elapsed")
	}
}
