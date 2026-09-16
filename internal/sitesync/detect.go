package sitesync

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/bestruirui/octopus/internal/model"
)

var urlPlatformHints = []struct {
	pattern          string
	platform         model.SitePlatform
	defaultRouteType model.SiteModelRouteType
}{
	// 智谱 open.bigmodel.cn 的 /api/status 会返回含 "success" 的 JSON，
	// 若不在这里短路会被下面的 status 探测误判为 NewAPI。
	{"open.bigmodel.cn", model.SitePlatformZhipu, model.SiteModelRouteTypeOpenAIChat},
	{"api.zhipu.ai", model.SitePlatformZhipu, model.SiteModelRouteTypeOpenAIChat},
	{"api.z.ai", model.SitePlatformZhipu, model.SiteModelRouteTypeOpenAIChat},
	// DeepSeek 开放平台没有管理页面特征（首页非 new-api 系标题，
	// 也无 /api/status），只能靠 URL 短路识别。
	{"api.deepseek.com", model.SitePlatformDeepSeek, model.SiteModelRouteTypeOpenAIChat},
	// 小米 MiMo 开放平台同理：域名族短路覆盖按量端点与 Token Plan 三地
	// 专属端点（api / token-plan-{cn,sgp,ams}.xiaomimimo.com）。
	{"xiaomimimo.com", model.SitePlatformMiMo, model.SiteModelRouteTypeOpenAIChat},
	// Moonshot Kimi 开放平台同理：国内 api.moonshot.cn 与国际 api.moonshot.ai
	// 双域（key 不通用），域名族短路覆盖按量端点与其 /anthropic 兼容路径。
	{"moonshot.cn", model.SitePlatformKimi, model.SiteModelRouteTypeOpenAIChat},
	{"moonshot.ai", model.SitePlatformKimi, model.SiteModelRouteTypeOpenAIChat},
	{"api.openai.com", model.SitePlatformAPI, model.SiteModelRouteTypeOpenAIChat},
	{"api.anthropic.com", model.SitePlatformAPI, model.SiteModelRouteTypeAnthropic},
	{"anthropic.com/v1", model.SitePlatformAPI, model.SiteModelRouteTypeAnthropic},
	{"generativelanguage.googleapis.com", model.SitePlatformAPI, model.SiteModelRouteTypeGemini},
	{"googleapis.com/v1beta/openai", model.SitePlatformAPI, model.SiteModelRouteTypeGemini},
}

var titlePlatformHints = []struct {
	keyword  string
	platform model.SitePlatform
}{
	{"anyrouter", model.SitePlatformAnyRouter},
	{"done hub", model.SitePlatformDoneHub},
	{"donehub", model.SitePlatformDoneHub},
	{"one hub", model.SitePlatformOneHub},
	{"onehub", model.SitePlatformOneHub},
	{"sub2api", model.SitePlatformSub2API},
	{"new api", model.SitePlatformNewAPI},
	{"newapi", model.SitePlatformNewAPI},
	{"one api", model.SitePlatformOneAPI},
	{"oneapi", model.SitePlatformOneAPI},
}

func DetectPlatform(ctx context.Context, rawURL string) (model.SitePlatform, model.SiteModelRouteType, error) {
	normalizedURL := strings.TrimRight(strings.TrimSpace(rawURL), "/")
	if normalizedURL == "" {
		return "", "", fmt.Errorf("url is empty")
	}
	loweredURL := strings.ToLower(normalizedURL)

	for _, hint := range urlPlatformHints {
		if strings.Contains(loweredURL, hint.pattern) {
			return hint.platform, hint.defaultRouteType, nil
		}
	}

	// Try fetching the page title
	platform, err := detectByPageTitle(ctx, normalizedURL)
	if err == nil && platform != "" {
		return platform, "", nil
	}

	// Try /api/status endpoint
	platform, err = detectByStatusEndpoint(ctx, normalizedURL)
	if err == nil && platform != "" {
		return platform, "", nil
	}

	return "", "", fmt.Errorf("could not detect platform for %s", normalizedURL)
}

func detectByPageTitle(ctx context.Context, baseURL string) (model.SitePlatform, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; Octopus/1.0)")
	req.Header.Set("Accept", "text/html, */*")

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	if err != nil {
		return "", err
	}

	return matchTitlePlatform(string(body)), nil
}

func detectByStatusEndpoint(ctx context.Context, baseURL string) (model.SitePlatform, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	statusURL := strings.TrimRight(baseURL, "/") + "/api/status"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, statusURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/json")

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusOK {
		body, err := io.ReadAll(io.LimitReader(resp.Body, 16*1024))
		if err == nil {
			lowered := strings.ToLower(string(body))
			for _, hint := range titlePlatformHints {
				if strings.Contains(lowered, hint.keyword) {
					return hint.platform, nil
				}
			}
			// If /api/status returns valid JSON, it's likely a management platform
			if strings.Contains(lowered, "\"success\"") || strings.Contains(lowered, "\"data\"") {
				return model.SitePlatformNewAPI, nil
			}
		}
	}

	return "", nil
}

func matchTitlePlatform(html string) model.SitePlatform {
	lowered := strings.ToLower(html)

	titleStart := strings.Index(lowered, "<title")
	if titleStart < 0 {
		return ""
	}
	titleContentStart := strings.Index(lowered[titleStart:], ">")
	if titleContentStart < 0 {
		return ""
	}
	titleEnd := strings.Index(lowered[titleStart+titleContentStart:], "</title>")
	if titleEnd < 0 {
		return ""
	}
	title := lowered[titleStart+titleContentStart+1 : titleStart+titleContentStart+titleEnd]

	for _, hint := range titlePlatformHints {
		if strings.Contains(title, hint.keyword) {
			return hint.platform
		}
	}

	// Also check full body for script/meta hints
	for _, hint := range titlePlatformHints {
		if strings.Contains(lowered, hint.keyword) {
			return hint.platform
		}
	}

	return ""
}
