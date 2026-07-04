// Package pricing loads a configurable plan→monthly-price map used to produce
// ESTIMATED revenue figures. There is no billing integration; these numbers are
// derived from the active plan distribution and must always be labelled
// "estimated" in API responses.
package pricing

import (
	"os"
	"strconv"
	"strings"
	"sync"
)

const (
	defaultPrices   = "guest=0,free=0,pro=12"
	defaultCurrency = "USD"
)

var (
	once     sync.Once
	prices   map[string]float64
	currency string
)

func load() {
	prices = parsePrices(getEnv("PLAN_PRICES", defaultPrices))
	currency = getEnv("PLAN_CURRENCY", defaultCurrency)
}

func getEnv(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}

// parsePrices turns "free=0,pro=12" into {"free":0,"pro":12}. Malformed pairs
// are skipped rather than failing the whole map.
func parsePrices(raw string) map[string]float64 {
	out := make(map[string]float64)
	for _, pair := range strings.Split(raw, ",") {
		parts := strings.SplitN(strings.TrimSpace(pair), "=", 2)
		if len(parts) != 2 {
			continue
		}
		name := strings.TrimSpace(parts[0])
		val, err := strconv.ParseFloat(strings.TrimSpace(parts[1]), 64)
		if name == "" || err != nil {
			continue
		}
		out[name] = val
	}
	return out
}

// Prices returns the configured plan→monthly-price map (loaded once).
func Prices() map[string]float64 {
	once.Do(load)
	return prices
}

// Currency returns the configured currency code (loaded once).
func Currency() string {
	once.Do(load)
	return currency
}

// PriceOf returns the monthly price for a plan, or 0 when unconfigured.
func PriceOf(plan string) float64 {
	once.Do(load)
	return prices[plan]
}
