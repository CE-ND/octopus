package relay

import (
	"strings"

	"github.com/bestruirui/octopus/internal/op"
	"github.com/bestruirui/octopus/internal/transformer/inbound"
	"github.com/gin-gonic/gin"
)

// 调用方 agent 归因：参照 magpie 的「请求特征归因」思路，优先使用各官方 CLI
// 独有的请求头（强信号），再按 op.AgentDefinitions 注册表的 UA 子串匹配；
// 均未命中归为 other（前端以 Bot 图标展示）。

const (
	agentClaudeCode = "claude-code"
	agentCodex      = "codex"
	agentGeminiCLI  = "gemini-cli"
	agentOther      = "other"
)

// detectAgentCaller 从请求头/User-Agent 归因调用方 agent
func detectAgentCaller(c *gin.Context) string {
	ua := strings.ToLower(c.Request.Header.Get("User-Agent"))
	// DSH 的 Responses SDK 也会发送 session_id；明确的应用身份优先于该通用会话头。
	if strings.Contains(ua, "deepseek-harness/") {
		return "deepseek-harness"
	}
	// 强信号：官方 CLI 独有的稳定请求头
	if strings.TrimSpace(c.GetHeader(claudeSessionIDHeader)) != "" || strings.Contains(ua, "claude-cli") {
		return agentClaudeCode
	}
	if strings.EqualFold(strings.TrimSpace(c.GetHeader("Originator")), "codex_cli_rs") ||
		strings.TrimSpace(codexClientSessionHeader(c)) != "" {
		return agentCodex
	}
	// 注册表 UA 子串匹配（注册表顺序即优先级）
	for _, def := range op.AgentDefinitions {
		for _, match := range def.UAMatches {
			if strings.Contains(ua, match) {
				return def.ID
			}
		}
	}
	return agentOther
}

// inboundProtocolName 拓扑视图用的协议名
func inboundProtocolName(inboundType inbound.InboundType) string {
	switch inboundType {
	case inbound.InboundTypeOpenAIChat:
		return "openai-chat"
	case inbound.InboundTypeOpenAIResponse:
		return "openai-responses"
	case inbound.InboundTypeAnthropic:
		return "anthropic"
	case inbound.InboundTypeOpenAIEmbedding:
		return "embedding"
	default:
		return "unknown"
	}
}
