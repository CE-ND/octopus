package openai

import (
	"context"
	"strings"
	"testing"

	outboundopenai "github.com/bestruirui/octopus/internal/transformer/outbound/openai"
)

// Full outbound(Responses SSE) -> internal -> inbound(Responses SSE) round trip.
// Regression for tool calls losing their arguments when the gateway falls back
// to HTTP SSE transformation (e.g. upstream WS handshake fails). The outbound
// emits arguments via StreamDelta.Arguments; the inbound must forward them so
// Codex receives an executable function call instead of an empty one.
func TestResponsesRoundTripPreservesToolArguments(t *testing.T) {
	ctx := context.Background()
	out := &outboundopenai.ResponseOutbound{}
	in := &ResponseInbound{}

	upstream := []string{
		`{"type":"response.created","sequence_number":0,"response":{"object":"response","id":"resp_1","model":"gpt-5.5","status":"in_progress"}}`,
		`{"type":"response.output_item.added","output_index":1,"sequence_number":1,"item":{"id":"fc_1","type":"function_call","status":"in_progress","arguments":"","call_id":"call_1","name":"exec_command"}}`,
		`{"type":"response.function_call_arguments.delta","output_index":1,"sequence_number":2,"item_id":"fc_1","delta":"{\"cmd\":"}`,
		`{"type":"response.function_call_arguments.delta","output_index":1,"sequence_number":3,"item_id":"fc_1","delta":"\"ls\"}"}`,
		`{"type":"response.function_call_arguments.done","output_index":1,"sequence_number":4,"item_id":"fc_1","arguments":"{\"cmd\":\"ls\"}"}`,
		`{"type":"response.output_item.done","output_index":1,"sequence_number":5,"item":{"id":"fc_1","type":"function_call","status":"completed","arguments":"{\"cmd\":\"ls\"}","call_id":"call_1","name":"exec_command"}}`,
		`{"type":"response.completed","sequence_number":6,"response":{"object":"response","id":"resp_1","model":"gpt-5.5","status":"completed","output":[{"id":"fc_1","type":"function_call","call_id":"call_1","name":"exec_command","arguments":"{\"cmd\":\"ls\"}"}],"usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}}`,
	}

	var downstream strings.Builder
	for _, data := range upstream {
		events, err := out.TransformStreamEvent(ctx, []byte(data))
		if err != nil {
			t.Fatalf("outbound TransformStreamEvent: %v", err)
		}
		if len(events) == 0 {
			continue
		}
		encoded, err := in.TransformStreamEvents(ctx, events)
		if err != nil {
			t.Fatalf("inbound TransformStreamEvents: %v", err)
		}
		downstream.Write(encoded)
	}

	got := downstream.String()
	if !strings.Contains(got, `response.function_call_arguments.delta`) {
		t.Fatalf("downstream missing function_call_arguments.delta events:\n%s", got)
	}
	// The concatenated argument deltas must reconstruct the full JSON payload.
	if !strings.Contains(got, `{\"cmd\":`) || !strings.Contains(got, `\"ls\"}`) {
		t.Fatalf("downstream did not carry tool arguments:\n%s", got)
	}
}
