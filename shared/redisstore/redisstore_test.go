package redisstore

import (
	"context"
	"testing"

	"github.com/alicebob/miniredis/v2"
)

// withEnvRedis points the package's env-driven Connect at an in-process
// miniredis and restores the global Client afterwards. Connect os.Exit(1)s on
// an unreachable server, so every test here must hand it a live one — the
// failure path is untestable in-process by design (it kills the test binary).
func withEnvRedis(t *testing.T) *miniredis.Miniredis {
	t.Helper()
	mr := miniredis.RunT(t)
	t.Setenv("REDIS_ADDR", mr.Addr())
	t.Setenv("REDIS_PASSWORD", "")
	t.Setenv("REDIS_DB", "")

	prev := Client
	t.Cleanup(func() {
		if Client != nil && Client != prev {
			_ = Client.Close()
		}
		Client = prev
	})
	return mr
}

func TestConnectEstablishesGlobalClient(t *testing.T) {
	withEnvRedis(t)

	Connect()
	if Client == nil {
		t.Fatal("Connect left the global Client nil")
	}
	if err := Client.Set(context.Background(), "k", "v", 0).Err(); err != nil {
		t.Fatalf("connected client cannot write: %v", err)
	}
	got, err := Client.Get(context.Background(), "k").Result()
	if err != nil || got != "v" {
		t.Fatalf("round-trip = (%q, %v), want (\"v\", nil)", got, err)
	}
}

func TestConnectUsesPasswordFromEnv(t *testing.T) {
	mr := withEnvRedis(t)
	mr.RequireAuth("hunter2")
	t.Setenv("REDIS_PASSWORD", "hunter2")

	Connect()
	if err := Client.Ping(context.Background()).Err(); err != nil {
		t.Fatalf("authenticated ping failed: %v", err)
	}
}

func TestConnectSelectsDBIndexFromEnv(t *testing.T) {
	withEnvRedis(t)
	t.Setenv("REDIS_DB", "3")

	Connect()
	if got := Client.Options().DB; got != 3 {
		t.Fatalf("client DB index = %d, want 3", got)
	}
	// The index must actually isolate keys, not just sit in the options
	// struct: a write in DB 3 must be invisible from DB 0.
	if err := Client.Set(context.Background(), "scoped", "1", 0).Err(); err != nil {
		t.Fatalf("write in DB 3: %v", err)
	}
	t.Setenv("REDIS_DB", "")
	Connect()
	if err := Client.Get(context.Background(), "scoped").Err(); err == nil {
		t.Fatal("key written in DB 3 is visible from DB 0")
	}
}

func TestConnectTrimsWhitespaceAddr(t *testing.T) {
	// A trailing space in .env (an easy hand-edit typo) must not make Connect
	// dial a malformed address and exit the process. GetEnv owns the trim;
	// quote-stripping is LoadConfig's job and is deliberately NOT covered
	// here — Connect requires LoadConfig to have normalized the env first.
	mr := withEnvRedis(t)
	t.Setenv("REDIS_ADDR", " "+mr.Addr()+" ")

	Connect()
	if err := Client.Ping(context.Background()).Err(); err != nil {
		t.Fatalf("whitespace-padded REDIS_ADDR was not trimmed: %v", err)
	}
}

func TestCloseIsNilSafe(t *testing.T) {
	prev := Client
	t.Cleanup(func() { Client = prev })

	Client = nil
	Close() // must not panic
}

func TestCloseReleasesConnectedClient(t *testing.T) {
	withEnvRedis(t)

	Connect()
	Close()
	if err := Client.Ping(context.Background()).Err(); err == nil {
		t.Fatal("client still serves commands after Close")
	}
}
