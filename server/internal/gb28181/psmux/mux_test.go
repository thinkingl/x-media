package psmux

import (
	"bytes"
	"testing"
)

// packHeaderSCR 解析 PS pack header 的 SCR base（33-bit，27MHz）。
func packHeaderSCR(ps []byte) (int64, bool) {
	for i := 0; i+14 <= len(ps); i++ {
		if ps[i] == 0 && ps[i+1] == 0 && ps[i+2] == 1 && ps[i+3] == 0xBA {
			b4, b5, b6, b7, b8 := ps[i+4], ps[i+5], ps[i+6], ps[i+7], ps[i+8]
			scr := int64((b4>>3)&0x07)<<30 |
				int64(b4&0x03)<<28 |
				int64(b5)<<20 |
				int64((b6>>3)&0x1F)<<15 |
				int64(b6&0x03)<<13 |
				int64(b7)<<5 |
				int64((b8>>3)&0x1F)
			return scr, true
		}
	}
	return 0, false
}

// TestPackHeaderSCRMonotonicWithBFrames B 帧源（PTS 非单调、DTS 单调）时，
// PS pack header 的 SCR 必须按解码时间（DTS）单调递增——MPEG-PS 的系统时钟
// 不允许回退；用 PTS 生成 SCR 会让接收端按错误时钟抖动/回退。
func TestPackHeaderSCRMonotonicWithBFrames(t *testing.T) {
	m := New()
	m.SetVideoCodec("h264")
	au := []byte{0x00, 0x00, 0x00, 0x01, 0x65, 0x11, 0x22}

	f := int64(3600) // 25fps @ 90kHz
	// 解码序：I0 P3 B1 B2 P6 B4 B5（PTS 非单调、DTS 单调）
	frames := []struct{ pts, dts int64 }{
		{0, 0},
		{3 * f, 1 * f},
		{1 * f, 2 * f},
		{2 * f, 3 * f},
		{6 * f, 4 * f},
		{4 * f, 5 * f},
		{5 * f, 6 * f},
	}

	var scrs []int64
	for _, fr := range frames {
		out := m.WriteAU(au, fr.pts, fr.dts, fr.dts == 0)
		scr, ok := packHeaderSCR(out)
		if !ok {
			t.Fatalf("应包含 PS pack header")
		}
		scrs = append(scrs, scr)
	}
	for i := 1; i < len(scrs); i++ {
		if scrs[i] < scrs[i-1] {
			t.Fatalf("SCR 必须单调不减（DTS 推导），index %d: %d -> %d (all=%v)",
				i, scrs[i-1], scrs[i], scrs)
		}
	}
}

// TestWriteAUPSMOnEveryIDR GB/T 28181 附录 C：系统头 + PSM 属于关键帧 PS 包
// 结构（非关键帧一般不带）。每个 IDR 都应携带 PSM，而不能只在会话首个 IDR
// 发一次——否则平台在后续关键帧重新同步时拿不到 stream_type/PSM。
func TestWriteAUPSMOnEveryIDR(t *testing.T) {
	m := New()
	m.SetVideoCodec("h265")
	au := []byte{0x00, 0x00, 0x00, 0x01, 0x26, 0x01, 0xAA}

	for i := 0; i < 3; i++ {
		out := m.WriteAU(au, int64(i*3600), int64(i*3600), true)
		if n := bytes.Count(out, []byte{0x00, 0x00, 0x01, 0xBC}); n != 1 {
			t.Fatalf("第 %d 个 IDR 应携带 1 个 PSM，实际 %d 个", i+1, n)
		}
		if n := bytes.Count(out, []byte{0x00, 0x00, 0x01, 0xBB}); n != 1 {
			t.Fatalf("第 %d 个 IDR 应携带 1 个 system header，实际 %d 个", i+1, n)
		}
	}
}
