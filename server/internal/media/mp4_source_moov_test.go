package media

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/Eyevinn/mp4ff/mp4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ftypOnlyMP4 仅含 ftyp box 的合法 MP4 前缀。
// 实测 mp4ff 对其 DecodeFile 返回 err==nil 且 Moov==nil ——
// 下游若直接解引用 Moov 即 nil panic（外部上传/探测可控输入）。
func ftypOnlyMP4() []byte {
	return []byte{
		0, 0, 0, 20, 'f', 't', 'y', 'p',
		'i', 's', 'o', 'm', 0, 0, 2, 0,
		'i', 's', 'o', 'm',
	}
}

// TestBuildMP4TracksRejectsMissingMoov 复现无 moov 文件触发的 panic。
func TestBuildMP4TracksRejectsMissingMoov(t *testing.T) {
	mp4f, err := mp4.DecodeFile(bytes.NewReader(ftypOnlyMP4()), mp4.WithDecodeMode(mp4.DecModeLazyMdat))
	require.NoError(t, err, "ftyp-only 文件 DecodeFile 应成功（实测行为）")
	require.Nil(t, mp4f.Moov)

	var streams []StreamInfo
	var tracks []*mp4Track
	require.NotPanics(t, func() {
		streams, tracks, err = buildMP4Tracks(mp4f)
	}, "Moov 为 nil 时不得 panic")
	assert.Error(t, err)
	assert.Nil(t, streams)
	assert.Nil(t, tracks)
}

// TestAnalyzeTimestampStatsMissingMoov 复现探测路径（上传/探测接口）的 panic。
func TestAnalyzeTimestampStatsMissingMoov(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nomov.mp4")
	require.NoError(t, os.WriteFile(path, ftypOnlyMP4(), 0644))

	var stats []TimestampDeltaStats
	require.NotPanics(t, func() {
		stats = AnalyzeMP4TimestampStats(path)
	}, "无 moov 文件统计不得 panic")
	assert.Nil(t, stats)
}

// TestSeekTrackWithoutStts 复现 grid 模式下 Stts==nil track 的 seek panic：
// buildMP4Tracks 在 gridEnabled 时允许无 Stts 的 track 存活（mp4_source.go:399）。
func TestSeekTrackWithoutStts(t *testing.T) {
	tr := &mp4Track{info: StreamInfo{ChannelID: 0}}
	tr.trak = &mp4.TrakBox{
		Mdia: &mp4.MdiaBox{
			Minf: &mp4.MinfBox{Stbl: &mp4.StblBox{}}, // Stts == nil
		},
	}
	m := &MP4Source{tracks: []*mp4Track{tr}}

	require.NotPanics(t, func() {
		m.seek(0, 1000)
	}, "Stts 为 nil 的 track seek 不得 panic")
	assert.Equal(t, uint32(1), tr.sampleNr, "无 Stts 时应回退到样本 1")
}
