package relay

import (
	"encoding/json"
	"strings"

	transformerModel "github.com/bestruirui/octopus/internal/transformer/model"
)

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
