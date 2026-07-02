# Notifications API (notification-service)

Base URL (via gateway): `http://localhost:8080`

The gateway forwards `/api/notifications/*` to `notification-service:8091`, verifying the JWT and injecting `X-User-ID`. Notifications are per-user. The `/internal/notifications` endpoint is **mesh-only** — reachable service-to-service, never through the gateway. Responses use the standard envelope `{success, message, data, error, meta}`.

Notifications are created two ways: automatically by the NATS subscriber on `JobCompleted`/`JobFailed`, and explicitly by other services via `POST /internal/notifications` (e.g. document-service `export.ready`).

---

## GET /api/notifications
Recent notifications (max 50, newest first) plus the unread count.

**200 OK**
```json
{
  "success": true,
  "message": "Notifications retrieved",
  "data": {
    "notifications": [
      {
        "id": "018f...",
        "userId": "550e...",
        "type": "job.completed",
        "title": "Processing complete",
        "body": "Your pdf-to-word job finished.",
        "link": "/jobs/018e...",
        "readAt": null,
        "createdAt": "2026-07-02T10:00:00Z"
      }
    ],
    "unreadCount": 1
  }
}
```

## GET /api/notifications/stream
Server-Sent Events stream for the live bell. Subscribes to core-NATS subject `notify.<userId>` and forwards each new notification.

**200 OK** (`text/event-stream`)
```
event: connected
data: {}

event: notification
data: {"id":"018f...","type":"job.completed","title":"Processing complete", ...}
```
Errors: `500 NATS_UNAVAILABLE` (NATS down), `500 STREAM_UNSUPPORTED` (client can't stream).

## POST /api/notifications/read-all
Mark all of the caller's notifications read. **200 OK**.

## POST /api/notifications/:id/read
Mark one notification read. **200 OK** `data: {id}` / **400 INVALID_ID**.

---

## POST /internal/notifications  (mesh-only)
Other services raise a notification for a user. Not gateway-proxied.

**Body:**
```json
{
  "userId": "550e...",
  "type": "export.ready",
  "title": "Your export is ready",
  "body": "documents.csv is ready to download.",
  "link": "/exports/018f...",
  "sourceId": "018f..."
}
```
| Field | Required | Notes |
|-------|----------|-------|
| userId | Yes | Recipient UUID |
| title | Yes | Notification title |
| type, body, link | No | Metadata |
| sourceId | No | Idempotency key — a duplicate returns **200 "Notification already exists"** without inserting |

**201 Created** on insert (also publishes `notify.<userId>` for live push). Errors: `400 INVALID_USER`, `400 INVALID_TITLE`.

---

## Error Codes
`INVALID_BODY`, `INVALID_ID`, `INVALID_USER`, `INVALID_TITLE`, `CREATE_FAILED`, `UPDATE_FAILED`, `LIST_FAILED`, `NATS_UNAVAILABLE`, `STREAM_UNSUPPORTED`, `SUBSCRIBE_FAILED`.
