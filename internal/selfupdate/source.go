package selfupdate

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// Release is the release metadata the updater needs: the exact tag plus a
// download URL per asset name.
type Release struct {
	Tag    string
	Assets map[string]string // asset name → download URL
}

// Source is the release-source boundary: the updater sees release metadata
// and asset bytes through it, so tests can back it with a fake release host.
type Source interface {
	Latest(ctx context.Context) (Release, error)
	ReleaseByTag(ctx context.Context, tag string) (Release, error)
	Fetch(ctx context.Context, url string) ([]byte, error)
}

// DefaultRepo is the release repository of this product.
const DefaultRepo = "Ceinl/plumtree"

// DefaultAPIBase is the GitHub REST API base the updater talks to. Tests and
// air-gapped installs override it via PLUMTREE_UPDATE_API_BASE.
const DefaultAPIBase = "https://api.github.com"

// GitHubSource resolves releases from the GitHub releases API and downloads
// assets over HTTPS.
type GitHubSource struct {
	Repo    string
	APIBase string
	Client  *http.Client
}

// NewGitHubSource returns the production source for the given repository.
func NewGitHubSource(repo string) *GitHubSource {
	return &GitHubSource{Repo: repo, APIBase: DefaultAPIBase, Client: http.DefaultClient}
}

func (s GitHubSource) client() *http.Client {
	if s.Client != nil {
		return s.Client
	}
	return http.DefaultClient
}

// Latest resolves the latest stable release. GitHub's latest-release endpoint
// already excludes prereleases and drafts, matching the product channel.
func (s GitHubSource) Latest(ctx context.Context) (Release, error) {
	return s.byRef(ctx, "latest")
}

// ReleaseByTag resolves one pinned release by tag.
func (s GitHubSource) ReleaseByTag(ctx context.Context, tag string) (Release, error) {
	pinned := strings.TrimPrefix(tag, "tags/") // tolerate callers passing a git ref
	return s.byRef(ctx, "tags/"+pinned)
}

func (s GitHubSource) byRef(ctx context.Context, ref string) (Release, error) {
	url := fmt.Sprintf("%s/repos/%s/releases/%s", strings.TrimSuffix(s.APIBase, "/"), s.Repo, ref)
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return Release{}, err
	}
	response, err := s.client().Do(request)
	if err != nil {
		return Release{}, err
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return Release{}, err
	}
	if response.StatusCode != http.StatusOK {
		message := strings.TrimSpace(string(body))
		if len(message) > 200 {
			message = message[:200]
		}
		return Release{}, fmt.Errorf("release lookup returned HTTP %d: %s", response.StatusCode, message)
	}
	var payload struct {
		TagName string `json:"tag_name"`
		Assets  []struct {
			Name        string `json:"name"`
			DownloadURL string `json:"browser_download_url"`
		} `json:"assets"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return Release{}, fmt.Errorf("unreadable release metadata: %w", err)
	}
	release := Release{Tag: payload.TagName, Assets: map[string]string{}}
	for _, asset := range payload.Assets {
		release.Assets[asset.Name] = asset.DownloadURL
	}
	return release, nil
}

// Fetch downloads asset bytes.
func (s GitHubSource) Fetch(ctx context.Context, url string) ([]byte, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	response, err := s.client().Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("asset download returned HTTP %d", response.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, 256<<20))
	if err != nil {
		return nil, err
	}
	return data, nil
}
