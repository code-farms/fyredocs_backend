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

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"

	"fyredocs/shared/circuitbreaker"
	"fyredocs/shared/logger"
)

func baseURL() string {
	if v := strings.TrimSpace(os.Getenv("USER_SERVICE_URL")); v != "" {
		return strings.TrimRight(v, "/")
	}
	return "http://user-service:8090"
}

var httpClient = &http.Client{Timeout: 4 * time.Second}

// breaker trips after repeated user-service failures so document-service fails
// fast (and fails safe) instead of every request eating the 4s timeout while
// user-service is down. 404/403 are normal answers and do NOT count as failures.
var breaker = circuitbreaker.New[membershipResult]("user-service.membership")

type membershipResult struct {
	role   string
	member bool
}

type orgResponse struct {
	Data struct {
		Role string `json:"role"`
	} `json:"data"`
}

// Membership returns the caller's role in an org. member=false means the user
// is not a member (or the org does not exist). A non-nil error means the check
// could not be performed (user-service unreachable / breaker open) — callers
// should fail safe.
func Membership(ctx context.Context, orgID, userID string) (role string, member bool, err error) {
	res, err := breaker.Execute(func() (membershipResult, error) {
		url := fmt.Sprintf("%s/api/orgs/%s", baseURL(), orgID)
		req, rerr := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if rerr != nil {
			return membershipResult{}, rerr
		}
		// Internal call on the trusted mesh: assert the caller's identity the same
		// way the gateway would.
		// Propagate the caller's trace and request ID so the internal hop stays
		// correlated with the originating request (Grafana log↔trace jump).
		otel.GetTextMapPropagator().Inject(ctx, propagation.HeaderCarrier(req.Header))
		if rid := logger.RequestIDFromContext(ctx); rid != "" {
			req.Header.Set(logger.RequestIDHeader, rid)
		}
		req.Header.Set("X-User-ID", userID)

		resp, rerr := httpClient.Do(req)
		if rerr != nil {
			return membershipResult{}, rerr
		}
		defer resp.Body.Close()

		switch resp.StatusCode {
		case http.StatusOK:
			var body orgResponse
			if derr := json.NewDecoder(resp.Body).Decode(&body); derr != nil {
				return membershipResult{}, derr
			}
			return membershipResult{role: body.Data.Role, member: true}, nil
		case http.StatusNotFound, http.StatusForbidden:
			// A definitive "not a member" — not a dependency failure.
			return membershipResult{}, nil
		default:
			return membershipResult{}, fmt.Errorf("membership check failed: status %d", resp.StatusCode)
		}
	})
	return res.role, res.member, err
}
