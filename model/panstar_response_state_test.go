package model

import (
	"os"
	"strings"
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func TestPanstarResponseStateIsOwnerModelAndChannelScoped(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&PanstarResponseState{}))
	state := &PanstarResponseState{
		ResponseDigest: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		OwnerDigest:    "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		Model:          "gpt-test", ChannelID: 5, Ciphertext: []byte("ciphertext"), Nonce: []byte("nonce"),
		KeyVersion: 1, ContextBytes: 10, CreatedTime: 100, ExpiresTime: 200,
	}
	require.NoError(t, createPanstarResponseState(db, state))

	stored, err := getPanstarResponseState(db, state.ResponseDigest, state.OwnerDigest, state.Model, 5, 150)
	require.NoError(t, err)
	require.Equal(t, []byte("ciphertext"), []byte(stored.Ciphertext))
	for _, query := range []struct {
		owner, model string
		channel      int
		now          int64
	}{
		{owner: "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc", model: state.Model, channel: 5, now: 150},
		{owner: state.OwnerDigest, model: "other", channel: 5, now: 150},
		{owner: state.OwnerDigest, model: state.Model, channel: 6, now: 150},
		{owner: state.OwnerDigest, model: state.Model, channel: 5, now: 200},
	} {
		_, err = getPanstarResponseState(db, state.ResponseDigest, query.owner, query.model, query.channel, query.now)
		require.ErrorIs(t, err, gorm.ErrRecordNotFound)
	}

	foreign := *state
	foreign.OwnerDigest = "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
	require.ErrorIs(t, createPanstarResponseState(db, &foreign), ErrPanstarResponseStateConflict)
}

func TestPanstarResponseStateDatabaseMatrix(t *testing.T) {
	for _, test := range []struct {
		name, env, expectedType string
		dialector               func(string) gorm.Dialector
	}{
		{name: "sqlite", expectedType: "blob", dialector: func(string) gorm.Dialector { return sqlite.Open(":memory:") }},
		{name: "mysql", env: "TEST_PANSTAR_STATE_MYSQL_DSN", expectedType: "longblob", dialector: func(dsn string) gorm.Dialector { return mysql.Open(dsn) }},
		{name: "postgres", env: "TEST_PANSTAR_STATE_POSTGRES_DSN", expectedType: "bytea", dialector: func(dsn string) gorm.Dialector {
			return postgres.New(postgres.Config{DSN: dsn, PreferSimpleProtocol: true})
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			dsn := ""
			if test.env != "" {
				dsn = os.Getenv(test.env)
				if dsn == "" {
					t.Skip(test.env + " is not configured")
				}
			}
			db, err := gorm.Open(test.dialector(dsn), &gorm.Config{})
			require.NoError(t, err)
			require.NoError(t, db.Migrator().DropTable(&PanstarResponseState{}))
			require.NoError(t, db.AutoMigrate(&PanstarResponseState{}))
			state := &PanstarResponseState{
				ResponseDigest: strings.Repeat("a", 64), OwnerDigest: strings.Repeat("b", 64),
				Model: "gpt-test", ChannelID: 5, Ciphertext: []byte("ciphertext"), Nonce: []byte("nonce"),
				KeyVersion: 1, ContextBytes: 10, CreatedTime: 100, ExpiresTime: 200,
			}
			require.NoError(t, createPanstarResponseState(db, state))
			stored, err := getPanstarResponseState(db, state.ResponseDigest, state.OwnerDigest, state.Model, 5, 150)
			require.NoError(t, err)
			require.Equal(t, []byte("ciphertext"), []byte(stored.Ciphertext))
			columns, err := db.Migrator().ColumnTypes(&PanstarResponseState{})
			require.NoError(t, err)
			for _, column := range columns {
				if column.Name() == "ciphertext" {
					require.Equal(t, test.expectedType, strings.ToLower(column.DatabaseTypeName()))
					return
				}
			}
			t.Fatal("ciphertext column is missing")
		})
	}
}
