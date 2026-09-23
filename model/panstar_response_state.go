package model

import (
	"errors"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"gorm.io/gorm/schema"
)

var ErrPanstarResponseStateConflict = errors.New("panstar response state ownership conflict")

type ResponseStateBinary []byte

func (ResponseStateBinary) GormDataType() string { return "bytes" }

func (ResponseStateBinary) GormDBDataType(db *gorm.DB, _ *schema.Field) string {
	switch db.Dialector.Name() {
	case "mysql":
		return "LONGBLOB"
	case "postgres":
		return "BYTEA"
	default:
		return "BLOB"
	}
}

type PanstarResponseState struct {
	ID             int64               `json:"-" gorm:"primaryKey"`
	ResponseDigest string              `json:"-" gorm:"type:char(64);not null;uniqueIndex"`
	OwnerDigest    string              `json:"-" gorm:"type:char(64);not null;index:idx_panstar_response_owner,priority:1"`
	Model          string              `json:"-" gorm:"type:varchar(256);not null;index:idx_panstar_response_owner,priority:2"`
	ChannelID      int                 `json:"-" gorm:"not null"`
	Ciphertext     ResponseStateBinary `json:"-" gorm:"not null"`
	Nonce          ResponseStateBinary `json:"-" gorm:"not null"`
	KeyVersion     int                 `json:"-" gorm:"not null"`
	ContextBytes   int64               `json:"-" gorm:"not null"`
	CreatedTime    int64               `json:"-" gorm:"not null"`
	ExpiresTime    int64               `json:"-" gorm:"not null;index"`
}

func CreatePanstarResponseState(state *PanstarResponseState) error {
	return createPanstarResponseState(DB, state)
}

func createPanstarResponseState(db *gorm.DB, state *PanstarResponseState) error {
	if db == nil || state == nil {
		return ErrPanstarResponseStateConflict
	}
	if err := db.Clauses(clause.OnConflict{DoNothing: true}).Create(state).Error; err != nil {
		return err
	}
	var stored PanstarResponseState
	if err := db.Where("response_digest = ?", state.ResponseDigest).First(&stored).Error; err != nil {
		return err
	}
	if stored.OwnerDigest != state.OwnerDigest || stored.Model != state.Model || stored.ChannelID != state.ChannelID {
		return ErrPanstarResponseStateConflict
	}
	return nil
}

func GetPanstarResponseState(responseDigest, ownerDigest, model string, channelID int, now int64) (*PanstarResponseState, error) {
	return getPanstarResponseState(DB, responseDigest, ownerDigest, model, channelID, now)
}

func getPanstarResponseState(db *gorm.DB, responseDigest, ownerDigest, model string, channelID int, now int64) (*PanstarResponseState, error) {
	var state PanstarResponseState
	err := db.Where("response_digest = ? AND owner_digest = ? AND model = ? AND channel_id = ? AND expires_time > ?",
		responseDigest, ownerDigest, model, channelID, now).First(&state).Error
	if err != nil {
		return nil, err
	}
	return &state, nil
}

func DeleteExpiredPanstarResponseStates(now int64, limit int) error {
	if limit <= 0 {
		return nil
	}
	var ids []int64
	if err := DB.Model(&PanstarResponseState{}).Where("expires_time <= ?", now).Limit(limit).Pluck("id", &ids).Error; err != nil || len(ids) == 0 {
		return err
	}
	return DB.Where("id IN ?", ids).Delete(&PanstarResponseState{}).Error
}
