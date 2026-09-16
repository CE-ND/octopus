package sitesync

import (
	"reflect"
	"testing"

	"github.com/bestruirui/octopus/internal/model"
	"github.com/bestruirui/octopus/internal/transformer/outbound"
)

func TestDetectPlatformKimiURL(t *testing.T) {
	tests := []struct {
		name string
		url  string
	}{
		{name: "cn bare domain", url: "https://api.moonshot.cn"},
		{name: "cn v1 endpoint", url: "https://api.moonshot.cn/v1"},
		{name: "cn anthropic endpoint", url: "https://api.moonshot.cn/anthropic"},
		{name: "intl bare domain", url: "https://api.moonshot.ai"},
		{name: "intl anthropic endpoint", url: "https://api.moonshot.ai/anthropic"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// DetectPlatform may reach out to the network for unknown URLs;
			// kimi URLs must be resolved purely by the URL hints.
			platform, routeType, err := DetectPlatform(testContext(), tt.url)
			if err != nil {
				t.Fatalf("DetectPlatform(%q) error: %v", tt.url, err)
			}
			if platform != model.SitePlatformKimi {
				t.Errorf("DetectPlatform(%q) platform = %q, want kimi", tt.url, platform)
			}
			if routeType != model.SiteModelRouteTypeOpenAIChat {
				t.Errorf("DetectPlatform(%q) route type = %q, want openai_chat", tt.url, routeType)
			}
		})
	}
}

func TestKimiPlatformBehaviorMatchesDirectAPI(t *testing.T) {
	// kimi keys carry the official sk- prefix and are used verbatim: never
	// force the new-api family "sk-" prefix onto them.
	if model.NormalizeSiteSyncTokenValueForPlatform(model.SitePlatformKimi, " abc ") != "abc" {
		t.Errorf("kimi token must be trimmed verbatim")
	}

	// single endpoint per protocol family; route splits stay off.
	if model.ShouldSplitSiteChannelRoutes(model.SitePlatformKimi) {
		t.Errorf("kimi should not split site channel routes")
	}

	// platform constant must round-trip validation.
	if err := model.SitePlatformKimi.Validate(); err != nil {
		t.Errorf("kimi platform should validate: %v", err)
	}
}

func TestKimiModelFetchBaseURLs(t *testing.T) {
	// 无论站点协议如何，模型列表统一从 OpenAI 兼容端点的 /v1/models 拉取：
	// Anthropic 端点没有模型列表（实测 /anthropic/v1/models 404），列表
	// 按 key 权限过滤，对话渠道仍按协议投影（见 TestKimiProjectedChannelBaseURL）。
	tests := []struct {
		name      string
		baseURL   string
		routeType model.SiteModelRouteType
		want      []string
	}{
		{
			name:      "cn bare domain completes to v1",
			baseURL:   "https://api.moonshot.cn",
			routeType: model.SiteModelRouteTypeOpenAIChat,
			want:      []string{"https://api.moonshot.cn/v1"},
		},
		{
			name:      "cn bare domain anthropic still uses OpenAI endpoint",
			baseURL:   "https://api.moonshot.cn",
			routeType: model.SiteModelRouteTypeAnthropic,
			want:      []string{"https://api.moonshot.cn/v1"},
		},
		{
			name:      "anthropic URL falls back to bare domain v1 for models",
			baseURL:   "https://api.moonshot.cn/anthropic",
			routeType: model.SiteModelRouteTypeAnthropic,
			want:      []string{"https://api.moonshot.cn/v1"},
		},
		{
			name:      "anthropic v1 URL falls back to bare domain v1 for models",
			baseURL:   "https://api.moonshot.cn/anthropic/v1",
			routeType: model.SiteModelRouteTypeOpenAIChat,
			want:      []string{"https://api.moonshot.cn/v1"},
		},
		{
			name:      "v1 endpoint used verbatim",
			baseURL:   "https://api.moonshot.cn/v1",
			routeType: model.SiteModelRouteTypeOpenAIChat,
			want:      []string{"https://api.moonshot.cn/v1"},
		},
		{
			name:      "intl domain keeps its own host",
			baseURL:   "https://api.moonshot.ai/anthropic",
			routeType: model.SiteModelRouteTypeAnthropic,
			want:      []string{"https://api.moonshot.ai/v1"},
		},
		{
			name:      "trailing slash trimmed",
			baseURL:   "https://api.moonshot.cn/anthropic/",
			routeType: model.SiteModelRouteTypeAnthropic,
			want:      []string{"https://api.moonshot.cn/v1"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			site := &model.Site{Platform: model.SitePlatformKimi, BaseURL: tt.baseURL, DefaultRouteType: tt.routeType}
			got := buildModelFetchBaseURLs(site)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("buildModelFetchBaseURLs(%q, %s) = %v, want %v", tt.baseURL, tt.routeType, got, tt.want)
			}
		})
	}
}

func TestKimiProjectedChannelBaseURL(t *testing.T) {
	tests := []struct {
		name      string
		baseURL   string
		routeType model.SiteModelRouteType
		want      string
	}{
		{
			// 渠道投影到 OpenAI 兼容端点：outbound openai_chat 拼
			// /chat/completions 后即官方端点。
			name:      "bare domain chat projects to v1",
			baseURL:   "https://api.moonshot.cn",
			routeType: model.SiteModelRouteTypeOpenAIChat,
			want:      "https://api.moonshot.cn/v1",
		},
		{
			// anthropic 出站直接拼 /messages，投影 base 需带 /v1。
			name:      "bare domain anthropic projects to anthropic v1",
			baseURL:   "https://api.moonshot.cn",
			routeType: model.SiteModelRouteTypeAnthropic,
			want:      "https://api.moonshot.cn/anthropic/v1",
		},
		{
			name:      "anthropic URL projects verbatim despite openai_chat default",
			baseURL:   "https://api.moonshot.cn/anthropic",
			routeType: model.SiteModelRouteTypeOpenAIChat,
			want:      "https://api.moonshot.cn/anthropic/v1",
		},
		{
			name:      "anthropic v1 base projects verbatim",
			baseURL:   "https://api.moonshot.cn/anthropic/v1",
			routeType: model.SiteModelRouteTypeAnthropic,
			want:      "https://api.moonshot.cn/anthropic/v1",
		},
		{
			name:      "v1 URL wins over mismatched anthropic default",
			baseURL:   "https://api.moonshot.cn/v1",
			routeType: model.SiteModelRouteTypeAnthropic,
			want:      "https://api.moonshot.cn/v1",
		},
		{
			name:      "intl anthropic URL projects to its own anthropic v1",
			baseURL:   "https://api.moonshot.ai/anthropic",
			routeType: model.SiteModelRouteTypeAnthropic,
			want:      "https://api.moonshot.ai/anthropic/v1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			site := &model.Site{Platform: model.SitePlatformKimi, BaseURL: tt.baseURL, DefaultRouteType: tt.routeType}
			if got := buildProjectedChannelBaseURL(site); got != tt.want {
				t.Errorf("buildProjectedChannelBaseURL(%q, %s) = %q, want %q", tt.baseURL, tt.routeType, got, tt.want)
			}
		})
	}
}

func TestKimiFetchOutboundTypeAlwaysOpenAIChat(t *testing.T) {
	// 模型列表统一走 OpenAI 兼容端点：拉取出站协议不随站点协议变化
	// （Anthropic 端点无模型列表，出站 Anthropic 只用于对话渠道）。
	tests := []struct {
		name      string
		baseURL   string
		routeType model.SiteModelRouteType
	}{
		{name: "cn bare domain chat", baseURL: "https://api.moonshot.cn", routeType: model.SiteModelRouteTypeOpenAIChat},
		{name: "cn bare domain anthropic default", baseURL: "https://api.moonshot.cn", routeType: model.SiteModelRouteTypeAnthropic},
		{name: "anthropic URL", baseURL: "https://api.moonshot.cn/anthropic", routeType: model.SiteModelRouteTypeOpenAIChat},
		{name: "intl anthropic URL", baseURL: "https://api.moonshot.ai/anthropic", routeType: model.SiteModelRouteTypeAnthropic},
		{name: "v1 URL", baseURL: "https://api.moonshot.cn/v1", routeType: model.SiteModelRouteTypeAnthropic},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			site := &model.Site{Platform: model.SitePlatformKimi, BaseURL: tt.baseURL, DefaultRouteType: tt.routeType}
			if got := fetchOutboundType(site, site.BaseURL); got != outbound.OutboundTypeOpenAIChat {
				t.Errorf("fetchOutboundType(%q, %s) = %v, want openai_chat", tt.baseURL, tt.routeType, got)
			}
		})
	}
}

func TestKimiOpenAIAPIBaseURL(t *testing.T) {
	// 模型列表与余额查询共用的 OpenAI 兼容 API 根归一：剥 /anthropic、
	// 确保 /v1 唯一（余额路径 /users/me/balance 直接拼在该根上）。
	tests := []struct {
		name    string
		baseURL string
		want    string
	}{
		{name: "bare domain", baseURL: "https://api.moonshot.cn", want: "https://api.moonshot.cn/v1"},
		{name: "anthropic path uses bare domain v1", baseURL: "https://api.moonshot.cn/anthropic", want: "https://api.moonshot.cn/v1"},
		{name: "anthropic v1 path uses bare domain v1", baseURL: "https://api.moonshot.cn/anthropic/v1", want: "https://api.moonshot.cn/v1"},
		{name: "v1 used verbatim", baseURL: "https://api.moonshot.cn/v1", want: "https://api.moonshot.cn/v1"},
		{name: "intl domain keeps its own host", baseURL: "https://api.moonshot.ai", want: "https://api.moonshot.ai/v1"},
		{name: "trailing slash trimmed", baseURL: "https://api.moonshot.cn/anthropic/", want: "https://api.moonshot.cn/v1"},
		{name: "empty returns empty", baseURL: "   ", want: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := kimiOpenAIAPIBaseURL(tt.baseURL); got != tt.want {
				t.Errorf("kimiOpenAIAPIBaseURL(%q) = %q, want %q", tt.baseURL, got, tt.want)
			}
		})
	}
}

func TestParseKimiBalance(t *testing.T) {
	// 官方余额响应（/v1/users/me/balance）：available_balance 为数字（元）。
	got := parseKimiBalance(map[string]any{
		"code":   float64(0),
		"status": true,
		"scode":  "0",
		"data": map[string]any{
			"available_balance": 12.34,
			"voucher_balance":   float64(15),
		},
	})
	if got != 12.34 {
		t.Errorf("parseKimiBalance available = %v, want 12.34", got)
	}

	// code 非 0 视为查询失败。
	if got := parseKimiBalance(map[string]any{
		"code":   float64(401),
		"status": false,
		"data":   map[string]any{"available_balance": 12.34},
	}); got != 0 {
		t.Errorf("parseKimiBalance(code=401) = %v, want 0", got)
	}

	// status 缺失不阻断（仅显式 false 才视为失败）。
	if got := parseKimiBalance(map[string]any{
		"data": map[string]any{"available_balance": float64(7)},
	}); got != 7 {
		t.Errorf("parseKimiBalance(no status) = %v, want 7", got)
	}

	// available_balance 缺失、data 缺失或响应为空均按 0 处理。
	if got := parseKimiBalance(map[string]any{"code": float64(0)}); got != 0 {
		t.Errorf("parseKimiBalance(no data) = %v, want 0", got)
	}
	if got := parseKimiBalance(map[string]any{}); got != 0 {
		t.Errorf("parseKimiBalance(empty) = %v, want 0", got)
	}
	if got := parseKimiBalance(nil); got != 0 {
		t.Errorf("parseKimiBalance(nil) = %v, want 0", got)
	}
}
