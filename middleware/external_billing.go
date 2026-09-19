package middleware

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
)

var panstarRequestIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{7,127}$`)
var managedBillingGroupPattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_-]{2,63}$`)

// authenticateExternalBillingService recognizes a private Panstar service
// credential by digest. The raw credential never needs a tokens-table row.
func authenticateExternalBillingService(c *gin.Context, bearer string) bool {
	if !common.GetEnvOrDefaultBool("EXTERNAL_BILLING_ENABLED", false) {
		return false
	}
	primary := externalBillingBearerMatches(bearer, os.Getenv("EXTERNAL_BILLING_BEARER_SHA256"))
	managed := externalBillingBearerMatches(bearer, os.Getenv("EXTERNAL_BILLING_MANAGED_BEARER_SHA256"))
	if !primary && !managed {
		return false
	}
	if primary && managed {
		abortExternalBilling(c, "external_billing_identity_ambiguous")
		return true
	}
	userIDEnv := "EXTERNAL_BILLING_USER_ID"
	expectedGroup := ""
	if managed {
		userIDEnv = "EXTERNAL_BILLING_MANAGED_USER_ID"
		expectedGroup = strings.TrimSpace(os.Getenv("EXTERNAL_BILLING_MANAGED_GROUP"))
	}
	userID, err := strconv.Atoi(strings.TrimSpace(os.Getenv(userIDEnv)))
	if err != nil || userID <= 0 {
		abortExternalBilling(c, "external_billing_identity_invalid")
		return true
	}
	if managed {
		primaryID, parseErr := strconv.Atoi(strings.TrimSpace(os.Getenv("EXTERNAL_BILLING_USER_ID")))
		if parseErr != nil || primaryID == userID || !validManagedBillingGroup(expectedGroup) {
			abortExternalBilling(c, "external_billing_identity_invalid")
			return true
		}
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
	if managed && user.Group != expectedGroup {
		abortExternalBilling(c, "external_billing_group_invalid")
		return true
	}
	if managed {
		channelID, parseErr := strconv.Atoi(strings.TrimSpace(
			os.Getenv("EXTERNAL_BILLING_MANAGED_CHANNEL_ID")))
		if parseErr != nil || channelID <= 0 {
			abortExternalBilling(c, "external_billing_channel_invalid")
			return true
		}
		c.Set("external_billing_managed_channel_id", channelID)
	}
	requestID := c.GetHeader("X-Panstar-Request-Id")
	if !panstarRequestIDPattern.MatchString(requestID) {
		abortExternalBilling(c, "external_billing_request_id_invalid")
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
	c.Set(common.RequestIdKey, requestID)
	c.Header(common.RequestIdKey, requestID)
	c.Header("X-Panstar-Request-Id", requestID)
	requestContext := context.WithValue(c.Request.Context(), common.RequestIdKey, requestID)
	c.Request = c.Request.WithContext(context.WithValue(requestContext,
		string(constant.ContextKeyExternalBilling), true))
	return true
}

func validManagedBillingGroup(group string) bool {
	return group != "" && group != "default" &&
		managedBillingGroupPattern.MatchString(group)
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
