# Organizations API (user-service)

Base URL: `http://localhost` (the Caddy edge; the api-gateway is internal-only)

The gateway forwards `/api/orgs/*` to `user-service:8090`. All requests go through the gateway, which verifies the JWT and injects `X-User-ID`. Org-level RBAC is enforced per handler from the caller's membership role. Responses use the standard envelope `{success, message, data, error, meta}`.

**RBAC roles:** `owner` > `admin` > `editor` > `viewer`. Member-management endpoints require `admin`+. Non-members receive `404` on org-scoped reads (existence is not leaked); insufficient role receives `403`.

---

## GET /api/orgs
List organizations the caller belongs to, each annotated with the caller's `role`.

**200 OK**
```json
{
  "success": true,
  "message": "Organizations retrieved",
  "data": [
    { "id": "018f...", "name": "Acme", "slug": "acme", "planName": "free", "role": "owner" }
  ]
}
```

## POST /api/orgs
Create an organization; the caller becomes `owner`.

**Body:** `{ "name": "Acme" }` (`name` required)

**201 Created** — `data: {organization, role: "owner"}`. Errors: `400 INVALID_NAME`.

## GET /api/orgs/:id
Get an org the caller belongs to, with the caller's role. **200 OK** / **404 NOT_FOUND** (also returned to non-members). This is the endpoint document-service calls to resolve membership/role for org-scoped requests.

## GET /api/orgs/:id/members
List members of the org (caller must be a member). **200 OK** — `data: [membership]`.

## POST /api/orgs/:id/members
Add or update a member. Caller must be `admin`+.

**Body:**
```json
{ "userId": "550e...", "role": "editor" }
```
| Field | Required | Notes |
|-------|----------|-------|
| userId | Yes | Target user UUID |
| role | Yes | `admin` \| `editor` \| `viewer` |

- If the user is already a member → role is updated (**200 OK**). Otherwise created (**201 Created**).
- The owner's role cannot be changed here (**403 FORBIDDEN**).
- Errors: `403 FORBIDDEN` (caller < admin), `400 INVALID_USER`, `400 INVALID_ROLE`.

## PATCH /api/orgs/:id/members/:userId
Change a member's role. Caller must be `admin`+. Body: `{ "role": "viewer" }`. The owner's role cannot be changed (**403 FORBIDDEN**). Errors: `404 NOT_FOUND`, `400 INVALID_ROLE`.

## DELETE /api/orgs/:id/members/:userId
Remove a member. Caller must be `admin`+. The owner cannot be removed (**403 FORBIDDEN**). **200 OK** / **404 NOT_FOUND**.

---

## Error Codes
`INVALID_BODY`, `INVALID_ID`, `INVALID_NAME`, `INVALID_USER`, `INVALID_ROLE`, `NOT_FOUND`, `FORBIDDEN`, `LIST_FAILED`, `CREATE_FAILED`, `ADD_FAILED`.
