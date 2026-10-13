package auth

import (
	"sync"
	"time"
)

// Limiter 是进程内滑动窗口计数器，用于登录尝试限流。
//
// 触发限流时对外仍返回与其他登录失败完全相同的响应（不返回 429），
// 真实原因只写服务端日志——避免成为账号枚举的判别依据。
//
// 局限：仅对单副本有效；多副本需共享存储（属生产 HA，不在本 change 范围）。
type Limiter struct {
	mu     sync.Mutex
	hits   map[string][]time.Time
	max    int
	window time.Duration
}

func NewLimiter(max int, window time.Duration) *Limiter {
	return &Limiter{hits: make(map[string][]time.Time), max: max, window: window}
}

// Allow 记录一次尝试并判断是否在阈值内。
func (l *Limiter) Allow(key string) bool {
	if l == nil || l.max <= 0 {
		return true
	}
	now := time.Now()
	cutoff := now.Add(-l.window)

	l.mu.Lock()
	defer l.mu.Unlock()

	kept := l.hits[key][:0]
	for _, t := range l.hits[key] {
		if t.After(cutoff) {
			kept = append(kept, t)
		}
	}
	kept = append(kept, now)
	l.hits[key] = kept
	return len(kept) <= l.max
}
