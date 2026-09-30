package controller

import (
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestExternalBillingRouteAttestationRequiresActualSelection(t *testing.T) {
	for _, managed := range []bool{false, true} {
		recorder := httptest.NewRecorder()
		context, _ := gin.CreateTestContext(recorder)
		context.Set(string(constant.ContextKeyExternalBilling), true)
		if managed {
			context.Set("external_billing_managed_channel_id", 91)
			context.Header(middleware.PanstarRelayStageHeader, middleware.PanstarRelayStagePreRoute)
		}
		require.Empty(t, recorder.Header().Get("X-Panstar-NewAPI-Channel-Id"))

		attestExternalBillingRoute(context, 91)
		require.Equal(t, "91", recorder.Header().Get("X-Panstar-NewAPI-Channel-Id"))
		if managed {
			require.Equal(t, middleware.PanstarRelayStageRouted,
				recorder.Header().Get(middleware.PanstarRelayStageHeader))
		} else {
			require.Empty(t, recorder.Header().Get(middleware.PanstarRelayStageHeader))
		}
	}
}
