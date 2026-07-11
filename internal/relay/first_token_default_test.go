package relay

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bestruirui/octopus/internal/model"
	"github.com/bestruirui/octopus/internal/op"
	"github.com/bestruirui/octopus/internal/transformer/inbound"
	transformerModel "github.com/bestruirui/octopus/internal/transformer/model"
	"github.com/bestruirui/octopus/internal/transformer/outbound"
	"github.com/coder/websocket"
	"github.com/gin-gonic/gin"
)

func shrinkDefaultFirstTokenTimeout(t *testing.T, seconds int) {
	t.Helper()
	original := defaultFirstTokenTimeoutSec
	defaultFirstTokenTimeoutSec = seconds
	t.Cleanup(func() { defaultFirstTokenTimeoutSec = original })
}

// A group without 首字超时 must still be protected by the bottom-line default:
// an upstream that never returns response headers has to time out and fail
// over instead of hanging the relay (and the client) indefinitely.
func TestHandlerDefaultFirstTokenBudgetCoversUnsetGroupTimeout(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx := setupRelayTestDB(t)
	shrinkDefaultFirstTokenTimeout(t, 1)

	var firstHits atomic.Int32
	stall := make(chan struct{})
	firstServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		firstHits.Add(1)
		// Never write headers until the request is aborted or the test ends.
		select {
		case <-r.Context().Done():
		case <-stall:
		}
	}))
	defer firstServer.Close()
	defer close(stall)

	var secondHits atomic.Int32
	secondServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		secondHits.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(`data: {"id":"fast","object":"chat.completion.chunk","created":1,"model":"fast-model","choices":[{"index":0,"delta":{"role":"assistant","content":"ok"}}]}

data: [DONE]

`))
	}))
	defer secondServer.Close()

	firstChannel := &model.Channel{
		Name:     "relay-default-budget-stalled-header",
		Type:     outbound.OutboundTypeOpenAIChat,
		Enabled:  true,
		BaseUrls: []model.BaseUrl{{URL: firstServer.URL + "/v1"}},
		Model:    "default-budget-model",
		Keys:     []model.ChannelKey{{Enabled: true, ChannelKey: "stalled-key"}},
	}
	if err := op.ChannelCreate(firstChannel, ctx); err != nil {
		t.Fatalf("ChannelCreate first channel failed: %v", err)
	}
	secondChannel := &model.Channel{
		Name:     "relay-default-budget-fallback",
		Type:     outbound.OutboundTypeOpenAIChat,
		Enabled:  true,
		BaseUrls: []model.BaseUrl{{URL: secondServer.URL + "/v1"}},
		Model:    "default-budget-model",
		Keys:     []model.ChannelKey{{Enabled: true, ChannelKey: "fallback-key"}},
	}
	if err := op.ChannelCreate(secondChannel, ctx); err != nil {
		t.Fatalf("ChannelCreate second channel failed: %v", err)
	}

	// FirstTokenTimeOut deliberately unset (0).
	group := &model.Group{Name: "relay-default-budget-group", Mode: model.GroupModeFailover}
	if err := op.GroupCreate(group, ctx); err != nil {
		t.Fatalf("GroupCreate failed: %v", err)
	}
	if err := op.GroupItemAdd(&model.GroupItem{GroupID: group.ID, ChannelID: firstChannel.ID, ModelName: "default-budget-model", Priority: 1, Weight: 1}, ctx); err != nil {
		t.Fatalf("GroupItemAdd first item failed: %v", err)
	}
	if err := op.GroupItemAdd(&model.GroupItem{GroupID: group.ID, ChannelID: secondChannel.ID, ModelName: "default-budget-model", Priority: 2, Weight: 1}, ctx); err != nil {
		t.Fatalf("GroupItemAdd second item failed: %v", err)
	}

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"relay-default-budget-group","messages":[{"role":"user","content":"hello"}],"stream":true}`))
	c.Request.Header.Set("Content-Type", "application/json")

	done := make(chan struct{})
	go func() {
		defer close(done)
		Handler(inbound.InboundTypeOpenAIChat, c)
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatalf("relay did not finish within 10s; default first token budget did not fire")
	}

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected relay handler to succeed via fallback channel, got status %d body %s", recorder.Code, recorder.Body.String())
	}
	if firstHits.Load() != 1 {
		t.Fatalf("expected stalled channel to be attempted once, got %d", firstHits.Load())
	}
	if secondHits.Load() != 1 {
		t.Fatalf("expected fallback channel to be attempted once, got %d", secondHits.Load())
	}
	if !strings.Contains(recorder.Body.String(), `"content":"ok"`) {
		t.Fatalf("expected fallback stream response to be returned, got %s", recorder.Body.String())
	}

	logs, err := op.RelayLogList(ctx, nil, nil, nil, 1, 10)
	if err != nil {
		t.Fatalf("RelayLogList failed: %v", err)
	}
	if len(logs) == 0 || len(logs[0].Attempts) != 2 {
		t.Fatalf("expected exactly two attempts in relay log, got %#v", logs)
	}
	if logs[0].Attempts[0].Status != model.AttemptFailed || !strings.Contains(logs[0].Attempts[0].Msg, "first token timeout") {
		t.Fatalf("expected first attempt to fail with first token timeout, got %#v", logs[0].Attempts[0])
	}
}

// An upstream WS connection that accepts the request but never sends a single
// event must trip the first-event watchdog instead of blocking Read until the
// downstream connection cap.
func TestWSPassthroughFirstEventTimeout(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx := setupRelayTestDB(t)
	shrinkDefaultFirstTokenTimeout(t, 1)
	resetWSUpstreamPool()
	defer resetWSUpstreamPool()

	silent := make(chan struct{})
	defer close(silent)
	wsServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer conn.CloseNow()
		// Swallow the request and never answer.
		_, _, _ = conn.Read(r.Context())
		<-silent
	}))
	defer wsServer.Close()

	channel := &model.Channel{
		Name:     "relay-ws-passthrough-silent",
		Type:     outbound.OutboundTypeOpenAIResponse,
		Enabled:  true,
		BaseUrls: []model.BaseUrl{{URL: wsServer.URL + "/v1"}},
		Model:    "gpt-4o",
		Keys:     []model.ChannelKey{{Enabled: true, ChannelKey: "silent-key"}},
	}
	if err := op.ChannelCreate(channel, ctx); err != nil {
		t.Fatalf("ChannelCreate failed: %v", err)
	}

	pc := TryUpstreamWS(context.Background(), channel, channel.GetBaseUrl(), channel.Keys[0].ChannelKey, channel.Keys[0].ID, nil, true)
	if pc == nil {
		t.Fatalf("expected upstream ws dial to succeed")
	}
	defer wsUpstreamPool.RemoveConn(pc)

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	internalReq := &transformerModel.InternalLLMRequest{Model: "gpt-4o", Stream: boolPtr(true)}
	req := &relayRequest{
		c:               c,
		inAdapter:       inbound.Get(inbound.InboundTypeOpenAIResponse),
		internalRequest: internalReq,
		metrics:         NewRelayMetrics(1, "gpt-4o", nil, internalReq),
		apiKeyID:        1,
		requestModel:    "gpt-4o",
	}
	ra := &relayAttempt{
		relayRequest: req,
		outAdapter:   outbound.Get(channel.Type),
		channel:      channel,
		usedKey:      channel.Keys[0],
	}

	start := time.Now()
	_, err := ra.handleWSPassthroughStream(context.Background(), pc)
	elapsed := time.Since(start)
	if err == nil {
		t.Fatalf("expected first-event timeout error, got nil")
	}
	if !errors.Is(err, errFirstTokenTimeout) {
		t.Fatalf("expected first token timeout error, got %v", err)
	}
	if elapsed > 5*time.Second {
		t.Fatalf("expected timeout to fire around the 1s budget, took %v", elapsed)
	}
}
