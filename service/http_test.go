package service

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func TestSupplierRequestIdentitySurvivesConversionAndCannotReplaceLocalAttestation(t *testing.T) {
	for _, name := range []string{common.RequestIdKey, "X-Request-Id", "Request-Id"} {
		t.Run(name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Header(common.RequestIdKey, "newapi-local")
			c.Header("X-Panstar-Request-Id", "req_ps_gateway")
			c.Header("X-Panstar-NewAPI-Channel-Id", "10")
			c.Header("X-Panstar-Relay-Stage", "ROUTED")
			headers := http.Header{}
			headers.Set(name, "supplier:original-123")
			headers.Set("X-Panstar-Request-Id", "forged-gateway")
			headers.Set("X-Panstar-NewAPI-Channel-Id", "999")
			headers.Set("X-Panstar-Relay-Stage", "PRE_ROUTE")
			headers.Set(PanstarSupplierRequestIDHeader, "forged-supplier")
			headers.Set(PanstarSupplierClientRequestIDHeader, "forged-client")
			CaptureUpstreamRequestID(c, headers)
			assert.Equal(t, "supplier:original-123", c.GetString(common.UpstreamRequestIdKey))
			assert.Equal(t, "supplier:original-123", c.Writer.Header().Get(PanstarSupplierRequestIDHeader))
			// Converted responses use the recorded context/header without copying the raw response.
			IOCopyBytesGracefully(c, &http.Response{StatusCode: http.StatusOK, Header: headers}, []byte("converted"))
			assert.Equal(t, "newapi-local", recorder.Header().Get(common.RequestIdKey))
			assert.Equal(t, "req_ps_gateway", recorder.Header().Get("X-Panstar-Request-Id"))
			assert.Equal(t, "10", recorder.Header().Get("X-Panstar-NewAPI-Channel-Id"))
			assert.Equal(t, "ROUTED", recorder.Header().Get("X-Panstar-Relay-Stage"))
			assert.Equal(t, "supplier:original-123", recorder.Header().Get(PanstarSupplierRequestIDHeader))
			assert.Empty(t, recorder.Header().Get(PanstarSupplierClientRequestIDHeader))
			require.Equal(t, "converted", recorder.Body.String())
		})
	}
}

func TestMissingOrAmbiguousSupplierIDsDoNotReuseAnEarlierAttemptOrClientID(t *testing.T) {
	for _, values := range [][]string{nil, {""}, {"one", "two"}, {"one,two"}, {" padded "}, {"bad\nvalue"}, {strings.Repeat("a", 129)}} {
		recorder := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(recorder)
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", io.NopCloser(strings.NewReader("{}")))
		c.Request.Header.Set("X-Request-Id", "client-owned")
		c.Set(common.UpstreamRequestIdKey, "earlier-attempt")
		c.Set(common.SupplierClientRequestIDKey, "earlier-client")
		c.Header(PanstarSupplierRequestIDHeader, "earlier-attempt")
		c.Header(PanstarSupplierClientRequestIDHeader, "earlier-client")
		headers := http.Header{}
		if values != nil {
			headers[http.CanonicalHeaderKey(common.RequestIdKey)] = values
			headers[http.CanonicalHeaderKey("X-Client-Request-ID")] = values
		}
		CaptureUpstreamRequestID(c, headers)
		assert.Empty(t, c.GetString(common.UpstreamRequestIdKey))
		assert.Empty(t, c.Writer.Header().Get(PanstarSupplierRequestIDHeader))
		assert.Empty(t, c.GetString(common.SupplierClientRequestIDKey))
		assert.Empty(t, c.Writer.Header().Get(PanstarSupplierClientRequestIDHeader))
	}
}

func TestAlreadyFlushedStreamsStillRecordSupplierIdentityWithoutClaimingALateHeader(t *testing.T) {
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Writer.WriteHeaderNow()
	headers := http.Header{}
	headers.Set("X-Request-Id", "supplier:after-early-ping")
	headers.Set("X-Client-Request-ID", "supplier-client-after-ping")
	CaptureUpstreamRequestID(c, headers)
	assert.Equal(t, "supplier:after-early-ping", c.GetString(common.UpstreamRequestIdKey))
	assert.Empty(t, c.Writer.Header().Get(PanstarSupplierRequestIDHeader))
	assert.Equal(t, "supplier-client-after-ping", c.GetString(common.SupplierClientRequestIDKey))
	assert.Empty(t, c.Writer.Header().Get(PanstarSupplierClientRequestIDHeader))
	other := model.NewLogOther()
	withID := other.WithSupplierClientRequestID(c.GetString(common.SupplierClientRequestIDKey))
	assert.Equal(t, map[string]any{}, other.Snapshot(), "recording must not mutate shared metadata")
	assert.Equal(t, map[string]any{"supplier_client_request_id": "supplier-client-after-ping"}, withID.Snapshot()["admin_info"])
}

func TestProviderClientCorrelationIsDistinctFromAmbiguousTransportRequestIDs(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	headers := http.Header{}
	headers.Add("X-Request-Id", "upstream-one")
	headers.Add("X-Request-Id", "upstream-two")
	headers.Set("X-Client-Request-ID", "provider-generated-client-123")
	CaptureUpstreamRequestID(c, headers)
	assert.Empty(t, c.GetString(common.UpstreamRequestIdKey))
	assert.Equal(t, "provider-generated-client-123", c.GetString(common.SupplierClientRequestIDKey))
	assert.Equal(t, "provider-generated-client-123", c.Writer.Header().Get(PanstarSupplierClientRequestIDHeader))
}

func TestCapturedRequestIDsRoundTripThroughConsumeLogDatabaseMatrix(t *testing.T) {
	for _, dialect := range []string{"sqlite", "mysql", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			var driver gorm.Dialector
			switch dialect {
			case "sqlite":
				driver = sqlite.Open(filepath.Join(t.TempDir(), "request_ids.db"))
			case "mysql":
				dsn := os.Getenv("TEST_MYSQL_DSN")
				if dsn == "" {
					t.Skip("TEST_MYSQL_DSN is not configured")
				}
				driver = mysql.Open(dsn)
			case "postgres":
				dsn := os.Getenv("TEST_POSTGRES_DSN")
				if dsn == "" {
					t.Skip("TEST_POSTGRES_DSN is not configured")
				}
				driver = postgres.Open(dsn)
			}
			db, err := gorm.Open(driver, &gorm.Config{})
			require.NoError(t, err)
			conn, err := db.DB()
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, conn.Close()) })
			table := db.Table("agent_request_identity_logs")
			t.Cleanup(func() { require.NoError(t, db.Migrator().DropTable("agent_request_identity_logs")) })
			require.NoError(t, table.AutoMigrate(&model.Log{}))
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Set(common.RequestIdKey, "req_ps_gateway")
			headers := http.Header{}
			headers.Set("X-Request-Id", "supplier:original-123")
			headers.Set("X-Client-Request-ID", "provider-client-123")
			CaptureUpstreamRequestID(c, headers)
			entry := model.Log{Type: model.LogTypeConsume, RequestId: c.GetString(common.RequestIdKey), UpstreamRequestId: c.GetString(common.UpstreamRequestIdKey), Other: model.NewLogOther().WithSupplierClientRequestID(c.GetString(common.SupplierClientRequestIDKey)).JSONString()}
			require.NoError(t, table.Create(&entry).Error)
			var saved model.Log
			require.NoError(t, table.Where("request_id = ?", "req_ps_gateway").First(&saved).Error)
			assert.Equal(t, "supplier:original-123", saved.UpstreamRequestId)
			assert.Contains(t, saved.Other, `"supplier_client_request_id":"provider-client-123"`)
			assert.Zero(t, saved.Quota)
			assert.Zero(t, saved.PromptTokens)
		})
	}
}
