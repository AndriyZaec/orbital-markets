package telegrambot

import "sync"

type updateDeduper struct {
	mu       sync.Mutex
	seen     map[int64]struct{}
	order    []int64
	next     int
	capacity int
}

func newUpdateDeduper(capacity int) *updateDeduper {
	return &updateDeduper{
		seen:     make(map[int64]struct{}, capacity),
		order:    make([]int64, 0, capacity),
		capacity: capacity,
	}
}

func (d *updateDeduper) add(updateID int64) bool {
	if updateID == 0 {
		return true
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if _, exists := d.seen[updateID]; exists {
		return false
	}
	if len(d.order) < d.capacity {
		d.order = append(d.order, updateID)
	} else {
		delete(d.seen, d.order[d.next])
		d.order[d.next] = updateID
		d.next = (d.next + 1) % d.capacity
	}
	d.seen[updateID] = struct{}{}
	return true
}
