package service

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/logger"

	"github.com/gin-gonic/gin"
)

func CloseResponseBodyGracefully(httpResponse *http.Response) {
	if httpResponse == nil || httpResponse.Body == nil {
		return
	}
	err := httpResponse.Body.Close()
	if err != nil {
		common.SysError("failed to close response body: " + err.Error())
	}
}

const PanstarSupplierRequestIDHeader = "X-Panstar-Supplier-Request-Id"
const PanstarSupplierClientRequestIDHeader = "X-Panstar-Supplier-Client-Request-Id"

// CaptureUpstreamRequestID runs before any response conversion or stream write.
// The supplier's ID and the local New API ID have separate owners. Never infer
// a supplier ID from the client request, body, or a previous relay attempt.
func CaptureUpstreamRequestID(c *gin.Context, headers http.Header) {
	if c == nil {
		return
	}
	c.Set(common.UpstreamRequestIdKey, "")
	c.Set(common.SupplierClientRequestIDKey, "")
	if !c.Writer.Written() {
		c.Header(PanstarSupplierRequestIDHeader, "")
		c.Header(PanstarSupplierClientRequestIDHeader, "")
	}
	if ids := headers.Values("X-Client-Request-ID"); len(ids) == 1 && validSupplierRequestID(ids[0]) {
		c.Set(common.SupplierClientRequestIDKey, ids[0])
		if !c.Writer.Written() {
			c.Header(PanstarSupplierClientRequestIDHeader, ids[0])
		}
	}
	for _, name := range []string{common.RequestIdKey, "X-Request-Id", "Request-Id"} {
		values := headers.Values(name)
		if len(values) == 0 {
			continue
		}
		if len(values) != 1 || !validSupplierRequestID(values[0]) {
			return
		}
		c.Set(common.UpstreamRequestIdKey, values[0])
		if !c.Writer.Written() {
			c.Header(PanstarSupplierRequestIDHeader, values[0])
		}
		return
	}
}

func validSupplierRequestID(value string) bool {
	if len(value) == 0 || len(value) > 128 {
		return false
	}
	for _, ch := range value {
		if !(ch >= 'a' && ch <= 'z' || ch >= 'A' && ch <= 'Z' || ch >= '0' && ch <= '9' || strings.ContainsRune("_.:-", ch)) {
			return false
		}
	}
	return true
}

// Preserve local request/channel attestation when copying supplier headers.
func ShouldCopyUpstreamHeader(c *gin.Context, k string, v []string) bool {
	for _, reserved := range []string{"Content-Length", common.RequestIdKey,
		"X-Panstar-Request-Id", "X-Panstar-NewAPI-Channel-Id", "X-Panstar-Relay-Stage",
		PanstarSupplierRequestIDHeader, PanstarSupplierClientRequestIDHeader} {
		if strings.EqualFold(k, reserved) {
			return false
		}
	}
	return true
}

func IOCopyBytesGracefully(c *gin.Context, src *http.Response, data []byte) {
	if c.Writer == nil {
		return
	}

	body := io.NopCloser(bytes.NewBuffer(data))

	// We shouldn't set the header before we parse the response body, because the parse part may fail.
	// And then we will have to send an error response, but in this case, the header has already been set.
	// So the httpClient will be confused by the response.
	// For example, Postman will report error, and we cannot check the response at all.
	if src != nil {
		for k, v := range src.Header {
			if !ShouldCopyUpstreamHeader(c, k, v) {
				continue
			}
			c.Writer.Header().Set(k, v[0])
		}
	}

	// set Content-Length header manually BEFORE calling WriteHeader
	c.Writer.Header().Set("Content-Length", fmt.Sprintf("%d", len(data)))

	// Write header with status code (this sends the headers)
	if src != nil {
		c.Writer.WriteHeader(src.StatusCode)
	} else {
		c.Writer.WriteHeader(http.StatusOK)
	}

	_, err := io.Copy(c.Writer, body)
	if err != nil {
		logger.LogError(c, fmt.Sprintf("failed to copy response body: %s", err.Error()))
	}
	c.Writer.Flush()
}
