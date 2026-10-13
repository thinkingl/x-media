package logger

import (
	"fmt"
	"os"
	"sync/atomic"

	"github.com/x-media/x-media-server/internal/config"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"gopkg.in/natefinch/lumberjack.v2"
)

// pair 是一组不可变的 logger 组合；全局指针原子替换以支持热重建（Rebuild）。
type pair struct {
	log   *zap.Logger
	sugar *zap.SugaredLogger
}

var current atomic.Pointer[pair]

func currentPair() *pair { return current.Load() }

// newLoggerCore 按配置构造 zap logger（Init 与 Rebuild 共用）。
func newLoggerCore(cfg *config.LogConfig) *zap.Logger {
	if cfg.MaxSize <= 0 {
		cfg.MaxSize = 100
	}
	if cfg.MaxBackups <= 0 {
		cfg.MaxBackups = 5
	}
	if cfg.MaxAge <= 0 {
		cfg.MaxAge = 30
	}
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

	consoleEncoder := zapcore.NewConsoleEncoder(encoderConfig)
	jsonEncoder := zapcore.NewJSONEncoder(encoderConfig)

	consoleSyncer := zapcore.AddSync(os.Stdout)

	cores := []zapcore.Core{
		zapcore.NewCore(consoleEncoder, consoleSyncer, level),
	}

	if cfg.Filename != "" {
		fileSyncer := zapcore.AddSync(&lumberjack.Logger{
			Filename:   cfg.Filename,
			MaxSize:    cfg.MaxSize,
			MaxBackups: cfg.MaxBackups,
			MaxAge:     cfg.MaxAge,
			Compress:   cfg.Compress,
		})
		cores = append(cores, zapcore.NewCore(jsonEncoder, fileSyncer, level))
	}

	core := zapcore.NewTee(cores...)

	// CallerSkip(1) 抵消本包 Info/Warn/... 包装函数多出的一跳，
	// 使 sugar 系列日志的 caller 指向真实业务调用点而非 pkg/logger/logger.go。
	// 注意：With()/GetLogger() 返回裸 log（不经过包装函数），不要加 skip。
	log := zap.New(core, zap.AddCaller(), zap.AddStacktrace(zapcore.ErrorLevel))
	return log
}

func apply(l *zap.Logger) {
	current.Store(&pair{log: l, sugar: l.WithOptions(zap.AddCallerSkip(1)).Sugar()})
}

// Init 初始化日志
func Init(cfg *config.LogConfig) error {
	if cfg == nil {
		return fmt.Errorf("logger config is nil")
	}
	apply(newLoggerCore(cfg))
	return nil
}

// Rebuild 按新配置热重建全局 logger（供 API 动态更新日志配置）。
// 构造失败时保留现有 logger 并返回错误。
func Rebuild(cfg *config.LogConfig) error {
	if cfg == nil {
		return fmt.Errorf("logger config is nil")
	}
	l := newLoggerCore(cfg)
	if old := currentPair(); old != nil {
		_ = old.log.Sync() // 落盘旧缓冲（lumberjack 同步写，通常无缓冲）
	}
	apply(l)
	return nil
}

func Sync() {
	if p := currentPair(); p != nil {
		p.log.Sync()
	}
}

func Debug(args ...interface{}) {
	if p := currentPair(); p != nil {
		p.sugar.Debug(args...)
	}
}

func Debugf(template string, args ...interface{}) {
	if p := currentPair(); p != nil {
		p.sugar.Debugf(template, args...)
	}
}

func Info(args ...interface{}) {
	if p := currentPair(); p != nil {
		p.sugar.Info(args...)
	}
}

func Infof(template string, args ...interface{}) {
	if p := currentPair(); p != nil {
		p.sugar.Infof(template, args...)
	}
}

func Warn(args ...interface{}) {
	if p := currentPair(); p != nil {
		p.sugar.Warn(args...)
	}
}

func Warnf(template string, args ...interface{}) {
	if p := currentPair(); p != nil {
		p.sugar.Warnf(template, args...)
	}
}

func Error(args ...interface{}) {
	if p := currentPair(); p != nil {
		p.sugar.Error(args...)
	}
}

func Errorf(template string, args ...interface{}) {
	if p := currentPair(); p != nil {
		p.sugar.Errorf(template, args...)
	}
}

func Fatal(args ...interface{}) {
	if p := currentPair(); p != nil {
		p.sugar.Fatal(args...)
	}
	fmt.Fprintln(os.Stderr, args...)
	os.Exit(1)
}

func Fatalf(template string, args ...interface{}) {
	if p := currentPair(); p != nil {
		p.sugar.Fatalf(template, args...)
	}
	fmt.Fprintf(os.Stderr, template+"\n", args...)
	os.Exit(1)
}

func With(fields ...zap.Field) *zap.Logger {
	if p := currentPair(); p != nil {
		return p.log.With(fields...)
	}
	return zap.NewNop()
}

func GetLogger() *zap.Logger {
	if p := currentPair(); p != nil {
		return p.log
	}
	return zap.NewNop()
}

// SetLogger 替换全局 logger（仅供测试注入观测钩子，如 zaptest/observer，
// 用于断言日志限频行为；生产代码勿用）。
func SetLogger(l *zap.Logger) {
	apply(l)
}
