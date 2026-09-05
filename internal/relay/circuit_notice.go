package relay

import (
	"errors"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/bestruirui/octopus/internal/model"
)

const circuitNoticeStateTTL = 10 * time.Minute

var errAllChannelsCircuitBreak = errors.New("all candidate channels are circuit-breaker tripped")

type circuitNoticeScope struct {
	APIKeyID   int
	GroupID    int
	RoutingKey string
}

type circuitNoticeRegistry struct {
	mu      sync.Mutex
	entries map[circuitNoticeScope]time.Time
}

var circuitNotices = circuitNoticeRegistry{
	entries: make(map[circuitNoticeScope]time.Time),
}

func (r *circuitNoticeRegistry) shouldEmit(scope circuitNoticeScope, retryHeader string, attempts []model.ChannelAttempt, now time.Time) bool {
	if !isPureCircuitBreak(attempts) {
		r.reset(scope)
		return true
	}

	retryCount, known := parseStainlessRetryCount(retryHeader)

	r.mu.Lock()
	defer r.mu.Unlock()

	last, seen := r.entries[scope]
	if seen && now.Sub(last) < circuitNoticeStateTTL && (!known || retryCount > 0) {
		r.entries[scope] = now
		return false
	}

	// A known retry count of zero is a new SDK request. Without the header,
	// cancellation/connection scope controls when a new request starts a burst.
	r.entries[scope] = now
	r.cleanupLocked(now)
	return true
}

func (r *circuitNoticeRegistry) reset(scope circuitNoticeScope) {
	r.mu.Lock()
	delete(r.entries, scope)
	r.mu.Unlock()
}

func (r *circuitNoticeRegistry) cleanupLocked(now time.Time) {
	if len(r.entries) < 1024 {
		return
	}
	for scope, last := range r.entries {
		if now.Sub(last) >= circuitNoticeStateTTL {
			delete(r.entries, scope)
		}
	}
}

func parseStainlessRetryCount(value string) (int, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, false
	}
	retryCount, err := strconv.Atoi(value)
	if err != nil || retryCount < 0 {
		return 0, false
	}
	return retryCount, true
}

func isPureCircuitBreak(attempts []model.ChannelAttempt) bool {
	if len(attempts) == 0 {
		return false
	}
	for _, attempt := range attempts {
		if attempt.Status != model.AttemptCircuitBreak {
			return false
		}
	}
	return true
}
