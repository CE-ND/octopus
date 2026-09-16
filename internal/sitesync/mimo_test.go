package sitesync

import (
	"reflect"
	"testing"

	"github.com/bestruirui/octopus/internal/model"
	"github.com/bestruirui/octopus/internal/transformer/outbound"
)

func TestDetectPlatformMiMoURL(t *testing.T) {
	tests := []struct {
		name string
		url  string
	}{
		{name: "pay-as-you-go bare domain", url: "https://api.xiaomimimo.com"},
		{name: "pay-as-you-go v1 endpoint", url: "https://api.xiaomimimo.com/v1"},
		{name: "token plan cn domain", url: "https://token-plan-cn.xiaomimimo.com"},
		{name: "token plan sgp v1 endpoint", url: "https://token-plan-sgp.xiaomimimo.com/v1"},
		{name: "token plan ams anthropic endpoint", url: "https://token-plan-ams.xiaomimimo.com/anthropic"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// DetectPlatform may reach out to the network for unknown URLs;
			// mimo URLs must be resolved purely by the URL hints.
			platform, routeType, err := DetectPlatform(testContext(), tt.url)
			if err != nil {
				t.Fatalf("DetectPlatform(%q) error: %v", tt.url, err)
			}
			if platform != model.SitePlatformMiMo {
				t.Errorf("DetectPlatform(%q) platform = %q, want mimo", tt.url, platform)
			}
			if routeType != model.SiteModelRouteTypeOpenAIChat {
				t.Errorf("DetectPlatform(%q) route type = %q, want openai_chat", tt.url, routeType)
			}
		})
	}
}

func TestMiMoPlatformBehaviorMatchesDirectAPI(t *testing.T) {
	// mimo keys are used verbatim like direct provider keys: never force
	// the new-api family "sk-" prefix onto them (official keys already
	// carry the sk- prefix, Token Plan keys carry tp-).
	if model.NormalizeSiteSyncTokenValueForPlatform(model.SitePlatformMiMo, " abc ") != "abc" {
		t.Errorf("mimo token must be trimmed verbatim")
	}

	// single endpoint per protocol family; route splits stay off.
	if model.ShouldSplitSiteChannelRoutes(model.SitePlatformMiMo) {
		t.Errorf("mimo should not split site channel routes")
	}

	// platform constant must round-trip validation.
	if err := model.SitePlatformMiMo.Validate(); err != nil {
		t.Errorf("mimo platform should validate: %v", err)
	}
}

func TestMiMoModelFetchBaseURLs(t *testing.T) {
	// 无论站点协议如何，模型列表统一从 OpenAI 兼容端点的 /v1/models 拉取：
	// MiMo 没有裸域名 /models 别名（实测 404），Anthropic 端点也没有模型
	// 列表（实测 /anthropic/v1/models 404），对话渠道仍按协议投影
	// （见 TestMiMoProjectedChannelBaseURL）。
	tests := []struct {
		name      string
		baseURL   string
		routeType model.SiteModelRouteType
		want      []string
	}{
		{
			name:      "bare domain completes to v1",
			baseURL:   "https://api.xiaomimimo.com",
			routeType: model.SiteModelRouteTypeOpenAIChat,
			want:      []string{"https://api.xiaomimimo.com/v1"},
		},
		{
			name:      "bare domain anthropic still uses OpenAI endpoint",
			baseURL:   "https://api.xiaomimimo.com",
			routeType: model.SiteModelRouteTypeAnthropic,
			want:      []string{"https://api.xiaomimimo.com/v1"},
		},
		{
			name:      "anthropic URL falls back to bare domain v1 for models",
			baseURL:   "https://api.xiaomimimo.com/anthropic",
			routeType: model.SiteModelRouteTypeAnthropic,
			want:      []string{"https://api.xiaomimimo.com/v1"},
		},
		{
			name:      "anthropic v1 URL falls back to bare domain v1 for models",
			baseURL:   "https://api.xiaomimimo.com/anthropic/v1",
			routeType: model.SiteModelRouteTypeOpenAIChat,
			want:      []string{"https://api.xiaomimimo.com/v1"},
		},
		{
			name:      "v1 endpoint used verbatim",
			baseURL:   "https://api.xiaomimimo.com/v1",
			routeType: model.SiteModelRouteTypeOpenAIChat,
			want:      []string{"https://api.xiaomimimo.com/v1"},
		},
		{
			name:      "token plan anthropic URL uses its own bare domain v1",
			baseURL:   "https://token-plan-cn.xiaomimimo.com/anthropic",
			routeType: model.SiteModelRouteTypeAnthropic,
			want:      []string{"https://token-plan-cn.xiaomimimo.com/v1"},
		},
		{
			name:      "trailing slash trimmed",
			baseURL:   "https://api.xiaomimimo.com/anthropic/",
			routeType: model.SiteModelRouteTypeAnthropic,
			want:      []string{"https://api.xiaomimimo.com/v1"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			site := &model.Site{Platform: model.SitePlatformMiMo, BaseURL: tt.baseURL, DefaultRouteType: tt.routeType}
			got := buildModelFetchBaseURLs(site)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("buildModelFetchBaseURLs(%q, %s) = %v, want %v", tt.baseURL, tt.routeType, got, tt.want)
			}
		})
	}
}

func TestMiMoProjectedChannelBaseURL(t *testing.T) {
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
			baseURL:   "https://api.xiaomimimo.com",
			routeType: model.SiteModelRouteTypeOpenAIChat,
			want:      "https://api.xiaomimimo.com/v1",
		},
		{
			// anthropic 出站直接拼 /messages，投影 base 需带 /v1。
			name:      "bare domain anthropic projects to anthropic v1",
			baseURL:   "https://api.xiaomimimo.com",
			routeType: model.SiteModelRouteTypeAnthropic,
			want:      "https://api.xiaomimimo.com/anthropic/v1",
		},
		{
			name:      "anthropic URL projects verbatim despite openai_chat default",
			baseURL:   "https://api.xiaomimimo.com/anthropic",
			routeType: model.SiteModelRouteTypeOpenAIChat,
			want:      "https://api.xiaomimimo.com/anthropic/v1",
		},
		{
			name:      "anthropic v1 base projects verbatim",
			baseURL:   "https://api.xiaomimimo.com/anthropic/v1",
			routeType: model.SiteModelRouteTypeAnthropic,
			want:      "https://api.xiaomimimo.com/anthropic/v1",
		},
		{
			name:      "v1 URL wins over mismatched anthropic default",
			baseURL:   "https://api.xiaomimimo.com/v1",
			routeType: model.SiteModelRouteTypeAnthropic,
			want:      "https://api.xiaomimimo.com/v1",
		},
		{
			name:      "token plan anthropic URL projects to its own anthropic v1",
			baseURL:   "https://token-plan-sgp.xiaomimimo.com/anthropic",
			routeType: model.SiteModelRouteTypeAnthropic,
			want:      "https://token-plan-sgp.xiaomimimo.com/anthropic/v1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			site := &model.Site{Platform: model.SitePlatformMiMo, BaseURL: tt.baseURL, DefaultRouteType: tt.routeType}
			if got := buildProjectedChannelBaseURL(site); got != tt.want {
				t.Errorf("buildProjectedChannelBaseURL(%q, %s) = %q, want %q", tt.baseURL, tt.routeType, got, tt.want)
			}
		})
	}
}

func TestMiMoFetchOutboundTypeAlwaysOpenAIChat(t *testing.T) {
	// 模型列表统一走 OpenAI 兼容端点：拉取出站协议不随站点协议变化
	// （Anthropic 端点无模型列表，出站 Anthropic 只用于对话渠道）。
	tests := []struct {
		name      string
		baseURL   string
		routeType model.SiteModelRouteType
	}{
		{name: "bare domain chat", baseURL: "https://api.xiaomimimo.com", routeType: model.SiteModelRouteTypeOpenAIChat},
		{name: "bare domain anthropic default", baseURL: "https://api.xiaomimimo.com", routeType: model.SiteModelRouteTypeAnthropic},
		{name: "anthropic URL", baseURL: "https://api.xiaomimimo.com/anthropic", routeType: model.SiteModelRouteTypeOpenAIChat},
		{name: "token plan anthropic URL", baseURL: "https://token-plan-cn.xiaomimimo.com/anthropic", routeType: model.SiteModelRouteTypeAnthropic},
		{name: "v1 alias URL", baseURL: "https://api.xiaomimimo.com/v1", routeType: model.SiteModelRouteTypeAnthropic},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			site := &model.Site{Platform: model.SitePlatformMiMo, BaseURL: tt.baseURL, DefaultRouteType: tt.routeType}
			if got := fetchOutboundType(site, site.BaseURL); got != outbound.OutboundTypeOpenAIChat {
				t.Errorf("fetchOutboundType(%q, %s) = %v, want openai_chat", tt.baseURL, tt.routeType, got)
			}
		})
	}
}

func TestMiMoOpenAIModelFetchURLs(t *testing.T) {
	tests := []struct {
		name    string
		baseURL string
		want    []string
	}{
		{name: "bare domain", baseURL: "https://api.xiaomimimo.com", want: []string{"https://api.xiaomimimo.com/v1"}},
		{name: "anthropic path uses bare domain v1", baseURL: "https://api.xiaomimimo.com/anthropic", want: []string{"https://api.xiaomimimo.com/v1"}},
		{name: "anthropic v1 path uses bare domain v1", baseURL: "https://api.xiaomimimo.com/anthropic/v1", want: []string{"https://api.xiaomimimo.com/v1"}},
		{name: "v1 used verbatim", baseURL: "https://api.xiaomimimo.com/v1", want: []string{"https://api.xiaomimimo.com/v1"}},
		{name: "token plan bare domain", baseURL: "https://token-plan-ams.xiaomimimo.com", want: []string{"https://token-plan-ams.xiaomimimo.com/v1"}},
		{name: "trailing slash trimmed", baseURL: "https://api.xiaomimimo.com/anthropic/", want: []string{"https://api.xiaomimimo.com/v1"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := mimoOpenAIModelFetchURLs(tt.baseURL)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("mimoOpenAIModelFetchURLs(%q) = %v, want %v", tt.baseURL, got, tt.want)
			}
		})
	}
}

func TestMiMoChatModelFilter(t *testing.T) {
	// /v1/models 会把 ASR 与 TTS 一并列出（实测返回 mimo-v2.5-asr、
	// mimo-v2.5-tts、mimo-v2.5-tts-voiceclone、mimo-v2.5-tts-voicedesign），
	// 它们不走 /chat/completions，投影前必须剔除。
	got := mimoChatModelNames([]string{
		"mimo-v2.5",
		"mimo-v2.5-pro",
		"mimo-v2.5-asr",
		"mimo-v2.5-tts",
		"mimo-v2.5-tts-voiceclone",
		"mimo-v2.5-tts-voicedesign",
	})
	want := []string{"mimo-v2.5", "mimo-v2.5-pro"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("mimoChatModelNames = %v, want %v", got, want)
	}

	if got := mimoChatModelNames(nil); len(got) != 0 {
		t.Errorf("mimoChatModelNames(nil) = %v, want empty", got)
	}
}
