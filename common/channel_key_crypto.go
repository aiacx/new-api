package common

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
)

const (
	DefaultChannelKeyMasterFile = "/run/secrets/panstar-new-api/channel-key-master"
	channelKeyAADPrefix         = "panstar-new-api/channel-key"
	maxChannelKeyMasterBytes    = 16 * 1024
	maxChannelKeyPlaintextBytes = 64 * 1024
)

var (
	ErrChannelKeyMasterUnavailable = errors.New("channel key master is unavailable")
	ErrChannelKeyCiphertextInvalid = errors.New("channel key ciphertext is invalid")
)

type ChannelKeyEnvelope struct {
	Ciphertext string
	Nonce      string
	Version    int
	Mask       string
}

type ChannelKeyring struct {
	activeVersion int
	keys          map[int][]byte
}

type channelKeyringFile struct {
	ActiveVersion int               `json:"active_version"`
	Keys          map[string]string `json:"keys"`
}

func ChannelKeyEncryptionRequired() bool {
	return GetEnvOrDefaultBool("PANSTAR_CHANNEL_KEY_ENCRYPTION_REQUIRED", false)
}

func ChannelKeyMasterFile() string {
	path := strings.TrimSpace(os.Getenv("PANSTAR_CHANNEL_KEY_MASTER_FILE"))
	if path == "" {
		return DefaultChannelKeyMasterFile
	}
	return path
}

func LoadChannelKeyring() (*ChannelKeyring, error) {
	path := ChannelKeyMasterFile()
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() < 1 || info.Size() > maxChannelKeyMasterBytes {
		return nil, ErrChannelKeyMasterUnavailable
	}
	if info.Mode().Perm()&0o077 != 0 {
		return nil, ErrChannelKeyMasterUnavailable
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, ErrChannelKeyMasterUnavailable
	}
	defer zeroBytes(data)
	return ParseChannelKeyring(data)
}

func ParseChannelKeyring(data []byte) (*ChannelKeyring, error) {
	var payload channelKeyringFile
	if err := json.Unmarshal(data, &payload); err != nil || payload.ActiveVersion < 1 || len(payload.Keys) == 0 {
		return nil, ErrChannelKeyMasterUnavailable
	}
	keys := make(map[int][]byte, len(payload.Keys))
	for rawVersion, encoded := range payload.Keys {
		version, err := strconv.Atoi(rawVersion)
		if err != nil || version < 1 || strings.TrimSpace(encoded) == "" {
			return nil, ErrChannelKeyMasterUnavailable
		}
		key, err := base64.StdEncoding.DecodeString(encoded)
		if err != nil || len(key) != 32 {
			zeroBytes(key)
			return nil, ErrChannelKeyMasterUnavailable
		}
		keys[version] = key
	}
	if _, ok := keys[payload.ActiveVersion]; !ok {
		for _, key := range keys {
			zeroBytes(key)
		}
		return nil, ErrChannelKeyMasterUnavailable
	}
	return &ChannelKeyring{activeVersion: payload.ActiveVersion, keys: keys}, nil
}

func (keyring *ChannelKeyring) Close() {
	if keyring == nil {
		return
	}
	for _, key := range keyring.keys {
		zeroBytes(key)
	}
}

func (keyring *ChannelKeyring) Encrypt(plaintext, cipherID string) (ChannelKeyEnvelope, error) {
	if keyring == nil || len(plaintext) == 0 || len(plaintext) > maxChannelKeyPlaintextBytes || !validCipherID(cipherID) {
		return ChannelKeyEnvelope{}, ErrChannelKeyCiphertextInvalid
	}
	key, ok := keyring.keys[keyring.activeVersion]
	if !ok {
		return ChannelKeyEnvelope{}, ErrChannelKeyMasterUnavailable
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return ChannelKeyEnvelope{}, ErrChannelKeyMasterUnavailable
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return ChannelKeyEnvelope{}, ErrChannelKeyMasterUnavailable
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return ChannelKeyEnvelope{}, ErrChannelKeyMasterUnavailable
	}
	plain := []byte(plaintext)
	defer zeroBytes(plain)
	sealed := aead.Seal(nil, nonce, plain, channelKeyAAD(cipherID, keyring.activeVersion))
	defer zeroBytes(sealed)
	return ChannelKeyEnvelope{
		Ciphertext: base64.StdEncoding.EncodeToString(sealed),
		Nonce:      base64.StdEncoding.EncodeToString(nonce),
		Version:    keyring.activeVersion,
		Mask:       MaskChannelKey(plaintext),
	}, nil
}

func (keyring *ChannelKeyring) Decrypt(envelope ChannelKeyEnvelope, cipherID string) (string, error) {
	if keyring == nil || envelope.Version < 1 || !validCipherID(cipherID) {
		return "", ErrChannelKeyCiphertextInvalid
	}
	key, ok := keyring.keys[envelope.Version]
	if !ok {
		return "", ErrChannelKeyMasterUnavailable
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", ErrChannelKeyMasterUnavailable
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return "", ErrChannelKeyMasterUnavailable
	}
	nonce, nonceErr := base64.StdEncoding.DecodeString(envelope.Nonce)
	sealed, sealedErr := base64.StdEncoding.DecodeString(envelope.Ciphertext)
	if nonceErr != nil || sealedErr != nil || len(nonce) != aead.NonceSize() || len(sealed) < aead.Overhead() {
		zeroBytes(nonce)
		zeroBytes(sealed)
		return "", ErrChannelKeyCiphertextInvalid
	}
	defer zeroBytes(nonce)
	defer zeroBytes(sealed)
	plain, err := aead.Open(nil, nonce, sealed, channelKeyAAD(cipherID, envelope.Version))
	if err != nil || len(plain) == 0 || len(plain) > maxChannelKeyPlaintextBytes {
		zeroBytes(plain)
		return "", ErrChannelKeyCiphertextInvalid
	}
	result := string(plain)
	zeroBytes(plain)
	return result, nil
}

func MaskChannelKey(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if len(value) <= 4 {
		return "****"
	}
	return "****" + value[len(value)-4:]
}

func channelKeyAAD(cipherID string, version int) []byte {
	return []byte(fmt.Sprintf("%s|%s|v%d", channelKeyAADPrefix, cipherID, version))
}

func validCipherID(value string) bool {
	if len(value) != 32 {
		return false
	}
	for _, char := range value {
		if !((char >= '0' && char <= '9') || (char >= 'a' && char <= 'f')) {
			return false
		}
	}
	return true
}

func zeroBytes(value []byte) {
	for index := range value {
		value[index] = 0
	}
}
