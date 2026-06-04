package media

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestFileInput_OpenRealMP4(t *testing.T) {
	tests := []struct {
		name string
		path string
	}{
		{"H264 MP4", testFixturePath(t, "../../test/fixtures/test.mp4")},
		{"H265 MP4", testFixturePath(t, "../../test/fixtures/h265_test.mp4")},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := &InputConfig{
				ID:   "test_" + tt.name,
				Type: "file",
				Path: tt.path,
			}

			input, err := NewFileInput(config)
			assert.NoError(t, err)
			assert.NotNil(t, input)
			assert.Equal(t, StreamStatusStopped, input.Status())

			ctx := context.Background()
			err = input.Start(ctx)
			assert.NoError(t, err)
			assert.Equal(t, StreamStatusRunning, input.Status())

			err = input.Stop()
			assert.NoError(t, err)
			assert.Equal(t, StreamStatusStopped, input.Status())
		})
	}
}

func TestFileInput_ProbeStreams(t *testing.T) {
	tests := []struct {
		name         string
		path         string
		expectVideo  bool
		expectAudio  bool
		videoCodec   string
	}{
		{"H264 MP4", testFixturePath(t, "../../test/fixtures/test.mp4"), true, true, "h264"},
		{"H265 MP4", testFixturePath(t, "../../test/fixtures/h265_test.mp4"), true, false, "hevc"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := &InputConfig{
				ID:   "probe_" + tt.name,
				Type: "file",
				Path: tt.path,
			}

			input, err := NewFileInput(config)
			assert.NoError(t, err)

			ctx := context.Background()
			err = input.Start(ctx)
			assert.NoError(t, err)
			defer input.Stop()

			streams := input.GetStreams()
			assert.NotNil(t, streams)
			assert.Greater(t, len(streams), 0, "should detect at least one stream")

			hasVideo := false
			hasAudio := false
			for _, s := range streams {
				if s.Kind == "video" {
					hasVideo = true
					assert.Equal(t, tt.videoCodec, s.CodecID.FFmpegName())
				}
				if s.Kind == "audio" {
					hasAudio = true
				}
			}

			if tt.expectVideo {
				assert.True(t, hasVideo, "should detect video stream")
			}
			if tt.expectAudio {
				assert.True(t, hasAudio, "should detect audio stream")
			}
		})
	}
}

func TestFileInput_ReadPacketWhenStopped(t *testing.T) {
	config := &InputConfig{
		ID:   "test_stopped_read",
		Type: "file",
		Path: testFixturePath(t, "../../test/fixtures/h265_test.mp4"),
	}

	input, err := NewFileInput(config)
	assert.NoError(t, err)

	_, err = input.ReadPacket()
	assert.Error(t, err)
	assert.Equal(t, ErrStreamNotRunning, err)
}

func TestFileInput_DoubleStartStop(t *testing.T) {
	config := &InputConfig{
		ID:   "test_double",
		Type: "file",
		Path: testFixturePath(t, "../../test/fixtures/h265_test.mp4"),
	}

	input, err := NewFileInput(config)
	assert.NoError(t, err)

	ctx := context.Background()

	err = input.Start(ctx)
	assert.NoError(t, err)
	err = input.Start(ctx)
	assert.NoError(t, err)

	err = input.Stop()
	assert.NoError(t, err)
	err = input.Stop()
	assert.NoError(t, err)
}

func TestFileInput_NonexistentFile(t *testing.T) {
	config := &InputConfig{
		ID:   "test_nonexist",
		Type: "file",
		Path: "/nonexistent/file.mp4",
	}

	input, err := NewFileInput(config)
	assert.NoError(t, err)

	ctx := context.Background()
	err = input.Start(ctx)
	assert.Error(t, err)
	assert.Equal(t, StreamStatusError, input.Status())
}

func TestFileInput_EmptyPath(t *testing.T) {
	config := &InputConfig{
		ID:   "test_empty",
		Type: "file",
		Path: "",
	}

	input, err := NewFileInput(config)
	assert.Error(t, err)
	assert.Nil(t, input)
	assert.Equal(t, ErrInvalidConfig, err)
}

func TestFileInput_ViaEngine(t *testing.T) {
	engine := NewMediaEngine()
	ctx := context.Background()
	engine.Start(ctx)

	config := &InputConfig{
		ID:   "engine_file_input",
		Type: "file",
		Path: testFixturePath(t, "../../test/fixtures/h265_test.mp4"),
	}

	input, err := engine.CreateInput(config)
	assert.NoError(t, err)
	assert.NotNil(t, input)

	err = input.Start(ctx)
	assert.NoError(t, err)
	assert.Equal(t, StreamStatusRunning, input.Status())

	fi := input.(*FileInput)
	streams := fi.GetStreams()
	assert.Greater(t, len(streams), 0)

	err = engine.RemoveInput("engine_file_input")
	assert.NoError(t, err)
}

func TestFileInput_ProbeH264AudioVideo(t *testing.T) {
	config := &InputConfig{
		ID:   "test_h264_av",
		Type: "file",
		Path: testFixturePath(t, "../../test/fixtures/test.mp4"),
	}

	input, err := NewFileInput(config)
	assert.NoError(t, err)

	ctx := context.Background()
	err = input.Start(ctx)
	assert.NoError(t, err)
	defer input.Stop()

	streams := input.GetStreams()
	assert.GreaterOrEqual(t, len(streams), 2, "should have video and audio")

	videoFound := false
	audioFound := false
	for _, s := range streams {
		if s.Kind == "video" {
			videoFound = true
			assert.Equal(t, CodecH264, s.CodecID)
		}
		if s.Kind == "audio" {
			audioFound = true
			assert.NotEmpty(t, s.CodecName)
		}
	}
	assert.True(t, videoFound, "should find video stream")
	assert.True(t, audioFound, "should find audio stream")
}

func TestFileInput_ProbeH265VideoOnly(t *testing.T) {
	config := &InputConfig{
		ID:   "test_h265_v",
		Type: "file",
		Path: testFixturePath(t, "../../test/fixtures/h265_test.mp4"),
	}

	input, err := NewFileInput(config)
	assert.NoError(t, err)

	ctx := context.Background()
	err = input.Start(ctx)
	assert.NoError(t, err)
	defer input.Stop()

	streams := input.GetStreams()
	assert.GreaterOrEqual(t, len(streams), 1, "should have video")

	videoFound := false
	for _, s := range streams {
		if s.Kind == "video" {
			videoFound = true
			assert.Equal(t, CodecH265, s.CodecID)
			assert.Equal(t, "hevc", s.CodecID.FFmpegName())
		}
	}
	assert.True(t, videoFound, "should find H265 video stream")
}
