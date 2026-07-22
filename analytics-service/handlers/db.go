package handlers

import (
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"analytics-service/internal/models"
)

// rdb returns the DB handle scoped to the request context, so a client
// disconnect or deadline cancels the in-flight query (belt-and-suspenders with
// the server-side statement_timeout). Use it in place of models.DB in handlers.
func rdb(c *gin.Context) *gorm.DB {
	if models.DB == nil {
		return nil // preserves the handlers' nil-DB guards (WithContext panics on a nil handle)
	}
	return models.DB.WithContext(c.Request.Context())
}
