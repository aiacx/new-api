package service

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

const (
	PanstarStateOwnerHeader    = "X-Panstar-State-Owner"
	panstarResponseStateCtxKey = "panstar_response_state_context"
	panstarResponseStateTTL    = 30 * 24 * time.Hour
)

var (
	panstarStateOwnerPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)
	panstarResponseIDPattern = regexp.MustCompile(`^resp_[A-Za-z0-9_-]{1,251}$`)
)

type panstarResponseStateContext struct {
	ownerDigest string
	model       string
	channelID   int
	context     []json.RawMessage
	store       bool
	captured    bool
}

type PanstarResponseStateSSETap struct {
	buffer  []byte
	maximum int64
	done    bool
}

func PanstarResponsesStateEnabled() bool {
	return common.GetEnvOrDefaultBool("PANSTAR_RESPONSES_STATE_ENABLED", false)
}

func PreparePanstarResponseState(c *gin.Context, request any) *types.NewAPIError {
	owner := ""
	if c != nil && c.Request != nil {
		owner = strings.ToLower(strings.TrimSpace(c.GetHeader(PanstarStateOwnerHeader)))
		c.Request.Header.Del(PanstarStateOwnerHeader)
	}
	if !PanstarResponsesStateEnabled() {
		return nil
	}
	managedChannel, managed := c.Get("external_billing_managed_channel_id")
	channelID, ok := managedChannel.(int)
	if !managed || !ok || channelID <= 0 || !IsExternalBilling(c) {
		return nil
	}
	// State lookup happens before normal channel selection. Still attest the
	// managed channel fixed by the external-billing credential so Gateway can
	// authenticate safe pre-route errors such as response_not_found.
	c.Header("X-Panstar-NewAPI-Channel-Id", strconv.Itoa(channelID))
	req, ok := request.(*dto.OpenAIResponsesRequest)
	if !ok || req == nil {
		return nil
	}
	store, err := panstarResponsesStoreEnabled(req.Store)
	if err != nil {
		return panstarResponseStateInvalid("store", "store must be a boolean")
	}
	previousID := strings.TrimSpace(req.PreviousResponseID)
	if !store && previousID == "" {
		return nil
	}
	if !panstarStateOwnerPattern.MatchString(owner) {
		return types.NewErrorWithStatusCode(errors.New("response state owner is invalid"),
			types.ErrorCodeAccessDenied, http.StatusForbidden, types.ErrOptionWithSkipRetry())
	}
	if strings.TrimSpace(req.Model) == "" || len(req.Model) > 256 {
		return panstarResponseStateInvalid("model", "model is invalid")
	}
	current, err := normalizePanstarResponseInput(req.Input)
	if err != nil {
		return panstarResponseStateInvalid("input", "input cannot be preserved for response state")
	}
	contextItems := current
	if previousID != "" {
		if !panstarResponseIDPattern.MatchString(previousID) {
			return panstarResponseNotFound()
		}
		stored, loadErr := model.GetPanstarResponseState(
			panstarResponseDigest(previousID), owner, req.Model, channelID, time.Now().Unix())
		if loadErr != nil {
			if errors.Is(loadErr, gorm.ErrRecordNotFound) {
				return panstarResponseNotFound()
			}
			return panstarResponseStateUnavailable()
		}
		parent, decryptErr := decryptPanstarResponseContext(stored)
		if decryptErr != nil {
			return panstarResponseStateUnavailable()
		}
		contextItems = append(parent, current...)
		if int64(panstarResponseContextBytes(contextItems)) > panstarResponseStateMaxBytes() {
			return panstarResponseStateTooLarge()
		}
	}

	patched, err := patchPanstarResponseBody(c, contextItems)
	if err != nil {
		return panstarResponseStateInvalid("request", "request body cannot be prepared for response state")
	}
	req.Input = patched
	req.PreviousResponseID = ""
	req.Store = json.RawMessage("false")
	c.Set(panstarResponseStateCtxKey, &panstarResponseStateContext{
		ownerDigest: owner, model: req.Model, channelID: channelID,
		context: contextItems, store: store,
	})
	return nil
}

func CapturePanstarResponseState(c *gin.Context, response []byte) *types.NewAPIError {
	state := panstarResponseStateContextFrom(c)
	if state == nil || !state.store || state.captured {
		return nil
	}
	var root struct {
		ID     string          `json:"id"`
		Output json.RawMessage `json:"output"`
		Status string          `json:"status"`
	}
	if err := common.Unmarshal(response, &root); err != nil || !panstarResponseIDPattern.MatchString(root.ID) || len(root.Output) == 0 {
		return panstarResponseStateUnavailable()
	}
	if root.Status != "completed" && root.Status != "incomplete" {
		return nil
	}
	var outputItems []json.RawMessage
	if err := common.Unmarshal(root.Output, &outputItems); err != nil {
		return panstarResponseStateUnavailable()
	}
	contextItems := append(append([]json.RawMessage(nil), state.context...), outputItems...)
	plain, err := common.Marshal(contextItems)
	if err != nil || len(plain) == 0 || int64(len(plain)) > panstarResponseStateMaxBytes() {
		return panstarResponseStateTooLarge()
	}
	compressed, err := compressPanstarResponseContext(plain)
	zeroPanstarBytes(plain)
	if err != nil {
		return panstarResponseStateUnavailable()
	}
	responseDigest := panstarResponseDigest(root.ID)
	binding := panstarResponseBinding(responseDigest, state.ownerDigest, state.model, state.channelID)
	envelope, err := common.EncryptResponseState(compressed, binding)
	zeroPanstarBytes(compressed)
	if err != nil {
		return panstarResponseStateUnavailable()
	}
	now := time.Now().Unix()
	record := &model.PanstarResponseState{
		ResponseDigest: responseDigest, OwnerDigest: state.ownerDigest,
		Model: state.model, ChannelID: state.channelID,
		Ciphertext: envelope.Ciphertext, Nonce: envelope.Nonce, KeyVersion: envelope.Version,
		ContextBytes: int64(panstarResponseContextBytes(contextItems)), CreatedTime: now,
		ExpiresTime: now + int64(panstarResponseStateTTL.Seconds()),
	}
	if err = model.CreatePanstarResponseState(record); err != nil {
		return panstarResponseStateUnavailable()
	}
	state.captured = true
	_ = model.DeleteExpiredPanstarResponseStates(now, 100)
	return nil
}

func NewPanstarResponseStateSSETap(c *gin.Context) *PanstarResponseStateSSETap {
	state := panstarResponseStateContextFrom(c)
	if state == nil || !state.store || state.captured {
		return nil
	}
	return &PanstarResponseStateSSETap{maximum: panstarResponseStateMaxBytes()}
}

func (tap *PanstarResponseStateSSETap) Observe(c *gin.Context, chunk []byte) *types.NewAPIError {
	if tap == nil || tap.done || len(chunk) == 0 {
		return nil
	}
	if int64(len(tap.buffer)+len(chunk)) > tap.maximum {
		return panstarResponseStateTooLarge()
	}
	tap.buffer = append(tap.buffer, chunk...)
	for {
		end, width := panstarSSEEventBoundary(tap.buffer)
		if end < 0 {
			return nil
		}
		event := append([]byte(nil), tap.buffer[:end]...)
		remaining := append([]byte(nil), tap.buffer[end+width:]...)
		zeroPanstarBytes(tap.buffer)
		tap.buffer = remaining
		data := panstarSSEData(event)
		zeroPanstarBytes(event)
		if len(data) == 0 || bytes.Equal(data, []byte("[DONE]")) {
			continue
		}
		var envelope struct {
			Type     string          `json:"type"`
			Response json.RawMessage `json:"response"`
		}
		if err := common.Unmarshal(data, &envelope); err != nil {
			zeroPanstarBytes(data)
			continue
		}
		zeroPanstarBytes(data)
		if envelope.Type != "response.completed" && envelope.Type != "response.incomplete" {
			continue
		}
		if len(envelope.Response) == 0 {
			return panstarResponseStateUnavailable()
		}
		if stateErr := CapturePanstarResponseState(c, envelope.Response); stateErr != nil {
			return stateErr
		}
		tap.done = true
		zeroPanstarBytes(tap.buffer)
		tap.buffer = nil
		return nil
	}
}

func panstarSSEEventBoundary(buffer []byte) (int, int) {
	lf := bytes.Index(buffer, []byte("\n\n"))
	crlf := bytes.Index(buffer, []byte("\r\n\r\n"))
	switch {
	case lf < 0:
		if crlf < 0 {
			return -1, 0
		}
		return crlf, 4
	case crlf < 0 || lf < crlf:
		return lf, 2
	default:
		return crlf, 4
	}
}

func panstarSSEData(event []byte) []byte {
	lines := bytes.Split(event, []byte("\n"))
	var data []byte
	for _, line := range lines {
		line = bytes.TrimSuffix(line, []byte("\r"))
		if !bytes.HasPrefix(line, []byte("data:")) {
			continue
		}
		part := bytes.TrimSpace(line[len("data:"):])
		if len(data) > 0 {
			data = append(data, '\n')
		}
		data = append(data, part...)
	}
	return data
}

func panstarResponseStateContextFrom(c *gin.Context) *panstarResponseStateContext {
	if c == nil {
		return nil
	}
	value, exists := c.Get(panstarResponseStateCtxKey)
	if !exists {
		return nil
	}
	state, _ := value.(*panstarResponseStateContext)
	return state
}

func panstarResponsesStoreEnabled(raw json.RawMessage) (bool, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return true, nil
	}
	var store bool
	if err := common.Unmarshal(raw, &store); err != nil {
		return false, err
	}
	return store, nil
}

func normalizePanstarResponseInput(raw json.RawMessage) ([]json.RawMessage, error) {
	if len(raw) == 0 {
		return nil, errors.New("input is missing")
	}
	var items []json.RawMessage
	if err := common.Unmarshal(raw, &items); err == nil {
		if len(items) == 0 {
			return nil, errors.New("input is empty")
		}
		return items, nil
	}
	var text string
	if err := common.Unmarshal(raw, &text); err != nil || text == "" {
		return nil, errors.New("input is invalid")
	}
	message, err := common.Marshal(map[string]any{"role": "user", "content": text})
	if err != nil {
		return nil, err
	}
	return []json.RawMessage{message}, nil
}

func patchPanstarResponseBody(c *gin.Context, contextItems []json.RawMessage) (json.RawMessage, error) {
	storage, err := common.GetBodyStorage(c)
	if err != nil {
		return nil, err
	}
	body, err := storage.Bytes()
	if err != nil {
		return nil, err
	}
	var object map[string]json.RawMessage
	if err = common.Unmarshal(body, &object); err != nil {
		return nil, err
	}
	input, err := common.Marshal(contextItems)
	if err != nil {
		return nil, err
	}
	object["input"] = input
	object["store"] = json.RawMessage("false")
	delete(object, "previous_response_id")
	patched, err := common.Marshal(object)
	if err != nil || int64(len(patched)) > panstarResponseStateMaxBytes() {
		return nil, common.ErrRequestBodyTooLarge
	}
	replacement, err := common.CreateBodyStorage(patched)
	if err != nil {
		return nil, err
	}
	c.Set(common.KeyBodyStorage, replacement)
	_ = storage.Close()
	return input, nil
}

func decryptPanstarResponseContext(state *model.PanstarResponseState) ([]json.RawMessage, error) {
	binding := panstarResponseBinding(state.ResponseDigest, state.OwnerDigest, state.Model, state.ChannelID)
	compressed, err := common.DecryptResponseState(common.ResponseStateEnvelope{
		Ciphertext: []byte(state.Ciphertext), Nonce: []byte(state.Nonce), Version: state.KeyVersion,
	}, binding)
	if err != nil {
		return nil, err
	}
	defer zeroPanstarBytes(compressed)
	plain, err := decompressPanstarResponseContext(compressed, panstarResponseStateMaxBytes())
	if err != nil {
		return nil, err
	}
	defer zeroPanstarBytes(plain)
	var contextItems []json.RawMessage
	if err = common.Unmarshal(plain, &contextItems); err != nil || len(contextItems) == 0 {
		return nil, errors.New("stored response context is invalid")
	}
	return contextItems, nil
}

func compressPanstarResponseContext(plain []byte) ([]byte, error) {
	var buffer bytes.Buffer
	writer, err := gzip.NewWriterLevel(&buffer, gzip.BestSpeed)
	if err != nil {
		return nil, err
	}
	if _, err = writer.Write(plain); err != nil {
		_ = writer.Close()
		return nil, err
	}
	if err = writer.Close(); err != nil {
		return nil, err
	}
	return buffer.Bytes(), nil
}

func decompressPanstarResponseContext(compressed []byte, maximum int64) ([]byte, error) {
	reader, err := gzip.NewReader(bytes.NewReader(compressed))
	if err != nil {
		return nil, err
	}
	defer reader.Close()
	plain, err := io.ReadAll(io.LimitReader(reader, maximum+1))
	if err != nil || int64(len(plain)) > maximum {
		zeroPanstarBytes(plain)
		return nil, errors.New("stored response context exceeds the limit")
	}
	return plain, nil
}

func panstarResponseContextBytes(items []json.RawMessage) int {
	bytes := 2
	for _, item := range items {
		bytes += len(item) + 1
	}
	return bytes
}

func panstarResponseStateMaxBytes() int64 {
	megabytes := common.GetEnvOrDefault("PANSTAR_RESPONSES_STATE_MAX_MB", 16)
	if megabytes < 1 {
		megabytes = 1
	}
	if megabytes > 32 {
		megabytes = 32
	}
	return int64(megabytes) << 20
}

func panstarResponseDigest(value string) string {
	digest := sha256.Sum256([]byte(value))
	return hex.EncodeToString(digest[:])
}

func panstarResponseBinding(responseDigest, ownerDigest, model string, channelID int) string {
	modelDigest := panstarResponseDigest(model)
	return responseDigest + "." + ownerDigest + "." + modelDigest + "." + strconv.Itoa(channelID)
}

func zeroPanstarBytes(value []byte) {
	for index := range value {
		value[index] = 0
	}
}

func panstarResponseNotFound() *types.NewAPIError {
	return types.NewErrorWithStatusCode(errors.New("previous response is unavailable"),
		types.ErrorCode("response_not_found"), http.StatusBadRequest, types.ErrOptionWithSkipRetry())
}

func panstarResponseStateInvalid(param, message string) *types.NewAPIError {
	return types.NewErrorWithStatusCode(fmt.Errorf("%s: %s", param, message),
		types.ErrorCodeInvalidRequest, http.StatusBadRequest, types.ErrOptionWithSkipRetry())
}

func panstarResponseStateTooLarge() *types.NewAPIError {
	return types.NewErrorWithStatusCode(errors.New("response state exceeds the configured limit"),
		types.ErrorCode("response_state_too_large"), http.StatusRequestEntityTooLarge, types.ErrOptionWithSkipRetry())
}

func panstarResponseStateUnavailable() *types.NewAPIError {
	return types.NewErrorWithStatusCode(errors.New("response state is temporarily unavailable"),
		types.ErrorCode("response_state_unavailable"), http.StatusServiceUnavailable, types.ErrOptionWithSkipRetry())
}
