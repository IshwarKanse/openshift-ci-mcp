package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"

	"github.com/openshift-eng/openshift-ci-mcp/pkg/client"
)

type releasesResponse struct {
	Releases []string                   `json:"releases"`
	Dates    map[string]releaseDateInfo `json:"dates"`
}

type releaseDateInfo struct {
	GA *string `json:"ga,omitempty"`
}

func ResolveRelease(ctx context.Context, sippy client.Sippy, release string) (string, error) {
	if release != "" {
		return release, nil
	}

	data, err := sippy.Get(ctx, "/api/releases", nil)
	if err != nil {
		return "", fmt.Errorf("fetching releases: %w", err)
	}

	var resp releasesResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		return "", fmt.Errorf("parsing releases: %w", err)
	}

	if len(resp.Releases) == 0 {
		return "", fmt.Errorf("no releases available")
	}

	// Sippy also lists non-OCP releases (gcp-hcp-main, quay-*, *-okd, ...)
	// that have no GA date, so prefer OCP releases before falling back.
	var ocp []string
	for _, r := range resp.Releases {
		if ocpRelease.MatchString(r) {
			ocp = append(ocp, r)
		}
	}
	if len(ocp) > 0 {
		return firstWithoutGA(ocp, resp.Dates), nil
	}
	return firstWithoutGA(resp.Releases, resp.Dates), nil
}

var ocpRelease = regexp.MustCompile(`^\d+\.\d+$`)

// firstWithoutGA returns the first release that has no GA date (in
// development), or the first release if all of them are GA.
func firstWithoutGA(releases []string, dates map[string]releaseDateInfo) string {
	for _, r := range releases {
		d, ok := dates[r]
		if !ok || d.GA == nil {
			return r
		}
	}
	return releases[0]
}
