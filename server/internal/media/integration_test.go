package media

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestHTTPFLVSink_EndToEndHTTP 真实 MP4Source → Pipe → HTTPFLVSink，经 HTTP 拉流校验 FLV header。
func TestHTTPFLVSink_EndToEndHTTP(t *testing.T) {
	sink, err := NewHTTPFLVSink(&OutputConfig{
		ID:   "flv_e2e_" + t.Name(),
		Type: "http-flv",
		Addr: ":0",
	})
	require.NoError(t, err)
	require.NoError(t, sink.Start(context.Background()))
	defer sink.Stop()

	src, err := NewMP4Source(&InputConfig{
		ID:   "src_" + t.Name(),
		Type: "file",
		Path: testFixturePath(t, "../../test/fixtures/test.mp4"),
	})
	require.NoError(t, err)

	pipe := NewDefaultPipe(256)
	require.NoError(t, pipe.Bind(src, sink))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	require.NoError(t, src.Start(ctx))
	require.NoError(t, pipe.Start(ctx))
	defer pipe.Stop()

	// sink 应已 Configure（含 FLV header + sequence header）
	waitCond(t, 3*time.Second, func() bool {
		return sink.ready
	}, "sink configured")

	mux := http.NewServeMux()
	mux.Handle(sink.GetRoutePath(), sink)
	server := httptest.NewServer(mux)
	defer server.Close()

	resp, err := http.Get(server.URL + sink.GetRoutePath())
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, "video/x-flv", resp.Header.Get("Content-Type"))

	buf := make([]byte, 4096)
	n, err := io.ReadAtLeast(resp.Body, buf, 13)
	assert.NoError(t, err, "should read at least FLV header (13 bytes)")
	assert.GreaterOrEqual(t, n, 13)
	assert.Equal(t, byte(0x46), buf[0], "FLV magic 'F'")
	assert.Equal(t, byte(0x4C), buf[1], "FLV magic 'L'")
	assert.Equal(t, byte(0x56), buf[2], "FLV magic 'V'")

	// 等待收到实际帧数据（超过 header 长度）
	waitCond(t, 5*time.Second, func() bool {
		extra := make([]byte, 4096)
		rn, err := resp.Body.Read(extra)
		return err == nil && rn > 0
	}, "received media data")
}

// TestHTTPFLVSink_MultipleClients 多客户端同时拉流。
func TestHTTPFLVSink_MultipleClients(t *testing.T) {
	sink, err := NewHTTPFLVSink(&OutputConfig{
		ID:   "flv_multi_" + t.Name(),
		Type: "http-flv",
		Addr: ":0",
	})
	require.NoError(t, err)
	require.NoError(t, sink.Start(context.Background()))
	defer sink.Stop()

	src := NewMockSource("multi_src", flvTestStreams())
	pipe := NewDefaultPipe(256)
	require.NoError(t, pipe.Bind(src, sink))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	require.NoError(t, pipe.Start(ctx))
	defer pipe.Stop()

	waitCond(t, 3*time.Second, func() bool {
		return sink.ready
	}, "sink configured")

	mux := http.NewServeMux()
	mux.Handle(sink.GetRoutePath(), sink)
	server := httptest.NewServer(mux)
	defer server.Close()

	done := make(chan bool, 2)
	for i := 0; i < 2; i++ {
		go func() {
			resp, err := http.Get(server.URL + sink.GetRoutePath())
			if err != nil {
				done <- false
				return
			}
			defer resp.Body.Close()
			buf := make([]byte, 13)
			_, err = io.ReadAtLeast(resp.Body, buf, 13)
			done <- err == nil && buf[0] == 0x46
		}()
	}

	success := 0
	for i := 0; i < 2; i++ {
		select {
		case ok := <-done:
			if ok {
				success++
			}
		case <-time.After(10 * time.Second):
			t.Fatal("timeout waiting for client")
		}
	}
	assert.Equal(t, 2, success, "both clients should receive FLV data")
}

// TestL3_MP4ToHTTPFLV 真实 MP4Source → Pipe → HTTPFLVSink，离线端到端（flv 内存校验）。
func TestL3_MP4ToHTTPFLV(t *testing.T) {
	sink, err := NewHTTPFLVSink(&OutputConfig{ID: "l3_flv_" + t.Name(), Type: "http-flv", Addr: ":0"})
	require.NoError(t, err)
	require.NoError(t, sink.Start(context.Background()))
	defer sink.Stop()

	src, err := NewMP4Source(&InputConfig{
		ID:   "l3_src_" + t.Name(),
		Type: "file",
		Path: testFixturePath(t, "../../test/fixtures/test.mp4"),
	})
	require.NoError(t, err)

	pipe := NewDefaultPipe(512)
	require.NoError(t, pipe.Bind(src, sink))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	require.NoError(t, src.Start(ctx))
	require.NoError(t, pipe.Start(ctx))
	defer pipe.Stop()

	// 等 3s 收帧
	waitCond(t, 5*time.Second, func() bool {
		return pipe.Written() >= 60
	}, "60 frames written")

	// 校验 FLV header（在 prefix 中）
	sink.mu.RLock()
	prefix := append([]byte{}, sink.prefix...)
	sink.mu.RUnlock()
	require.GreaterOrEqual(t, len(prefix), 13)
	assert.Equal(t, byte(0x46), prefix[0])
	assert.Equal(t, byte(0x4C), prefix[1])

	// 从 ring 读 FLV 数据 tag 并校验结构
	sink.ring.mu.Lock()
	n := sink.ring.availableLocked()
	raw := make([]byte, n)
	i := sink.ring.head
	for j := 0; j < n; j++ {
		raw[j] = sink.ring.buf[i]
		i = (i + 1) % len(sink.ring.buf)
	}
	sink.ring.mu.Unlock()

	// 扫描 tag：应含 video tag (9) 和 audio tag (8)
	var hasVideoTag, hasAudioTag bool
	pos := 0
	for pos+11 <= len(raw) {
		tagType := raw[pos]
		dataSize := int(raw[pos+1])<<16 | int(raw[pos+2])<<8 | int(raw[pos+3])
		if pos+11+dataSize > len(raw) {
			break
		}
		if tagType == 9 {
			hasVideoTag = true
		} else if tagType == 8 {
			hasAudioTag = true
		}
		pos += 11 + dataSize + 4
	}
	assert.True(t, hasVideoTag, "should have video tags")
	assert.True(t, hasAudioTag, "should have audio tags")
}

// TestL3_MP4RelayToRTSP 真实 MP4Source → RTSPSink → RTSPInput 拉流（MP4→RTSP→RTSP 中继）。
func TestL3_MP4RelayToRTSP(t *testing.T) {
	// RTSP server sink
	sink, err := NewRTSPSink(&OutputConfig{
		ID:   "l3_rtsp_server_" + t.Name(),
		Type: "rtsp",
		Mode: "server",
		Addr: "127.0.0.1:0",
	})
	require.NoError(t, err)
	require.NoError(t, sink.Start(context.Background()))
	defer sink.Stop()

	// MP4 source → pipe → RTSP sink
	src, err := NewMP4Source(&InputConfig{
		ID:   "l3_mp4_" + t.Name(),
		Type: "file",
		Path: testFixturePath(t, "../../test/fixtures/test.mp4"),
	})
	require.NoError(t, err)
	upPipe := NewDefaultPipe(512)
	require.NoError(t, upPipe.Bind(src, sink))

	// RTSP input 拉流 → pipe → mock sink
	input, err := NewRTSPInput(&InputConfig{
		ID:   "l3_relay_" + t.Name(),
		Type: "rtsp",
		URL:  "rtsp://" + sinkListenerAddr(sink) + "/live/" + sink.ID(),
	})
	require.NoError(t, err)
	recv := NewMockSink("l3_recv_" + t.Name())
	downPipe := NewDefaultPipe(512)
	require.NoError(t, downPipe.Bind(input, recv))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	require.NoError(t, src.Start(ctx))
	require.NoError(t, sink.Start(ctx)) // 已启动
	require.NoError(t, upPipe.Start(ctx))
	defer upPipe.Stop()

	require.NoError(t, input.Start(ctx))
	require.NoError(t, downPipe.Start(ctx))
	defer downPipe.Stop()

	// 等待中继帧到达
	waitCond(t, 10*time.Second, func() bool {
		return recv.FrameCount() >= 5
	}, "relayed frames from MP4 via RTSP")

	frames := recv.Frames()
	assert.GreaterOrEqual(t, len(frames), 5, "should relay multiple frames")
	for _, f := range frames {
		assert.Equal(t, FrameTypeVideo, f.Header.FrameType)
		assert.Equal(t, CodecH264, f.Header.Codec)
		assert.NotEmpty(t, f.Payload)
	}
	t.Logf("MP4→RTSP→RTSP relayed %d frames", len(frames))
}
