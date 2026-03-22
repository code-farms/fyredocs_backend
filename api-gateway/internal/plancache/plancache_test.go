package plancache

import (
	"context"
	"testing"
)

func TestGetPlanInfoNilClient(t *testing.T) {
	info := GetPlanInfo(context.Background(), nil, "user-123")
	if info.Plan != "free" {
		t.Errorf("expected default plan 'free', got %q", info.Plan)
	}
	if info.MaxFileMB != 25 {
		t.Errorf("expected default MaxFileMB 25, got %d", info.MaxFileMB)
	}
	if info.MaxFiles != 10 {
		t.Errorf("expected default MaxFiles 10, got %d", info.MaxFiles)
	}
}

func TestGetPlanInfoEmptyUserID(t *testing.T) {
	info := GetPlanInfo(context.Background(), nil, "")
	if info != DefaultPlan {
		t.Errorf("expected default plan, got %+v", info)
	}
}
