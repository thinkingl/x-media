package device

import (
	"bytes"
	"testing"
)

// TestAnnexBFromNALUsUsesFourByteStartCodes 科达 KDM 的 H.265 I 帧判定
// （CPSFrameParser::IsH265IFrame）扫描 NAL 时只识别 4 字节起始码
// 00 00 00 01；若 SPS/PPS 使用 3 字节起始码，平台只识别到 VPS，
// VPS && SPS 条件不成立 → IDR 不被判为 I 帧 → 平台报 No stream received。
//
// 因此实时媒体路径（annexBFromNALUs）必须对**每个** NAL 都输出 4 字节起始码。
func TestAnnexBFromNALUsUsesFourByteStartCodes(t *testing.T) {
	nals := []NALU{
		{Data: []byte{0x40, 0x01, 0xAA}}, // VPS (32)
		{Data: []byte{0x42, 0x01, 0xBB}}, // SPS (33)
		{Data: []byte{0x44, 0x01, 0xCC}}, // PPS (34)
		{Data: []byte{0x26, 0x01, 0xDD}}, // IDR (19)
	}
	out := annexBFromNALUs(nals)

	if got := bytes.Count(out, []byte{0x00, 0x00, 0x00, 0x01}); got != len(nals) {
		t.Fatalf("4 字节起始码数量 = %d，期望 %d（每个 NAL 都必须是 00 00 00 01）: % x",
			got, len(nals), out)
	}
	if got := bytes.Count(out, []byte{0x00, 0x00, 0x01}); got != len(nals) {
		t.Fatalf("起始码总数 = %d，期望 %d", got, len(nals))
	}
	// 每个 NAL 前必须是 4 字节起始码
	for i, n := range nals {
		marker := append([]byte{0x00, 0x00, 0x00, 0x01}, n.Data...)
		if !bytes.Contains(out, marker) {
			t.Fatalf("第 %d 个 NAL 前缺少 4 字节起始码: % x", i, n.Data)
		}
	}
}
