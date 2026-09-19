package service

import (
	"context"
	"github.com/QuantumNous/new-api/constant"
	"github.com/gin-gonic/gin"
)

func ExternalBillingFromContext(ctx context.Context) bool {
	if ctx == nil {
		return false
	}
	value, ok := ctx.Value(string(constant.ContextKeyExternalBilling)).(bool)
	return ok && value
}

// IsExternalBilling reports whether the authenticated request is an internal
// relay whose customer reservation and settlement are owned by Panstar.
func IsExternalBilling(c *gin.Context) bool {
	if c == nil {
		return false
	}
	value, ok := c.Get(string(constant.ContextKeyExternalBilling))
	return ok && value == true
}

func ManagedExternalBillingChannelMatches(c *gin.Context, channelID int) bool {
	if c == nil || !IsExternalBilling(c) {
		return true
	}
	expected, managed := c.Get("external_billing_managed_channel_id")
	if !managed {
		return true
	}
	value, ok := expected.(int)
	return ok && value > 0 && channelID == value
}
