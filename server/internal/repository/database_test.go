package repository

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/x-media/x-media-server/internal/config"
	"github.com/x-media/x-media-server/internal/model"
	"gorm.io/gorm"
)

func newTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := InitDB(&config.DatabaseConfig{
		Driver: "sqlite",
		DSN:    filepath.Join(t.TempDir(), "test.db"),
	})
	require.NoError(t, err)
	return db
}

// TestInitDBEnablesWAL 复现 SQLite 无 WAL/busy_timeout：
// 媒体 goroutine + HTTP 并发写时直接 database is locked。
func TestInitDBEnablesWAL(t *testing.T) {
	db := newTestDB(t)

	var mode string
	require.NoError(t, db.Raw("PRAGMA journal_mode").Scan(&mode).Error)
	assert.Equal(t, "wal", strings.ToLower(mode), "journal_mode 必须为 WAL")

	var timeout int
	require.NoError(t, db.Raw("PRAGMA busy_timeout").Scan(&timeout).Error)
	assert.Equal(t, 5000, timeout, "busy_timeout 必须为 5000ms")
}

// TestPipeUniqueConstraint 复现可创建重复管道：
// (input_id, output_id) 无唯一约束时并发创建通过查重后仍会插入重复行。
func TestPipeUniqueConstraint(t *testing.T) {
	db := newTestDB(t)

	p1 := &model.Pipe{ID: "p1", InputID: "in1", OutputID: "out1", Status: model.PipeStatusStopped}
	require.NoError(t, db.Create(p1).Error)

	p2 := &model.Pipe{ID: "p2", InputID: "in1", OutputID: "out1", Status: model.PipeStatusStopped}
	err := db.Create(p2).Error
	require.Error(t, err, "重复 (input_id, output_id) 必须被唯一索引拒绝")
	assert.ErrorIs(t, err, gorm.ErrDuplicatedKey, "应翻译为 gorm.ErrDuplicatedKey，实际: %v", err)

	// 不同管道仍可创建
	p3 := &model.Pipe{ID: "p3", InputID: "in1", OutputID: "out2", Status: model.PipeStatusStopped}
	require.NoError(t, db.Create(p3).Error, "不同 output 的管道应可创建")
}
