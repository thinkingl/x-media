package repository

import (
	"context"
	"errors"
	"os"
	"path/filepath"
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
	db, err := gorm.Open(sqlite.Open(cfg.DSN), &gorm.Config{
		Logger: databaseLogger{level: logger.Warn},
	})
	if err != nil {
		return nil, err
	}

	// 自动迁移
	if err := db.AutoMigrate(
		&model.Input{},
		&model.Output{},
		&model.Pipe{},
		&model.Stat{},
	); err != nil {
		return nil, err
	}

	return db, nil
}
