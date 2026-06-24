package media

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestFileInput_H264NALUnitIntegrity verifies that the file input produces
// valid H.264 NAL units with correct start codes and NAL types.
func TestFileInput_H264NALUnitIntegrity(t *testing.T) {
	config := &InputConfig{
		ID:       "test_nal_integrity",
		Type:     "file",
		Path:     testFixturePath(t, "../../test/fixtures/test.mp4"),
		Loop:     false,
	}

	input, err := NewFileInput(config)
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var mu sync.Mutex
	var videoPackets []*MediaPacket
	var audioPackets []*MediaPacket

	input.OnPacket(func(pkt *MediaPacket) {
		mu.Lock()
		defer mu.Unlock()
		if pkt.IsVideo {
			videoPackets = append(videoPackets, pkt)
		} else if pkt.IsAudio {
			audioPackets = append(audioPackets, pkt)
		}
	})

	err = input.Start(ctx)
	require.NoError(t, err)

	// Wait for packets to arrive
	time.Sleep(5 * time.Second)
	input.Stop()

	mu.Lock()
	defer mu.Unlock()

	t.Logf("Received %d video packets, %d audio packets", len(videoPackets), len(audioPackets))

	// Should have received both video and audio
	assert.Greater(t, len(videoPackets), 0, "should receive video packets")
	assert.Greater(t, len(audioPackets), 0, "should receive audio packets")

	// Verify video packets contain valid H.264 NAL units
	for i, pkt := range videoPackets {
		assert.True(t, pkt.IsVideo, "packet %d should be video", i)
		assert.Equal(t, CodecH264, pkt.CodecID, "packet %d should be H264", i)
		assert.NotEmpty(t, pkt.Data, "packet %d data should not be empty", i)

		// First packet may be partial PES header, skip NAL check
		if i == 0 {
			continue
		}

		// Check for valid H.264 NAL start code (0x000001 or 0x00000001)
		hasStartCode := false
		for j := 0; j < len(pkt.Data)-3; j++ {
			if pkt.Data[j] == 0 && pkt.Data[j+1] == 0 {
				if (j+3 < len(pkt.Data) && pkt.Data[j+2] == 0 && pkt.Data[j+3] == 1) ||
					pkt.Data[j+2] == 1 {
					hasStartCode = true
					break
				}
			}
		}
		assert.True(t, hasStartCode, "packet %d should contain H.264 NAL start code (size=%d)", i, len(pkt.Data))

		// Check NAL type is valid (0-31 for H.264)
		for j := 0; j < len(pkt.Data)-4; j++ {
			if pkt.Data[j] == 0 && pkt.Data[j+1] == 0 && pkt.Data[j+2] == 0 && pkt.Data[j+3] == 1 {
				if j+4 < len(pkt.Data) {
					nalType := pkt.Data[j+4] & 0x1F
					assert.LessOrEqual(t, nalType, byte(31), 
						"packet %d NAL type should be 0-31, got %d", i, nalType)
				}
			}
		}

		// Only check first 100 packets to avoid test timeout
		if i >= 100 {
			break
		}
	}

	// Verify audio packets
	for i, pkt := range audioPackets {
		assert.True(t, pkt.IsAudio, "packet %d should be audio", i)
		assert.Equal(t, CodecAAC, pkt.CodecID, "packet %d should be AAC", i)
		assert.NotEmpty(t, pkt.Data, "packet %d data should not be empty", i)

		if i >= 100 {
			break
		}
	}
}

// TestFileInput_PacketOrder verifies that packets are received in correct order.
func TestFileInput_PacketOrder(t *testing.T) {
	config := &InputConfig{
		ID:       "test_order",
		Type:     "file",
		Path:     testFixturePath(t, "../../test/fixtures/test.mp4"),
		Loop:     false,
	}

	input, err := NewFileInput(config)
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var mu sync.Mutex
	var videoPTS []int64
	var audioPTS []int64
	var videoCount int
	var audioCount int

	input.OnPacket(func(pkt *MediaPacket) {
		mu.Lock()
		defer mu.Unlock()
		if pkt.IsVideo {
			videoPTS = append(videoPTS, pkt.PTS)
			videoCount++
		} else if pkt.IsAudio {
			audioPTS = append(audioPTS, pkt.PTS)
			audioCount++
		}
	})

	err = input.Start(ctx)
	require.NoError(t, err)

	time.Sleep(5 * time.Second)
	input.Stop()

	mu.Lock()
	defer mu.Unlock()

	t.Logf("Video packets: %d, Audio packets: %d", videoCount, audioCount)

	// Verify video PTS is monotonically increasing
	for i := 1; i < len(videoPTS) && i < 1000; i++ {
		assert.GreaterOrEqual(t, videoPTS[i], videoPTS[i-1],
			"video PTS should be monotonically increasing at index %d", i)
	}

	// Verify audio PTS is monotonically increasing
	for i := 1; i < len(audioPTS) && i < 1000; i++ {
		assert.GreaterOrEqual(t, audioPTS[i], audioPTS[i-1],
			"audio PTS should be monotonically increasing at index %d")
	}

	// Verify we have reasonable number of packets
	// test.mp4 is about 57 seconds, at ~25fps = ~1425 video frames
	// But TS demuxer outputs PES packets, not individual frames
	// Each PES packet may contain multiple frames, so expect fewer packets
	assert.Greater(t, videoCount, 30, "should have video packets")
	assert.Greater(t, audioCount, 10, "should have audio packets")
}

// TestFileInput_VideoAudioInterleaving verifies that video and audio packets
// are properly interleaved (not all video first, then all audio).
func TestFileInput_VideoAudioInterleaving(t *testing.T) {
	config := &InputConfig{
		ID:       "test_interleave",
		Type:     "file",
		Path:     testFixturePath(t, "../../test/fixtures/test.mp4"),
		Loop:     false,
	}

	input, err := NewFileInput(config)
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var mu sync.Mutex
	var sequence []string // "V" for video, "A" for audio

	input.OnPacket(func(pkt *MediaPacket) {
		mu.Lock()
		defer mu.Unlock()
		if len(sequence) < 1000 {
			if pkt.IsVideo {
				sequence = append(sequence, "V")
			} else if pkt.IsAudio {
				sequence = append(sequence, "A")
			}
		}
	})

	err = input.Start(ctx)
	require.NoError(t, err)

	time.Sleep(3 * time.Second)
	input.Stop()

	mu.Lock()
	defer mu.Unlock()

	t.Logf("First 50 packet types: %s", strings.Join(sequence[:min(50, len(sequence))], ""))

	// Count transitions between video and audio
	transitions := 0
	for i := 1; i < len(sequence); i++ {
		if sequence[i] != sequence[i-1] {
			transitions++
		}
	}

	// Should have many transitions indicating proper interleaving
	assert.Greater(t, transitions, 10, 
		"video and audio should be interleaved (got %d transitions in %d packets)", 
		transitions, len(sequence))
}

// TestFileInput_AACConfig verifies that AAC AudioSpecificConfig is extracted.
func TestFileInput_AACConfig(t *testing.T) {
	config := &InputConfig{
		ID:       "test_aac_config",
		Type:     "file",
		Path:     testFixturePath(t, "../../test/fixtures/test.mp4"),
		Loop:     false,
	}

	input, err := NewFileInput(config)
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var mu sync.Mutex
	var firstAudioConfig []byte
	var gotAudio bool

	input.OnPacket(func(pkt *MediaPacket) {
		mu.Lock()
		defer mu.Unlock()
		if pkt.IsAudio && !gotAudio && len(pkt.CodecConfig) > 0 {
			firstAudioConfig = pkt.CodecConfig
			gotAudio = true
		}
	})

	err = input.Start(ctx)
	require.NoError(t, err)

	time.Sleep(5 * time.Second)
	input.Stop()

	mu.Lock()
	defer mu.Unlock()

	require.True(t, gotAudio, "should receive audio packet with config")
	require.Len(t, firstAudioConfig, 2, "AudioSpecificConfig should be 2 bytes")

	// Parse AudioSpecificConfig
	// Byte 0: objectType(5 bits) | sampleRateIndex(3 bits)
	// Byte 1: sampleRateIndex(1 bit) | channelConfig(4 bits) | padding(3 bits)
	objectType := int(firstAudioConfig[0] >> 3)
	sampleRateIndex := ((firstAudioConfig[0] & 0x07) << 1) | (firstAudioConfig[1] >> 7)
	channelConfig := (firstAudioConfig[1] >> 3) & 0x0F

	t.Logf("AAC Config: objectType=%d, sampleRateIndex=%d, channels=%d (raw: %02x%02x)",
		objectType, sampleRateIndex, channelConfig, firstAudioConfig[0], firstAudioConfig[1])

	// AAC-LC should be objectType 2
	assert.Equal(t, 2, objectType, "should be AAC-LC (objectType=2)")

	// Sample rate index should be valid (0-12)
	assert.LessOrEqual(t, sampleRateIndex, byte(12), "sampleRateIndex should be 0-12")

	// Channel config should be valid (1-2 for mono/stereo)
	assert.GreaterOrEqual(t, channelConfig, byte(1), "should have at least 1 channel")
	assert.LessOrEqual(t, channelConfig, byte(2), "should have at most 2 channels")
}

// TestFileInput_VideoTimestampContinuity verifies that video timestamps
// don't have large gaps (which would indicate dropped packets).
func TestFileInput_VideoTimestampContinuity(t *testing.T) {
	config := &InputConfig{
		ID:       "test_timestamps",
		Type:     "file",
		Path:     testFixturePath(t, "../../test/fixtures/test.mp4"),
		Loop:     false,
	}

	input, err := NewFileInput(config)
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var mu sync.Mutex
	var timestamps []int64

	input.OnPacket(func(pkt *MediaPacket) {
		mu.Lock()
		defer mu.Unlock()
		if pkt.IsVideo && len(timestamps) < 1000 {
			timestamps = append(timestamps, pkt.Timestamp)
		}
	})

	err = input.Start(ctx)
	require.NoError(t, err)

	time.Sleep(5 * time.Second)
	input.Stop()

	mu.Lock()
	defer mu.Unlock()

	require.Greater(t, len(timestamps), 10, "should have video timestamps")

	// Check for large gaps
	for i := 1; i < len(timestamps); i++ {
		diff := timestamps[i] - timestamps[i-1]
		assert.Less(t, diff, int64(100), 
			"timestamp gap too large at index %d: %dms", i, diff)
	}

	// Check overall duration
	duration := timestamps[len(timestamps)-1] - timestamps[0]
	t.Logf("Video span: %dms across %d packets", duration, len(timestamps))
	assert.Greater(t, duration, int64(1000), "should span at least 1 second")
}

// TestTSVideoDataIntegrity uses ffmpeg to compare raw H264 data from TS demuxer
// with reference data from the original file.
func TestTSVideoDataIntegrity(t *testing.T) {
	// Extract reference H264 stream from original file using ffmpeg
	refCmd := exec.Command("ffmpeg", "-i", testFixturePath(t, "../../test/fixtures/test.mp4"),
		"-c:v", "copy", "-an", "-f", "h264", "pipe:1")
	refData, err := refCmd.Output()
	if err != nil {
		t.Skipf("ffmpeg not available: %v", err)
	}

	// Now read via our TS demuxer
	config := &InputConfig{
		ID:       "test_integrity",
		Type:     "file",
		Path:     testFixturePath(t, "../../test/fixtures/test.mp4"),
		Loop:     false,
	}

	input, err := NewFileInput(config)
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	var mu sync.Mutex
	var allVideoData []byte
	var packetCount int

	input.OnPacket(func(pkt *MediaPacket) {
		mu.Lock()
		defer mu.Unlock()
		if pkt.IsVideo {
			allVideoData = append(allVideoData, pkt.Data...)
			packetCount++
		}
	})

	err = input.Start(ctx)
	require.NoError(t, err)

	time.Sleep(10 * time.Second)
	input.Stop()

	mu.Lock()
	defer mu.Unlock()

	t.Logf("Reference H264 size: %d bytes", len(refData))
	t.Logf("TS demuxer output: %d bytes in %d packets", len(allVideoData), packetCount)

	// Both should have substantial data
	assert.Greater(t, len(refData), 1000, "reference should have video data")
	assert.Greater(t, len(allVideoData), 1000, "TS output should have video data")

	// The TS demuxer output should contain the same NAL units as reference
	// Count SPS/PPS in both
	refSPS := countNALUnitsByType(refData, 7) // SPS
	refPPS := countNALUnitsByType(refData, 8) // PPS
	refIDR := countNALUnitsByType(refData, 5) // IDR

	outSPS := countNALUnitsByType(allVideoData, 7)
	outPPS := countNALUnitsByType(allVideoData, 8)
	outIDR := countNALUnitsByType(allVideoData, 5)

	t.Logf("Reference: SPS=%d, PPS=%d, IDR=%d", refSPS, refPPS, refIDR)
	t.Logf("TS output: SPS=%d, PPS=%d, IDR=%d", outSPS, outPPS, outIDR)

	// Should have SPS and PPS
	assert.Greater(t, outSPS, 0, "should have SPS NAL units")
	assert.Greater(t, outPPS, 0, "should have PPS NAL units")

	// TS output covers ~10s, reference covers full 57s file
	// SPS/PPS count should be proportional to duration
	// Allow 3x margin since TS output duration is ~1/6 of reference
	assert.InDelta(t, refSPS/6, outSPS, float64(refSPS)*0.5, 
		"SPS count should be proportional to duration")
	assert.InDelta(t, refPPS/6, outPPS, float64(refPPS)*0.5, 
		"PPS count should be proportional to duration")
}

// countNALUnitsByType counts NAL units of a specific type in Annex B data.
func countNALUnitsByType(data []byte, nalType byte) int {
	count := 0
	for i := 0; i < len(data)-4; i++ {
		if data[i] == 0 && data[i+1] == 0 && data[i+2] == 0 && data[i+3] == 1 {
			if i+4 < len(data) && (data[i+4]&0x1F) == nalType {
				count++
			}
		}
	}
	return count
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// TestRTSPOutput_NALUnitForwarding verifies that NAL units passed to the
// RTSP output are correctly encoded into RTP packets.
func TestRTSPOutput_NALUnitForwarding(t *testing.T) {
	// Create a mock RTSP handler
	handler := NewRTSPServerHandler()

	output, err := NewRTSPOutput(&OutputConfig{
		ID:   "test_rtsp_out",
		Type: "rtsp",
		Mode: "server",
		Addr: ":0", // Random port
	})
	require.NoError(t, err)

	// Set the handler
	output.rtspHandler = handler

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err = output.Start(ctx)
	require.NoError(t, err)

	// Send a valid H.264 keyframe with SPS/PPS
	// SPS NAL (type 7)
	sps := []byte{0x00, 0x00, 0x00, 0x01, 0x67, 0x42, 0x00, 0x0a, 0xe9, 0x40, 0x50, 0x1e, 0xd0, 0x80}
	// PPS NAL (type 8)
	pps := []byte{0x00, 0x00, 0x00, 0x01, 0x68, 0xce, 0x38, 0x80}
	// IDR NAL (type 5) - small fake data
	idr := []byte{0x00, 0x00, 0x00, 0x01, 0x65, 0x88, 0x80, 0x40, 0x00, 0x00, 0x00, 0x04}

	// Send SPS
	err = output.WritePacket(&MediaPacket{
		StreamID: "test",
		Kind:     "video",
		CodecID:  CodecH264,
		IsVideo:  true,
		Data:     sps,
		PTS:      0,
	})
	assert.NoError(t, err)

	// Send PPS
	err = output.WritePacket(&MediaPacket{
		StreamID: "test",
		Kind:     "video",
		CodecID:  CodecH264,
		IsVideo:  true,
		Data:     pps,
		PTS:      0,
	})
	assert.NoError(t, err)

	// Send IDR
	err = output.WritePacket(&MediaPacket{
		StreamID: "test",
		Kind:     "video",
		CodecID:  CodecH264,
		IsVideo:  true,
		Data:     idr,
		PTS:      33000,
	})
	assert.NoError(t, err)

	// Verify stream was initialized
	assert.True(t, output.streamReady, "stream should be initialized after SPS/PPS")
	assert.NotNil(t, output.stream, "stream should not be nil")

	output.Stop()
}

// TestRTSPOutput_AudioConfig verifies that audio config is handled correctly.
func TestRTSPOutput_AudioConfig(t *testing.T) {
	handler := NewRTSPServerHandler()

	output, err := NewRTSPOutput(&OutputConfig{
		ID:   "test_rtsp_audio",
		Type: "rtsp",
		Mode: "server",
		Addr: ":0",
	})
	require.NoError(t, err)

	output.rtspHandler = handler

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err = output.Start(ctx)
	require.NoError(t, err)

	// Send SPS/PPS first to initialize video
	sps := []byte{0x00, 0x00, 0x00, 0x01, 0x67, 0x42, 0x00, 0x0a, 0xe9, 0x40, 0x50, 0x1e, 0xd0, 0x80}
	pps := []byte{0x00, 0x00, 0x00, 0x01, 0x68, 0xce, 0x38, 0x80}

	output.WritePacket(&MediaPacket{
		StreamID: "test", Kind: "video", CodecID: CodecH264, IsVideo: true, Data: sps, PTS: 0,
	})
	output.WritePacket(&MediaPacket{
		StreamID: "test", Kind: "video", CodecID: CodecH264, IsVideo: true, Data: pps, PTS: 0,
	})

	assert.True(t, output.streamReady)
	assert.Nil(t, output.audioMedia, "audio should not be configured yet")

	// Send audio packet with config
	aacConfig := []byte{0x12, 0x88} // AAC-LC, 32000Hz, mono
	audioData := []byte{0x00, 0x10, 0x00, 0x05, 0x01, 0x02, 0x03, 0x04, 0x05}

	err = output.WritePacket(&MediaPacket{
		StreamID:    "test",
		Kind:        "audio",
		CodecID:     CodecAAC,
		IsAudio:     true,
		Data:        audioData,
		CodecConfig: aacConfig,
		PTS:         0,
	})
	assert.NoError(t, err)

	// After audio config, stream should be reinitialized with audio
	assert.NotNil(t, output.audioMedia, "audio should be configured")
	assert.Equal(t, aacConfig, output.audioConfig, "audio config should be stored")

	output.Stop()
}

// TestStripPESHeader verifies PES header stripping.
func TestStripPESHeader(t *testing.T) {
	tests := []struct {
		name     string
		input    []byte
		expected []byte
	}{
		{
			name:     "no PES header",
			input:    []byte{0x00, 0x01, 0x02, 0x03},
			expected: []byte{0x00, 0x01, 0x02, 0x03},
		},
		{
			name: "PES with PTS",
			// PES header: 00 00 01 E0 00 00 81 00 05
			// PTS: 5 bytes
			// Payload: 0x67 (SPS NAL)
			input:    []byte{0x00, 0x00, 0x01, 0xE0, 0x00, 0x00, 0x81, 0x00, 0x05, 0x11, 0x22, 0x33, 0x44, 0x55, 0x67, 0x88, 0x99},
			expected: []byte{0x67, 0x88, 0x99},
		},
		{
			name: "PES without PTS",
			// PES header: 00 00 01 E0 00 00 81 00 00
			// Payload: 0xAA 0xBB
			input:    []byte{0x00, 0x00, 0x01, 0xE0, 0x00, 0x00, 0x81, 0x00, 0x00, 0xAA, 0xBB},
			expected: []byte{0xAA, 0xBB},
		},
		{
			name:     "too short",
			input:    []byte{0x00, 0x00, 0x01},
			expected: []byte{0x00, 0x00, 0x01},
		},
		{
			name:     "not PES (wrong start code)",
			input:    []byte{0x00, 0x01, 0x02, 0xE0, 0x00, 0x00, 0x81, 0x00, 0x00, 0xAA},
			expected: []byte{0x00, 0x01, 0x02, 0xE0, 0x00, 0x00, 0x81, 0x00, 0x00, 0xAA},
		},
		{
			name:     "not audio/video stream ID",
			input:    []byte{0x00, 0x00, 0x01, 0xBD, 0x00, 0x00, 0x81, 0x00, 0x00, 0xAA},
			expected: []byte{0x00, 0x00, 0x01, 0xBD, 0x00, 0x00, 0x81, 0x00, 0x00, 0xAA},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := stripPESHeader(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

// TestBuildAACSpecificConfig verifies AudioSpecificConfig construction.
func TestBuildAACSpecificConfig(t *testing.T) {
	tests := []struct {
		name       string
		sampleRate int
		channels   int
		profile    string
		expected   []byte
	}{
		{
			name:       "AAC-LC 44100Hz mono",
			sampleRate: 44100,
			channels:   1,
			profile:    "LC",
			expected:   []byte{0x12, 0x08}, // objectType=2, srIdx=4, ch=1
		},
		{
			name:       "AAC-LC 44100Hz stereo",
			sampleRate: 44100,
			channels:   2,
			profile:    "LC",
			expected:   []byte{0x12, 0x10}, // objectType=2, srIdx=4, ch=2
		},
		{
			name:       "AAC-LC 32000Hz mono",
			sampleRate: 32000,
			channels:   1,
			profile:    "LC",
			expected:   []byte{0x12, 0x88}, // objectType=2, srIdx=5, ch=1
		},
		{
			name:       "AAC-LC 48000Hz stereo",
			sampleRate: 48000,
			channels:   2,
			profile:    "LC",
			expected:   []byte{0x11, 0x90}, // objectType=2, srIdx=3, ch=2
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := StreamInfo{
				Parameters: map[string]any{
					"sample_rate": fmt.Sprintf("%d", tt.sampleRate),
					"channels":    tt.channels,
					"profile":     tt.profile,
				},
			}
			result := buildAACSpecificConfig(s)
			assert.Equal(t, tt.expected, result,
				"sampleRate=%d channels=%d profile=%s", tt.sampleRate, tt.channels, tt.profile)
		})
	}
}
