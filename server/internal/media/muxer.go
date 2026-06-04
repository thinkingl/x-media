package media

import (
	"context"
	"fmt"
	"io"
	"os/exec"
	"sync"

	"github.com/x-media/x-media-server/pkg/logger"
)

type StreamMuxer struct {
	mu       sync.RWMutex
	output   string
	format   string
	streams  map[uint8]*StreamInfo
	stdins   map[uint8]io.WriteCloser
	cmds     map[uint8]*exec.Cmd
	cancel   context.CancelFunc
	syncMode bool
}

func NewStreamMuxer(output, format string) *StreamMuxer {
	return &StreamMuxer{
		output:  output,
		format:  format,
		streams: make(map[uint8]*StreamInfo),
		stdins:  make(map[uint8]io.WriteCloser),
		cmds:    make(map[uint8]*exec.Cmd),
	}
}

func (m *StreamMuxer) SetSyncMode(sync bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.syncMode = sync
}

func (m *StreamMuxer) AddStream(info *StreamInfo) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.streams[info.ChannelID] = info
}

func (m *StreamMuxer) Start(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	ctx, m.cancel = context.WithCancel(ctx)

	videoStreams := make([]*StreamInfo, 0)
	audioStreams := make([]*StreamInfo, 0)

	for _, s := range m.streams {
		if s.Kind == "video" {
			videoStreams = append(videoStreams, s)
		} else if s.Kind == "audio" {
			audioStreams = append(audioStreams, s)
		}
	}

	if len(videoStreams) == 0 && len(audioStreams) == 0 {
		return fmt.Errorf("no streams to mux")
	}

	args := []string{"-y"}

	for _, s := range videoStreams {
		format := s.CodecID.FFmpegFormat()
		args = append(args, "-f", format, "-i", fmt.Sprintf("pipe:%d", s.ChannelID))
	}

	for _, s := range audioStreams {
		format := s.CodecID.FFmpegFormat()
		args = append(args, "-f", format, "-i", fmt.Sprintf("pipe:%d", s.ChannelID))
	}

	args = append(args, "-c", "copy")

	if m.syncMode {
		args = append(args, "-max_interleave_delta", "100ms")
	}

	args = append(args, "-f", m.format, m.output)

	cmd := exec.CommandContext(ctx, "ffmpeg", args...)
	cmd.Stderr = nil

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("failed to create stdin pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to start ffmpeg muxer: %w", err)
	}

	for _, s := range videoStreams {
		m.stdins[s.ChannelID] = stdin
	}
	for _, s := range audioStreams {
		m.stdins[s.ChannelID] = stdin
	}

	go func() {
		cmd.Wait()
		logger.Infof("muxer stopped for %s", m.output)
	}()

	logger.Infof("muxer started for %s (format=%s, sync=%v)", m.output, m.format, m.syncMode)
	return nil
}

func (m *StreamMuxer) Stop() {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.cancel != nil {
		m.cancel()
	}

	for _, cmd := range m.cmds {
		if cmd.Process != nil {
			cmd.Process.Kill()
		}
	}

	m.stdins = make(map[uint8]io.WriteCloser)
	m.cmds = make(map[uint8]*exec.Cmd)
}

func (m *StreamMuxer) WritePacket(pkt *MediaPacket) error {
	m.mu.RLock()
	stdin, ok := m.stdins[pkt.ChannelID]
	m.mu.RUnlock()

	if !ok {
		return fmt.Errorf("no stdin for channel %d", pkt.ChannelID)
	}

	_, err := stdin.Write(pkt.Data)
	return err
}

type SimpleMuxer struct {
	mu       sync.RWMutex
	cmd      *exec.Cmd
	stdin    io.WriteCloser
	cancel   context.CancelFunc
	started  bool
	format   string
	output   string
}

func NewSimpleMuxer(output, format string) *SimpleMuxer {
	return &SimpleMuxer{
		output: output,
		format: format,
	}
}

func (m *SimpleMuxer) Start(ctx context.Context, codec CodecID) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.started {
		return nil
	}

	ffmpegFormat := codec.FFmpegFormat()
	if ffmpegFormat == "" {
		return fmt.Errorf("unsupported codec: %s", codec)
	}

	args := []string{
		"-re",
		"-analyzeduration", "10000000",
		"-probesize", "10000000",
		"-f", ffmpegFormat,
		"-i", "pipe:0",
		"-c:v", "copy",
	}

	if m.format == "rtmp" || m.format == "flv" {
		args = append(args, "-f", "flv")
	} else if m.format == "rtsp" {
		args = append(args, "-f", "rtsp", "-rtsp_transport", "tcp")
	} else {
		args = append(args, "-f", m.format)
	}

	args = append(args, m.output)

	cmd := exec.CommandContext(ctx, "ffmpeg", args...)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("failed to create stdin pipe: %w", err)
	}

	cmd.Stdout = nil
	cmd.Stderr = nil

	m.cmd = cmd
	m.stdin = stdin
	m.started = true

	if err := cmd.Start(); err != nil {
		m.started = false
		return fmt.Errorf("failed to start ffmpeg: %w", err)
	}

	logger.Infof("simple muxer started for %s (format=%s, codec=%s)", m.output, m.format, codec)
	return nil
}

func (m *SimpleMuxer) Stop() {
	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.started {
		return
	}

	if m.stdin != nil {
		m.stdin.Close()
		m.stdin = nil
	}

	if m.cmd != nil && m.cmd.Process != nil {
		m.cmd.Process.Kill()
		m.cmd.Wait()
		m.cmd = nil
	}

	m.started = false
	logger.Infof("simple muxer stopped for %s", m.output)
}

func (m *SimpleMuxer) WritePacket(pkt *MediaPacket) error {
	m.mu.RLock()
	started := m.started
	stdin := m.stdin
	m.mu.RUnlock()

	if !started || stdin == nil {
		return fmt.Errorf("muxer not started")
	}

	_, err := stdin.Write(pkt.Data)
	return err
}

type HTTPFLVMuxer struct {
	mu       sync.RWMutex
	cmd      *exec.Cmd
	stdin    io.WriteCloser
	cancel   context.CancelFunc
	started  bool
	output   string
	clients  map[io.Writer]bool
}

func NewHTTPFLVMuxer(output string) *HTTPFLVMuxer {
	return &HTTPFLVMuxer{
		output:  output,
		clients: make(map[io.Writer]bool),
	}
}

func (m *HTTPFLVMuxer) Start(ctx context.Context, codec CodecID) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.started {
		return nil
	}

	ffmpegFormat := codec.FFmpegFormat()
	if ffmpegFormat == "" {
		return fmt.Errorf("unsupported codec: %s", codec)
	}

	args := []string{
		"-re",
		"-analyzeduration", "10000000",
		"-probesize", "10000000",
		"-f", ffmpegFormat,
		"-i", "pipe:0",
		"-c:v", "copy",
		"-f", "flv",
		"pipe:1",
	}

	cmd := exec.CommandContext(ctx, "ffmpeg", args...)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("failed to create stdin pipe: %w", err)
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		stdin.Close()
		return fmt.Errorf("failed to create stdout pipe: %w", err)
	}

	cmd.Stderr = nil

	m.cmd = cmd
	m.stdin = stdin
	m.started = true

	if err := cmd.Start(); err != nil {
		m.started = false
		return fmt.Errorf("failed to start ffmpeg: %w", err)
	}

	go func() {
		buf := make([]byte, 4096)
		for {
			n, err := stdout.Read(buf)
			if n > 0 {
				data := make([]byte, n)
				copy(data, buf[:n])
				m.mu.RLock()
				for w := range m.clients {
					w.Write(data)
				}
				m.mu.RUnlock()
			}
			if err != nil {
				break
			}
		}
	}()

	logger.Infof("HTTP-FLV muxer started for %s (codec=%s)", m.output, codec)
	return nil
}

func (m *HTTPFLVMuxer) Stop() {
	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.started {
		return
	}

	if m.stdin != nil {
		m.stdin.Close()
		m.stdin = nil
	}

	if m.cmd != nil && m.cmd.Process != nil {
		m.cmd.Process.Kill()
		m.cmd.Wait()
		m.cmd = nil
	}

	m.started = false
	m.clients = make(map[io.Writer]bool)
	logger.Infof("HTTP-FLV muxer stopped for %s", m.output)
}

func (m *HTTPFLVMuxer) AddClient(w io.Writer) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.clients[w] = true
}

func (m *HTTPFLVMuxer) RemoveClient(w io.Writer) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.clients, w)
}

func (m *HTTPFLVMuxer) WritePacket(pkt *MediaPacket) error {
	m.mu.RLock()
	started := m.started
	stdin := m.stdin
	m.mu.RUnlock()

	if !started || stdin == nil {
		return fmt.Errorf("muxer not started")
	}

	_, err := stdin.Write(pkt.Data)
	return err
}

func ProbeAndExtractThumbnail(filePath, outputPath string, timeSeconds float64) error {
	cmd := exec.Command("ffmpeg",
		"-y",
		"-ss", fmt.Sprintf("%.2f", timeSeconds),
		"-i", filePath,
		"-vframes", "1",
		"-vf", "scale=320:-1",
		outputPath,
	)

	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("ffmpeg thumbnail failed: %w, output: %s", err, string(output))
	}
	return nil
}
