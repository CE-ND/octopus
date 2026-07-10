package openai

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/bestruirui/octopus/internal/transformer/model"
)

// Upstreams like codex distributors omit arguments from all delta/done events
// and only include them in the final response.completed output.
func TestTransformStreamEventEmitsArgumentsFromCompletedOutput(t *testing.T) {
	o := &ResponseOutbound{}
	ctx := context.Background()

	feed := []string{
		`{"type":"response.created","sequence_number":0,"response":{"object":"response","id":"resp_1","model":"gpt-5.5","status":"in_progress"}}`,
		`{"type":"response.output_item.added","sequence_number":2,"output_index":0,"item":{"id":"m1","type":"message","role":"assistant","content":[],"status":"in_progress"}}`,
		`{"type":"response.output_text.delta","sequence_number":4,"output_index":0,"item_id":"m1","content_index":0,"delta":"好的"}`,
		`{"type":"response.output_item.done","sequence_number":5,"output_index":0,"item":{"id":"m1","type":"message","role":"assistant","content":[{"type":"output_text","text":"好的"}],"status":"completed"}}`,
		`{"type":"response.output_item.added","sequence_number":6,"output_index":1,"item":{"id":"c1","type":"function_call","status":"in_progress","call_id":"call_1","name":"exec_command"}}`,
		`{"type":"response.function_call_arguments.done","sequence_number":7,"output_index":1,"item_id":"c1"}`,
		`{"type":"response.output_item.done","sequence_number":8,"output_index":1,"item":{"id":"c1","type":"function_call","status":"completed","call_id":"call_1","name":"exec_command"}}`,
		`{"type":"response.completed","sequence_number":9,"response":{"object":"response","id":"resp_1","model":"gpt-5.5","status":"completed","output":[{"id":"m1","type":"message","role":"assistant","content":[{"type":"output_text","text":"好的"}]},{"id":"c1","type":"function_call","call_id":"call_1","name":"exec_command","arguments":"{\"cmd\":\"ls\"}"}],"usage":{"input_tokens":10,"output_tokens":5,"total_tokens":15}}}`,
	}

	gotArguments := ""
	for _, data := range feed {
		events, err := o.TransformStreamEvent(ctx, []byte(data))
		if err != nil {
			t.Fatalf("TransformStreamEvent(%s): %v", data[:40], err)
		}
		for _, ev := range events {
			if ev.Kind == model.StreamEventKindToolCallDelta && ev.Delta != nil {
				gotArguments += ev.Delta.Arguments
			}
		}
	}

	want := `{"cmd":"ls"}`
	if gotArguments != want {
		b, _ := json.Marshal(gotArguments)
		t.Fatalf("tool call arguments not forwarded: got %s, want %q", b, want)
	}
}
