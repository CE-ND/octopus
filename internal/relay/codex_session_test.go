package relay

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	transformerModel "github.com/bestruirui/octopus/internal/transformer/model"
	"github.com/gin-gonic/gin"
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

func TestResolveCodexSessionIDPrefersHeader(t *testing.T) {
	id, source := resolveCodexSessionID("01a07a88-aa31-7973-bb4d-4a1878da1d11", "01a07a88-bd02-78b0-bf11-0bd762ea04e7")
	if id != "01a07a88-aa31-7973-bb4d-4a1878da1d11" {
		t.Fatalf("expected header session id, got %q", id)
	}
	if source != codexSessionIDSourceHeader {
		t.Fatalf("expected source %q, got %q", codexSessionIDSourceHeader, source)
	}
}

func TestResolveCodexSessionIDFallsBackToBody(t *testing.T) {
	id, source := resolveCodexSessionID("", "01a07a88-bd02-78b0-bf11-0bd762ea04e7")
	if id != "01a07a88-bd02-78b0-bf11-0bd762ea04e7" {
		t.Fatalf("expected body session id, got %q", id)
	}
	if source != codexSessionIDSourceBody {
		t.Fatalf("expected source %q, got %q", codexSessionIDSourceBody, source)
	}
}

func TestResolveCodexSessionIDTrimsWhitespace(t *testing.T) {
	id, _ := resolveCodexSessionID("  header-id  ", "  body-id  ")
	if id != "header-id" {
		t.Fatalf("expected trimmed header id, got %q", id)
	}
	id, source := resolveCodexSessionID("   ", "  body-id  ")
	if id != "body-id" || source != codexSessionIDSourceBody {
		t.Fatalf("expected trimmed body fallback, got %q / %q", id, source)
	}
}

func TestResolveCodexSessionIDEmpty(t *testing.T) {
	id, source := resolveCodexSessionID("", "")
	if id != "" {
		t.Fatalf("expected empty session id, got %q", id)
	}
	if source != "" {
		t.Fatalf("expected empty source, got %q", source)
	}
}

func claudeSessionTestContext(header string) *gin.Context {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	if header != "" {
		c.Request.Header.Set(claudeSessionIDHeader, header)
	}
	return c
}

func TestClaudeSessionIDFromMetadata(t *testing.T) {
	req := &transformerModel.InternalLLMRequest{}
	req.SetTransformerMetadataValue(transformerModel.TransformerMetadataAnthropicUserID,
		`{"device_id":"0cf7c557","account_uuid":"","session_id":"15118156-381a-415b-8f43-208224f9e53a"}`)
	if got := claudeSessionID(req); got != "15118156-381a-415b-8f43-208224f9e53a" {
		t.Fatalf("unexpected claude session id: %q", got)
	}
	req.SetTransformerMetadataValue(transformerModel.TransformerMetadataAnthropicUserID, `not-json`)
	if got := claudeSessionID(req); got != "" {
		t.Fatalf("expected empty session id for invalid payload, got %q", got)
	}
	if got := claudeSessionID(nil); got != "" {
		t.Fatalf("expected empty session id for nil request, got %q", got)
	}
}

func TestResolveClientSessionIDPrefersClaudeHeader(t *testing.T) {
	c := claudeSessionTestContext("15118156-381a-415b-8f43-208224f9e53a")
	req := &transformerModel.InternalLLMRequest{}
	req.SetTransformerMetadataValue(transformerModel.TransformerMetadataAnthropicUserID,
		`{"session_id":"aa856952-1f03-4396-8122-634de0d6948a"}`)
	id, source := resolveClientSessionID(c, req)
	if id != "15118156-381a-415b-8f43-208224f9e53a" {
		t.Fatalf("expected claude header session id, got %q", id)
	}
	if source != codexSessionIDSourceClaudeHeader {
		t.Fatalf("expected source %q, got %q", codexSessionIDSourceClaudeHeader, source)
	}
}

func TestResolveClientSessionIDFallsBackToMetadata(t *testing.T) {
	c := claudeSessionTestContext("")
	req := &transformerModel.InternalLLMRequest{}
	req.SetTransformerMetadataValue(transformerModel.TransformerMetadataAnthropicUserID,
		`{"session_id":"aa856952-1f03-4396-8122-634de0d6948a"}`)
	id, source := resolveClientSessionID(c, req)
	if id != "aa856952-1f03-4396-8122-634de0d6948a" {
		t.Fatalf("expected metadata session id, got %q", id)
	}
	if source != codexSessionIDSourceClaudeBody {
		t.Fatalf("expected source %q, got %q", codexSessionIDSourceClaudeBody, source)
	}
}

func TestResolveClientSessionIDCodexHeaderWins(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	c.Request.Header.Set(codexSessionIDHeader, "01a07a88-aa31-7973-bb4d-4a1878da1d11")
	c.Request.Header.Set(claudeSessionIDHeader, "15118156-381a-415b-8f43-208224f9e53a")
	id, source := resolveClientSessionID(c, nil)
	if id != "01a07a88-aa31-7973-bb4d-4a1878da1d11" {
		t.Fatalf("expected codex header session id, got %q", id)
	}
	if source != codexSessionIDSourceHeader {
		t.Fatalf("expected source %q, got %q", codexSessionIDSourceHeader, source)
	}
}

func TestResolveClientSessionIDEmpty(t *testing.T) {
	c := claudeSessionTestContext("")
	id, source := resolveClientSessionID(c, nil)
	if id != "" || source != "" {
		t.Fatalf("expected empty session id/source, got %q / %q", id, source)
	}
}
