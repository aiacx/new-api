package service

import (
	"github.com/QuantumNous/new-api/constant"
	"github.com/gin-gonic/gin"
)

// IsExternalBilling reports whether the authenticated request is an internal
// relay whose customer reservation and settlement are owned by Panstar.
func IsExternalBilling(c *gin.Context) bool {
	if c == nil {
		return false
	}
	value, ok := c.Get(string(constant.ContextKeyExternalBilling))
	return ok && value == true
}
