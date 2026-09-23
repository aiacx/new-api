package common

import (
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestResponseStateCipherBindsOwnerAndSupportsKeyRotation(t *testing.T) {
	keyFile := filepath.Join(t.TempDir(), "keyring.json")
	writeResponseStateTestKeyring(t, keyFile, 1, map[int][]byte{1: []byte("0123456789abcdef0123456789abcdef")})
	t.Setenv("PANSTAR_CHANNEL_KEY_MASTER_FILE", keyFile)

	envelope, err := EncryptResponseState([]byte(`[{"role":"user","content":"private"}]`), "owner:model:response")
	require.NoError(t, err)
	require.NotContains(t, string(envelope.Ciphertext), "private")
	plaintext, err := DecryptResponseState(envelope, "owner:model:response")
	require.NoError(t, err)
	require.JSONEq(t, `[{"role":"user","content":"private"}]`, string(plaintext))
	zeroBytes(plaintext)

	_, err = DecryptResponseState(envelope, "foreign:model:response")
	require.ErrorIs(t, err, ErrResponseStateCiphertextInvalid)

	writeResponseStateTestKeyring(t, keyFile, 2, map[int][]byte{
		1: []byte("0123456789abcdef0123456789abcdef"),
		2: []byte("abcdef0123456789abcdef0123456789"),
	})
	plaintext, err = DecryptResponseState(envelope, "owner:model:response")
	require.NoError(t, err)
	zeroBytes(plaintext)
	fresh, err := EncryptResponseState([]byte(`["next"]`), "owner:model:next")
	require.NoError(t, err)
	require.Equal(t, 2, fresh.Version)
}

func writeResponseStateTestKeyring(t *testing.T, path string, active int, keys map[int][]byte) {
	t.Helper()
	encoded := ""
	for version, key := range keys {
		if encoded != "" {
			encoded += ","
		}
		encoded += fmt.Sprintf("%q:%q", fmt.Sprint(version), base64.StdEncoding.EncodeToString(key))
	}
	payload := fmt.Sprintf(`{"active_version":%d,"keys":{%s}}`, active, encoded)
	require.NoError(t, os.WriteFile(path, []byte(payload), 0o600))
}
