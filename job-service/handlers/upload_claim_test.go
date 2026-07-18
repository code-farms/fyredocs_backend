package handlers

import (
	"context"
	"sync"
	"testing"
)

// TestClaimUploads_Exclusive verifies the atomic per-upload claim that prevents a
// concurrent duplicate submission from consuming the same upload twice and
// creating duplicate jobs (finding B1).
func TestClaimUploads_Exclusive(t *testing.T) {
	withMiniRedis(t)
	ctx := context.Background()

	claimed, conflict, err := claimUploads(ctx, []string{"u1", "u2"})
	if err != nil || conflict {
		t.Fatalf("first claim: err=%v conflict=%v, want clean acquire", err, conflict)
	}
	if len(claimed) != 2 {
		t.Fatalf("claimed %d ids, want 2", len(claimed))
	}

	// A second claim overlapping u2 must conflict (u2 already held) and must not
	// leave u1new half-claimed.
	claimed2, conflict2, err := claimUploads(ctx, []string{"u1new", "u2"})
	if err != nil {
		t.Fatalf("second claim err = %v", err)
	}
	if !conflict2 {
		t.Fatal("second claim overlapping a held id must conflict")
	}
	if claimed2 != nil {
		t.Fatalf("conflicting claim must return no ids, got %v", claimed2)
	}
	// u1new must have been rolled back so it is claimable on its own.
	if c, conf, _ := claimUploads(ctx, []string{"u1new"}); conf {
		t.Error("u1new should have been released after the partial-conflict rollback")
	} else {
		releaseUploadClaims(ctx, c)
	}

	// Releasing the original claims frees them for reuse.
	releaseUploadClaims(ctx, claimed)
	claimed3, conflict3, err := claimUploads(ctx, []string{"u1", "u2"})
	if err != nil || conflict3 {
		t.Fatalf("re-claim after release: err=%v conflict=%v, want clean acquire", err, conflict3)
	}
	if len(claimed3) != 2 {
		t.Fatalf("re-claimed %d ids, want 2", len(claimed3))
	}
}

// TestClaimUploads_ConcurrentSingleWinner verifies that under many concurrent
// claims of the same upload id, exactly one wins.
func TestClaimUploads_ConcurrentSingleWinner(t *testing.T) {
	withMiniRedis(t)
	ctx := context.Background()

	const n = 20
	var wins int64
	var mu sync.Mutex
	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func() {
			defer wg.Done()
			claimed, conflict, err := claimUploads(ctx, []string{"same-upload"})
			if err == nil && !conflict && len(claimed) == 1 {
				mu.Lock()
				wins++
				mu.Unlock()
				// hold the claim (do not release) so only one winner is counted
			}
		}()
	}
	wg.Wait()

	if wins != 1 {
		t.Fatalf("exactly one claimer must win, got %d", wins)
	}
}
