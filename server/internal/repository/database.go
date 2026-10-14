package repository

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/x-media/x-media-server/internal/config"
	"github.com/x-media/x-media-server/internal/model"
	appLogger "github.com/x-media/x-media-server/pkg/logger"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

type databaseLogger struct {
	level logger.LogLevel
}

func (l databaseLogger) LogMode(level logger.LogLevel) logger.Interface {
	l.level = level
	return l
}

func (l databaseLogger) Info(_ context.Context, msg string, args ...interface{}) {
	if l.level >= logger.Info {
		appLogger.Infof(msg, args...)
	}
}

func (l databaseLogger) Warn(_ context.Context, msg string, args ...interface{}) {
	if l.level >= logger.Warn {
		appLogger.Warnf(msg, args...)
	}
}

func (l databaseLogger) Error(_ context.Context, msg string, args ...interface{}) {
	if l.level >= logger.Error {
		appLogger.Errorf(msg, args...)
	}
}

func (l databaseLogger) Trace(_ context.Context, begin time.Time, _ func() (string, int64), err error) {
	elapsed := time.Since(begin)
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) && l.level >= logger.Error {
		appLogger.Errorf("database query failed duration=%s: %v", elapsed, err)
	} else if elapsed > 200*time.Millisecond && l.level >= logger.Warn {
		appLogger.Warnf("slow database query duration=%s", elapsed)
	} else if l.level >= logger.Info {
		appLogger.Infof("database query duration=%s", elapsed)
	}
}

// InitDB 初始化数据库
func InitDB(cfg *config.DatabaseConfig) (*gorm.DB, error) {
	// 确保目录存在
	dir := filepath.Dir(cfg.DSN)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, err
	}

	// 打开数据库
	db, err := gorm.Open(sqlite.Open(buildSQLiteDSN(cfg.DSN)), &gorm.Config{
		Logger: databaseLogger{level: logger.Warn},
		// 约束冲突翻译为 gorm.ErrDuplicatedKey，供服务层映射 409
		TranslateError: true,
	})
	if err != nil {
		return nil, err
	}

	// 连接池：SQLite 单写者模型，固定 1 连接 + busy_timeout 避免
	// database is locked（媒体 goroutine 与 HTTP 并发写）。
	if sqlDB, err := db.DB(); err == nil {
		sqlDB.SetMaxOpenConns(1)
		sqlDB.SetMaxIdleConns(1)
	}

	// 自动迁移
	if err := db.AutoMigrate(
		&model.Input{},
		&model.Output{},
		&model.Pipe{},
		&model.Stat{},
	); err != nil {
		// 不阻塞启动：唯一索引可能因历史重复数据创建失败，
		// 打出可操作的告警待人工清理。
		appLogger.Warnf("database migration incomplete (可能存在重复数据需人工清理): %v", err)
	}

	return db, nil
}

// buildSQLiteDSN 为 DSN 追加并发安全所需的 pragma：
// busy_timeout（等待而非直接报 locked）与 WAL（读写并发）。
func buildSQLiteDSN(dsn string) string {
	const params = "_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)"
	if strings.Contains(dsn, "?") {
		return dsn + "&" + params
	}
	if !strings.HasPrefix(dsn, "file:") {
		dsn = "file:" + dsn
	}
	return dsn + "?" + params
}
