package ratelimit

import (
	"container/heap"
	"crypto/sha256"
	"fmt"
	"net"
	"net/netip"
	"sync"
	"time"
)

type Policy struct {
	// Interval gives the time to restore one token.
	Interval time.Duration
	Burst    int
}

const maxEntries = 50_000

type Limiter struct {
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
		entries: make(map[[sha256.Size]byte]*entry), policy: policy,
		capacity: maxEntries, now: now,
	}, nil
}

// Allow consumes one token. A positive result gives the delay before the next attempt.
func (l *Limiter) Allow(key string) time.Duration {
	digest := sha256.Sum256([]byte(key))
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	for len(l.expiry) > 0 && !now.Before(l.expiry[0].fullAt) {
		l.removeEarliest()
	}
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
	if retry := current.fullAt.Add(-time.Duration(l.policy.Burst-1) * l.policy.Interval).Sub(now); retry > 0 {
		return retry
	}
	current.fullAt = current.fullAt.Add(l.policy.Interval)
	heap.Fix(&l.expiry, current.index)
	return 0
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
