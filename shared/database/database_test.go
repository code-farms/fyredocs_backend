package database

import (
	"testing"
	"time"
)

func TestDefaultPoolConfig(t *testing.T) {
	cfg := DefaultPoolConfig()
	if cfg.MaxOpenConns != 25 || cfg.MaxIdleConns != 10 {
		t.Errorf("DefaultPoolConfig conns = %d/%d, want 25/10", cfg.MaxOpenConns, cfg.MaxIdleConns)
	}
	if cfg.ConnMaxLifetime != 5*time.Minute || cfg.ConnMaxIdleTime != 2*time.Minute {
		t.Errorf("DefaultPoolConfig lifetimes = %v/%v, want 5m/2m", cfg.ConnMaxLifetime, cfg.ConnMaxIdleTime)
	}
}

func TestWithOverrides(t *testing.T) {
	base := PoolConfig{
		MaxOpenConns:    15,
		MaxIdleConns:    5,
		ConnMaxLifetime: 5 * time.Minute,
		ConnMaxIdleTime: 2 * time.Minute,
	}

	t.Run("zero override keeps base", func(t *testing.T) {
		got := base.WithOverrides(PoolConfig{})
		if got != base {
			t.Errorf("WithOverrides(zero) = %+v, want base %+v", got, base)
		}
	})

	t.Run("partial override touches only positive fields", func(t *testing.T) {
		got := base.WithOverrides(PoolConfig{MaxOpenConns: 50, MaxIdleConns: 25})
		if got.MaxOpenConns != 50 || got.MaxIdleConns != 25 {
			t.Errorf("conns = %d/%d, want 50/25", got.MaxOpenConns, got.MaxIdleConns)
		}
		if got.ConnMaxLifetime != base.ConnMaxLifetime || got.ConnMaxIdleTime != base.ConnMaxIdleTime {
			t.Errorf("lifetimes changed: %v/%v", got.ConnMaxLifetime, got.ConnMaxIdleTime)
		}
	})

	t.Run("lifetime overrides are honored", func(t *testing.T) {
		got := base.WithOverrides(PoolConfig{ConnMaxLifetime: 10 * time.Minute, ConnMaxIdleTime: 4 * time.Minute})
		if got.ConnMaxLifetime != 10*time.Minute || got.ConnMaxIdleTime != 4*time.Minute {
			t.Errorf("lifetimes = %v/%v, want 10m/4m", got.ConnMaxLifetime, got.ConnMaxIdleTime)
		}
		if got.MaxOpenConns != base.MaxOpenConns {
			t.Errorf("MaxOpenConns changed: %d", got.MaxOpenConns)
		}
	})
}

func TestConnectFromEnvRequiresDSN(t *testing.T) {
	t.Setenv("DATABASE_URL", "")
	if _, err := ConnectFromEnv(DefaultPoolConfig()); err == nil {
		t.Fatal("expected error when DATABASE_URL is unset")
	}
}
