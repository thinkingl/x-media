//go:build e2e

// Package e2e 基准参照式端到端测试：
// 真实 MP4Source → DefaultPipe → RTSPSink，ffmpeg 拉流解码，
// 解码结果与生成器写入的基准（metadata.json + 内容编码）逐帧比对。
package e2e

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/x-media/x-media-server/internal/media"
	"github.com/x-media/x-media-server/test/e2e/ref"
	"github.com/x-media/x-media-server/test/e2e/verify"
)

const (
	fixtureDir = "../fixtures"
	scenario   = "ref_1080p_h264_30fps_10s"
)

func requireFFmpeg(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not installed")
	}
}

func fixturePath(name string) string {
	return filepath.Join(fixtureDir, scenario+"."+name)
}

func loadMeta(t *testing.T) *ref.Metadata {
	t.Helper()
	b, err := os.ReadFile(fixturePath("metadata.json"))
	if err != nil {
		t.Fatalf("read metadata: %v", err)
	}
	m, err := ref.LoadMetadata(b)
	if err != nil {
		t.Fatalf("parse metadata: %v", err)
	}
	return m
}

// pipeline 进程内真实链路：MP4Source → DefaultPipe → RTSPSink。
type pipeline struct {
	src    *media.MP4Source
	pipe   *media.DefaultPipe
	sink   *media.RTSPSink
	cancel context.CancelFunc
}

func startPipeline(t *testing.T, loop bool) *pipeline {
	t.Helper()
	sink, err := media.NewRTSPSink(&media.OutputConfig{
		ID:   "e2e_rtsp_" + t.Name(),
		Type: "rtsp",
		Mode: "server",
		Addr: "127.0.0.1:0",
	})
	if err != nil {
		t.Fatalf("new rtsp sink: %v", err)
	}
	src, err := media.NewMP4Source(&media.InputConfig{
		ID:   "e2e_src_" + t.Name(),
		Type: "file",
		Path: fixturePath("mp4"),
		Loop: loop,
	})
	if err != nil {
		t.Fatalf("new mp4 source: %v", err)
	}
	pipe := media.NewDefaultPipe(1024)
	if err := pipe.Bind(src, sink); err != nil {
		t.Fatalf("bind: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	if err := sink.Start(ctx); err != nil {
		t.Fatalf("sink start: %v", err)
	}
	if err := src.Start(ctx); err != nil {
		t.Fatalf("source start: %v", err)
	}
	if err := pipe.Start(ctx); err != nil {
		t.Fatalf("pipe start: %v", err)
	}

	// 等帧开始流动（sink 完成 Configure 注册 RTSP 路径）
	deadline := time.Now().Add(5 * time.Second)
	for pipe.Written() == 0 {
		if time.Now().After(deadline) {
			t.Fatal("pipeline not producing frames")
		}
		time.Sleep(20 * time.Millisecond)
	}
	time.Sleep(150 * time.Millisecond) // 让首 GOP 落到 sink 缓存，ffmpeg join 更干净

	p := &pipeline{src: src, pipe: pipe, sink: sink, cancel: cancel}
	t.Cleanup(p.close)
	return p
}

func (p *pipeline) close() {
	p.cancel()
	_ = p.src.Stop()
	_ = p.pipe.Stop()
	_ = p.sink.Stop()
}

func (p *pipeline) rtspURL() string {
	return "rtsp://" + p.sink.Addr() + "/live/" + p.sink.ID()
}

// runFFmpeg 运行 ffmpeg 拉流，返回 stdout 字节。
func runFFmpeg(t *testing.T, args []string, timeout time.Duration) []byte {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "ffmpeg", args...)
	var out bytes.Buffer
	var errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	if err := cmd.Run(); err != nil {
		// 拉流结束/中断属于预期；仅记录 stderr 用于诊断
		t.Logf("ffmpeg exited %v; stderr(截断): %s", err, truncateStderr(errb.String()))
	}
	return out.Bytes()
}

func truncateStderr(s string) string {
	if len(s) > 1500 {
		return s[:1500] + "..."
	}
	return s
}

func TestE2E_RTSP_VideoIntegrity(t *testing.T) {
	requireFFmpeg(t)
	meta := loadMeta(t)

	p := startPipeline(t, true)
	args := []string{
		"-rtsp_transport", "tcp",
		"-i", p.rtspURL(),
		"-t", "10",
		"-an",
		"-vf", "crop=720:64:0:0,format=gray",
		"-pix_fmt", "gray",
		"-f", "rawvideo", "-",
	}
	data := runFFmpeg(t, args, 60*time.Second)

	crop := meta.Video.Crop
	frameSize := crop.W * crop.H
	if len(data) < frameSize {
		t.Fatalf("ffmpeg 输出不足一帧 (%d bytes, 需要 %d)", len(data), frameSize)
	}
	frames := make([]verify.VideoFrame, 0, len(data)/frameSize)
	for off := 0; off+frameSize <= len(data); off += frameSize {
		frames = append(frames, verify.DecodeVideoFrame(data[off:off+frameSize], meta))
	}

	rep := verify.VerifyVideo(frames, meta, true)
	t.Logf("视频报告: 解码=%d 有效=%d 损坏=%d join遗漏=%d 丢帧=%d 重复=%d 主色不符=%d 首帧=%d 末帧=%d",
		rep.TotalDecoded, rep.OKFrames, rep.CorruptFrames, rep.JoinMissed,
		rep.LostFrames, rep.DuplicateFrames, rep.BgMismatch, rep.FirstOKFrame, rep.LastOKFrame)

	if !rep.Pass {
		t.Fatalf("视频序列校验失败: 丢帧=%d 重复=%d", rep.LostFrames, rep.DuplicateFrames)
	}
	if rep.OKFrames < 200 {
		t.Fatalf("有效帧过少: %d (应 >=200)", rep.OKFrames)
	}
	if rep.CorruptFrames > 10 {
		t.Fatalf("损坏帧过多: %d (应 <=10)", rep.CorruptFrames)
	}
}

func TestE2E_RTSP_AudioIntegrity(t *testing.T) {
	requireFFmpeg(t)
	meta := loadMeta(t)

	p := startPipeline(t, true)
	args := []string{
		"-rtsp_transport", "tcp",
		"-i", p.rtspURL(),
		"-t", "4",
		"-vn",
		"-ar", "48000", "-ac", "1",
		"-c:a", "pcm_s16le",
		"-f", "s16le", "-",
	}
	data := runFFmpeg(t, args, 40*time.Second)

	chunkBytes := meta.Audio.ChunkSize * meta.Audio.Channels * 2
	if len(data) < chunkBytes {
		t.Fatalf("ffmpeg 音频输出不足一块 (%d bytes)", len(data))
	}
	chunks := make([]verify.AudioChunk, 0, len(data)/chunkBytes)
	for off := 0; off+chunkBytes <= len(data); off += chunkBytes {
		samples := s16leToInt16(data[off : off+chunkBytes])
		chunks = append(chunks, verify.DecodeAudioChunk(samples))
	}

	rep := verify.VerifyAudio(chunks)
	t.Logf("音频报告: 解码块=%d 有效=%d 静音/无效=%d 丢块=%d 重复块=%d",
		rep.Chunks, rep.ValidChunks, rep.SilentChunks, rep.LostChunks, rep.DupChunks)

	if !rep.Pass {
		t.Fatalf("音频序列校验失败: 丢块=%d 重复块=%d", rep.LostChunks, rep.DupChunks)
	}
	if rep.ValidChunks < 80 {
		t.Fatalf("有效音频块过少: %d (应 >=80)", rep.ValidChunks)
	}
}

func s16leToInt16(b []byte) []int16 {
	out := make([]int16, len(b)/2)
	for i := range out {
		out[i] = int16(b[i*2]) | int16(b[i*2+1])<<8
	}
	return out
}
