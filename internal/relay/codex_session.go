package relay

import (
	"encoding/json"
	"strings"

	transformerModel "github.com/bestruirui/octopus/internal/transformer/model"
	"github.com/gin-gonic/gin"
)

// codexSessionIDHeader 是 codex-rs 客户端（CLI / 桌面端）随每个请求发送的
// 稳定会话 ID 请求头，值为整个会话生命周期不变的线程 UUID。请求体里的
// prompt_cache_key 在新版 CLI 中是每轮对话生成的临时 ID，跨轮会变，
// 不能用于会话绑定路由，只作为旧客户端的兜底。
const codexSessionIDHeader = "session_id"

func codexSessionID(req *transformerModel.InternalLLMRequest) string {
	if req == nil || req.ResponsesPromptCacheKey == nil {
		return ""
	}
	return strings.TrimSpace(*req.ResponsesPromptCacheKey)
}

func codexSessionIDFromRaw(body map[string]json.RawMessage) string {
	raw, ok := body["prompt_cache_key"]
	if !ok || len(raw) == 0 {
		return ""
	}
	var sessionID string
	if err := json.Unmarshal(raw, &sessionID); err != nil {
		return ""
	}
	return strings.TrimSpace(sessionID)
}

// codexClientSessionHeader 从请求里取稳定会话 ID 头。
// gin 的 GetHeader 做 MIME 规范化，下划线和连字符是两种规范化结果，各查一次。
func codexClientSessionHeader(c *gin.Context) string {
	if v := strings.TrimSpace(c.GetHeader(codexSessionIDHeader)); v != "" {
		return v
	}
	return strings.TrimSpace(c.GetHeader("Session-Id"))
}

type codexSessionIDSource string

const (
	codexSessionIDSourceHeader       codexSessionIDSource = "header"
	codexSessionIDSourceBody         codexSessionIDSource = "prompt_cache_key"
	codexSessionIDSourceClaudeHeader codexSessionIDSource = "claude_header"
	codexSessionIDSourceClaudeBody   codexSessionIDSource = "anthropic_user_id"
)

// claudeSessionIDHeader 是 Claude Code 随每个 /v1/messages 请求发送的稳定
// 会话 ID 请求头，值为会话生命周期不变的 UUID，与本地
// ~/.claude/projects/<工作区>/<UUID>.jsonl 记录文件同名。
const claudeSessionIDHeader = "X-Claude-Code-Session-Id"

func claudeClientSessionHeader(c *gin.Context) string {
	return strings.TrimSpace(c.GetHeader(claudeSessionIDHeader))
}

// claudeSessionID 从 Anthropic 请求 metadata.user_id 里解析会话 UUID 作兜底。
// Claude Code 发送的 user_id 是 JSON 字符串：
// {"device_id":"...","account_uuid":"...","session_id":"<uuid>"}。
func claudeSessionID(req *transformerModel.InternalLLMRequest) string {
	if req == nil {
		return ""
	}
	userID := strings.TrimSpace(req.TransformerMetadataValue(transformerModel.TransformerMetadataAnthropicUserID))
	if userID == "" {
		return ""
	}
	var payload struct {
		SessionID string `json:"session_id"`
	}
	if err := json.Unmarshal([]byte(userID), &payload); err != nil {
		return ""
	}
	return strings.TrimSpace(payload.SessionID)
}

// resolveCodexSessionID 会话绑定优先用请求头里的稳定 ID，缺失才退回
// 请求体 prompt_cache_key。返回值为空串时表示请求未携带任何会话标识。
func resolveCodexSessionID(headerID, bodyID string) (string, codexSessionIDSource) {
	if v := strings.TrimSpace(headerID); v != "" {
		return v, codexSessionIDSourceHeader
	}
	if v := strings.TrimSpace(bodyID); v != "" {
		return v, codexSessionIDSourceBody
	}
	return "", ""
}

// resolveClientSessionID 汇总 Codex / Claude Code 两类客户端的稳定会话标识。
// 两家的请求头都是整个会话周期不变的 UUID，优先取头；请求体标识
// （codex 的 prompt_cache_key / claude 的 metadata.user_id）只作兜底。
func resolveClientSessionID(c *gin.Context, req *transformerModel.InternalLLMRequest) (string, codexSessionIDSource) {
	if v := codexClientSessionHeader(c); v != "" {
		return v, codexSessionIDSourceHeader
	}
	if v := claudeClientSessionHeader(c); v != "" {
		return v, codexSessionIDSourceClaudeHeader
	}
	if v := codexSessionID(req); v != "" {
		return v, codexSessionIDSourceBody
	}
	if v := claudeSessionID(req); v != "" {
		return v, codexSessionIDSourceClaudeBody
	}
	return "", ""
}

func sessionRoutingKey(requestModel, sessionID string) string {
	if sessionID == "" {
		return requestModel
	}
	return requestModel + "\x00codex:" + sessionID
}

func supportsRequestOrGroup(supportedModels, requestModel, groupName string) bool {
	if strings.TrimSpace(supportedModels) == "" {
		return true
	}
	for _, allowed := range strings.Split(supportedModels, ",") {
		allowed = strings.TrimSpace(allowed)
		if allowed == requestModel || allowed == groupName {
			return true
		}
	}
	return false
}
