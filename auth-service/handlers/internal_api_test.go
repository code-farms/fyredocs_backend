// Tests for the internal plan-lookup endpoint's input guard. Only the pre-DB
// path is unit-testable (models.DB is nil here); the lookup and free-plan
// fallback need a database and are integration-tier. The InternalTokenGin
// middleware in front of this handler is covered in shared/authverify and in
// routes/routes_test.go — these tests call the handler directly.
package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

func TestGetUserPlanInvalidID(t *testing.T) {
	gin.SetMode(gin.TestMode)

	tests := []struct {
		name string
		id   string
	}{
		{"not a uuid", "not-a-uuid"},
		{"numeric", "12345"},
		{"uuid shape with bad hex", "gggggggg-gggg-gggg-gggg-gggggggggggg"},
		{"valid uuid with trailing garbage", uuid.NewString() + "x"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := gin.New()
			r.GET("/internal/users/:id/plan", GetUserPlan)
			rec := httptest.NewRecorder()
			// A valid UUID would proceed to models.DB (nil in unit tests), so
			// reaching 400 without a panic proves the parse guard runs first.
			req := httptest.NewRequest(http.MethodGet, "/internal/users/"+tt.id+"/plan", nil)
			r.ServeHTTP(rec, req)

			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400 (body %s)", rec.Code, rec.Body.String())
			}
			var env struct {
				Success bool `json:"success"`
				Error   *struct {
					Code string `json:"code"`
				} `json:"error"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
				t.Fatalf("decode envelope: %v", err)
			}
			if env.Success {
				t.Error("success = true on a 400 response")
			}
			if env.Error == nil || env.Error.Code != "INVALID_INPUT" {
				t.Errorf("error = %+v, want code INVALID_INPUT", env.Error)
			}
		})
	}
}
