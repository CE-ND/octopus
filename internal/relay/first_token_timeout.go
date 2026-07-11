package relay

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/bestruirui/octopus/internal/utils/log"
)

// defaultFirstTokenTimeoutSec bounds the wait for the first upstream token
// (including the wait for response headers) when the group does not configure
// 首字超时 (0). Without it a half-dead upstream connection blocks the relay
// indefinitely with no failover and no relay log. Generous so reasoning
// upstreams that queue before sending headers still fit. Var so tests can
// shrink it.
var defaultFirstTokenTimeoutSec = 180

// effectiveFirstTokenTimeoutSec returns the group-configured 首字超时 when set,
// otherwise the bottom-line default.
func (ra *relayAttempt) effectiveFirstTokenTimeoutSec() int {
	if ra != nil && ra.firstTokenTimeOutSec > 0 {
		return ra.firstTokenTimeOutSec
	}
	return defaultFirstTokenTimeoutSec
}

type firstTokenBudget struct {
	ctx     context.Context
	timer   *time.Timer
	cancel  context.CancelCauseFunc
	mu      sync.Mutex
	stopped bool
	once    sync.Once
}

func (b *firstTokenBudget) stopTimer() {
	if b == nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.stopped {
		return
	}
	b.stopped = true
	if b.timer == nil {
		return
	}
	b.timer.Stop()
}

func (b *firstTokenBudget) close() {
	if b == nil {
		return
	}
	b.once.Do(func() {
		b.stopTimer()
		if b.cancel != nil {
			b.cancel(context.Canceled)
		}
	})
}

func (ra *relayAttempt) attachFirstTokenBudget(req *http.Request) *http.Request {
	if req == nil || !ra.shouldUseFirstTokenBudget() {
		return req
	}

	ctx, cancel := context.WithCancelCause(req.Context())
	budget := &firstTokenBudget{ctx: ctx, cancel: cancel}
	budget.timer = time.AfterFunc(time.Duration(ra.effectiveFirstTokenTimeoutSec())*time.Second, func() {
		budget.mu.Lock()
		defer budget.mu.Unlock()
		if budget.stopped {
			return
		}
		cancel(errFirstTokenTimeout)
	})
	ra.firstTokenBudget = budget
	return req.WithContext(ctx)
}

func (ra *relayAttempt) shouldUseFirstTokenBudget() bool {
	return ra != nil &&
		ra.effectiveFirstTokenTimeoutSec() > 0 &&
		ra.internalRequest != nil &&
		ra.internalRequest.Stream != nil &&
		*ra.internalRequest.Stream
}

func (ra *relayAttempt) stopFirstTokenTimer() {
	if ra == nil || ra.firstTokenBudget == nil {
		return
	}
	ra.firstTokenBudget.stopTimer()
}

func (ra *relayAttempt) closeFirstTokenBudget() {
	if ra == nil || ra.firstTokenBudget == nil {
		return
	}
	ra.firstTokenBudget.close()
}

func (ra *relayAttempt) firstTokenTimeoutError() error {
	if ra == nil {
		return errFirstTokenTimeout
	}
	return fmt.Errorf("%w (%ds)", errFirstTokenTimeout, ra.effectiveFirstTokenTimeoutSec())
}

func (ra *relayAttempt) firstTokenTimeoutIfNeeded(ctx context.Context, err error) error {
	budgetCtx := context.Context(nil)
	if ra != nil && ra.firstTokenBudget != nil {
		budgetCtx = ra.firstTokenBudget.ctx
	}
	if isFirstTokenTimeout(ctx, err) || isFirstTokenTimeout(ctx, contextError(ctx)) ||
		isFirstTokenTimeout(budgetCtx, err) || isFirstTokenTimeout(budgetCtx, contextError(budgetCtx)) {
		if ra != nil {
			log.Warnf("first token timeout (%ds), switching channel", ra.effectiveFirstTokenTimeoutSec())
		}
		return ra.firstTokenTimeoutError()
	}
	return nil
}

type closeWithFuncReadCloser struct {
	io.ReadCloser
	onClose func()
}

func (c *closeWithFuncReadCloser) Close() error {
	err := c.ReadCloser.Close()
	if c.onClose != nil {
		c.onClose()
	}
	return err
}
