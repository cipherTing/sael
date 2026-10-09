// Package version reports gateway build metadata and checks its own release namespace.
package version

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"golang.org/x/mod/semver"
)

// These values are set by the image build, never by deployment environment variables.
var (
	Version  = "devel"
	Revision = ""
)

const releasePrefix = "gateway/v"

// Result keeps unknown update status distinct from a successful no-update check.
type Result struct {
	CurrentVersion  string     `json:"current_version"`
	Revision        string     `json:"revision,omitempty"`
	LatestVersion   string     `json:"latest_version,omitempty"`
	ReleaseURL      string     `json:"release_url,omitempty"`
	UpdateAvailable *bool      `json:"update_available,omitempty"`
	CheckedAt       *time.Time `json:"checked_at,omitempty"`
	CheckError      string     `json:"check_error,omitempty"`
}

// Checker caches external requests independently of gateway traffic and storage.
type Checker struct {
	mu          sync.Mutex
	current     string
	revision    string
	client      *http.Client
	releasesURL string
	checkedAt   time.Time
	cached      Result
}

// New creates a checker with bounded external requests and no GitHub credentials.
func New(current, revision string) *Checker {
	return &Checker{current: current, revision: revision, client: &http.Client{Timeout: 8 * time.Second}, releasesURL: "https://api.github.com/repos/cipherTing/sael/releases"}
}

// Check refreshes at most every six hours, or once per minute for manual requests.
// Failed checks have a shorter cache and never report an up-to-date verdict.
func (c *Checker) Check(ctx context.Context, force bool) Result {
	c.mu.Lock()
	defer c.mu.Unlock()
	result := Result{CurrentVersion: c.current, Revision: c.revision}
	current := "v" + c.current
	if !semver.IsValid(current) {
		return result
	}
	ttl := 6 * time.Hour
	if c.cached.CheckError != "" {
		ttl = 10 * time.Minute
	}
	if force {
		ttl = time.Minute
	}
	if !c.checkedAt.IsZero() && time.Since(c.checkedAt) < ttl {
		return c.cached
	}
	work, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	latest, err := c.latest(work, semver.Prerelease(current) != "")
	now := time.Now().UTC()
	result.CheckedAt = &now
	if err != nil {
		result.CheckError = "检查更新失败，请稍后重试"
	} else {
		result.LatestVersion = strings.TrimPrefix(latest, "v")
		result.ReleaseURL = "https://github.com/cipherTing/sael/releases/tag/" + releasePrefix + result.LatestVersion
		available := semver.Compare(latest, current) > 0
		result.UpdateAvailable = &available
	}
	c.checkedAt, c.cached = now, result
	return result
}

func (c *Checker) latest(ctx context.Context, preview bool) (string, error) {
	var latest string
	for page := 1; ; page++ {
		u, err := url.Parse(c.releasesURL)
		if err != nil {
			return "", err
		}
		q := u.Query()
		q.Set("per_page", "100")
		q.Set("page", fmt.Sprint(page))
		u.RawQuery = q.Encode()
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), http.NoBody)
		if err != nil {
			return "", err
		}
		req.Header.Set("Accept", "application/vnd.github+json")
		req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
		req.Header.Set("User-Agent", "sael-gateway/"+c.current)
		response, err := c.client.Do(req)
		if err != nil {
			return "", err
		}
		var releases []struct {
			Tag        string `json:"tag_name"`
			Draft      bool   `json:"draft"`
			Prerelease bool   `json:"prerelease"`
		}
		if response.StatusCode == http.StatusOK {
			err = json.NewDecoder(io.LimitReader(response.Body, 4<<20)).Decode(&releases)
		} else {
			err = fmt.Errorf("GitHub returned HTTP %d", response.StatusCode)
		}
		_ = response.Body.Close()
		if err != nil {
			return "", err
		}
		for _, release := range releases {
			if release.Draft || !strings.HasPrefix(release.Tag, releasePrefix) {
				continue
			}
			v := strings.TrimPrefix(release.Tag, "gateway/")
			if !semver.IsValid(v) || (!preview && (release.Prerelease || semver.Prerelease(v) != "")) {
				continue
			}
			if latest == "" || semver.Compare(v, latest) > 0 {
				latest = v
			}
		}
		if len(releases) < 100 {
			break
		}
	}
	if latest == "" {
		return "", fmt.Errorf("no published gateway releases")
	}
	return latest, nil
}
