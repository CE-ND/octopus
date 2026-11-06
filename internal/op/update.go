package op

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/bestruirui/octopus/internal/apperror"
	"github.com/bestruirui/octopus/internal/conf"
	"github.com/bestruirui/octopus/internal/model"
	"golang.org/x/mod/semver"
	"golang.org/x/net/proxy"
)

const (
	releaseCacheTTL       = 10 * time.Minute
	releaseRefreshMinimum = 30 * time.Second
	releaseRequestTimeout = 15 * time.Second
	releaseResponseLimit  = 4 << 20
)

var releaseRepositoryPart = regexp.MustCompile(`^[A-Za-z0-9_.-]+$`)
var releaseDevelopmentVersion = regexp.MustCompile(`^v?\d+\.\d+\.\d+-\d+-g[0-9a-f]+(?:-dirty)?$`)

type releaseRepository struct{ owner, name, url string }

// 发布源与 Git 远程保持一致，也允许部署时指定另一个 GitHub fork。
func resolveReleaseRepository() (releaseRepository, error) {
	raw := strings.TrimSpace(os.Getenv("OCTOPUS_GITHUB_REPO"))
	if raw == "" {
		raw = conf.Repo
	}
	return parseReleaseRepository(raw)
}

func parseReleaseRepository(raw string) (releaseRepository, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Scheme != "https" || parsed.Host != "github.com" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return releaseRepository{}, fmt.Errorf("release repository must be an HTTPS GitHub repository URL")
	}
	parts := strings.Split(strings.Trim(parsed.Path, "/"), "/")
	if len(parts) != 2 {
		return releaseRepository{}, fmt.Errorf("release repository must contain an owner and repository name")
	}
	parts[1] = strings.TrimSuffix(parts[1], ".git")
	for _, part := range parts {
		if !releaseRepositoryPart.MatchString(part) || part == "." || part == ".." {
			return releaseRepository{}, fmt.Errorf("invalid GitHub repository name")
		}
	}
	return releaseRepository{owner: parts[0], name: parts[1], url: "https://github.com/" + strings.Join(parts, "/")}, nil
}

func VersionInfoGet() model.VersionInfo {
	info := model.VersionInfo{
		Version: conf.Version, Commit: conf.Commit, BuildTime: conf.BuildTime,
		Platform: runtime.GOOS, Architecture: runtime.GOARCH,
		Desktop: os.Getenv("OCTOPUS_DESKTOP") == "1",
	}
	if repo, err := resolveReleaseRepository(); err == nil {
		info.Repository = repo.url
		info.ReleasesURL = repo.url + "/releases"
	}
	return info
}

type githubRelease struct {
	ID          int64     `json:"id"`
	TagName     string    `json:"tag_name"`
	Name        string    `json:"name"`
	HTMLURL     string    `json:"html_url"`
	Body        string    `json:"body"`
	PublishedAt time.Time `json:"published_at"`
	Draft       bool      `json:"draft"`
	Prerelease  bool      `json:"prerelease"`
	Assets      []struct {
		Name        string `json:"name"`
		Size        int64  `json:"size"`
		State       string `json:"state"`
		DownloadURL string `json:"browser_download_url"`
	} `json:"assets"`
}

type releaseSnapshot struct {
	latest    *model.ReleaseVersion
	releases  []model.ReleaseVersion
	checkedAt time.Time
}

type releaseFlight struct {
	done     chan struct{}
	snapshot releaseSnapshot
	err      error
}

type releaseService struct {
	mu            sync.Mutex
	cache         map[string]releaseSnapshot
	flights       map[string]*releaseFlight
	apiBase       string
	clientFactory func() (*http.Client, error)
}

var versionReleases = &releaseService{apiBase: "https://api.github.com", clientFactory: releaseHTTPClient}

func ReleaseCheckGet(ctx context.Context, refresh bool) (model.ReleaseCheck, error) {
	repo, err := resolveReleaseRepository()
	if err != nil {
		return model.ReleaseCheck{}, apperror.Wrap("update.invalid_repository", "GitHub release repository is invalid", err).WithStatus(http.StatusBadRequest)
	}
	token := strings.TrimSpace(os.Getenv("OCTOPUS_GITHUB_PAT"))
	snapshot, err := versionReleases.get(ctx, repo, token, refresh)
	if err != nil {
		return model.ReleaseCheck{}, err
	}
	return model.ReleaseCheck{
		CurrentVersion: conf.Version, Status: compareReleaseVersion(conf.Version, snapshot.latest),
		CheckedAt: snapshot.checkedAt, Latest: snapshot.latest, Releases: snapshot.releases,
	}, nil
}

// 并发查询合并为一个请求；等待方可自行取消，手动检查最多每 30 秒访问一次 GitHub。
func (s *releaseService) get(ctx context.Context, repo releaseRepository, token string, refresh bool) (releaseSnapshot, error) {
	key := fmt.Sprintf("%s:%x", repo.url, sha256.Sum256([]byte(token)))
	s.mu.Lock()
	if cached, ok := s.cache[key]; ok {
		age := time.Since(cached.checkedAt)
		if age < releaseRefreshMinimum || (!refresh && age < releaseCacheTTL) {
			s.mu.Unlock()
			return cached, nil
		}
	}
	if flight := s.flights[key]; flight != nil {
		s.mu.Unlock()
		select {
		case <-ctx.Done():
			return releaseSnapshot{}, ctx.Err()
		case <-flight.done:
			return flight.snapshot, flight.err
		}
	}
	flight := &releaseFlight{done: make(chan struct{})}
	if s.flights == nil {
		s.flights = make(map[string]*releaseFlight)
	}
	s.flights[key] = flight
	s.mu.Unlock()

	snapshot, err := s.fetch(ctx, repo, token)
	s.mu.Lock()
	if err == nil {
		if s.cache == nil {
			s.cache = make(map[string]releaseSnapshot)
		}
		s.cache[key] = snapshot
	}
	flight.snapshot, flight.err = snapshot, err
	delete(s.flights, key)
	close(flight.done)
	s.mu.Unlock()
	return snapshot, err
}

func (s *releaseService) fetch(ctx context.Context, repo releaseRepository, token string) (releaseSnapshot, error) {
	ctx, cancel := context.WithTimeout(ctx, releaseRequestTimeout)
	defer cancel()
	client, err := s.clientFactory()
	if err != nil {
		return releaseSnapshot{}, apperror.Wrap("update.github_unavailable", "Unable to connect to GitHub Releases", err)
	}
	defer client.CloseIdleConnections()
	base := s.apiBase + "/repos/" + repo.owner + "/" + repo.name + "/releases"
	var latest githubRelease
	var releases []githubRelease
	var latestFound bool
	var latestErr, historyErr error
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		latestFound, latestErr = fetchGitHubReleaseJSON(ctx, client, base+"/latest", token, &latest, true)
	}()
	go func() {
		defer wg.Done()
		_, historyErr = fetchGitHubReleaseJSON(ctx, client, base+"?per_page=10", token, &releases, false)
	}()
	wg.Wait()
	if latestErr != nil {
		return releaseSnapshot{}, latestErr
	}
	if historyErr != nil {
		return releaseSnapshot{}, historyErr
	}
	if releases == nil || (latestFound && (latest.TagName == "" || latest.ID <= 0)) {
		return releaseSnapshot{}, apperror.New("update.github_unavailable", "GitHub Releases returned an invalid release record")
	}
	snapshot := releaseSnapshot{releases: make([]model.ReleaseVersion, 0, len(releases)), checkedAt: time.Now().UTC()}
	if latestFound && !latest.Draft && !latest.Prerelease {
		converted := exportReleaseVersion(repo, latest)
		snapshot.latest = &converted
	}
	for _, release := range releases {
		if !release.Draft && release.TagName != "" && release.ID > 0 {
			snapshot.releases = append(snapshot.releases, exportReleaseVersion(repo, release))
		}
	}
	sort.SliceStable(snapshot.releases, func(i, j int) bool {
		return snapshot.releases[i].PublishedAt.After(snapshot.releases[j].PublishedAt)
	})
	return snapshot, nil
}

func fetchGitHubReleaseJSON(ctx context.Context, client *http.Client, endpoint, token string, result any, missingOK bool) (bool, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return false, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("User-Agent", "Octopus/"+conf.Version)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	response, err := client.Do(req)
	if err != nil {
		return false, apperror.Wrap("update.github_unavailable", "Unable to connect to GitHub Releases", err)
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusNotFound && missingOK {
		return false, nil
	}
	if response.StatusCode == http.StatusTooManyRequests || (response.StatusCode == http.StatusForbidden && response.Header.Get("X-RateLimit-Remaining") == "0") {
		return false, apperror.New("update.github_rate_limited", "GitHub release checks are rate limited; try again later").WithStatus(http.StatusServiceUnavailable)
	}
	if response.StatusCode != http.StatusOK {
		return false, apperror.New("update.github_unavailable", "Unable to read GitHub Releases").WithStatus(http.StatusBadGateway)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, releaseResponseLimit+1))
	if err != nil || len(body) > releaseResponseLimit {
		return false, apperror.New("update.github_unavailable", "GitHub Releases returned an unreadable response")
	}
	if err := json.Unmarshal(body, result); err != nil {
		return false, apperror.Wrap("update.github_unavailable", "GitHub Releases returned an invalid response", err)
	}
	return true, nil
}

func exportReleaseVersion(repo releaseRepository, release githubRelease) model.ReleaseVersion {
	result := model.ReleaseVersion{
		ID: release.ID, Version: release.TagName, Name: release.Name,
		URL:   repo.url + "/releases/tag/" + url.PathEscape(release.TagName),
		Notes: release.Body, PublishedAt: release.PublishedAt, Prerelease: release.Prerelease,
		Assets: make([]model.ReleaseAsset, 0),
	}
	for _, asset := range release.Assets {
		name := strings.ToLower(asset.Name)
		if asset.State != "uploaded" || asset.Name == "" || strings.HasSuffix(name, ".sha256") || strings.HasSuffix(name, ".sha512") || strings.HasSuffix(name, ".blockmap") || strings.HasSuffix(name, ".sig") || strings.HasSuffix(name, ".yml") || strings.HasSuffix(name, ".json") {
			continue
		}
		parsed, err := url.Parse(asset.DownloadURL)
		if err != nil || parsed.Scheme != "https" || parsed.Host != "github.com" || parsed.User != nil || !strings.HasPrefix(strings.ToLower(parsed.Path), strings.ToLower("/"+repo.owner+"/"+repo.name+"/releases/download/")) {
			continue
		}
		platform := ""
		switch {
		case strings.HasSuffix(name, ".exe") || strings.Contains(name, "windows"):
			platform = "windows"
		case strings.HasSuffix(name, ".dmg") || strings.Contains(name, "-mac-") || strings.Contains(name, "darwin"):
			platform = "darwin"
		case strings.Contains(name, "linux") || strings.HasSuffix(name, ".appimage") || strings.HasSuffix(name, ".deb") || strings.HasSuffix(name, ".rpm"):
			platform = "linux"
		}
		result.Assets = append(result.Assets, model.ReleaseAsset{Name: asset.Name, Size: asset.Size, DownloadURL: asset.DownloadURL, Platform: platform})
	}
	// 同一版本同时发布原生安装器和 NSIS 更新包，手动下载只展示原生安装器。
	for _, asset := range result.Assets {
		name := strings.ReplaceAll(strings.ToLower(asset.Name), " ", ".")
		if strings.HasPrefix(name, "octopus.custom.setup.") && strings.HasSuffix(name, ".exe") {
			visible := result.Assets[:0]
			for _, item := range result.Assets {
				name := strings.ToLower(item.Name)
				if strings.HasPrefix(name, "octopus-") && strings.Contains(name, "-windows-") && strings.HasSuffix(name, "-setup.exe") {
					continue
				}
				visible = append(visible, item)
			}
			result.Assets = visible
			break
		}
	}
	return result
}

func compareReleaseVersion(current string, latest *model.ReleaseVersion) model.ReleaseStatus {
	if latest == nil {
		return model.ReleaseStatusNoRelease
	}
	current = strings.TrimSpace(current)
	if current == "dev" || strings.HasPrefix(current, "dev-") || releaseDevelopmentVersion.MatchString(current) {
		return model.ReleaseStatusDevelopment
	}
	current = "v" + strings.TrimPrefix(current, "v")
	tag := "v" + strings.TrimPrefix(strings.TrimSpace(latest.Version), "v")
	if !semver.IsValid(current) || !semver.IsValid(tag) {
		return model.ReleaseStatusUnknownVersion
	}
	switch semver.Compare(current, tag) {
	case -1:
		return model.ReleaseStatusUpdateAvailable
	case 1:
		return model.ReleaseStatusAhead
	default:
		return model.ReleaseStatusUpToDate
	}
}

// 版本查询支持应用设置中的代理，以及 HTTP_PROXY/HTTPS_PROXY 环境变量。
func releaseHTTPClient() (*http.Client, error) {
	base, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		return nil, fmt.Errorf("default transport is not an HTTP transport")
	}
	transport := base.Clone()
	proxyURL, _ := SettingGetString(model.SettingKeyProxyURL)
	if proxyURL != "" {
		parsed, err := url.Parse(proxyURL)
		if err != nil {
			return nil, fmt.Errorf("invalid release proxy URL")
		}
		switch parsed.Scheme {
		case "http", "https":
			transport.Proxy = http.ProxyURL(parsed)
		case "socks", "socks5":
			dialer, err := proxy.FromURL(parsed, proxy.Direct)
			if err != nil {
				return nil, err
			}
			contextDialer, ok := dialer.(proxy.ContextDialer)
			if !ok {
				return nil, fmt.Errorf("release proxy does not support request cancellation")
			}
			transport.Proxy = nil
			transport.DialContext = contextDialer.DialContext
		default:
			return nil, fmt.Errorf("unsupported release proxy scheme")
		}
	}
	return &http.Client{Transport: transport, Timeout: releaseRequestTimeout, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		// GitHub API 的凭据只用于查询，不跟随重定向发送到其他主机。
		if req.URL.Scheme != "https" || req.URL.Host != "api.github.com" || len(via) >= 5 {
			return fmt.Errorf("unexpected GitHub API redirect")
		}
		return nil
	}}, nil
}
