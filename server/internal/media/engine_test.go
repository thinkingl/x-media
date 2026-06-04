package media

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/x-media/x-media-server/internal/config"
	"github.com/x-media/x-media-server/pkg/logger"
)

func testFixturePathE(t *testing.T, relPath string) string {
	t.Helper()
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("failed to get caller info")
	}
	abs, err := filepath.Abs(filepath.Join(filepath.Dir(filename), relPath))
	if err != nil {
		t.Fatalf("failed to resolve path: %v", err)
	}
	return abs
}

func TestMain(m *testing.M) {
	// 初始化日志
	cfg := &config.LogConfig{
		Level: "debug",
	}
	logger.Init(cfg)
	os.Exit(m.Run())
}

func TestDefaultMediaEngine_CreateInput(t *testing.T) {
	t.Run("成功创建文件输入", func(t *testing.T) {
		engine := NewMediaEngine()
		engine.Start(context.Background())

		config := &InputConfig{
			ID:   "input_001",
			Type: "file",
			Path: testFixturePathE(t, "../../test/fixtures/h265_test.mp4"),
		}

		input, err := engine.CreateInput(config)
		assert.NoError(t, err)
		assert.NotNil(t, input)
		assert.Equal(t, "input_001", input.ID())
		assert.Equal(t, StreamStatusStopped, input.Status())
	})

	t.Run("成功创建RTSP输入", func(t *testing.T) {
		// Arrange
		engine := NewMediaEngine()
		ctx := context.Background()
		engine.Start(ctx)

		config := &InputConfig{
			ID:        "input_002",
			Type:      "rtsp",
			URL:       "rtsp://example.com/stream",
			Transport: "tcp",
		}

		// Act
		input, err := engine.CreateInput(config)

		// Assert
		assert.NoError(t, err)
		assert.NotNil(t, input)
		assert.Equal(t, "input_002", input.ID())
	})

	t.Run("不支持的输入类型", func(t *testing.T) {
		// Arrange
		engine := NewMediaEngine()
		ctx := context.Background()
		engine.Start(ctx)

		config := &InputConfig{
			ID:   "input_003",
			Type: "unsupported",
		}

		// Act
		input, err := engine.CreateInput(config)

		// Assert
		assert.Error(t, err)
		assert.Nil(t, input)
		assert.Equal(t, ErrUnsupportedType, err)
	})
}

func TestDefaultMediaEngine_CreateOutput(t *testing.T) {
	t.Run("成功创建RTMP输出", func(t *testing.T) {
		// Arrange
		engine := NewMediaEngine()
		ctx := context.Background()
		engine.Start(ctx)

		config := &OutputConfig{
			ID:  "output_001",
			Type: "rtmp",
			URL: "rtmp://live.example.com/live/test",
		}

		// Act
		output, err := engine.CreateOutput(config)

		// Assert
		assert.NoError(t, err)
		assert.NotNil(t, output)
		assert.Equal(t, "output_001", output.ID())
		assert.Equal(t, StreamStatusStopped, output.Status())
	})

	t.Run("成功创建RTSP输出", func(t *testing.T) {
		// Arrange
		engine := NewMediaEngine()
		ctx := context.Background()
		engine.Start(ctx)

		config := &OutputConfig{
			ID:   "output_002",
			Type: "rtsp",
			Mode: "server",
			Addr: ":5544",
		}

		// Act
		output, err := engine.CreateOutput(config)

		// Assert
		assert.NoError(t, err)
		assert.NotNil(t, output)
		assert.Equal(t, "output_002", output.ID())
	})

	t.Run("成功创建HTTP-FLV输出", func(t *testing.T) {
		// Arrange
		engine := NewMediaEngine()
		ctx := context.Background()
		engine.Start(ctx)

		config := &OutputConfig{
			ID:   "output_003",
			Type: "http-flv",
			Addr: ":8080",
		}

		// Act
		output, err := engine.CreateOutput(config)

		// Assert
		assert.NoError(t, err)
		assert.NotNil(t, output)
		assert.Equal(t, "output_003", output.ID())
	})

	t.Run("不支持的输出类型", func(t *testing.T) {
		// Arrange
		engine := NewMediaEngine()
		ctx := context.Background()
		engine.Start(ctx)

		config := &OutputConfig{
			ID:   "output_004",
			Type: "unsupported",
		}

		// Act
		output, err := engine.CreateOutput(config)

		// Assert
		assert.Error(t, err)
		assert.Nil(t, output)
		assert.Equal(t, ErrUnsupportedType, err)
	})
}

func TestDefaultMediaEngine_Connect(t *testing.T) {
	t.Run("成功连接", func(t *testing.T) {
		// Arrange
		engine := NewMediaEngine()
		ctx := context.Background()
		engine.Start(ctx)

		inputConfig := &InputConfig{
			ID:   "input_001",
			Type: "file",
			Path: "/data/test.mp4",
		}

		outputConfig := &OutputConfig{
			ID:   "output_001",
			Type: "rtmp",
			URL:  "rtmp://live.example.com/live/test",
		}

		engine.CreateInput(inputConfig)
		engine.CreateOutput(outputConfig)

		// Act
		err := engine.Connect("input_001", "output_001")

		// Assert
		assert.NoError(t, err)
	})

	t.Run("输入流不存在", func(t *testing.T) {
		// Arrange
		engine := NewMediaEngine()
		ctx := context.Background()
		engine.Start(ctx)

		outputConfig := &OutputConfig{
			ID:   "output_001",
			Type: "rtmp",
			URL:  "rtmp://live.example.com/live/test",
		}

		engine.CreateOutput(outputConfig)

		// Act
		err := engine.Connect("nonexistent", "output_001")

		// Assert
		assert.Error(t, err)
		assert.Equal(t, ErrInputNotFound, err)
	})

	t.Run("输出流不存在", func(t *testing.T) {
		// Arrange
		engine := NewMediaEngine()
		ctx := context.Background()
		engine.Start(ctx)

		inputConfig := &InputConfig{
			ID:   "input_001",
			Type: "file",
			Path: "/data/test.mp4",
		}

		engine.CreateInput(inputConfig)

		// Act
		err := engine.Connect("input_001", "nonexistent")

		// Assert
		assert.Error(t, err)
		assert.Equal(t, ErrOutputNotFound, err)
	})
}

func TestDefaultMediaEngine_RemoveInput(t *testing.T) {
	t.Run("成功移除输入流", func(t *testing.T) {
		// Arrange
		engine := NewMediaEngine()
		ctx := context.Background()
		engine.Start(ctx)

		config := &InputConfig{
			ID:   "input_001",
			Type: "file",
			Path: "/data/test.mp4",
		}

		engine.CreateInput(config)

		// Act
		err := engine.RemoveInput("input_001")

		// Assert
		assert.NoError(t, err)

		// 验证已移除
		_, err = engine.GetInput("input_001")
		assert.Error(t, err)
		assert.Equal(t, ErrInputNotFound, err)
	})

	t.Run("输入流不存在", func(t *testing.T) {
		// Arrange
		engine := NewMediaEngine()
		ctx := context.Background()
		engine.Start(ctx)

		// Act
		err := engine.RemoveInput("nonexistent")

		// Assert
		assert.Error(t, err)
		assert.Equal(t, ErrInputNotFound, err)
	})
}

func TestDefaultMediaEngine_RemoveOutput(t *testing.T) {
	t.Run("成功移除输出流", func(t *testing.T) {
		// Arrange
		engine := NewMediaEngine()
		ctx := context.Background()
		engine.Start(ctx)

		config := &OutputConfig{
			ID:   "output_001",
			Type: "rtmp",
			URL:  "rtmp://live.example.com/live/test",
		}

		engine.CreateOutput(config)

		// Act
		err := engine.RemoveOutput("output_001")

		// Assert
		assert.NoError(t, err)

		// 验证已移除
		_, err = engine.GetOutput("output_001")
		assert.Error(t, err)
		assert.Equal(t, ErrOutputNotFound, err)
	})

	t.Run("输出流不存在", func(t *testing.T) {
		// Arrange
		engine := NewMediaEngine()
		ctx := context.Background()
		engine.Start(ctx)

		// Act
		err := engine.RemoveOutput("nonexistent")

		// Assert
		assert.Error(t, err)
		assert.Equal(t, ErrOutputNotFound, err)
	})
}

func TestFileInput_StartStop(t *testing.T) {
	t.Run("启动和停止文件输入", func(t *testing.T) {
		// Arrange
		config := &InputConfig{
			ID:   "file_input_001",
			Type: "file",
			Path: testFixturePathE(t, "../../test/fixtures/test.mp4"),
			Loop: true,
		}

		input, err := NewFileInput(config)
		assert.NoError(t, err)

		ctx := context.Background()

		// Act - 启动
		err = input.Start(ctx)

		// Assert
		assert.NoError(t, err)
		assert.Equal(t, StreamStatusRunning, input.Status())

		// Act - 停止
		err = input.Stop()

		// Assert
		assert.NoError(t, err)
		assert.Equal(t, StreamStatusStopped, input.Status())
	})
}

func TestRTSPInput_StartStop(t *testing.T) {
	t.Run("启动RTSP输入_不可达URL", func(t *testing.T) {
		config := &InputConfig{
			ID:        "rtsp_input_001",
			Type:      "rtsp",
			URL:       "rtsp://example.com/stream",
			Transport: "tcp",
		}

		input, err := NewRTSPInput(config)
		assert.NoError(t, err)

		ctx := context.Background()

		err = input.Start(ctx)
		assert.Error(t, err, "should fail for unreachable RTSP URL")
		assert.Equal(t, StreamStatusError, input.Status())
	})

	t.Run("RTSP输入_空URL", func(t *testing.T) {
		config := &InputConfig{
			ID:   "rtsp_input_002",
			Type: "rtsp",
			URL:  "",
		}

		input, err := NewRTSPInput(config)
		assert.Error(t, err)
		assert.Nil(t, input)
	})
}

func TestRTMPOutput_StartStop(t *testing.T) {
	t.Run("启动和停止RTMP输出", func(t *testing.T) {
		// Arrange
		config := &OutputConfig{
			ID:   "rtmp_output_001",
			Type: "rtmp",
			URL:  "rtmp://live.example.com/live/test",
		}

		output, err := NewRTMPOutput(config)
		assert.NoError(t, err)

		ctx := context.Background()

		// Act - 启动
		err = output.Start(ctx)

		// Assert
		assert.NoError(t, err)
		assert.Equal(t, StreamStatusRunning, output.Status())

		// Act - 停止
		err = output.Stop()

		// Assert
		assert.NoError(t, err)
		assert.Equal(t, StreamStatusStopped, output.Status())
	})
}
