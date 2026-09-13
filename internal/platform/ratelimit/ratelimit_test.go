package ratelimit

import (
	"crypto/sha256"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func newTestLimiter(t *testing.T, policy Policy, now func() time.Time) *Limiter {
	t.Helper()
	limiter, err := New(policy, now)
	if err != nil {
		t.Fatal(err)
	}
	return limiter
}

func TestPolicies(t *testing.T) {
	for _, tc := range []struct {
		name     string
		interval time.Duration
		burst    int
	}{
		{"general IP", time.Second / 5, 60},
		{"auth IP", 3 * time.Second, 10},
		{"register IP", 12 * time.Second, 3},
		{"login email", 12 * time.Second, 5},
		{"register email", time.Minute, 2},
		{"user", time.Second / 2, 30},
	} {
		t.Run(tc.name, func(t *testing.T) {
			now := time.Now()
			limiter := newTestLimiter(t, Policy{Interval: tc.interval, Burst: tc.burst}, func() time.Time { return now })
			for range tc.burst {
				if delay := limiter.Allow("a"); delay != 0 {
					t.Fatalf("burst request rejected: %s", delay)
				}
			}
			for range 3 {
				if delay := limiter.Allow("a"); delay != tc.interval {
					t.Fatalf("delay = %s, want %s", delay, tc.interval)
				}
			}
			if delay := limiter.Allow("b"); delay != 0 {
				t.Fatalf("another identity rejected: %s", delay)
			}
			now = now.Add(tc.interval - time.Nanosecond)
			if delay := limiter.Allow("a"); delay != time.Nanosecond {
				t.Fatalf("early refill: %s", delay)
			}
			now = now.Add(time.Nanosecond)
			if delay := limiter.Allow("a"); delay != 0 {
				t.Fatalf("refill rejected: %s", delay)
			}
			if delay := limiter.Allow("a"); delay != tc.interval {
				t.Fatalf("refill admitted extra request: %s", delay)
			}
			now = now.Add(time.Duration(tc.burst) * tc.interval)
			for range tc.burst {
				if delay := limiter.Allow("a"); delay != 0 {
					t.Fatalf("full recovery rejected: %s", delay)
				}
			}
		})
	}
}

func TestConcurrentLimit(t *testing.T) {
	now := time.Now()
	limiter := newTestLimiter(t, Policy{Interval: 12 * time.Second, Burst: 5}, func() time.Time { return now })
	var admitted atomic.Int32
	var workers sync.WaitGroup
	for range 100 {
		workers.Go(func() {
			if limiter.Allow("user@example.com") == 0 {
				admitted.Add(1)
			}
		})
	}
	workers.Wait()
	if got := admitted.Load(); got != 5 {
		t.Fatalf("admitted = %d, want 5", got)
	}
}

func TestCapacityAndCleanup(t *testing.T) {
	now := time.Now()
	limiter := newTestLimiter(t, Policy{Interval: 12 * time.Second, Burst: 5}, func() time.Time { return now })
	limiter.capacity = 2
	for range 5 {
		if limiter.Allow("active") != 0 {
			t.Fatal("initial burst rejected")
		}
	}
	for i := range 100 {
		if limiter.Allow(fmt.Sprint(i)) != 0 {
			t.Fatal("full table blocked a new identity")
		}
		if len(limiter.entries) != 2 || len(limiter.expiry) != 2 {
			t.Fatal("storage exceeded its capacity")
		}
		if delay := limiter.Allow("active"); delay != 12*time.Second {
			t.Fatalf("eviction reset the later recovery: %s", delay)
		}
	}
	now = now.Add(12 * time.Second)
	if limiter.Allow("new") != 0 {
		t.Fatal("recovered entry blocked a new identity")
	}
	if _, ok := limiter.entries[sha256.Sum256([]byte("99"))]; ok {
		t.Fatal("recovered entry survived cleanup")
	}
	if limiter.Allow("active") != 0 {
		t.Fatal("active identity did not refill")
	}
	now = now.Add(time.Minute)
	if limiter.Allow("last") != 0 || len(limiter.entries) != 1 || len(limiter.expiry) != 1 {
		t.Fatal("fully recovered entries survived cleanup")
	}
}

func TestEvictionTracksUpdatedRecovery(t *testing.T) {
	now := time.Now()
	limiter := newTestLimiter(t, Policy{Interval: time.Second, Burst: 3}, func() time.Time { return now })
	limiter.capacity = 2
	for _, key := range []string{"a", "b", "a", "a", "c"} {
		if limiter.Allow(key) != 0 {
			t.Fatal("unexpected rejection")
		}
	}
	if limiter.entries[sha256.Sum256([]byte("a"))] == nil || limiter.entries[sha256.Sum256([]byte("b"))] != nil {
		t.Fatal("eviction did not use the updated recovery time")
	}
}

func TestInvalidPolicy(t *testing.T) {
	for _, policy := range []Policy{
		{}, {Interval: -time.Second, Burst: 1}, {Interval: time.Second, Burst: 0},
		{Interval: time.Second, Burst: -1}, {Interval: 1 << 62, Burst: 2},
	} {
		if _, err := New(policy, nil); err == nil {
			t.Fatalf("invalid policy accepted: %+v", policy)
		}
	}
	limiter := newTestLimiter(t, Policy{Interval: time.Second, Burst: 1}, nil)
	if limiter.Allow("a") != 0 {
		t.Fatal("default clock rejected the first request")
	}
}

func TestIPKey(t *testing.T) {
	for _, tc := range []struct{ remote, want string }{
		{"192.0.2.1:1234", "192.0.2.1"},
		{"192.0.2.1:5678", "192.0.2.1"},
		{"192.0.2.1", "192.0.2.1"},
		{"[::ffff:192.0.2.1]:1234", "192.0.2.1"},
		{"[2001:db8:1:2::1]:1234", "2001:db8:1:2::/64"},
		{"2001:0db8:1:2::abcd", "2001:db8:1:2::/64"},
		{"[fe80::1%eth0]:1234", "fe80::/64"},
		{"malformed", "unknown"},
		{"", "unknown"},
	} {
		t.Run(tc.remote, func(t *testing.T) {
			if got := IPKey(tc.remote); got != tc.want {
				t.Fatalf("key = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestRetryAfter(t *testing.T) {
	for _, tc := range []struct {
		delay time.Duration
		want  string
	}{
		{0, "1"}, {time.Nanosecond, "1"}, {time.Second, "1"}, {time.Second + time.Nanosecond, "2"},
	} {
		t.Run(fmt.Sprint(tc.delay), func(t *testing.T) {
			if got := RetryAfter(tc.delay); got != tc.want {
				t.Fatalf("Retry-After = %q, want %q", got, tc.want)
			}
		})
	}
}
