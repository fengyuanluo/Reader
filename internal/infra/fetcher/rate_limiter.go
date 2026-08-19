package fetcher

import (
	"context"
	"strconv"
	"strings"
	"sync"
	"time"
)

type sourceRateLimiter struct {
	mu      sync.Mutex
	records map[string]rateRecord
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
		record, exists := l.records[key]
		if !exists || record.limit != limit || record.interval != interval || !now.Before(record.started.Add(record.interval)) {
			l.records[key] = rateRecord{limit: limit, interval: interval, started: now, count: 1}
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
