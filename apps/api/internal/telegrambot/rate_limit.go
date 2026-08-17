package telegrambot

import (
	"sync"
	"time"
)

const (
	globalActionRate  = 20.0
	globalActionBurst = 20.0
	chatActionRate    = 1.0
	chatActionBurst   = 3.0
	refreshCooldown   = 5 * time.Second
	maxLimitedChats   = 10_000
)

type tokenBucket struct {
	tokens    float64
	updatedAt time.Time
}

type chatLimit struct {
	bucket      tokenBucket
	lastRefresh time.Time
}

type actionLimiter struct {
	mu     sync.Mutex
	now    func() time.Time
	global tokenBucket
	chats  map[int64]chatLimit
	order  []int64
	next   int
}

func newActionLimiter(now func() time.Time) *actionLimiter {
	current := now()
	return &actionLimiter{
		now:    now,
		global: tokenBucket{tokens: globalActionBurst, updatedAt: current},
		chats:  make(map[int64]chatLimit),
		order:  make([]int64, 0, maxLimitedChats),
	}
}

func (l *actionLimiter) allowAction(chatID int64) bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := l.now()
	refillBucket(&l.global, now, globalActionRate, globalActionBurst)
	chat := l.chat(chatID, now)
	refillBucket(&chat.bucket, now, chatActionRate, chatActionBurst)
	if l.global.tokens < 1 || chat.bucket.tokens < 1 {
		l.chats[chatID] = chat
		return false
	}
	l.global.tokens--
	chat.bucket.tokens--
	l.chats[chatID] = chat
	return true
}

func (l *actionLimiter) allowRefresh(chatID int64) bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := l.now()
	chat := l.chat(chatID, now)
	if !chat.lastRefresh.IsZero() && now.Sub(chat.lastRefresh) < refreshCooldown {
		return false
	}
	chat.lastRefresh = now
	l.chats[chatID] = chat
	return true
}

func (l *actionLimiter) chat(chatID int64, now time.Time) chatLimit {
	if chat, ok := l.chats[chatID]; ok {
		return chat
	}
	if len(l.order) < maxLimitedChats {
		l.order = append(l.order, chatID)
	} else {
		delete(l.chats, l.order[l.next])
		l.order[l.next] = chatID
		l.next = (l.next + 1) % maxLimitedChats
	}
	return chatLimit{bucket: tokenBucket{tokens: chatActionBurst, updatedAt: now}}
}

func refillBucket(bucket *tokenBucket, now time.Time, rate, capacity float64) {
	elapsed := now.Sub(bucket.updatedAt).Seconds()
	if elapsed <= 0 {
		return
	}
	bucket.tokens += elapsed * rate
	if bucket.tokens > capacity {
		bucket.tokens = capacity
	}
	bucket.updatedAt = now
}
