package model

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"regexp"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
)

var (
	ErrChannelKeyRotationInvalid  = errors.New("channel key rotation request is invalid")
	ErrChannelKeyRotationConflict = errors.New("channel key rotation idempotency conflict")
	channelKeyIdempotencyPattern  = regexp.MustCompile(`^[A-Za-z0-9._:-]{16,128}$`)
)

type ChannelKeyRotation struct {
	Id                int64  `json:"id"`
	ChannelId         int    `json:"channel_id" gorm:"not null;index"`
	IdempotencyDigest string `json:"-" gorm:"type:char(64);not null;uniqueIndex"`
	CredentialVersion int    `json:"credential_version" gorm:"not null"`
	MasterKeyVersion  int    `json:"key_version" gorm:"not null"`
	KeyMask           string `json:"key_mask" gorm:"type:varchar(32);not null"`
	CreatedTime       int64  `json:"created_time" gorm:"bigint;not null"`
}

type ChannelCredentialMetadata struct {
	Id                int
	Name              string
	Configured        bool
	CredentialVersion int
	MasterKeyVersion  int
	KeyMask           string
	UpdatedTime       int64
	ValidatedTime     int64
}

func GetChannelCredentialMetadata(channelID int) (*ChannelCredentialMetadata, error) {
	if channelID < 1 {
		return nil, ErrChannelKeyRotationInvalid
	}
	var metadata ChannelCredentialMetadata
	err := DB.Model(&Channel{}).Where("id = ?", channelID).Select(`
		id,name,(CASE WHEN key <> '' OR key_version > 0 THEN 1 ELSE 0 END) AS configured,
		credential_version,key_version AS master_key_version,key_mask,
		key_updated_time AS updated_time,key_validated_time AS validated_time`).Find(&metadata).Error
	if err != nil {
		return nil, err
	}
	if metadata.Id == 0 {
		return nil, gorm.ErrRecordNotFound
	}
	return &metadata, nil
}

func RotateChannelKey(channelID int, plaintext, idempotencyKey string) (*ChannelKeyRotation, bool, error) {
	if channelID < 1 || plaintext == "" || !channelKeyIdempotencyPattern.MatchString(idempotencyKey) {
		return nil, false, ErrChannelKeyRotationInvalid
	}
	digestBytes := sha256.Sum256([]byte(idempotencyKey))
	digest := hex.EncodeToString(digestBytes[:])
	var result ChannelKeyRotation
	replayed := false
	err := DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("idempotency_digest = ?", digest).First(&result).Error; err == nil {
			if result.ChannelId != channelID {
				return ErrChannelKeyRotationConflict
			}
			replayed = true
			return nil
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}

		var channel Channel
		if err := lockForUpdate(tx).First(&channel, "id = ?", channelID).Error; err != nil {
			return err
		}
		channel.Key = plaintext
		channel.CredentialVersion++
		if err := channel.EncryptKeyForStorage(true); err != nil {
			return err
		}
		updates := map[string]any{
			"key":                "",
			"key_ciphertext":     channel.KeyCiphertext,
			"key_nonce":          channel.KeyNonce,
			"key_cipher_id":      channel.KeyCipherID,
			"key_version":        channel.KeyVersion,
			"credential_version": channel.CredentialVersion,
			"key_mask":           channel.KeyMask,
			"key_updated_time":   channel.KeyUpdatedTime,
			"key_validated_time": 0,
		}
		if err := tx.Model(&Channel{}).Where("id = ?", channelID).Updates(updates).Error; err != nil {
			return err
		}
		result = ChannelKeyRotation{
			ChannelId: channelID, IdempotencyDigest: digest,
			CredentialVersion: channel.CredentialVersion,
			MasterKeyVersion:  channel.KeyVersion, KeyMask: channel.KeyMask,
			CreatedTime: common.GetTimestamp(),
		}
		return tx.Create(&result).Error
	})
	if err != nil {
		return nil, false, err
	}
	return &result, replayed, nil
}
