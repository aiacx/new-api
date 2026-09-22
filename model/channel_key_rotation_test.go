package model

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestRotateChannelKeyStoresOnlyCiphertextAndReplaysIdempotently(t *testing.T) {
	originalDB := DB
	t.Cleanup(func() { DB = originalDB })
	database, err := gorm.Open(sqlite.Open("file::memory:?cache=shared"), &gorm.Config{})
	require.NoError(t, err)
	DB = database
	require.NoError(t, DB.AutoMigrate(&Channel{}, &ChannelKeyRotation{}, &Ability{}))

	master := bytes.Repeat([]byte{0x31}, 32)
	keyringPayload, err := json.Marshal(map[string]any{
		"active_version": 3,
		"keys":           map[string]string{"3": base64.StdEncoding.EncodeToString(master)},
	})
	require.NoError(t, err)
	keyringPath := filepath.Join(t.TempDir(), "channel-key-master")
	require.NoError(t, os.WriteFile(keyringPath, keyringPayload, 0o600))
	t.Setenv("PANSTAR_CHANNEL_KEY_MASTER_FILE", keyringPath)
	t.Setenv("PANSTAR_CHANNEL_KEY_ENCRYPTION_REQUIRED", "true")

	channel := Channel{Name: "encrypted-channel", Key: "", Status: 0,
		Models: "synthetic-model", Group: "private", KeyCipherID: strings.Repeat(" ", 32)}
	require.NoError(t, DB.Create(&channel).Error)

	first, replayed, err := RotateChannelKey(channel.Id, "supplier-token-test-only",
		"rotation-test-00000001")
	require.NoError(t, err)
	require.False(t, replayed)
	require.Equal(t, 1, first.CredentialVersion)
	require.Equal(t, 3, first.MasterKeyVersion)

	var stored Channel
	require.NoError(t, DB.First(&stored, channel.Id).Error)
	require.Empty(t, stored.Key)
	require.NotEmpty(t, stored.KeyCiphertext)
	require.NotEmpty(t, stored.KeyNonce)
	require.Equal(t, "****only", stored.KeyMask)
	resolved, err := stored.ResolveKey()
	require.NoError(t, err)
	require.Equal(t, "supplier-token-test-only", resolved)
	metadata, err := GetChannelCredentialMetadata(channel.Id)
	require.NoError(t, err)
	require.True(t, metadata.Configured)
	require.Equal(t, 1, metadata.CredentialVersion)
	require.Equal(t, 3, metadata.MasterKeyVersion)
	require.Equal(t, "****only", metadata.KeyMask)

	second, replayed, err := RotateChannelKey(channel.Id, "ignored-new-token",
		"rotation-test-00000001")
	require.NoError(t, err)
	require.True(t, replayed)
	require.Equal(t, first.Id, second.Id)
	require.NoError(t, DB.First(&stored, channel.Id).Error)
	resolved, err = stored.ResolveKey()
	require.NoError(t, err)
	require.Equal(t, "supplier-token-test-only", resolved)

	var plaintextRows int64
	require.NoError(t, DB.Model(&Channel{}).Where("key <> ''").Count(&plaintextRows).Error)
	require.Zero(t, plaintextRows)
	var rotation ChannelKeyRotation
	require.NoError(t, DB.First(&rotation).Error)
	encoded, err := json.Marshal(rotation)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), "supplier-token-test-only")
	require.NotContains(t, string(encoded), "ignored-new-token")
}
