package ratelimit

import (
	"cmp"
	"container/heap"
	"crypto/sha256"
	"fmt"
	"net"
	"net/netip"
	"slices"
	"sync"
	"sync/atomic"
	"time"
)

type Policy struct {
	// Interval gives the time to restore one token.
	Interval time.Duration
	Burst    int
}

const maxEntries = 50_000

var nextID atomic.Uint64

type Limiter struct {
	id       uint64
	mu       sync.Mutex
	entries  map[[sha256.Size]byte]*entry
	expiry   expiryHeap
	policy   Policy
	capacity int
	now      func() time.Time
}

func New(policy Policy, now func() time.Time) (*Limiter, error) {
	if policy.Interval <= 0 || policy.Burst <= 0 || int64(policy.Burst) > int64((1<<63-1)/policy.Interval) {
		return nil, fmt.Errorf("rate limit interval and burst must be positive and their product must fit time.Duration")
	}
	if now == nil {
		now = time.Now
	}
	return &Limiter{
		id:      nextID.Add(1),
		entries: make(map[[sha256.Size]byte]*entry), policy: policy,
		capacity: maxEntries, now: now,
	}, nil
}

// Allow consumes one token. A positive result gives the delay before the next attempt.
func (l *Limiter) Allow(key string) time.Duration {
	return AllowAll(Check{Limiter: l, Key: key})
}

type Check struct {
	Limiter *Limiter
	Key     string
}

// AllowAll consumes one token for each check only when all checks allow the request.
// A positive result gives the longest delay among the rejecting checks.
func AllowAll(checks ...Check) time.Duration {
	type pending struct {
		limiter *Limiter
		digest  [sha256.Size]byte
		now     time.Time
	}
	items := make([]pending, len(checks))
	for i, check := range checks {
		items[i] = pending{limiter: check.Limiter, digest: sha256.Sum256([]byte(check.Key))}
	}
	// Lock in ID order to prevent deadlock between concurrent calls.
	slices.SortStableFunc(items, func(a, b pending) int { return cmp.Compare(a.limiter.id, b.limiter.id) })
	for i, item := range items {
		if i == 0 || item.limiter != items[i-1].limiter {
			item.limiter.mu.Lock()
			defer item.limiter.mu.Unlock()
		}
	}
	var delay time.Duration
	for i := range items {
		item := &items[i]
		item.now = item.limiter.now()
		item.limiter.removeRecovered(item.now)
		delay = max(delay, item.limiter.retry(item.digest, item.now))
	}
	if delay > 0 {
		return delay
	}
	for _, item := range items {
		item.limiter.consume(item.digest, item.now)
	}
	return 0
}

func (l *Limiter) removeRecovered(now time.Time) {
	for len(l.expiry) > 0 && !now.Before(l.expiry[0].fullAt) {
		l.removeEarliest()
	}
}

func (l *Limiter) retry(digest [sha256.Size]byte, now time.Time) time.Duration {
	current := l.entries[digest]
	if current == nil {
		return 0
	}
	return current.fullAt.Add(-time.Duration(l.policy.Burst-1) * l.policy.Interval).Sub(now)
}

func (l *Limiter) consume(digest [sha256.Size]byte, now time.Time) {
	current := l.entries[digest]
	if current == nil {
		if len(l.entries) >= l.capacity {
			// Prefer new clients over a table-wide lockout. Eviction can reset an active identity's limit.
			l.removeEarliest()
		}
		current = &entry{key: digest, fullAt: now}
		l.entries[digest] = current
		heap.Push(&l.expiry, current)
	}
	current.fullAt = current.fullAt.Add(l.policy.Interval)
	heap.Fix(&l.expiry, current.index)
}

func (l *Limiter) removeEarliest() {
	removed := heap.Pop(&l.expiry).(*entry)
	delete(l.entries, removed.key)
}

// IPKey uses the connection address. Forwarded headers cannot change the key.
func IPKey(remoteAddr string) string {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		host = remoteAddr
	}
	addr, err := netip.ParseAddr(host)
	if err != nil {
		return "unknown"
	}
	addr = addr.Unmap().WithZone("")
	if addr.Is6() {
		// Group IPv6 addresses by /64 to limit address rotation within one network.
		return netip.PrefixFrom(addr, 64).Masked().String()
	}
	return addr.String()
}
