package sitesync

import (
	"context"
	"reflect"
	"testing"

	"github.com/bestruirui/octopus/internal/model"
)

func TestBuildModelFetchBaseURLsForZhipu(t *testing.T) {
	tests := []struct {
		name      string
		baseURL   string
		routeType model.SiteModelRouteType
		want      []string
	}{
		{
			name:      "bare domain openai_chat tries coding endpoint first then pay-as-you-go",
			baseURL:   "https://open.bigmodel.cn",
			routeType: model.SiteModelRouteTypeOpenAIChat,
			want: []string{
				"https://open.bigmodel.cn/api/coding/paas/v4",
				"https://open.bigmodel.cn/api/paas/v4",
			},
		},
		{
			name:      "trailing slash is trimmed",
			baseURL:   "https://open.bigmodel.cn/",
			routeType: model.SiteModelRouteTypeOpenAIChat,
			want: []string{
				"https://open.bigmodel.cn/api/coding/paas/v4",
				"https://open.bigmodel.cn/api/paas/v4",
			},
		},
		{
			name:      "complete coding endpoint is used verbatim",
			baseURL:   "https://open.bigmodel.cn/api/coding/paas/v4",
			routeType: model.SiteModelRouteTypeOpenAIChat,
			want:      []string{"https://open.bigmodel.cn/api/coding/paas/v4"},
		},
		{
			name:      "pay-as-you-go v4 endpoint is used verbatim",
			baseURL:   "https://open.bigmodel.cn/api/paas/v4",
			routeType: model.SiteModelRouteTypeOpenAIChat,
			want:      []string{"https://open.bigmodel.cn/api/paas/v4"},
		},
		{
			name:      "anthropic default builds official anthropic endpoint",
			baseURL:   "https://open.bigmodel.cn",
			routeType: model.SiteModelRouteTypeAnthropic,
			want:      []string{"https://open.bigmodel.cn/api/anthropic/v1"},
		},
		{
			name:      "anthropic base URL gets v1 appended",
			baseURL:   "https://open.bigmodel.cn/api/anthropic",
			routeType: model.SiteModelRouteTypeAnthropic,
			want:      []string{"https://open.bigmodel.cn/api/anthropic/v1"},
		},
		{
			name:      "anthropic full endpoint is used verbatim",
			baseURL:   "https://open.bigmodel.cn/api/anthropic/v1",
			routeType: model.SiteModelRouteTypeAnthropic,
			want:      []string{"https://open.bigmodel.cn/api/anthropic/v1"},
		},
		{
			name:      "openai_response default builds official responses endpoint",
			baseURL:   "https://open.bigmodel.cn",
			routeType: model.SiteModelRouteTypeOpenAIResponse,
			want:      []string{"https://open.bigmodel.cn/api/v1"},
		},
		{
			name:      "openai_response full endpoint is used verbatim",
			baseURL:   "https://open.bigmodel.cn/api/v1",
			routeType: model.SiteModelRouteTypeOpenAIResponse,
			want:      []string{"https://open.bigmodel.cn/api/v1"},
		},
		{
			name:      "international z.ai is not treated as a complete endpoint",
			baseURL:   "https://api.z.ai",
			routeType: model.SiteModelRouteTypeOpenAIChat,
			want: []string{
				"https://api.z.ai/api/coding/paas/v4",
				"https://api.z.ai/api/paas/v4",
			},
		},
		{
			// URL 指明 responses 端点家族时，即使默认协议仍选着
			// openai_chat，也以 URL 为准——不再把 /api/coding/paas/v4
			// 拼到 /api/v1 后面导致 404。
			name:      "responses URL wins over mismatched openai_chat default",
			baseURL:   "https://open.bigmodel.cn/api/v1",
			routeType: model.SiteModelRouteTypeOpenAIChat,
			want:      []string{"https://open.bigmodel.cn/api/v1"},
		},
		{
			name:      "anthropic URL wins over mismatched openai_chat default",
			baseURL:   "https://open.bigmodel.cn/api/anthropic",
			routeType: model.SiteModelRouteTypeOpenAIChat,
			want:      []string{"https://open.bigmodel.cn/api/anthropic/v1"},
		},
		{
			name:      "coding URL wins over mismatched anthropic default",
			baseURL:   "https://open.bigmodel.cn/api/coding/paas/v4",
			routeType: model.SiteModelRouteTypeAnthropic,
			want:      []string{"https://open.bigmodel.cn/api/coding/paas/v4"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			site := &model.Site{Platform: model.SitePlatformZhipu, BaseURL: tt.baseURL, DefaultRouteType: tt.routeType}
			got := buildModelFetchBaseURLs(site)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("buildModelFetchBaseURLs(%q, %s) = %v, want %v", tt.baseURL, tt.routeType, got, tt.want)
			}
		})
	}
}

func TestZhipuProjectedBaseURLFollowsDefaultRoute(t *testing.T) {
	tests := []struct {
		name      string
		baseURL   string
		routeType model.SiteModelRouteType
		want      string
	}{
		{
			name:      "bare domain chat projects to coding endpoint",
			baseURL:   "https://open.bigmodel.cn",
			routeType: model.SiteModelRouteTypeOpenAIChat,
			want:      "https://open.bigmodel.cn/api/coding/paas/v4",
		},
		{
			name:      "bare domain anthropic projects to anthropic endpoint",
			baseURL:   "https://open.bigmodel.cn",
			routeType: model.SiteModelRouteTypeAnthropic,
			want:      "https://open.bigmodel.cn/api/anthropic/v1",
		},
		{
			name:      "anthropic base URL gains v1",
			baseURL:   "https://open.bigmodel.cn/api/anthropic",
			routeType: model.SiteModelRouteTypeAnthropic,
			want:      "https://open.bigmodel.cn/api/anthropic/v1",
		},
		{
			name:      "bare domain responses projects to responses endpoint",
			baseURL:   "https://open.bigmodel.cn",
			routeType: model.SiteModelRouteTypeOpenAIResponse,
			want:      "https://open.bigmodel.cn/api/v1",
		},
		{
			// 与模型拉取一致：URL 指明端点家族时投影不再受默认协议影响。
			name:      "responses URL projects verbatim despite openai_chat default",
			baseURL:   "https://open.bigmodel.cn/api/v1",
			routeType: model.SiteModelRouteTypeOpenAIChat,
			want:      "https://open.bigmodel.cn/api/v1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			site := &model.Site{Platform: model.SitePlatformZhipu, BaseURL: tt.baseURL, DefaultRouteType: tt.routeType}
			got := buildProjectedChannelBaseURL(site)
			if got != tt.want {
				t.Errorf("buildProjectedChannelBaseURL(%q, %s) = %q, want %q", tt.baseURL, tt.routeType, got, tt.want)
			}
		})
	}
}

func TestZhipuPlatformBehaviorMatchesDirectAPI(t *testing.T) {
	// zhipu keys are used verbatim like direct provider keys: never force
	// the new-api family "sk-" prefix onto them.
	if model.NormalizeSiteSyncTokenValueForPlatform(model.SitePlatformZhipu, "abc.secret") != "abc.secret" {
		t.Errorf("zhipu token must be used verbatim, got forced sk- prefix")
	}
	if model.NormalizeSiteSyncTokenValueForPlatform(model.SitePlatformZhipu, "sk-abc") != "sk-abc" {
		t.Errorf("zhipu sk- token must stay untouched")
	}

	// single channel by default; route overrides trigger the split elsewhere.
	if model.ShouldSplitSiteChannelRoutes(model.SitePlatformZhipu) {
		t.Errorf("zhipu should not split site channel routes by default")
	}

	// platform constant must round-trip validation.
	if err := model.SitePlatformZhipu.Validate(); err != nil {
		t.Errorf("zhipu platform should validate: %v", err)
	}
}

func TestDetectPlatformZhipuURLs(t *testing.T) {
	tests := []struct {
		name         string
		url          string
		wantPlatform model.SitePlatform
	}{
		{name: "bigmodel domain", url: "https://open.bigmodel.cn", wantPlatform: model.SitePlatformZhipu},
		{name: "bigmodel with path", url: "https://open.bigmodel.cn/api/coding/paas/v4", wantPlatform: model.SitePlatformZhipu},
		{name: "zhipu legacy domain", url: "https://api.zhipu.ai", wantPlatform: model.SitePlatformZhipu},
		{name: "z.ai international", url: "https://api.z.ai", wantPlatform: model.SitePlatformZhipu},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// DetectPlatform may reach out to the network for unknown URLs;
			// zhipu URLs must be resolved purely by the URL hints.
			platform, routeType, err := DetectPlatform(testContext(), tt.url)
			if err != nil {
				t.Fatalf("DetectPlatform(%q) error: %v", tt.url, err)
			}
			if platform != tt.wantPlatform {
				t.Errorf("DetectPlatform(%q) platform = %q, want %q", tt.url, platform, tt.wantPlatform)
			}
			if routeType != model.SiteModelRouteTypeOpenAIChat {
				t.Errorf("DetectPlatform(%q) route type = %q, want openai_chat", tt.url, routeType)
			}
		})
	}
}

func testContext() context.Context {
	return context.Background()
}
