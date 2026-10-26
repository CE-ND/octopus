package relay

import (
	"encoding/json"
	"strings"

	dbmodel "github.com/bestruirui/octopus/internal/model"
	"github.com/bestruirui/octopus/internal/transformer/model"
	"github.com/bestruirui/octopus/internal/utils/log"
)

// isGPTFamilyModel 判断模型是否为 GPT 系（gpt-*/codex-* 前缀，大小写不敏感）。
// 历史清洗仅对 GPT 系模型生效：服务端条目引用的资源绑定问题是 OpenAI Responses 生态特有行为，
// Claude 等其他模型不受影响。
func isGPTFamilyModel(modelName string) bool {
	name := strings.ToLower(strings.TrimSpace(modelName))
	return strings.HasPrefix(name, "gpt") || strings.HasPrefix(name, "codex")
}

// shouldSanitizeHistory 分组清洗开关开启且模型为 GPT 系时启用历史清洗。
func shouldSanitizeHistory(group dbmodel.Group, requestModel string) bool {
	return group.SanitizeHistory && isGPTFamilyModel(requestModel)
}

// sanitizeResponsesHistoryRequest 将 OpenAI Responses 请求转为无状态形式：
// 清除 previous_response_id/conversation，并丢弃 input 数组中无法跨上游资源迁移的服务端引用条目。
// 返回是否有实际变更；无变更时不修改请求，保持 passthrough 等原有路径不变。
func sanitizeResponsesHistoryRequest(req *model.InternalLLMRequest) bool {
	if req == nil {
		return false
	}
	changed := false

	responsesOptions := req.GetOpenAIResponsesOptions()
	if responsesOptions.PreviousResponseID != nil {
		responsesOptions.PreviousResponseID = nil
		changed = true
	}
	if responsesOptions.Conversation != nil {
		responsesOptions.Conversation = nil
		changed = true
	}
	if changed {
		req.SetOpenAIResponsesOptions(responsesOptions)
	}

	rawItems := req.OpenAIRawInputItems()
	if len(rawItems) == 0 {
		return changed
	}
	sanitizedItems, itemsChanged := sanitizeResponsesHistoryItems(rawItems)
	if !itemsChanged {
		return changed
	}
	req.SetOpenAIRawInputItems(sanitizedItems)
	return true
}

// sanitizeResponsesHistoryItems 清洗 input 条目数组，返回清洗后的数组与是否发生变更。
// 解析失败时原样返回（容错优先）。
func sanitizeResponsesHistoryItems(raw json.RawMessage) (json.RawMessage, bool) {
	var items []map[string]json.RawMessage
	if err := json.Unmarshal(raw, &items); err != nil {
		return raw, false
	}
	sanitized := make([]map[string]json.RawMessage, 0, len(items))
	changed := false
	dropped := 0
	for _, item := range items {
		if shouldDropResponsesHistoryItem(item) {
			changed = true
			dropped++
			continue
		}
		sanitized = append(sanitized, item)
	}
	if !changed {
		return raw, false
	}
	out, err := json.Marshal(sanitized)
	if err != nil {
		return raw, false
	}
	log.Debugf("responses history sanitized: dropped %d server-state item(s)", dropped)
	return out, true
}

// shouldDropResponsesHistoryItem 判断单个 input 条目是否为可安全丢弃的服务端状态引用：
//   - item_reference：纯服务端引用，离开创建它的资源即无法解析
//   - reasoning（无 encrypted_content）：裸引用，不含可移植正文
//   - web_search_call：服务端执行记录，结果已体现在后续消息中
//
// message / function_call / function_call_output 等正文与工具配对条目永不丢弃。
func shouldDropResponsesHistoryItem(item map[string]json.RawMessage) bool {
	rawType, ok := item["type"]
	if !ok {
		return false
	}
	var itemType string
	if err := json.Unmarshal(rawType, &itemType); err != nil {
		return false
	}
	switch itemType {
	case "item_reference", "web_search_call":
		return true
	case "reasoning":
		// 带密文的 reasoning 可跨资源移植，保留；裸 id 引用丢弃
		if enc, ok := item["encrypted_content"]; ok {
			var encStr string
			if err := json.Unmarshal(enc, &encStr); err == nil && strings.TrimSpace(encStr) != "" {
				return false
			}
		}
		return true
	default:
		return false
	}
}

// isStateReferenceError 识别"服务端状态引用失效"类错误：
// 上游（聚合站资源池）轮换后，历史中的条目引用在新的资源上不存在。
func isStateReferenceError(err error) bool {
	if err == nil {
		return false
	}
	message := relayErrorMessage(err)
	if message == "" {
		return false
	}
	return strings.Contains(message, "created under a different") ||
		strings.Contains(message, "different azure openai resource") ||
		strings.Contains(message, "no item found") ||
		strings.Contains(message, "item not found") ||
		strings.Contains(message, "does not have an item with id") ||
		strings.Contains(message, "unknown item")
}
