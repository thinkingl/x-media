package media

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestMediaHub_CreateInputIdempotent 同 ID 重复 CreateInput 应返回同一实例，
// 避免旧实例 readLoop 泄漏（旧实例持续读文件并 emit 造成 CPU 争用与重复帧）。
func TestMediaHub_CreateInputIdempotent(t *testing.T) {
	h := NewMediaHub()
	cfg := &InputConfig{ID: "in_" + t.Name(), Type: "file", Path: testFixturePath(t, "../../test/fixtures/test.mp4")}

	src1, err := h.CreateInput(cfg)
	require.NoError(t, err)
	src2, err := h.CreateInput(cfg)
	require.NoError(t, err)

	assert.Same(t, src1, src2, "CreateInput 应幂等返回同一实例")
	assert.Equal(t, 1, len(h.sources), "不应创建重复 source")
}

// TestMediaHub_CreateOutputIdempotent 同 ID 重复 CreateOutput 应返回同一实例。
func TestMediaHub_CreateOutputIdempotent(t *testing.T) {
	h := NewMediaHub()
	cfg := &OutputConfig{ID: "out_" + t.Name(), Type: "rtsp", Mode: "server", Addr: "127.0.0.1:0"}

	sink1, err := h.CreateOutput(cfg)
	require.NoError(t, err)
	sink2, err := h.CreateOutput(cfg)
	require.NoError(t, err)

	assert.Same(t, sink1, sink2, "CreateOutput 应幂等返回同一实例")
	assert.Equal(t, 1, len(h.sinks), "不应创建重复 sink")
}
