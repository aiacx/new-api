package common

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestChannelKeyringEncryptsWithRandomNonceAndBoundAAD(t *testing.T) {
	key := bytes.Repeat([]byte{0x42}, 32)
	payload, err := json.Marshal(channelKeyringFile{ActiveVersion: 7,
		Keys: map[string]string{"7": base64.StdEncoding.EncodeToString(key)}})
	require.NoError(t, err)
	keyring, err := ParseChannelKeyring(payload)
	require.NoError(t, err)
	defer keyring.Close()

	first, err := keyring.Encrypt("supplier-token-test-only", "0123456789abcdef0123456789abcdef")
	require.NoError(t, err)
	second, err := keyring.Encrypt("supplier-token-test-only", "0123456789abcdef0123456789abcdef")
	require.NoError(t, err)
	require.NotEqual(t, first.Nonce, second.Nonce)
	require.NotEqual(t, first.Ciphertext, second.Ciphertext)
	require.Equal(t, 7, first.Version)
	require.Equal(t, "****only", first.Mask)

	plaintext, err := keyring.Decrypt(first, "0123456789abcdef0123456789abcdef")
	require.NoError(t, err)
	require.Equal(t, "supplier-token-test-only", plaintext)
	_, err = keyring.Decrypt(first, "fedcba9876543210fedcba9876543210")
	require.ErrorIs(t, err, ErrChannelKeyCiphertextInvalid)
}

func TestChannelKeyringRejectsMissingActiveKeyAndInvalidLengths(t *testing.T) {
	payload, marshalErr := json.Marshal(channelKeyringFile{ActiveVersion: 2,
		Keys: map[string]string{"1": "bad"}})
	require.NoError(t, marshalErr)
	_, err := ParseChannelKeyring(payload)
	require.ErrorIs(t, err, ErrChannelKeyMasterUnavailable)
}
