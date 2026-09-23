package common

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"strings"
)

const (
	responseStateAADPrefix     = "panstar-new-api/response-state/v1"
	maxResponseStatePlainBytes = 32 << 20
)

var ErrResponseStateCiphertextInvalid = errors.New("response state ciphertext is invalid")

type ResponseStateEnvelope struct {
	Ciphertext []byte
	Nonce      []byte
	Version    int
}

func EncryptResponseState(plaintext []byte, binding string) (ResponseStateEnvelope, error) {
	keyring, err := LoadChannelKeyring()
	if err != nil {
		return ResponseStateEnvelope{}, ErrChannelKeyMasterUnavailable
	}
	defer keyring.Close()
	return keyring.encryptResponseState(plaintext, binding)
}

func DecryptResponseState(envelope ResponseStateEnvelope, binding string) ([]byte, error) {
	keyring, err := LoadChannelKeyring()
	if err != nil {
		return nil, ErrChannelKeyMasterUnavailable
	}
	defer keyring.Close()
	return keyring.decryptResponseState(envelope, binding)
}

func (keyring *ChannelKeyring) encryptResponseState(plaintext []byte, binding string) (ResponseStateEnvelope, error) {
	if keyring == nil || len(plaintext) == 0 || len(plaintext) > maxResponseStatePlainBytes || !validResponseStateBinding(binding) {
		return ResponseStateEnvelope{}, ErrResponseStateCiphertextInvalid
	}
	key, ok := keyring.keys[keyring.activeVersion]
	if !ok {
		return ResponseStateEnvelope{}, ErrChannelKeyMasterUnavailable
	}
	aead, err := responseStateAEAD(key)
	if err != nil {
		return ResponseStateEnvelope{}, err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err = io.ReadFull(rand.Reader, nonce); err != nil {
		return ResponseStateEnvelope{}, ErrChannelKeyMasterUnavailable
	}
	ciphertext := aead.Seal(nil, nonce, plaintext, responseStateAAD(binding, keyring.activeVersion))
	return ResponseStateEnvelope{Ciphertext: ciphertext, Nonce: nonce, Version: keyring.activeVersion}, nil
}

func (keyring *ChannelKeyring) decryptResponseState(envelope ResponseStateEnvelope, binding string) ([]byte, error) {
	if keyring == nil || envelope.Version < 1 || !validResponseStateBinding(binding) {
		return nil, ErrResponseStateCiphertextInvalid
	}
	key, ok := keyring.keys[envelope.Version]
	if !ok {
		return nil, ErrChannelKeyMasterUnavailable
	}
	aead, err := responseStateAEAD(key)
	if err != nil || len(envelope.Nonce) != aead.NonceSize() || len(envelope.Ciphertext) < aead.Overhead() {
		return nil, ErrResponseStateCiphertextInvalid
	}
	plaintext, err := aead.Open(nil, envelope.Nonce, envelope.Ciphertext, responseStateAAD(binding, envelope.Version))
	if err != nil || len(plaintext) == 0 || len(plaintext) > maxResponseStatePlainBytes {
		zeroBytes(plaintext)
		return nil, ErrResponseStateCiphertextInvalid
	}
	return plaintext, nil
}

func responseStateAEAD(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, ErrChannelKeyMasterUnavailable
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, ErrChannelKeyMasterUnavailable
	}
	return aead, nil
}

func responseStateAAD(binding string, version int) []byte {
	return []byte(fmt.Sprintf("%s|%s|v%d", responseStateAADPrefix, binding, version))
}

func validResponseStateBinding(binding string) bool {
	if len(binding) < 16 || len(binding) > 1024 || strings.TrimSpace(binding) != binding {
		return false
	}
	for _, char := range binding {
		if char < 33 || char > 126 {
			return false
		}
	}
	return true
}
