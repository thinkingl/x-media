package logger

import (
	"fmt"
	"os"

	"github.com/x-media/x-media-server/internal/config"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"gopkg.in/natefinch/lumberjack.v2"
)

var log *zap.Logger
var sugar *zap.SugaredLogger

// Init 初始化日志
func Init(cfg *config.LogConfig) error {
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

	log = zap.New(core, zap.AddCaller(), zap.AddStacktrace(zapcore.ErrorLevel))
	// CallerSkip(1) 抵消本包 Info/Warn/... 包装函数多出的一跳，
	// 使 sugar 系列日志的 caller 指向真实业务调用点而非 pkg/logger/logger.go。
	// 注意：With()/GetLogger() 返回裸 log（不经过包装函数），不要加 skip。
	sugar = log.WithOptions(zap.AddCallerSkip(1)).Sugar()

	return nil
}

func Sync() {
	if log != nil {
		log.Sync()
	}
}

func Debug(args ...interface{}) {
	if sugar != nil {
		sugar.Debug(args...)
	}
}

func Debugf(template string, args ...interface{}) {
	if sugar != nil {
		sugar.Debugf(template, args...)
	}
}

func Info(args ...interface{}) {
	if sugar != nil {
		sugar.Info(args...)
	}
}

func Infof(template string, args ...interface{}) {
	if sugar != nil {
		sugar.Infof(template, args...)
	}
}

func Warn(args ...interface{}) {
	if sugar != nil {
		sugar.Warn(args...)
	}
}

func Warnf(template string, args ...interface{}) {
	if sugar != nil {
		sugar.Warnf(template, args...)
	}
}

func Error(args ...interface{}) {
	if sugar != nil {
		sugar.Error(args...)
	}
}

func Errorf(template string, args ...interface{}) {
	if sugar != nil {
		sugar.Errorf(template, args...)
	}
}

func Fatal(args ...interface{}) {
	if sugar != nil {
		sugar.Fatal(args...)
	}
	fmt.Fprintln(os.Stderr, args...)
	os.Exit(1)
}

func Fatalf(template string, args ...interface{}) {
	if sugar != nil {
		sugar.Fatalf(template, args...)
	}
	fmt.Fprintf(os.Stderr, template+"\n", args...)
	os.Exit(1)
}

func With(fields ...zap.Field) *zap.Logger {
	if log != nil {
		return log.With(fields...)
	}
	return zap.NewNop()
}

func GetLogger() *zap.Logger {
	if log != nil {
		return log
	}
	return zap.NewNop()
}

// SetLogger 替换全局 logger（仅供测试注入观测钩子，如 zaptest/observer，
// 用于断言日志限频行为；生产代码勿用）。
func SetLogger(l *zap.Logger) {
	log = l
	sugar = l.WithOptions(zap.AddCallerSkip(1)).Sugar()
}
