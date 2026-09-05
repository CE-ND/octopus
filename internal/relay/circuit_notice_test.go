package relay

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bestruirui/octopus/internal/model"
)

func TestCircuitNoticeConcurrentRetriesEmitOnce(t *testing.T) {
	registry := circuitNoticeRegistry{entries: make(map[circuitNoticeScope]time.Time)}
	scope := circuitNoticeScope{APIKeyID: 1, GroupID: 2, RoutingKey: "gpt\x00codex:session"}
	pure := []model.ChannelAttempt{{Status: model.AttemptCircuitBreak}}
	now := time.Now()

	var emitted atomic.Int32
	var wg sync.WaitGroup
	for range 32 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if registry.shouldEmit(scope, "1", pure, now) {
				emitted.Add(1)
			}
		}()
	}
	wg.Wait()

	if got := emitted.Load(); got != 1 {
		t.Fatalf("concurrent retry burst emitted %d notices, want 1", got)
	}
}

func TestCircuitNoticeSuppressesOnlyAutomaticPureCircuitRetries(t *testing.T) {
	registry := circuitNoticeRegistry{entries: make(map[circuitNoticeScope]time.Time)}
	scope := circuitNoticeScope{APIKeyID: 1, GroupID: 2, RoutingKey: "gpt\x00codex:session"}
	now := time.Now()
	pure := []model.ChannelAttempt{{Status: model.AttemptCircuitBreak}}

	if !registry.shouldEmit(scope, "0", pure, now) {
		t.Fatal("initial request must emit")
	}
	if registry.shouldEmit(scope, "1", pure, now.Add(time.Second)) {
		t.Fatal("automatic pure-circuit retry must be suppressed")
	}
	if !registry.shouldEmit(scope, "0", pure, now.Add(2*time.Second)) {
		t.Fatal("new user request must emit again")
	}
}

func TestCircuitNoticeNeverSuppressesRealAttempts(t *testing.T) {
	registry := circuitNoticeRegistry{entries: make(map[circuitNoticeScope]time.Time)}
	scope := circuitNoticeScope{APIKeyID: 1, GroupID: 2, RoutingKey: "gpt"}
	now := time.Now()
	pure := []model.ChannelAttempt{{Status: model.AttemptCircuitBreak}}
	mixed := []model.ChannelAttempt{
		{Status: model.AttemptCircuitBreak},
		{Status: model.AttemptFailed},
	}

	if !registry.shouldEmit(scope, "0", pure, now) {
		t.Fatal("initial request must emit")
	}
	if !registry.shouldEmit(scope, "1", mixed, now.Add(time.Second)) {
		t.Fatal("a request with a real upstream attempt must emit")
	}
	if !registry.shouldEmit(scope, "2", pure, now.Add(2*time.Second)) {
		t.Fatal("pure-circuit state after a real attempt must emit once")
	}
}

func TestCircuitNoticeSuppressesRequestsWithoutRetryHeaderUntilReset(t *testing.T) {
	registry := circuitNoticeRegistry{entries: make(map[circuitNoticeScope]time.Time)}
	scope := circuitNoticeScope{APIKeyID: 1, GroupID: 2, RoutingKey: "gpt"}
	pure := []model.ChannelAttempt{{Status: model.AttemptCircuitBreak}}

	if !registry.shouldEmit(scope, "", pure, time.Now()) {
		t.Fatal("initial request must emit")
	}
	if registry.shouldEmit(scope, "", pure, time.Now().Add(time.Second)) {
		t.Fatal("repeated request without a retry marker must be suppressed")
	}
	registry.reset(scope)
	if !registry.shouldEmit(scope, "", pure, time.Now().Add(2*time.Second)) {
		t.Fatal("a request after cancellation/reset must emit")
	}
}

func TestPureCircuitBreakClassification(t *testing.T) {
	tests := []struct {
		name     string
		attempts []model.ChannelAttempt
		want     bool
	}{
		{name: "empty", want: false},
		{name: "all circuit", attempts: []model.ChannelAttempt{{Status: model.AttemptCircuitBreak}, {Status: model.AttemptCircuitBreak}}, want: true},
		{name: "real failure", attempts: []model.ChannelAttempt{{Status: model.AttemptCircuitBreak}, {Status: model.AttemptFailed}}, want: false},
		{name: "other skip", attempts: []model.ChannelAttempt{{Status: model.AttemptCircuitBreak}, {Status: model.AttemptSkipped}}, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isPureCircuitBreak(tt.attempts); got != tt.want {
				t.Fatalf("isPureCircuitBreak() = %v, want %v", got, tt.want)
			}
		})
	}
}
