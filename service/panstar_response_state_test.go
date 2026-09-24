package service

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

const testPanstarStateOwner = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

func TestPanstarResponseStateIsDefaultOff(t *testing.T) {
	t.Setenv("PANSTAR_RESPONSES_STATE_ENABLED", "false")
	body := []byte(`{"model":"gpt-test","input":"private","store":true}`)
	context := panstarResponseTestContext(t, body, testPanstarStateOwner)
	request := &dto.OpenAIResponsesRequest{
		Model: "gpt-test", Input: json.RawMessage(`"private"`), Store: json.RawMessage(`true`),
	}
	require.Nil(t, PreparePanstarResponseState(context, request))
	require.JSONEq(t, `true`, string(request.Store))
	require.Nil(t, panstarResponseStateContextFrom(context))
	require.Empty(t, context.Request.Header.Get(PanstarStateOwnerHeader))
}

func TestPanstarResponseStateReplaysEncryptedOwnedContext(t *testing.T) {
	setupPanstarResponseStateTest(t)

	firstBody := []byte(`{"model":"gpt-test","input":"private first","store":true,"future_control":{"keep":1}}`)
	firstContext := panstarResponseTestContext(t, firstBody, testPanstarStateOwner)
	firstRequest := &dto.OpenAIResponsesRequest{
		Model: "gpt-test", Input: json.RawMessage(`"private first"`), Store: json.RawMessage(`true`),
	}
	require.Nil(t, PreparePanstarResponseState(firstContext, firstRequest))
	require.Empty(t, firstContext.Request.Header.Get(PanstarStateOwnerHeader))
	require.JSONEq(t, `[{"role":"user","content":"private first"}]`, string(firstRequest.Input))
	require.JSONEq(t, `false`, string(firstRequest.Store))
	patched := bodyStorageBytes(t, firstContext)
	require.Contains(t, string(patched), `"future_control":{"keep":1}`)
	require.NotContains(t, string(patched), `"store":true`)

	response := []byte(`{"id":"resp_first","status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"private answer"}],"future_output":{"keep":2}}]}`)
	require.Nil(t, CapturePanstarResponseState(firstContext, response))
	var stored model.PanstarResponseState
	require.NoError(t, model.DB.First(&stored).Error)
	require.NotContains(t, string(stored.Ciphertext), "private first")
	require.NotContains(t, string(stored.Ciphertext), "private answer")

	secondBody := []byte(`{"model":"gpt-test","input":"private second","store":false,"previous_response_id":"resp_first","future_control":{"keep":3}}`)
	secondContext := panstarResponseTestContext(t, secondBody, testPanstarStateOwner)
	secondRequest := &dto.OpenAIResponsesRequest{
		Model: "gpt-test", Input: json.RawMessage(`"private second"`), Store: json.RawMessage(`false`),
		PreviousResponseID: "resp_first",
	}
	require.Nil(t, PreparePanstarResponseState(secondContext, secondRequest))
	require.Empty(t, secondRequest.PreviousResponseID)
	var replayed []json.RawMessage
	require.NoError(t, common.Unmarshal(secondRequest.Input, &replayed))
	require.Len(t, replayed, 3)
	require.Contains(t, string(replayed[1]), `"future_output":{"keep":2}`)
	require.Contains(t, string(replayed[2]), "private second")
	secondPatched := bodyStorageBytes(t, secondContext)
	require.NotContains(t, string(secondPatched), "previous_response_id")
	require.Contains(t, string(secondPatched), `"future_control":{"keep":3}`)
}

func TestPanstarResponseStateHidesMissingAndForeignOwnership(t *testing.T) {
	setupPanstarResponseStateTest(t)
	seedBody := []byte(`{"model":"gpt-test","input":"seed","store":true}`)
	seedContext := panstarResponseTestContext(t, seedBody, testPanstarStateOwner)
	seedRequest := &dto.OpenAIResponsesRequest{Model: "gpt-test", Input: json.RawMessage(`"seed"`), Store: json.RawMessage(`true`)}
	require.Nil(t, PreparePanstarResponseState(seedContext, seedRequest))
	require.Nil(t, CapturePanstarResponseState(seedContext,
		[]byte(`{"id":"resp_owned","status":"completed","output":[{"type":"message","role":"assistant","content":"seeded"}]}`)))

	for _, test := range []struct {
		name, owner, model, responseID string
		channel                        int
	}{
		{name: "missing", owner: testPanstarStateOwner, model: "gpt-test", responseID: "resp_missing", channel: 5},
		{name: "foreign-owner", owner: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", model: "gpt-test", responseID: "resp_owned", channel: 5},
		{name: "foreign-model", owner: testPanstarStateOwner, model: "other-model", responseID: "resp_owned", channel: 5},
		{name: "foreign-channel", owner: testPanstarStateOwner, model: "gpt-test", responseID: "resp_owned", channel: 6},
	} {
		t.Run(test.name, func(t *testing.T) {
			body := []byte(`{"model":"` + test.model + `","input":"next","store":false,"previous_response_id":"` + test.responseID + `"}`)
			context := panstarResponseTestContextWithChannel(t, body, test.owner, test.channel)
			request := &dto.OpenAIResponsesRequest{Model: test.model, Input: json.RawMessage(`"next"`), Store: json.RawMessage(`false`), PreviousResponseID: test.responseID}
			apiErr := PreparePanstarResponseState(context, request)
			require.NotNil(t, apiErr)
			require.Equal(t, http.StatusBadRequest, apiErr.StatusCode)
			require.Equal(t, "response_not_found", string(apiErr.GetErrorCode()))
			require.Equal(t, strconv.Itoa(test.channel), context.Writer.Header().Get("X-Panstar-NewAPI-Channel-Id"))
		})
	}
}

func TestPanstarResponseStateSSETapCapturesSplitTerminalWithoutChangingFrames(t *testing.T) {
	setupPanstarResponseStateTest(t)
	body := []byte(`{"model":"gpt-test","input":"stream seed","store":true,"stream":true}`)
	context := panstarResponseTestContext(t, body, testPanstarStateOwner)
	request := &dto.OpenAIResponsesRequest{
		Model: "gpt-test", Input: json.RawMessage(`"stream seed"`), Store: json.RawMessage(`true`), Stream: common.GetPointer(true),
	}
	require.Nil(t, PreparePanstarResponseState(context, request))
	tap := NewPanstarResponseStateSSETap(context)
	require.NotNil(t, tap)
	first := []byte("event: response.created\ndata: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_stream\"}}\n\n" +
		"event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_stream\",\"status\":\"completed\",\"output\":[")
	second := []byte("{\"type\":\"message\",\"role\":\"assistant\",\"content\":\"stream private\"}]}}\n\ndata: [DONE]\n\n")
	require.Nil(t, tap.Observe(context, first))
	var count int64
	require.NoError(t, model.DB.Model(&model.PanstarResponseState{}).Count(&count).Error)
	require.Zero(t, count)
	require.Nil(t, tap.Observe(context, second))
	require.NoError(t, model.DB.Model(&model.PanstarResponseState{}).Count(&count).Error)
	require.EqualValues(t, 1, count)
	require.Equal(t, first, []byte("event: response.created\ndata: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_stream\"}}\n\n"+
		"event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_stream\",\"status\":\"completed\",\"output\":["))
	require.Equal(t, second, []byte("{\"type\":\"message\",\"role\":\"assistant\",\"content\":\"stream private\"}]}}\n\ndata: [DONE]\n\n"))
}

func setupPanstarResponseStateTest(t *testing.T) {
	t.Helper()
	previousDB, previousType := model.DB, common.MainDatabaseType()
	database, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := database.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	model.DB = database
	common.SetMainDatabaseType(common.DatabaseTypeSQLite)
	t.Cleanup(func() {
		model.DB = previousDB
		common.SetMainDatabaseType(previousType)
		require.NoError(t, sqlDB.Close())
	})
	require.NoError(t, database.AutoMigrate(&model.PanstarResponseState{}))
	keyring, err := common.Marshal(map[string]any{
		"active_version": 1,
		"keys":           map[string]string{"1": base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{0x31}, 32))},
	})
	require.NoError(t, err)
	keyringPath := filepath.Join(t.TempDir(), "channel-key-master")
	require.NoError(t, os.WriteFile(keyringPath, keyring, 0o600))
	t.Setenv("PANSTAR_CHANNEL_KEY_MASTER_FILE", keyringPath)
	t.Setenv("PANSTAR_RESPONSES_STATE_ENABLED", "true")
	t.Setenv("PANSTAR_RESPONSES_STATE_MAX_MB", "16")
}

func panstarResponseTestContext(t *testing.T, body []byte, owner string) *gin.Context {
	t.Helper()
	return panstarResponseTestContextWithChannel(t, body, owner, 5)
}

func panstarResponseTestContextWithChannel(t *testing.T, body []byte, owner string, channel int) *gin.Context {
	t.Helper()
	context, _ := gin.CreateTestContext(httptest.NewRecorder())
	context.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))
	context.Request.Header.Set("Content-Type", "application/json")
	context.Request.Header.Set(PanstarStateOwnerHeader, owner)
	context.Set(string(constant.ContextKeyExternalBilling), true)
	context.Set("external_billing_managed_channel_id", channel)
	return context
}

func bodyStorageBytes(t *testing.T, context *gin.Context) []byte {
	t.Helper()
	storage, err := common.GetBodyStorage(context)
	require.NoError(t, err)
	body, err := storage.Bytes()
	require.NoError(t, err)
	return body
}
