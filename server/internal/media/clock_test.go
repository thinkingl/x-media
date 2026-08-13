package media

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestConvertClock(t *testing.T) {
	tests := []struct {
		name      string
		pts       int64
		fromRate  int
		toRate    int
		expected  int64
	}{
		{"same rate", 90000, 90000, 90000, 90000},
		{"90k to ms", 90000, 90000, 1000, 1000},
		{"90k to 1s ms", 9000000, 90000, 1000, 100000},
		{"audio 32000 to ms", 32000, 32000, 1000, 1000},
		{"audio 32000 to 90k", 32000, 32000, 90000, 90000},
		{"fractional ms trunc", 45000, 90000, 1000, 500}, // 0.5s
		{"sub-frame ms trunc", 1000, 90000, 1000, 11},    // 1000/90 = 11.11 -> 11
		{"zero fromRate", 100, 0, 90000, 100},             // passthrough
		{"zero toRate", 100, 90000, 0, 0},
		{"zero pts", 0, 90000, 1000, 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ConvertClock(tt.pts, tt.fromRate, tt.toRate)
			assert.Equal(t, tt.expected, got)
		})
	}
}

func TestToMilliseconds(t *testing.T) {
	assert.Equal(t, int64(1000), ToMilliseconds(90000, 90000))
	assert.Equal(t, int64(2000), ToMilliseconds(64000, 32000))
}

func TestTo90k(t *testing.T) {
	assert.Equal(t, int64(90000), To90k(90000, 90000))
	// audio 1s at 32000 -> 90k ticks
	assert.Equal(t, int64(90000), To90k(32000, 32000))
	// audio 1 frame ~23.2ms at 32000: 1024 samples -> 2880 ticks
	assert.Equal(t, int64(2880), To90k(1024, 32000))
}

func TestConvertClockRounding(t *testing.T) {
	// 视频 30fps: 每帧 3000 ticks(90k), 3000/90k = 33.33ms -> 33ms
	assert.Equal(t, int64(33), ToMilliseconds(3000, 90000))
	// 整帧累计不丢帧: 33ms * 30 = 990ms(截断), 真实 1000ms
	// 验证 90k 内部累计无损
	assert.Equal(t, int64(90000), To90k(3000*30, 90000))
}
