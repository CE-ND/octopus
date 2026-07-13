package relay

import (
	"encoding/json"
	"testing"

	transformerModel "github.com/bestruirui/octopus/internal/transformer/model"
)

func TestCodexSessionRoutingKey(t *testing.T) {
	sessionID := "019f4cc0-b38b-7f40-9ded-e65ceeaea9fe"
	req := &transformerModel.InternalLLMRequest{ResponsesPromptCacheKey: &sessionID}
	if got := codexSessionID(req); got != sessionID {
		t.Fatalf("unexpected session id: %q", got)
	}
	if a, b := sessionRoutingKey("gpt-5.6", sessionID), sessionRoutingKey("gpt-5.6", "other"); a == b {
		t.Fatalf("different sessions must use different sticky keys")
	}
	if got := codexSessionIDFromRaw(map[string]json.RawMessage{"prompt_cache_key": json.RawMessage(`"` + sessionID + `"`)}); got != sessionID {
		t.Fatalf("unexpected raw session id: %q", got)
	}
}

func TestSupportsRequestOrGroup(t *testing.T) {
	if !supportsRequestOrGroup("codex-primary", "gpt-5.6", "codex-primary") {
		t.Fatal("target group authorization should be accepted")
	}
	if supportsRequestOrGroup("other", "gpt-5.6", "codex-primary") {
		t.Fatal("unrelated authorization should be rejected")
	}
}
