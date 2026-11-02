package relay

import (
	"net/http/httptest"
	"testing"

	"github.com/bestruirui/octopus/internal/transformer/inbound"
	"github.com/gin-gonic/gin"
)

func detectAgentCallerWithHeaders(t *testing.T, headers map[string]string) string {
	t.Helper()
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest("POST", "/v1/responses", nil)
	for key, value := range headers {
		c.Request.Header.Set(key, value)
	}
	return detectAgentCaller(c)
}

func TestDetectAgentCallerClaudeCode(t *testing.T) {
	cases := []map[string]string{
		{"X-Claude-Code-Session-Id": "5f2a-uuid"},
		{"User-Agent": "claude-cli/1.0.83 (external, cli)"},
		{"User-Agent": "Claude-CLI/2.0"},
	}
	for _, headers := range cases {
		if got := detectAgentCallerWithHeaders(t, headers); got != agentClaudeCode {
			t.Fatalf("expected claude-code for %v, got %s", headers, got)
		}
	}
}

func TestDetectAgentCallerCodex(t *testing.T) {
	cases := []map[string]string{
		{"Originator": "codex_cli_rs"},
		{"originator": "codex_cli_rs"},
		{"User-Agent": "codex_cli_rs/0.42.0 (Ubuntu 24.04; x86_64) Windows"},
		{"session_id": "8e14-uuid"},
	}
	for _, headers := range cases {
		if got := detectAgentCallerWithHeaders(t, headers); got != agentCodex {
			t.Fatalf("expected codex for %v, got %s", headers, got)
		}
	}
}

func TestDetectAgentCallerGeminiCLI(t *testing.T) {
	cases := []map[string]string{
		{"User-Agent": "GeminiCLI/v18.0.0 (gzip)"},
		{"User-Agent": "gemini-cli/1.0"},
	}
	for _, headers := range cases {
		if got := detectAgentCallerWithHeaders(t, headers); got != agentGeminiCLI {
			t.Fatalf("expected gemini-cli for %v, got %s", headers, got)
		}
	}
}

func TestDetectAgentCallerRegistryAgents(t *testing.T) {
	cases := []struct {
		headers  map[string]string
		expected string
	}{
		{map[string]string{"User-Agent": "zcode-cli/1.2.0"}, "zcode"},
		{map[string]string{"User-Agent": "WorkBuddy/2.0 (desktop)"}, "workbuddy"},
		{map[string]string{"User-Agent": "codebuddy-cli/3.1"}, "codebuddy"},
		{map[string]string{"User-Agent": "github-copilot-cli/1.0"}, "copilot"},
		{map[string]string{"User-Agent": "qoder-cli/0.9"}, "qoder"},
	}
	for _, tc := range cases {
		if got := detectAgentCallerWithHeaders(t, tc.headers); got != tc.expected {
			t.Fatalf("expected %s for %v, got %s", tc.expected, tc.headers, got)
		}
	}
}

func TestDetectAgentCallerFallbacks(t *testing.T) {
	cases := []struct {
		headers  map[string]string
		expected string
	}{
		{map[string]string{"User-Agent": "curl/8.0"}, agentOther},
		{map[string]string{}, agentOther},
		{map[string]string{"User-Agent": "python-requests/2.31"}, agentOther},
		{map[string]string{"User-Agent": "deepseek-python/1.0"}, agentOther},
		{map[string]string{"User-Agent": "deepseek-api-client/2.0"}, agentOther},
	}
	for _, tc := range cases {
		if got := detectAgentCallerWithHeaders(t, tc.headers); got != tc.expected {
			t.Fatalf("expected %s for %v, got %s", tc.expected, tc.headers, got)
		}
	}
}

func TestDetectAgentCallerDeepSeekHarness(t *testing.T) {
	cases := []string{
		"deepseek-harness/0.2.1-rc.5 (+https://github.com/deepseek-ai/deepseek-harness)",
		"DeepSeek-Harness/0.2.1",
	}
	for _, ua := range cases {
		if got := detectAgentCallerWithHeaders(t, map[string]string{"User-Agent": ua}); got != "deepseek-harness" {
			t.Fatalf("expected deepseek-harness for %q, got %s", ua, got)
		}
	}
}

func TestDetectDshResponsesWithSharedSessionHeader(t *testing.T) {
	headers := map[string]string{
		"User-Agent": "deepseek-harness/0.2.1-rc.5 (+https://github.com/deepseek-ai/deepseek-harness)",
		"session_id": "dsh-session-123",
	}
	if got := detectAgentCallerWithHeaders(t, headers); got != "deepseek-harness" {
		t.Fatalf("DSH was attributed to %s", got)
	}
}

func TestInboundProtocolName(t *testing.T) {
	cases := []struct {
		inboundType inbound.InboundType
		expected    string
	}{
		{inbound.InboundTypeOpenAIChat, "openai-chat"},
		{inbound.InboundTypeOpenAIResponse, "openai-responses"},
		{inbound.InboundTypeAnthropic, "anthropic"},
		{inbound.InboundTypeOpenAIEmbedding, "embedding"},
		{inbound.InboundType(99), "unknown"},
	}
	for _, tc := range cases {
		if got := inboundProtocolName(tc.inboundType); got != tc.expected {
			t.Fatalf("inboundProtocolName(%d) = %q, want %q", tc.inboundType, got, tc.expected)
		}
	}
}
