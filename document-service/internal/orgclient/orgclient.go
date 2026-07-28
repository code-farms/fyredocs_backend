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

	"fyredocs/shared/authverify"
	"fyredocs/shared/circuitbreaker"
	"fyredocs/shared/logger"
)

// baseURL resolves user-service's internal address.
func baseURL() string {
	if v := strings.TrimSpace(os.Getenv("USER_SERVICE_URL")); v != "" {
		return strings.TrimRight(v, "/")
	}
	return "http://user-service:8090"
}

// httpClient is shared for connection reuse; its timeout bounds how long an
// authorization check can block a request.
var httpClient = &http.Client{Timeout: 4 * time.Second}

// breaker trips after repeated user-service failures so document-service fails
// fast (and fails safe) instead of every request eating the 4s timeout while
// user-service is down. 404/403 are normal answers and do NOT count as failures.
var breaker = circuitbreaker.New[membershipResult]("user-service.membership")

// membershipResult is the caller's role within an organization, as reported by
// user-service. This service asks rather than reading user-service's tables —
// cross-service database access is forbidden — which means an authorization check
// here is a network call that can fail.
type membershipResult struct {
	role   string
	member bool
}

// orgResponse mirrors user-service's organization payload, decoding only the
// fields this service needs.
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
		// The /internal/v1 mirror, not the public /api/orgs route: this hop holds
		// the end user's ID but no JWT to forward, and user-service verifies tokens
		// locally, so the public route would reject it with 401.
		// NOTE: contract with user-service/routes/routes.go — that group is guarded
		// by InternalTokenGin and reads identity via InternalIdentity.
		url := fmt.Sprintf("%s/internal/v1/orgs/%s", baseURL(), orgID)
		req, rerr := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if rerr != nil {
			return membershipResult{}, rerr
		}
		// Propagate the caller's trace and request ID so the internal hop stays
		// correlated with the originating request (Grafana log↔trace jump).
		otel.GetTextMapPropagator().Inject(ctx, propagation.HeaderCarrier(req.Header))
		if rid := logger.RequestIDFromContext(ctx); rid != "" {
			req.Header.Set(logger.RequestIDHeader, rid)
		}
		// The shared secret is what authorizes naming an arbitrary user here; the
		// header below is only trusted because of it. An unset INTERNAL_API_TOKEN
		// makes user-service fail closed (503), which the breaker treats as a
		// dependency failure — callers then fail safe rather than granting access.
		req.Header.Set("X-User-ID", userID)
		if tok := strings.TrimSpace(os.Getenv("INTERNAL_API_TOKEN")); tok != "" {
			req.Header.Set(authverify.InternalTokenHeader, tok)
		}

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
