package model

type LLMPrice struct {
	Input      float64 `json:"input"`
	Output     float64 `json:"output"`
	CacheRead  float64 `json:"cache_read"`
	CacheWrite float64 `json:"cache_write"`
}

type LLMInfo struct {
	Name string `json:"name" gorm:"primaryKey;not null"`
	LLMPrice
}

type LLMChannel struct {
	Name            string `json:"name"`
	Enabled         bool   `json:"enabled"`
	ChannelID       int    `json:"channel_id"`
	ChannelName     string `json:"channel_name"`
	SiteID          *int   `json:"site_id,omitempty"`
	SiteAccountID   *int   `json:"site_account_id,omitempty"`
	SiteGroupKey    string `json:"site_group_key,omitempty"`
	SiteGroupName   string `json:"site_group_name,omitempty"`
	SiteName        string `json:"site_name,omitempty"`
	SiteAccountName string `json:"site_account_name,omitempty"`
	EndpointType    string `json:"endpoint_type,omitempty"`
}

type GeminiModel struct {
	Name        string `json:"name"`
	DisplayName string `json:"displayName"`
	Description string `json:"description"`
}

type GeminiModelList struct {
	Models        []GeminiModel `json:"models"`
	NextPageToken string        `json:"nextPageToken"`
}

type OpenAIModel struct {
	ID      string `json:"id"`
	Object  string `json:"object"`
	Created int    `json:"created"`
	OwnedBy string `json:"owned_by"`
}

// CodexStyleModel 描述 Codex 风格 /models 响应里的模型条目
// （智谱 OpenAI Responses 端点等），模型标识在 slug 字段。
type CodexStyleModel struct {
	Slug        string `json:"slug"`
	DisplayName string `json:"display_name"`
}

type OpenAIModelList struct {
	Object string        `json:"object"`
	Data   []OpenAIModel `json:"data"`
	// Models 兼容 Codex 风格响应（智谱 OpenAI Responses 端点），
	// 与 Data 二选一出现。
	Models []CodexStyleModel `json:"models"`
}

// IDs 返回去重后的模型 ID 列表，优先取 Data[].ID，为空时回退 Models[].Slug。
func (l OpenAIModelList) IDs() []string {
	models := make([]string, 0, len(l.Data)+len(l.Models))
	for _, m := range l.Data {
		if m.ID != "" {
			models = append(models, m.ID)
		}
	}
	if len(models) == 0 {
		for _, m := range l.Models {
			if m.Slug != "" {
				models = append(models, m.Slug)
			}
		}
	}
	return models
}

type AnthropicModel struct {
	ID          string `json:"id"`
	CreatedAt   string `json:"created_at"`
	DisplayName string `json:"display_name"`
	Type        string `json:"type"`
}

type AnthropicModelList struct {
	Data    []AnthropicModel `json:"data"`
	FirstID string           `json:"first_id"`
	HasMore bool             `json:"has_more"`
	LastID  string           `json:"last_id"`
}
