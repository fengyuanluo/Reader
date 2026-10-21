package fetcher

import (
	"context"
	"strconv"
	"strings"
	"sync"
	"time"
)

// maxRateRecords 限制按源缓存的限速记录数量，防止长期运行下 map 无界增长（S08-10/S09-02）。
const maxRateRecords = 1024

// minIdleEvict 是记录窗口结束后保留的最小时长，超过后可在写入时惰性淘汰。
const minIdleEvict = 10 * time.Minute

type sourceRateLimiter struct {
	mu      sync.Mutex
	records map[string]rateRecord
	order   []string
}

type rateRecord struct {
	limit    int
	interval time.Duration
	started  time.Time
	count    int
}

func newSourceRateLimiter() *sourceRateLimiter {
	return &sourceRateLimiter{records: map[string]rateRecord{}}
}

func (l *sourceRateLimiter) wait(ctx context.Context, key string, spec string) error {
	limit, interval, ok := parseConcurrentRate(spec)
	if !ok || strings.TrimSpace(key) == "" {
		return nil
	}
	for {
		now := time.Now()
		l.mu.Lock()
		// 惰性淘汰：窗口结束超过 max(interval, minIdleEvict) 的记录在写入时删除（S08-10）。
		l.evictLocked(now)
		record, exists := l.records[key]
		if !exists || record.limit != limit || record.interval != interval || !now.Before(record.started.Add(record.interval)) {
			if _, exists := l.records[key]; !exists {
				l.order = append(l.order, key)
			}
			l.records[key] = rateRecord{limit: limit, interval: interval, started: now, count: 1}
			// 超出容量上限时按最旧淘汰（S08-10）。
			for len(l.order) > maxRateRecords {
				oldest := l.order[0]
				l.order = l.order[1:]
				delete(l.records, oldest)
			}
			l.mu.Unlock()
			return nil
		}
		if record.count < record.limit {
			record.count++
			l.records[key] = record
			l.mu.Unlock()
			return nil
		}
		wait := time.Until(record.started.Add(record.interval))
		l.mu.Unlock()
		if wait <= 0 {
			continue
		}
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return ctx.Err()
		case <-timer.C:
		}
	}
}

// evictLocked 删除长期未使用的记录；须在持有 l.mu 时调用。
func (l *sourceRateLimiter) evictLocked(now time.Time) {
	kept := l.order[:0]
	for _, key := range l.order {
		record, ok := l.records[key]
		if !ok {
			continue
		}
		idle := record.interval
		if idle < minIdleEvict {
			idle = minIdleEvict
		}
		if now.After(record.started.Add(record.interval).Add(idle)) {
			delete(l.records, key)
			continue
		}
		kept = append(kept, key)
	}
	l.order = kept
}

func parseConcurrentRate(spec string) (int, time.Duration, bool) {
	spec = strings.TrimSpace(spec)
	if spec == "" || spec == "0" {
		return 0, 0, false
	}
	limit := 1
	intervalText := spec
	if before, after, ok := strings.Cut(spec, "/"); ok {
		parsed, err := strconv.Atoi(strings.TrimSpace(before))
		if err != nil {
			return 0, 0, false
		}
		limit = parsed
		intervalText = after
	}
	intervalMS, err := strconv.Atoi(strings.TrimSpace(intervalText))
	if err != nil || limit <= 0 || intervalMS <= 0 {
		return 0, 0, false
	}
	return limit, time.Duration(intervalMS) * time.Millisecond, true
}
