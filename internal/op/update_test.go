package op

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bestruirui/octopus/internal/apperror"
	"github.com/bestruirui/octopus/internal/model"
)

func TestReleaseVersionComparison(t *testing.T) {
	cases := []struct {
		current, latest string
		want            model.ReleaseStatus
	}{
		{"v0.1.9", "v0.1.10", model.ReleaseStatusUpdateAvailable},
		{"0.1.6", "v0.1.6", model.ReleaseStatusUpToDate},
		{"v0.1.6+local.1", "v0.1.6", model.ReleaseStatusUpToDate},
		{"v0.1.6-rc.2", "v0.1.6", model.ReleaseStatusUpdateAvailable},
		{"v0.2.0-rc.1", "v0.1.6", model.ReleaseStatusAhead},
		{"dev", "v0.1.6", model.ReleaseStatusDevelopment},
		{"v0.1.6-3-gabcdef", "v0.1.6", model.ReleaseStatusDevelopment},
		{"unknown", "v0.1.6", model.ReleaseStatusUnknownVersion},
		{"broken", "v0.1.6", model.ReleaseStatusUnknownVersion},
		{"v0.1.6", "release-next", model.ReleaseStatusUnknownVersion},
	}
	for _, tc := range cases {
		t.Run(tc.current+"_"+tc.latest, func(t *testing.T) {
			got := compareReleaseVersion(tc.current, &model.ReleaseVersion{Version: tc.latest})
			if got != tc.want {
				t.Fatalf("status = %q, want %q", got, tc.want)
			}
		})
	}
	if got := compareReleaseVersion("v0.1.6", nil); got != model.ReleaseStatusNoRelease {
		t.Fatalf("no-release status = %q", got)
	}
}

func TestReleaseRepositoryValidation(t *testing.T) {
	for _, raw := range []string{"https://github.com/CE-ND/octopus", "https://github.com/CE-ND/octopus.git/"} {
		repo, err := parseReleaseRepository(raw)
		if err != nil || repo.url != "https://github.com/CE-ND/octopus" {
			t.Fatalf("parse %q: %+v, %v", raw, repo, err)
		}
	}
	for _, raw := range []string{
		"http://github.com/CE-ND/octopus", "https://example.com/CE-ND/octopus",
		"https://github.com/CE-ND", "https://github.com/CE-ND/octopus/releases",
		"https://token@github.com/CE-ND/octopus", "https://github.com/../octopus",
		"https://github.com/CE-ND/octopus?token=secret", "https://github.com/CE-ND/octopus#release",
	} {
		if _, err := parseReleaseRepository(raw); err == nil {
			t.Fatalf("accepted invalid repository %q", raw)
		}
	}
}

func TestReleaseFetchLatestHistoryAssetsAndCache(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer test-token") || r.Header.Get("X-GitHub-Api-Version") == "" || r.Header.Get("Accept") != "application/vnd.github+json" {
			t.Errorf("missing GitHub API request headers")
		}
		w.Header().Set("Content-Type", "application/json")
		if strings.HasSuffix(r.URL.Path, "/latest") {
			_, _ = w.Write([]byte(`{"id":16,"tag_name":"v0.1.6","published_at":"2026-10-26T08:42:14Z","assets":[
				{"name":"Octopus.Custom.Setup.0.1.6.exe","size":173360707,"state":"uploaded","browser_download_url":"https://github.com/CE-ND/octopus/releases/download/v0.1.6/setup.exe"},
				{"name":"setup.exe.sha256","state":"uploaded","browser_download_url":"https://github.com/CE-ND/octopus/releases/download/v0.1.6/setup.exe.sha256"},
				{"name":"Octopus-0.1.6-windows-x64-setup.exe","size":90000000,"state":"uploaded","browser_download_url":"https://github.com/CE-ND/octopus/releases/download/v0.1.6/Octopus-0.1.6-windows-x64-setup.exe"},
				{"name":"latest.yml","state":"uploaded","browser_download_url":"https://github.com/CE-ND/octopus/releases/download/v0.1.6/latest.yml"},
				{"name":"Octopus-0.1.6-windows-x64-setup.exe.blockmap","state":"uploaded","browser_download_url":"https://github.com/CE-ND/octopus/releases/download/v0.1.6/Octopus-0.1.6-windows-x64-setup.exe.blockmap"},
				{"name":"unfinished.dmg","state":"starter","browser_download_url":"https://github.com/CE-ND/octopus/releases/download/v0.1.6/unfinished.dmg"},
				{"name":"foreign.exe","state":"uploaded","browser_download_url":"https://example.com/foreign.exe"},
				{"name":"other-repo.exe","state":"uploaded","browser_download_url":"https://github.com/other/repo/releases/download/v1/setup.exe"}]}`))
			return
		}
		if r.URL.Query().Get("per_page") != "10" {
			t.Errorf("history must request ten releases")
		}
		_, _ = w.Write([]byte(`[
			{"id":15,"tag_name":"v0.1.5","published_at":"2026-09-16T16:52:33Z"},
			{"id":20,"tag_name":"v0.2.0-rc.1","prerelease":true,"published_at":"2026-11-01T00:00:00Z"},
			{"id":30,"tag_name":"v0.3.0","draft":true,"published_at":"2026-11-02T00:00:00Z"}]`))
	}))
	defer server.Close()
	service := &releaseService{apiBase: server.URL, clientFactory: func() (*http.Client, error) { return server.Client(), nil }}
	repo, _ := parseReleaseRepository("https://github.com/CE-ND/octopus")
	snapshot, err := service.get(context.Background(), repo, "test-token", false)
	if err != nil {
		t.Fatalf("fetch releases: %v", err)
	}
	// 正式版通过 /latest 独立查询，不会被历史列表中较新的预发布版本覆盖。
	if snapshot.latest == nil || snapshot.latest.Version != "v0.1.6" || len(snapshot.releases) != 2 || snapshot.releases[0].Version != "v0.2.0-rc.1" {
		t.Fatalf("unexpected release snapshot: %+v", snapshot)
	}
	if len(snapshot.latest.Assets) != 1 || snapshot.latest.Assets[0].Platform != "windows" {
		t.Fatalf("unexpected downloadable assets: %+v", snapshot.latest.Assets)
	}
	if snapshot.latest.URL != "https://github.com/CE-ND/octopus/releases/tag/v0.1.6" || snapshot.checkedAt.IsZero() {
		t.Fatalf("missing release URL or check time")
	}
	for _, refresh := range []bool{false, true} {
		if _, err := service.get(context.Background(), repo, "test-token", refresh); err != nil {
			t.Fatal(err)
		}
	}
	if requests.Load() != 2 {
		t.Fatalf("fresh cache made %d API requests, want 2", requests.Load())
	}
	service.mu.Lock()
	for key, cached := range service.cache {
		cached.checkedAt = time.Now().Add(-releaseRefreshMinimum - time.Second)
		service.cache[key] = cached
	}
	service.mu.Unlock()
	if _, err := service.get(context.Background(), repo, "test-token", true); err != nil {
		t.Fatal(err)
	}
	if requests.Load() != 4 {
		t.Fatalf("manual refresh made %d API requests, want 4", requests.Load())
	}
	if _, err := service.get(context.Background(), repo, "test-token-new", false); err != nil {
		t.Fatal(err)
	}
	if requests.Load() != 6 {
		t.Fatalf("changed credentials reused the old cache")
	}
}

func TestReleaseFetchNoStableReleaseAndUpstreamFailures(t *testing.T) {
	cases := []struct {
		name      string
		status    int
		body      string
		remaining string
		wantCode  string
	}{
		{"prereleases_only", http.StatusOK, `[{"id":1,"tag_name":"v1.0.0-rc.1","prerelease":true}]`, "", ""},
		{"repository_missing", http.StatusNotFound, `{}`, "", "update.github_unavailable"},
		{"rate_limited", http.StatusForbidden, `{}`, "0", "update.github_rate_limited"},
		{"invalid_json", http.StatusOK, `<html>unavailable</html>`, "", "update.github_unavailable"},
		{"null_history", http.StatusOK, `null`, "", "update.github_unavailable"},
		{"too_large", http.StatusOK, strings.Repeat(" ", releaseResponseLimit+1), "", "update.github_unavailable"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if strings.HasSuffix(r.URL.Path, "/latest") {
					w.WriteHeader(http.StatusNotFound)
					return
				}
				if tc.remaining != "" {
					w.Header().Set("X-RateLimit-Remaining", tc.remaining)
				}
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer server.Close()
			service := &releaseService{apiBase: server.URL, clientFactory: func() (*http.Client, error) { return server.Client(), nil }}
			repo, _ := parseReleaseRepository("https://github.com/CE-ND/octopus")
			snapshot, err := service.get(context.Background(), repo, "", false)
			if tc.wantCode != "" {
				if apperror.Code(err) != tc.wantCode || len(service.cache) != 0 {
					t.Fatalf("error = %v, cache size = %d", err, len(service.cache))
				}
				return
			}
			if err != nil || snapshot.latest != nil || len(snapshot.releases) != 1 {
				t.Fatalf("prerelease-only snapshot: %+v, %v", snapshot, err)
			}
		})
	}
}

func TestReleaseConcurrentChecksAndCanceledWaiter(t *testing.T) {
	started := make(chan struct{}, 2)
	gate := make(chan struct{})
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		started <- struct{}{}
		<-gate
		if strings.HasSuffix(r.URL.Path, "/latest") {
			_ = json.NewEncoder(w).Encode(githubRelease{ID: 1, TagName: "v1.0.0"})
		} else {
			_ = json.NewEncoder(w).Encode([]githubRelease{})
		}
	}))
	defer server.Close()
	var open sync.Once
	releaseGate := func() { open.Do(func() { close(gate) }) }
	defer releaseGate()
	service := &releaseService{apiBase: server.URL, clientFactory: func() (*http.Client, error) { return server.Client(), nil }}
	repo, _ := parseReleaseRepository("https://github.com/CE-ND/octopus")
	first := make(chan error, 1)
	go func() {
		_, err := service.get(context.Background(), repo, "", false)
		first <- err
	}()
	for range 2 {
		select {
		case <-started:
		case <-time.After(3 * time.Second):
			t.Fatal("release requests did not start")
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := service.get(ctx, repo, "", false); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled waiter error = %v", err)
	}
	second := make(chan error, 1)
	go func() {
		_, err := service.get(context.Background(), repo, "", true)
		second <- err
	}()
	releaseGate()
	for _, result := range []chan error{first, second} {
		if err := <-result; err != nil {
			t.Fatal(err)
		}
	}
	if requests.Load() != 2 {
		t.Fatalf("concurrent checks made %d requests, want 2", requests.Load())
	}
}
