package openai

import (
	"context"
	"io"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relay/helper"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
)

// Panstar observes and settles usage at the public Gateway. The private
// channel forwards provider SSE bytes without parsing or recreating events.
func OaiResponsesRawStreamHandler(c *gin.Context, info *relaycommon.RelayInfo,
	resp *http.Response) (*dto.Usage, *types.NewAPIError) {
	if resp == nil || resp.Body == nil {
		return nil, types.NewOpenAIError(io.ErrUnexpectedEOF, types.ErrorCodeBadResponse, http.StatusBadGateway)
	}
	defer service.CloseResponseBodyGracefully(resp)
	info.StreamStatus = relaycommon.NewStreamStatus()
	common.SetContextKey(c, constant.ContextKeyResponseStreamStatus, info.StreamStatus)
	helper.SetEventStreamHeaders(c)
	for _, name := range []string{"X-Reasoning-Included", "X-Codex-Turn-State"} {
		values := resp.Header.Values(name)
		if service.ShouldCopyUpstreamHeader(c, name, values) {
			for _, value := range values {
				c.Writer.Header().Add(name, value)
			}
		}
	}
	stopCancel := context.AfterFunc(c.Request.Context(), func() { _ = resp.Body.Close() })
	defer stopCancel()
	idle := time.Duration(constant.StreamingTimeout) * time.Second
	if idle <= 0 {
		idle = 300 * time.Second
	}
	var timedOut atomic.Bool
	timer := time.AfterFunc(idle, func() {
		timedOut.Store(true)
		_ = resp.Body.Close()
	})
	defer timer.Stop()
	buffer := make([]byte, 32*1024)
	stateTap := service.NewPanstarResponseStateSSETap(c)
	for {
		n, readErr := resp.Body.Read(buffer)
		if n > 0 {
			timer.Reset(idle)
			info.SetFirstResponseTime()
			if stateErr := stateTap.Observe(c, buffer[:n]); stateErr != nil {
				info.StreamStatus.SetEndReason(relaycommon.StreamEndReasonScannerErr, stateErr)
				return nil, stateErr
			}
			helper.ExtendWriteDeadline(c)
			written, writeErr := c.Writer.Write(buffer[:n])
			if writeErr == nil && written != n {
				writeErr = io.ErrShortWrite
			}
			if writeErr != nil {
				info.StreamStatus.SetEndReason(relaycommon.StreamEndReasonScannerErr, writeErr)
				return &dto.Usage{}, nil
			}
			c.Writer.Flush()
		}
		if readErr != nil {
			switch {
			case c.Request.Context().Err() != nil:
				info.StreamStatus.SetEndReason(relaycommon.StreamEndReasonClientGone, c.Request.Context().Err())
			case timedOut.Load():
				info.StreamStatus.SetEndReason(relaycommon.StreamEndReasonTimeout, readErr)
			case readErr == io.EOF:
				info.StreamStatus.SetEndReason(relaycommon.StreamEndReasonEOF, nil)
			default:
				info.StreamStatus.SetEndReason(relaycommon.StreamEndReasonScannerErr, readErr)
			}
			return &dto.Usage{}, nil
		}
	}
}
