package sitesync

import (
	"reflect"
	"testing"

	"github.com/bestruirui/octopus/internal/model"
	"github.com/bestruirui/octopus/internal/transformer/outbound"
)

func TestDetectPlatformDeepSeekURL(t *testing.T) {
	tests := []struct {
		name string
		url  string
	}{
		{name: "bare domain", url: "https://api.deepseek.com"},
		{name: "models endpoint", url: "https://api.deepseek.com/models"},
		{name: "v1 alias", url: "https://api.deepseek.com/v1"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// DetectPlatform may reach out to the network for unknown URLs;
			// deepseek URLs must be resolved purely by the URL hints.
			platform, routeType, err := DetectPlatform(testContext(), tt.url)
			if err != nil {
				t.Fatalf("DetectPlatform(%q) error: %v", tt.url, err)
			}
			if platform != model.SitePlatformDeepSeek {
				t.Errorf("DetectPlatform(%q) platform = %q, want deepseek", tt.url, platform)
			}
			if routeType != model.SiteModelRouteTypeOpenAIChat {
				t.Errorf("DetectPlatform(%q) route type = %q, want openai_chat", tt.url, routeType)
			}
		})
	}
}

func TestDeepSeekPlatformBehaviorMatchesDirectAPI(t *testing.T) {
	// deepseek keys are used verbatim like direct provider keys: never force
	// the new-api family "sk-" prefix onto them (official keys already
	// carry the sk- prefix).
	if model.NormalizeSiteSyncTokenValueForPlatform(model.SitePlatformDeepSeek, " abc ") != "abc" {
		t.Errorf("deepseek token must be trimmed verbatim")
	}

	// single OpenAI-compatible endpoint; route splits stay off.
	if model.ShouldSplitSiteChannelRoutes(model.SitePlatformDeepSeek) {
		t.Errorf("deepseek should not split site channel routes")
	}

	// platform constant must round-trip validation.
	if err := model.SitePlatformDeepSeek.Validate(); err != nil {
		t.Errorf("deepseek platform should validate: %v", err)
	}
}

func TestDeepSeekModelFetchBaseURLs(t *testing.T) {
	// 无论站点协议如何，模型列表统一从 OpenAI 兼容端点拉取：
	// Anthropic 端点不提供模型列表（实测 /anthropic/v1/models 404），
	// 而对话渠道仍按协议投影（见 TestDeepSeekProjectedChannelBaseURL）。
	tests := []struct {
		name      string
		baseURL   string
		routeType model.SiteModelRouteType
		want      []string
	}{
		{
			name:      "bare domain chat tries bare then v1 alias",
			baseURL:   "https://api.deepseek.com",
			routeType: model.SiteModelRouteTypeOpenAIChat,
			want:      []string{"https://api.deepseek.com", "https://api.deepseek.com/v1"},
		},
		{
			name:      "bare domain anthropic still uses OpenAI endpoints",
			baseURL:   "https://api.deepseek.com",
			routeType: model.SiteModelRouteTypeAnthropic,
			want:      []string{"https://api.deepseek.com", "https://api.deepseek.com/v1"},
		},
		{
			// 用户直接粘 /anthropic URL 时列表同样从裸域名拉取。
			name:      "anthropic URL falls back to bare domain for models",
			baseURL:   "https://api.deepseek.com/anthropic",
			routeType: model.SiteModelRouteTypeOpenAIChat,
			want:      []string{"https://api.deepseek.com", "https://api.deepseek.com/v1"},
		},
		{
			name:      "anthropic v1 URL falls back to bare domain for models",
			baseURL:   "https://api.deepseek.com/anthropic/v1",
			routeType: model.SiteModelRouteTypeAnthropic,
			want:      []string{"https://api.deepseek.com", "https://api.deepseek.com/v1"},
		},
		{
			name:      "v1 alias is used verbatim",
			baseURL:   "https://api.deepseek.com/v1",
			routeType: model.SiteModelRouteTypeOpenAIChat,
			want:      []string{"https://api.deepseek.com/v1"},
		},
		{
			name:      "v1 alias verbatim regardless of anthropic default",
			baseURL:   "https://api.deepseek.com/v1",
			routeType: model.SiteModelRouteTypeAnthropic,
			want:      []string{"https://api.deepseek.com/v1"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			site := &model.Site{Platform: model.SitePlatformDeepSeek, BaseURL: tt.baseURL, DefaultRouteType: tt.routeType}
			got := buildModelFetchBaseURLs(site)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("buildModelFetchBaseURLs(%q, %s) = %v, want %v", tt.baseURL, tt.routeType, got, tt.want)
			}
		})
	}
}

func TestDeepSeekProjectedChannelBaseURL(t *testing.T) {
	tests := []struct {
		name      string
		baseURL   string
		routeType model.SiteModelRouteType
		want      string
	}{
		{
			// 渠道投影到 /v1 别名：outbound openai_chat 拼 /chat/completions 后
			// 即官方兼容端点。
			name:      "bare domain chat projects to v1 alias",
			baseURL:   "https://api.deepseek.com",
			routeType: model.SiteModelRouteTypeOpenAIChat,
			want:      "https://api.deepseek.com/v1",
		},
		{
			// anthropic 出站直接拼 /messages，投影 base 需带 /v1。
			name:      "bare domain anthropic projects to anthropic v1",
			baseURL:   "https://api.deepseek.com",
			routeType: model.SiteModelRouteTypeAnthropic,
			want:      "https://api.deepseek.com/anthropic/v1",
		},
		{
			name:      "anthropic URL projects verbatim despite openai_chat default",
			baseURL:   "https://api.deepseek.com/anthropic",
			routeType: model.SiteModelRouteTypeOpenAIChat,
			want:      "https://api.deepseek.com/anthropic/v1",
		},
		{
			name:      "anthropic v1 base projects verbatim",
			baseURL:   "https://api.deepseek.com/anthropic/v1",
			routeType: model.SiteModelRouteTypeAnthropic,
			want:      "https://api.deepseek.com/anthropic/v1",
		},
		{
			name:      "v1 URL wins over mismatched anthropic default",
			baseURL:   "https://api.deepseek.com/v1",
			routeType: model.SiteModelRouteTypeAnthropic,
			want:      "https://api.deepseek.com/v1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			site := &model.Site{Platform: model.SitePlatformDeepSeek, BaseURL: tt.baseURL, DefaultRouteType: tt.routeType}
			if got := buildProjectedChannelBaseURL(site); got != tt.want {
				t.Errorf("buildProjectedChannelBaseURL(%q, %s) = %q, want %q", tt.baseURL, tt.routeType, got, tt.want)
			}
		})
	}
}

func TestDeepSeekFetchOutboundTypeAlwaysOpenAIChat(t *testing.T) {
	// 模型列表统一走 OpenAI 兼容端点：拉取出站协议不随站点协议变化
	// （Anthropic 端点无模型列表，出站 Anthropic 只用于对话渠道）。
	tests := []struct {
		name      string
		baseURL   string
		routeType model.SiteModelRouteType
	}{
		{name: "bare domain chat", baseURL: "https://api.deepseek.com", routeType: model.SiteModelRouteTypeOpenAIChat},
		{name: "bare domain anthropic default", baseURL: "https://api.deepseek.com", routeType: model.SiteModelRouteTypeAnthropic},
		{name: "anthropic URL", baseURL: "https://api.deepseek.com/anthropic", routeType: model.SiteModelRouteTypeOpenAIChat},
		{name: "anthropic v1 URL", baseURL: "https://api.deepseek.com/anthropic/v1", routeType: model.SiteModelRouteTypeAnthropic},
		{name: "v1 alias URL", baseURL: "https://api.deepseek.com/v1", routeType: model.SiteModelRouteTypeAnthropic},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			site := &model.Site{Platform: model.SitePlatformDeepSeek, BaseURL: tt.baseURL, DefaultRouteType: tt.routeType}
			if got := fetchOutboundType(site, site.BaseURL); got != outbound.OutboundTypeOpenAIChat {
				t.Errorf("fetchOutboundType(%q, %s) = %v, want openai_chat", tt.baseURL, tt.routeType, got)
			}
		})
	}
}

func TestDeepSeekOpenAIModelFetchURLs(t *testing.T) {
	tests := []struct {
		name    string
		baseURL string
		want    []string
	}{
		{name: "bare domain", baseURL: "https://api.deepseek.com", want: []string{"https://api.deepseek.com", "https://api.deepseek.com/v1"}},
		{name: "anthropic path uses bare domain", baseURL: "https://api.deepseek.com/anthropic", want: []string{"https://api.deepseek.com", "https://api.deepseek.com/v1"}},
		{name: "anthropic v1 path uses bare domain", baseURL: "https://api.deepseek.com/anthropic/v1", want: []string{"https://api.deepseek.com", "https://api.deepseek.com/v1"}},
		{name: "v1 alias used verbatim", baseURL: "https://api.deepseek.com/v1", want: []string{"https://api.deepseek.com/v1"}},
		{name: "trailing slash trimmed", baseURL: "https://api.deepseek.com/anthropic/", want: []string{"https://api.deepseek.com", "https://api.deepseek.com/v1"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := deepseekOpenAIModelFetchURLs(tt.baseURL)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("deepseekOpenAIModelFetchURLs(%q) = %v, want %v", tt.baseURL, got, tt.want)
			}
		})
	}
}

func TestDeepSeekParseBalancePayload(t *testing.T) {
	// fetchDeepSeekBalance 的解析规则：balance_infos[0].total_balance
	// 为字符串金额；缺失或非法时按 0 处理。
	payload := map[string]any{
		"is_available": true,
		"balance_infos": []any{
			map[string]any{"currency": "CNY", "total_balance": "110.25", "granted_balance": "10.00", "topped_up_balance": "100.25"},
		},
	}
	infos, ok := payload["balance_infos"].([]any)
	if !ok || len(infos) != 1 {
		t.Fatalf("balance_infos shape changed")
	}
	info := infos[0].(map[string]any)
	if total, err := parseDeepSeekTotalBalance(info); err != nil || total != 110.25 {
		t.Errorf("parseDeepSeekTotalBalance = %v, %v; want 110.25, nil", total, err)
	}

	if _, err := parseDeepSeekTotalBalance(map[string]any{"total_balance": ""}); err == nil {
		t.Errorf("empty total_balance should error")
	}
	if _, err := parseDeepSeekTotalBalance(map[string]any{}); err == nil {
		t.Errorf("missing total_balance should error")
	}
}
