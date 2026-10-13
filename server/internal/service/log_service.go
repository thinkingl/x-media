package service

import (
	"bufio"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/x-media/x-media-server/internal/config"
	"github.com/x-media/x-media-server/pkg/errors"
	"github.com/x-media/x-media-server/pkg/logger"
)

type LogService struct {
	mu     sync.RWMutex
	cfg    *config.LogConfig
	buffer []string
	maxBuf int
}

func NewLogService(cfg *config.LogConfig) *LogService {
	return &LogService{
		cfg:    cfg,
		buffer: make([]string, 0, 100),
		maxBuf: 1000,
	}
}

type LogEntry struct {
	Level     string `json:"level"`
	Message   string `json:"message"`
	Timestamp string `json:"timestamp"`
}

type LogConfigResponse struct {
	Level      string `json:"level"`
	Filename   string `json:"filename"`
	MaxSize    int    `json:"max_size"`
	MaxBackups int    `json:"max_backups"`
	MaxAge     int    `json:"max_age"`
	Compress   bool   `json:"compress"`
}

func (s *LogService) GetConfig() *LogConfigResponse {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return &LogConfigResponse{
		Level:      s.cfg.Level,
		Filename:   s.cfg.Filename,
		MaxSize:    s.cfg.MaxSize,
		MaxBackups: s.cfg.MaxBackups,
		MaxAge:     s.cfg.MaxAge,
		Compress:   s.cfg.Compress,
	}
}

func (s *LogService) UpdateConfig(req *LogConfigResponse) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// 先在副本上应用变更，校验/重建失败时不污染现配置
	next := *s.cfg

	if req.Level != "" {
		validLevels := map[string]bool{"debug": true, "info": true, "warn": true, "error": true}
		if !validLevels[req.Level] {
			return errors.NewValidationError("无效的日志级别")
		}
		next.Level = req.Level
	}
	if req.Filename != "" {
		// 热重建会按此路径打开文件：必须拒绝绝对路径与 .. 穿越，
		// 否则本接口变成"任意路径写日志"。
		if !isValidLogFilename(req.Filename) {
			return errors.NewValidationError("日志文件名必须是不含 .. 的相对路径")
		}
		next.Filename = req.Filename
	}
	if req.MaxSize > 0 {
		next.MaxSize = req.MaxSize
	}
	if req.MaxBackups > 0 {
		next.MaxBackups = req.MaxBackups
	}
	if req.MaxAge > 0 {
		next.MaxAge = req.MaxAge
	}
	next.Compress = req.Compress

	// 热重建：使级别/文件/轮转修改立即生效（此前只改内存结构体，
	// zap/lumberjack 早已构造完毕，修改完全无效）。
	if err := logger.Rebuild(&next); err != nil {
		return errors.NewInternalError(err)
	}
	*s.cfg = next
	return nil
}

// isValidLogFilename 校验日志文件名：相对路径且不含 ".." 路径段。
func isValidLogFilename(name string) bool {
	if name == "" || filepath.IsAbs(name) {
		return false
	}
	for _, part := range strings.Split(filepath.ToSlash(name), "/") {
		if part == ".." {
			return false
		}
	}
	return true
}

func (s *LogService) GetLogs(lines int) ([]string, error) {
	s.mu.RLock()
	filename := s.cfg.Filename
	s.mu.RUnlock()

	if filename == "" {
		return s.buffer, nil
	}

	file, err := os.Open(filename)
	if err != nil {
		if os.IsNotExist(err) {
			return []string{}, nil
		}
		return nil, errors.NewInternalError(err)
	}
	defer file.Close()

	info, err := file.Stat()
	if err != nil {
		return nil, errors.NewInternalError(err)
	}

	// 只读尾部窗口：按每行预估 4KB + 64KB 余量估算，避免把整份大日志
	//（曾达数十 MB）全量读进内存。
	const avgLineBytes = 4096
	window := int64(lines)*avgLineBytes + 64*1024
	if lines <= 0 || window > info.Size() {
		window = info.Size()
	}
	start := info.Size() - window
	if start > 0 {
		if _, err := file.Seek(start, io.SeekStart); err != nil {
			return nil, errors.NewInternalError(err)
		}
	} else {
		start = 0
	}

	scanner := bufio.NewScanner(file)
	// 放大 token 上限：单行超过 64KB（如含大 payload/栈）不再让接口 500；
	// 仍超限时返回已读部分而非整体失败。
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)

	var result []string
	partial := start > 0 // 窗口起点可能落在某行中间，首行丢弃
	for scanner.Scan() {
		if partial {
			partial = false
			continue
		}
		result = append(result, scanner.Text())
	}
	if err := scanner.Err(); err != nil {
		if !strings.Contains(err.Error(), "token too long") {
			return nil, errors.NewInternalError(err)
		}
	}

	if lines > 0 && len(result) > lines {
		result = result[len(result)-lines:]
	}

	return result, nil
}

func (s *LogService) AddLog(entry string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.buffer = append(s.buffer, entry)
	if len(s.buffer) > s.maxBuf {
		s.buffer = s.buffer[len(s.buffer)-s.maxBuf:]
	}
}

func (s *LogService) GetLogsJSON(lines int) ([]LogEntry, error) {
	logs, err := s.GetLogs(lines)
	if err != nil {
		return nil, err
	}

	var entries []LogEntry
	for _, line := range logs {
		var entry LogEntry
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			entries = append(entries, LogEntry{
				Level:   "info",
				Message: line,
			})
		} else {
			entries = append(entries, entry)
		}
	}

	return entries, nil
}
