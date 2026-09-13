package ratelimit

import (
	"crypto/sha256"
	"time"
)

type entry struct {
	key    [sha256.Size]byte
	fullAt time.Time
	index  int
}

type expiryHeap []*entry

func (h expiryHeap) Len() int           { return len(h) }
func (h expiryHeap) Less(i, j int) bool { return h[i].fullAt.Before(h[j].fullAt) }
func (h expiryHeap) Swap(i, j int) {
	h[i], h[j] = h[j], h[i]
	h[i].index, h[j].index = i, j
}
func (h *expiryHeap) Push(value any) {
	item := value.(*entry)
	item.index = len(*h)
	*h = append(*h, item)
}
func (h *expiryHeap) Pop() any {
	last := len(*h) - 1
	item := (*h)[last]
	(*h)[last] = nil
	*h = (*h)[:last]
	return item
}
