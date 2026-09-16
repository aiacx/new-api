package middleware

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"net/http"
	"os"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
)

// authenticateExternalBillingService recognizes a private Panstar service
// credential by digest. The raw credential never needs a tokens-table row.
func authenticateExternalBillingService(c *gin.Context, bearer string) bool {
	if !common.GetEnvOrDefaultBool("EXTERNAL_BILLING_ENABLED", false) ||
		!externalBillingBearerMatches(bearer, os.Getenv("EXTERNAL_BILLING_BEARER_SHA256")) {
		return false
	}
	userID, err := strconv.Atoi(strings.TrimSpace(os.Getenv("EXTERNAL_BILLING_USER_ID")))
	if err != nil || userID <= 0 {
		abortExternalBilling(c, "external_billing_identity_invalid")
		return true
	}
	user, err := model.GetUserCache(userID)
	if err != nil {
		abortWithOpenAiMessage(c, http.StatusInternalServerError,
			common.TranslateMessage(c, i18n.MsgDatabaseError))
		return true
	}
	if user.Status != common.UserStatusEnabled {
		abortExternalBilling(c, "external_billing_user_disabled")
		return true
	}

	user.WriteContext(c)
	common.SetContextKey(c, constant.ContextKeyUsingGroup, user.Group)
	synthetic := &model.Token{Id: 0, UserId: user.Id, Name: "panstar-external-service",
		UnlimitedQuota: true, Group: user.Group}
	if err := SetupContextForToken(c, synthetic); err != nil {
		abortExternalBilling(c, "external_billing_identity_invalid")
		return true
	}
	digest := sha256.Sum256([]byte(bearer))
	c.Set(string(constant.ContextKeyExternalBilling), true)
	c.Set("external_billing_token_sha256_prefix", hex.EncodeToString(digest[:4]))
	return true
}

func externalBillingBearerMatches(bearer, expectedHex string) bool {
	if bearer == "" {
		return false
	}
	expected, err := hex.DecodeString(strings.TrimSpace(expectedHex))
	if err != nil || len(expected) != sha256.Size {
		return false
	}
	digest := sha256.Sum256([]byte(bearer))
	return subtle.ConstantTimeCompare(digest[:], expected) == 1
}

func abortExternalBilling(c *gin.Context, code string) {
	c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": gin.H{
		"type": "permission_error", "code": code,
		"message": "External billing identity is invalid",
	}})
}

func IsExternalBilling(c *gin.Context) bool {
	value, ok := c.Get(string(constant.ContextKeyExternalBilling))
	return ok && value == true
}
