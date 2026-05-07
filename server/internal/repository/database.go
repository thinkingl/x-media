package repository

import (
	"os"
	"path/filepath"

	"github.com/x-media/x-media-server/internal/config"
	"github.com/x-media/x-media-server/internal/model"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// InitDB 初始化数据库
func InitDB(cfg *config.DatabaseConfig) (*gorm.DB, error) {
	// 确保目录存在
	dir := filepath.Dir(cfg.DSN)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, err
	}

	// 打开数据库
	db, err := gorm.Open(sqlite.Open(cfg.DSN), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Info),
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
