// Package orgclient performs service-to-service membership checks against
// user-service. document-service uses it to enforce organization-level RBAC:
// the caller's role in an org governs read vs write access to its documents.
package orgclient

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"
)

func baseURL() string {
	if v := strings.TrimSpace(os.Getenv("USER_SERVICE_URL")); v != "" {
		return strings.TrimRight(v, "/")
	}
	return "http://user-service:8090"
}

var httpClient = &http.Client{Timeout: 4 * time.Second}

type orgResponse struct {
	Data struct {
		Role string `json:"role"`
	} `json:"data"`
}

// Membership returns the caller's role in an org. member=false means the user
// is not a member (or the org does not exist). A non-nil error means the check
// could not be performed (user-service unreachable) — callers should fail safe.
func Membership(ctx context.Context, orgID, userID string) (role string, member bool, err error) {
	url := fmt.Sprintf("%s/api/orgs/%s", baseURL(), orgID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", false, err
	}
	// Internal call on the trusted mesh: assert the caller's identity the same
	// way the gateway would.
	req.Header.Set("X-User-ID", userID)

	resp, err := httpClient.Do(req)
	if err != nil {
		return "", false, err
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK:
		var body orgResponse
		if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
			return "", false, err
		}
		return body.Data.Role, true, nil
	case http.StatusNotFound, http.StatusForbidden:
		return "", false, nil
	default:
		return "", false, fmt.Errorf("membership check failed: status %d", resp.StatusCode)
	}
}
