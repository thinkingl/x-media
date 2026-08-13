package gen

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/x-media/x-media-server/test/e2e/ref"
	"github.com/x-media/x-media-server/test/e2e/verify"
)

func testConfig() Config {
	c := Config{Width: 640, Height: 360, FPS: 30, Frames: 60, WithAudio: true}
	c.fillDefaults()
	return c
}

// TestRenderFrameDeterministic 渲染确定性：同帧两次渲染字节一致。
func TestRenderFrameDeterministic(t *testing.T) {
	c := testConfig()
	a := c.RenderFrame(42)
	b := c.RenderFrame(42)
	require.Equal(t, len(a), c.Width*c.Height*3)
	assert.True(t, bytes.Equal(a, b), "frame render must be deterministic")
}

// TestBarcodeRoundTrip 条码自校验：渲染帧解码出的帧号与输入一致。
func TestBarcodeRoundTrip(t *testing.T) {
	c := testConfig()
	meta := c.Metadata()
	for _, n := range []int{1, 2, 61, 300, 4095} {
		frame := c.RenderFrame(n)
		// 构造 crop 区域灰度（取条码+背景区）
		gray := toGrayCrop(frame, c.Width, meta)
		f := verify.DecodeVideoFrame(gray, meta)
		assert.True(t, f.OK, "frame %d should decode", n)
		assert.Equal(t, n, f.FrameNo, "frame number round trip")
	}
}

// TestRenderAudioDeterministic 音频渲染确定性。
func TestRenderAudioDeterministic(t *testing.T) {
	c := testConfig()
	a := c.RenderAudio()
	b := c.RenderAudio()
	assert.True(t, bytes.Equal(a, b), "audio render must be deterministic")
}

// TestAudioDTMFDecodable 音频 DTMF 可解码：每块半字节 == 块号 % 16。
func TestAudioDTMFDecodable(t *testing.T) {
	c := testConfig()
	pcm := c.RenderAudio()
	samples := int16s(pcm)
	require.GreaterOrEqual(t, len(samples), c.ChunkSize*16)
	for ch := 0; ch < 16; ch++ {
		seg := samples[ch*c.ChunkSize : (ch+1)*c.ChunkSize]
		got := verify.DecodeAudioChunk(seg)
		assert.True(t, got.OK, "chunk %d should have DTMF", ch)
		assert.Equal(t, ch%16, got.Nibble, "chunk %d nibble", ch)
	}
}

func toGrayCrop(rgb []byte, width int, meta *ref.Metadata) []byte {
	v := meta.Video
	crop := v.Crop
	out := make([]byte, crop.W*crop.H)
	for y := 0; y < crop.H; y++ {
		for x := 0; x < crop.W; x++ {
			src := (y*width + x) * 3
			out[y*crop.W+x] = rgb[src]
		}
	}
	return out
}

func int16s(b []byte) []int16 {
	out := make([]int16, len(b)/2)
	for i := range out {
		out[i] = int16(b[i*2]) | int16(b[i*2+1])<<8
	}
	return out
}
