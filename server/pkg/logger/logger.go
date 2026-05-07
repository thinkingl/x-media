package logger

import (
	"os"

	"github.com/x-media/x-media-server/internal/config"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

var log *zap.Logger
var sugar *zap.SugaredLogger

// Init 初始化日志
func Init(cfg *config.LogConfig) error {
	// 设置日志级别
	level := zapcore.InfoLevel
	switch cfg.Level {
	case "debug":
		level = zapcore.DebugLevel
	case "info":
		level = zapcore.InfoLevel
	case "warn":
		level = zapcore.WarnLevel
	case "error":
		level = zapcore.ErrorLevel
	}

	// 配置编码器
	encoderConfig := zapcore.EncoderConfig{
		TimeKey:        "timestamp",
		LevelKey:       "level",
		NameKey:        "logger",
		CallerKey:      "caller",
		MessageKey:     "message",
		StacktraceKey:  "stacktrace",
		LineEnding:     zapcore.DefaultLineEnding,
		EncodeLevel:    zapcore.LowercaseLevelEncoder,
		EncodeTime:     zapcore.ISO8601TimeEncoder,
		EncodeDuration: zapcore.SecondsDurationEncoder,
		EncodeCaller:   zapcore.ShortCallerEncoder,
	}

	// 创建核心 - 只使用控制台输出
	core := zapcore.NewCore(
		zapcore.NewConsoleEncoder(encoderConfig),
		zapcore.AddSync(os.Stdout),
		level,
	)

	log = zap.New(core, zap.AddCaller(), zap.AddStacktrace(zapcore.ErrorLevel))
	sugar = log.Sugar()

	return nil
}

// Sync 刷新日志
func Sync() {
	if log != nil {
		log.Sync()
	}
}

// Debug 调试日志
func Debug(args ...interface{}) {
	sugar.Debug(args...)
}

// Debugf 格式化调试日志
func Debugf(template string, args ...interface{}) {
	sugar.Debugf(template, args...)
}

// Info 信息日志
func Info(args ...interface{}) {
	sugar.Info(args...)
}

// Infof 格式化信息日志
func Infof(template string, args ...interface{}) {
	sugar.Infof(template, args...)
}

// Warn 警告日志
func Warn(args ...interface{}) {
	sugar.Warn(args...)
}

// Warnf 格式化警告日志
func Warnf(template string, args ...interface{}) {
	sugar.Warnf(template, args...)
}

// Error 错误日志
func Error(args ...interface{}) {
	sugar.Error(args...)
}

// Errorf 格式化错误日志
func Errorf(template string, args ...interface{}) {
	sugar.Errorf(template, args...)
}

// Fatal 致命错误日志
func Fatal(args ...interface{}) {
	sugar.Fatal(args...)
}

// Fatalf 格式化致命错误日志
func Fatalf(template string, args ...interface{}) {
	sugar.Fatalf(template, args...)
}

// With 添加字段
func With(fields ...zap.Field) *zap.Logger {
	return log.With(fields...)
}

// GetLogger 获取原始logger
func GetLogger() *zap.Logger {
	return log
}
