package relay

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	dbmodel "github.com/bestruirui/octopus/internal/model"
	transformerModel "github.com/bestruirui/octopus/internal/transformer/model"
)

func TestIsGPTFamilyModel(t *testing.T) {
	cases := []struct {
		name  string
		model string
		want  bool
	}{
		{"gpt prefix", "gpt-5", true},
		{"gpt uppercase", "GPT-6-Astra", true},
		{"codex suffix", "gpt-5-codex", true},
		{"codex standalone", "codex-mini-latest", true},
		{"whitespace trimmed", "  gpt-5 ", true},
		{"claude", "claude-opus-5-5", false},
		{"gemini", "gemini-3-pro", false},
		{"deepseek", "deepseek-v4", false},
		{"empty", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isGPTFamilyModel(tc.model); got != tc.want {
				t.Fatalf("isGPTFamilyModel(%q) = %v, want %v", tc.model, got, tc.want)
			}
		})
	}
}

func TestShouldSanitizeHistory(t *testing.T) {
	enabled := dbmodel.Group{SanitizeHistory: true}
	disabled := dbmodel.Group{}

	if !shouldSanitizeHistory(enabled, "gpt-6-astra") {
		t.Fatal("enabled group + gpt model should sanitize")
	}
	if shouldSanitizeHistory(disabled, "gpt-6-astra") {
		t.Fatal("disabled group should not sanitize")
	}
	if shouldSanitizeHistory(enabled, "claude-opus-5-5") {
		t.Fatal("non-gpt model should not sanitize")
	}
}

func TestSanitizeResponsesHistoryItems(t *testing.T) {
	raw := json.RawMessage(`[
		{"type":"message","role":"user","content":[{"type":"input_text","text":"hi"}]},
		{"type":"reasoning","id":"rs_bare","summary":[]},
		{"type":"reasoning","id":"rs_portable","summary":[],"encrypted_content":"AAA="},
		{"type":"reasoning","id":"rs_empty_enc","encrypted_content":""},
		{"type":"item_reference","id":"item_1"},
		{"type":"web_search_call","id":"ws_1"},
		{"type":"function_call","call_id":"call_1","name":"exec","arguments":"{}"},
		{"type":"function_call_output","call_id":"call_1","output":"ok"}
	]`)

	out, changed := sanitizeResponsesHistoryItems(raw)
	if !changed {
		t.Fatal("expected items to be sanitized")
	}

	var items []map[string]json.RawMessage
	if err := json.Unmarshal(out, &items); err != nil {
		t.Fatalf("sanitized output is not valid JSON array: %v", err)
	}
	if len(items) != 4 {
		t.Fatalf("expected 4 items after sanitize, got %d: %s", len(items), out)
	}

	kept := map[string]bool{}
	for _, item := range items {
		var itemType string
		if err := json.Unmarshal(item["type"], &itemType); err != nil {
			t.Fatalf("item type unmarshal failed: %v", err)
		}
		kept[itemType] = true
	}
	for _, want := range []string{"message", "reasoning", "function_call", "function_call_output"} {
		if !kept[want] {
			t.Fatalf("expected %q to be kept, kept = %v", want, kept)
		}
	}
	if !strings.Contains(string(out), "rs_portable") {
		t.Fatal("portable reasoning (with encrypted_content) must be kept")
	}
	if strings.Contains(string(out), "rs_bare") || strings.Contains(string(out), "rs_empty_enc") {
		t.Fatal("bare reasoning must be dropped")
	}
	if strings.Contains(string(out), "item_reference") || strings.Contains(string(out), "web_search_call") {
		t.Fatal("item_reference / web_search_call must be dropped")
	}
	// function_call 与 function_call_output 配对必须完整保留
	if !strings.Contains(string(out), "call_1") {
		t.Fatal("function call pairing must be preserved")
	}
}

func TestSanitizeResponsesHistoryItemsUnchanged(t *testing.T) {
	raw := json.RawMessage(`[{"type":"message","role":"user","content":"hi"}]`)
	out, changed := sanitizeResponsesHistoryItems(raw)
	if changed {
		t.Fatal("clean input must not be modified")
	}
	if string(out) != string(raw) {
		t.Fatalf("unchanged input must be returned as-is, got %s", out)
	}
}

func TestSanitizeResponsesHistoryItemsInvalidJSON(t *testing.T) {
	raw := json.RawMessage(`not-a-json-array`)
	out, changed := sanitizeResponsesHistoryItems(raw)
	if changed {
		t.Fatal("invalid JSON must not be treated as changed")
	}
	if string(out) != string(raw) {
		t.Fatalf("invalid JSON must be returned as-is, got %s", out)
	}
}

func TestSanitizeResponsesHistoryRequest(t *testing.T) {
	req := &transformerModel.InternalLLMRequest{
		Model:        "gpt-6-astra",
		RawAPIFormat: transformerModel.APIFormatOpenAIResponse,
	}
	prevID := "resp_123"
	req.SetOpenAIResponsesOptions(transformerModel.OpenAIResponsesOptions{
		PreviousResponseID: &prevID,
		Conversation:       json.RawMessage(`"conv_1"`),
	})
	req.SetOpenAIRawInputItems(json.RawMessage(`[
		{"type":"reasoning","id":"rs_bare"},
		{"type":"message","role":"user","content":"hi"}
	]`))

	if !sanitizeResponsesHistoryRequest(req) {
		t.Fatal("expected request to be sanitized")
	}
	if got := req.OpenAIPreviousResponseID(); got != "" {
		t.Fatalf("previous_response_id should be cleared, got %q", got)
	}
	if opts := req.GetOpenAIResponsesOptions(); opts.Conversation != nil {
		t.Fatalf("conversation should be cleared, got %s", opts.Conversation)
	}
	items := req.OpenAIRawInputItems()
	if strings.Contains(string(items), "rs_bare") {
		t.Fatalf("bare reasoning should be dropped, got %s", items)
	}
	if !strings.Contains(string(items), `"hi"`) {
		t.Fatalf("message content must be kept, got %s", items)
	}
	// 清洗器本身不打标，由调用方决定是否强制标准路径
	if req.IsOpenAIExactReplayRequest() {
		t.Fatal("sanitizer must not mark replay_exact by itself")
	}
	// 清洗 + 打标后必须仍通过 Validate（replay_exact 要求无 previous_response_id 且有 raw_input_items）
	req.MarkOpenAIExactReplayRequest()
	if err := req.Validate(); err != nil {
		t.Fatalf("sanitized + marked request must pass Validate: %v", err)
	}
}

func TestSanitizeResponsesHistoryRequestNoop(t *testing.T) {
	req := &transformerModel.InternalLLMRequest{}
	raw := json.RawMessage(`[{"type":"message","role":"user","content":"hi"}]`)
	req.SetOpenAIRawInputItems(raw)

	if sanitizeResponsesHistoryRequest(req) {
		t.Fatal("clean request must not be modified")
	}
	if string(req.OpenAIRawInputItems()) != string(raw) {
		t.Fatal("clean request raw items must stay untouched")
	}
}

func TestIsStateReferenceError(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{
			name: "azure different resource",
			err:  fmt.Errorf(`channel failed: upstream error: 400: {"error":{"message":"The requested item was created under a different Azure OpenAI resource. Use the same resource that created the item to access it."}}`),
			want: true,
		},
		{name: "no item found", err: errors.New("400: No item found with id 'resp_123'."), want: true},
		{name: "item not found", err: errors.New("400: Item not found: rs_1"), want: true},
		{name: "does not have an item with id", err: errors.New("400: The model does not have an item with id `rs_1`."), want: true},
		{name: "unknown item", err: errors.New("400: unknown item type"), want: true},
		{name: "invalid api key", err: errors.New("401: invalid api key"), want: false},
		{name: "context limit", err: errors.New("400: context length exceeded"), want: false},
		{name: "nil error", err: nil, want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isStateReferenceError(tc.err); got != tc.want {
				t.Fatalf("isStateReferenceError(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}
