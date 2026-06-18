package config

import (
	"net/http"
	"time"
)

// ApplyServerTimeouts sets slowloris- and slow-client-resistant timeouts on an
// http.Server. It is a generic infrastructure helper (no business logic): every
// service constructs its own *http.Server and calls this to get consistent,
// env-overridable timeouts.
//
// ReadHeaderTimeout, ReadTimeout and IdleTimeout are always applied — they bound
// how long a client may take to send a request and how long idle keep-alive
// connections live, and never affect the duration of a response the server is
// streaming back. They are safe everywhere because request bodies are small JSON
// (large file bytes go directly to object storage via presigned URLs, never
// through these servers).
//
// WriteTimeout is the dangerous one: it caps total time to write the response,
// so it would abort long downloads and Server-Sent-Events streams. Pass
// streaming=true on services that stream responses (the gateway proxy, and any
// service exposing SSE or file exports) to leave WriteTimeout unset (0). Pass
// streaming=false on plain JSON services to bound it.
//
// Defaults (overridable via env):
//
//	HTTP_READ_HEADER_TIMEOUT  10s
//	HTTP_READ_TIMEOUT         30s
//	HTTP_IDLE_TIMEOUT         120s
//	HTTP_WRITE_TIMEOUT        60s  (only when streaming=false)
func ApplyServerTimeouts(srv *http.Server, streaming bool) {
	if srv == nil {
		return
	}
	srv.ReadHeaderTimeout = GetEnvDuration("HTTP_READ_HEADER_TIMEOUT", 10*time.Second)
	srv.ReadTimeout = GetEnvDuration("HTTP_READ_TIMEOUT", 30*time.Second)
	srv.IdleTimeout = GetEnvDuration("HTTP_IDLE_TIMEOUT", 120*time.Second)
	if streaming {
		// Leave WriteTimeout at 0 (unlimited) so long downloads / SSE streams
		// are not severed mid-response.
		srv.WriteTimeout = 0
		return
	}
	srv.WriteTimeout = GetEnvDuration("HTTP_WRITE_TIMEOUT", 60*time.Second)
}
