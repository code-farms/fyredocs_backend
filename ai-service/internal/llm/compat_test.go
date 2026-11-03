package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"fyredocs/shared/response"
)

// fakeProvider emulates the OpenAI-compatible streaming wire format, so the
// client is tested without any real vendor. It records the last request body.
type fakeProvider struct {
	status   int
	errBody  string
	deltas   []string
	usage    string
	calls    atomic.Int32
	lastBody atomic.Value
	lastAuth atomic.Value
	lastPath atomic.Value
}

func (f *fakeProvider) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.calls.Add(1)
	body, _ := io.ReadAll(r.Body)
	f.lastBody.Store(string(body))
	f.lastAuth.Store(r.Header.Get("Authorization"))
	f.lastPath.Store(r.URL.Path)
	if f.status != 0 {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(f.status)
		_, _ = io.WriteString(w, f.errBody)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	for _, d := range f.deltas {
		b, _ := json.Marshal(d)
		fmt.Fprintf(w, "data: {\"id\":\"x\",\"object\":\"chat.completion.chunk\",\"created\":1,\"model\":\"m\",\"choices\":[{\"index\":0,\"delta\":{\"content\":%s}}]}\n\n", b)
	}
	if f.usage != "" {
		fmt.Fprintf(w, "data: {\"id\":\"x\",\"object\":\"chat.completion.chunk\",\"created\":1,\"model\":\"m\",\"choices\":[],\"usage\":%s}\n\n", f.usage)
	}
	_, _ = io.WriteString(w, "data: [DONE]\n\n")
}

func (f *fakeProvider) body(t *testing.T) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(f.lastBody.Load().(string)), &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func newTestClient(t *testing.T, f *fakeProvider, mutate func(*Config)) Client {
	t.Helper()
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	cfg := Config{BaseURL: srv.URL + "/v1/", APIKey: "k", ModelFast: "fast-m", ModelSmart: "smart-m", Timeout: 5 * time.Second, MaxTokensParam: "max_tokens"}
	if mutate != nil {
		mutate(&cfg)
	}
	return New(cfg)
}

var req = Request{Tier: TierFast, MaxTokens: 64, Messages: []Message{
	{Role: "system", Content: "sys"}, {Role: "user", Content: "q1"}, {Role: "assistant", Content: "a1"}, {Role: "user", Content: "task"},
}}

func collect(c Client, r Request) (string, Usage, error) {
	var b strings.Builder
	u, err := c.Stream(context.Background(), r, func(d string) error { b.WriteString(d); return nil })
	return b.String(), u, err
}

func TestStreamsDeltasAndUsage(t *testing.T) {
	f := &fakeProvider{deltas: []string{"Hel", "lo"}, usage: `{"prompt_tokens":120,"completion_tokens":2,"total_tokens":122,"prompt_tokens_details":{"cached_tokens":100}}`}
	text, u, err := collect(newTestClient(t, f, nil), req)
	if err != nil || text != "Hello" {
		t.Fatalf("text=%q err=%v", text, err)
	}
	if u != (Usage{InputTokens: 120, OutputTokens: 2, CachedTokens: 100}) {
		t.Fatalf("usage %+v", u)
	}
	b := f.body(t)
	if b["model"] != "fast-m" || b["stream"] != true || b["max_tokens"] != float64(64) {
		t.Fatalf("body %+v", b)
	}
	if so, _ := b["stream_options"].(map[string]any); so["include_usage"] != true {
		t.Fatalf("usage not requested: %+v", b["stream_options"])
	}
	msgs := b["messages"].([]any)
	roles := []string{}
	for _, m := range msgs {
		roles = append(roles, m.(map[string]any)["role"].(string))
	}
	if strings.Join(roles, ",") != "system,user,assistant,user" {
		t.Fatalf("roles %v", roles)
	}
	if f.lastAuth.Load() != "Bearer k" || f.lastPath.Load() != "/v1/chat/completions" {
		t.Fatalf("auth=%v path=%v", f.lastAuth.Load(), f.lastPath.Load())
	}
	for _, banned := range []string{"tools", "response_format", "functions"} {
		if _, ok := b[banned]; ok {
			t.Errorf("non-portable field %q sent", banned)
		}
	}
}

func TestMissingUsageIsEstimated(t *testing.T) {
	_, u, err := collect(newTestClient(t, &fakeProvider{deltas: []string{"abcdefgh"}}, nil), req)
	if err != nil || u.OutputTokens != 2 || u.InputTokens != EstimateTokens(req.Messages) {
		t.Fatalf("usage %+v err %v", u, err)
	}
}

// Swapping provider and model is an env change only: the same code sends the
// configured model to the configured endpoint, nothing else differs.
func TestProviderSwapIsConfigurationOnly(t *testing.T) {
	for _, tc := range []struct{ fast, smart, param string }{
		{"gemini-2.5-flash", "gemini-2.5-pro", "max_tokens"},
		{"gpt-5-mini", "gpt-5", "max_completion_tokens"},
		{"llama3.1", "", "max_tokens"},
	} {
		f := &fakeProvider{deltas: []string{"ok"}}
		c := newTestClient(t, f, func(cfg *Config) { cfg.ModelFast, cfg.ModelSmart, cfg.MaxTokensParam = tc.fast, tc.smart, tc.param })
		smart := req
		smart.Tier = TierSmart
		if _, _, err := collect(c, smart); err != nil {
			t.Fatal(err)
		}
		b := f.body(t)
		want := tc.smart
		if want == "" {
			want = tc.fast
		}
		if b["model"] != want {
			t.Errorf("%s: model %v, want %s (smart falls back to fast)", tc.fast, b["model"], want)
		}
		if _, ok := b[tc.param]; !ok {
			t.Errorf("%s: %s not sent: %v", tc.fast, tc.param, b)
		}
	}
}

func TestErrorClassification(t *testing.T) {
	cases := []struct {
		name   string
		status int
		body   string
		code   string
	}{
		{"context length", 400, `{"error":{"message":"This model's maximum context length is 8192 tokens","type":"invalid_request_error"}}`, response.CodeAIRequestTooLarge},
		{"gemini array shape", 400, `[{"error":{"code":400,"message":"The input token count exceeds the maximum number of tokens allowed","status":"INVALID_ARGUMENT"}}]`, response.CodeAIRequestTooLarge},
		{"payload too large", 413, `{}`, response.CodeAIRequestTooLarge},
		{"other 400", 400, `{"error":{"message":"invalid model"}}`, response.CodeAIUnavailable},
		{"bad key", 401, `{"error":{"message":"invalid api key"}}`, response.CodeAIUnavailable},
		{"rate limited", 429, `{"error":{"message":"slow down"}}`, response.CodeAIUnavailable},
		{"server error", 500, `{}`, response.CodeAIUnavailable},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := collect(newTestClient(t, &fakeProvider{status: tc.status, errBody: tc.body}, nil), req)
			if e, ok := AsError(err); !ok || e.Code != tc.code {
				t.Fatalf("got %v, want %s", err, tc.code)
			}
		})
	}
}

func TestBreakerTripsOnProviderFailuresOnly(t *testing.T) {
	tooLarge := &fakeProvider{status: 413, errBody: `{}`}
	c := newTestClient(t, tooLarge, nil)
	for i := 0; i < 8; i++ {
		_, _, _ = collect(c, req)
	}
	if tooLarge.calls.Load() != 8 {
		t.Fatalf("input-size errors must not trip the breaker (calls=%d)", tooLarge.calls.Load())
	}

	down := &fakeProvider{status: 500, errBody: `{}`}
	c = newTestClient(t, down, nil)
	for i := 0; i < 5; i++ {
		_, _, _ = collect(c, req)
	}
	before := down.calls.Load()
	_, _, err := collect(c, req)
	if down.calls.Load() != before {
		t.Fatal("open breaker still called the provider")
	}
	if e, ok := AsError(err); !ok || e.Code != response.CodeAIUnavailable {
		t.Fatalf("open breaker error %v", err)
	}
}

func TestOnDeltaErrorStopsStream(t *testing.T) {
	c := newTestClient(t, &fakeProvider{deltas: []string{"a", "b", "c"}}, nil)
	n := 0
	_, err := c.Stream(context.Background(), req, func(string) error {
		n++
		return io.ErrClosedPipe
	})
	if err != io.ErrClosedPipe || n != 1 {
		t.Fatalf("err=%v n=%d", err, n)
	}
}

func TestNewWithoutEndpointIsUnconfigured(t *testing.T) {
	if _, ok := New(Config{ModelFast: "m"}).(Unconfigured); !ok {
		t.Fatal("missing base URL must yield Unconfigured")
	}
	if _, ok := New(Config{BaseURL: "http://x"}).(Unconfigured); !ok {
		t.Fatal("missing model must yield Unconfigured")
	}
}

func TestConfigFromEnv(t *testing.T) {
	t.Setenv("AI_BASE_URL", " http://p/v1 ")
	t.Setenv("AI_MODEL_FAST", "f")
	t.Setenv("AI_REQUEST_TIMEOUT", "nonsense")
	cfg := ConfigFromEnv()
	if cfg.BaseURL != "http://p/v1" || cfg.ModelFast != "f" || cfg.Timeout != 90*time.Second || cfg.MaxTokensParam != "max_tokens" || cfg.MaxRetries != 1 {
		t.Fatalf("%+v", cfg)
	}
}
