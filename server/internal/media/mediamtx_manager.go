package media

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"syscall"

	"github.com/x-media/x-media-server/pkg/logger"
)

type MediamtxManager struct {
	binaryPath string
	cmd        *exec.Cmd
	cancel     context.CancelFunc
	addr       string
	pathName   string
	apiAddr    string
}

func NewMediamtxManager(binaryPath string) *MediamtxManager {
	absPath, err := filepath.Abs(binaryPath)
	if err != nil {
		logger.Warnf("mediamtx binary path error: %v, using as-is", err)
		absPath = binaryPath
	}
	return &MediamtxManager{
		binaryPath: absPath,
	}
}

func (m *MediamtxManager) Start(ctx context.Context, addr string, pathName string) error {
	if m.cmd != nil {
		return fmt.Errorf("mediamtx already running")
	}

	m.addr = addr
	m.pathName = pathName
	m.apiAddr = ":0"

	args := []string{
		"--rtsp", "yes",
		"--rtspAddress", addr,
		"--rtspTransports", "tcp",
		"--rtmp", "no",
		"--hls", "no",
		"--webrtc", "no",
		"--srt", "no",
		"--api", "yes",
		"--apiAddress", m.apiAddr,
		"--authMethod", "internal",
		"--pathDefaults", "source=publisher,overridePublisher=yes",
		"--paths", fmt.Sprintf("%s:{}", pathName),
	}

	ctx, m.cancel = context.WithCancel(ctx)
	m.cmd = exec.CommandContext(ctx, m.binaryPath, args...)
	m.cmd.SysProcAttr = &syscall.SysProcAttr{
		Setpgid: true,
	}

	if err := m.cmd.Start(); err != nil {
		m.cmd = nil
		m.cancel = nil
		return fmt.Errorf("failed to start mediamtx: %w", err)
	}

	go func() {
		err := m.cmd.Wait()
		if err != nil {
			logger.Debugf("mediamtx exited for %s: %v", addr, err)
		}
		m.cmd = nil
		m.cancel = nil
	}()

	logger.Infof("mediamtx started on %s, path: %s (pid=%d)", addr, pathName, m.cmd.Process.Pid)
	return nil
}

func (m *MediamtxManager) Stop() {
	if m.cancel != nil {
		m.cancel()
	}
	if m.cmd != nil && m.cmd.Process != nil {
		m.cmd.Process.Kill()
		m.cmd = nil
		m.cancel = nil
		logger.Infof("mediamtx stopped on %s", m.addr)
	}
}

func (m *MediamtxManager) IsRunning() bool {
	return m.cmd != nil && m.cmd.Process != nil
}
