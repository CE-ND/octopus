package model

import "time"

// VersionInfo 描述当前运行的程序及其发布仓库，不依赖外部网络。
type VersionInfo struct {
	Version      string `json:"version"`
	Commit       string `json:"commit"`
	BuildTime    string `json:"build_time"`
	Repository   string `json:"repository"`
	ReleasesURL  string `json:"releases_url"`
	Platform     string `json:"platform"`
	Architecture string `json:"architecture"`
	Desktop      bool   `json:"desktop"`
}

// ReleaseAsset 仅包含发布页中可下载的程序文件，不包含校验清单。
type ReleaseAsset struct {
	Name        string `json:"name"`
	Size        int64  `json:"size"`
	DownloadURL string `json:"download_url"`
	Platform    string `json:"platform"`
}

type ReleaseVersion struct {
	ID          int64          `json:"id"`
	Version     string         `json:"version"`
	Name        string         `json:"name"`
	URL         string         `json:"url"`
	Notes       string         `json:"notes"`
	PublishedAt time.Time      `json:"published_at"`
	Prerelease  bool           `json:"prerelease"`
	Assets      []ReleaseAsset `json:"assets"`
}

type ReleaseStatus string

const (
	ReleaseStatusUpdateAvailable ReleaseStatus = "update_available"
	ReleaseStatusUpToDate        ReleaseStatus = "up_to_date"
	ReleaseStatusAhead           ReleaseStatus = "ahead"
	ReleaseStatusDevelopment     ReleaseStatus = "development"
	ReleaseStatusUnknownVersion  ReleaseStatus = "unknown_version"
	ReleaseStatusNoRelease       ReleaseStatus = "no_release"
)

type ReleaseCheck struct {
	CurrentVersion string           `json:"current_version"`
	Status         ReleaseStatus    `json:"status"`
	CheckedAt      time.Time        `json:"checked_at"`
	Latest         *ReleaseVersion  `json:"latest"`
	Releases       []ReleaseVersion `json:"releases"`
}
