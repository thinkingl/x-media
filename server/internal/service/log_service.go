package service

import (
	"bufio"
	"encoding/json"
	"os"
	"sync"

	"github.com/x-media/x-media-server/internal/config"
	"github.com/x-media/x-media-server/pkg/errors"
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

	if req.Level != "" {
		validLevels := map[string]bool{"debug": true, "info": true, "warn": true, "error": true}
		if !validLevels[req.Level] {
			return errors.NewValidationError("无效的日志级别")
		}
		s.cfg.Level = req.Level
	}
	if req.Filename != "" {
		s.cfg.Filename = req.Filename
	}
	if req.MaxSize > 0 {
		s.cfg.MaxSize = req.MaxSize
	}
	if req.MaxBackups > 0 {
		s.cfg.MaxBackups = req.MaxBackups
	}
	if req.MaxAge > 0 {
		s.cfg.MaxAge = req.MaxAge
	}
	s.cfg.Compress = req.Compress

	return nil
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

	var result []string
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		result = append(result, scanner.Text())
	}

	if err := scanner.Err(); err != nil {
		return nil, errors.NewInternalError(err)
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
