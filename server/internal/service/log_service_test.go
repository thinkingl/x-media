package service

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/x-media/x-media-server/internal/config"
)

func newLogTestService(t *testing.T) (*LogService, string) {
	t.Helper()
	filename := filepath.Join(t.TempDir(), "x-media.log")
	return NewLogService(&config.LogConfig{Filename: filename}), filename
}

func writeLogLines(t *testing.T, filename string, lines int) {
	t.Helper()
	var sb strings.Builder
	for i := 0; i < lines; i++ {
		sb.WriteString(`{"level":"info","message":"line `)
		sb.WriteString(strconv.Itoa(i))
		sb.WriteString(`","timestamp":"2026-01-01T00:00:00Z"}`)
		sb.WriteByte('\n')
	}
	require.NoError(t, os.WriteFile(filename, []byte(sb.String()), 0644))
}

// TestGetLogsReturnsTail 验证尾部读取语义：只返回最后 N 行且顺序正确。
func TestGetLogsReturnsTail(t *testing.T) {
	svc, filename := newLogTestService(t)
	writeLogLines(t, filename, 5000)

	logs, err := svc.GetLogs(3)
	require.NoError(t, err)
	require.Len(t, logs, 3)
	assert.Contains(t, logs[0], "line 4997")
	assert.Contains(t, logs[1], "line 4998")
	assert.Contains(t, logs[2], "line 4999")
}

// TestGetLogsHandlesOversizedLine 复现超长行导致接口 500：
// bufio.Scanner 默认 64KB token 上限，任一行超长即整个请求失败。
func TestGetLogsHandlesOversizedLine(t *testing.T) {
	svc, filename := newLogTestService(t)
	content := `{"level":"info","message":"before","timestamp":"t1"}` + "\n" +
		`{"level":"info","message":"` + strings.Repeat("x", 200*1024) + `","timestamp":"t2"}` + "\n" +
		`{"level":"info","message":"after","timestamp":"t3"}` + "\n"
	require.NoError(t, os.WriteFile(filename, []byte(content), 0644))

	logs, err := svc.GetLogs(10)
	require.NoError(t, err, "超长行不得导致整个日志接口失败")
	require.NotEmpty(t, logs)
	assert.Contains(t, logs[len(logs)-1], "after", "超长行之后的正常行必须可见")
}

// TestGetLogsEmptyAndMissing 文件为空/不存在时应返回空列表而非错误。
func TestGetLogsEmptyAndMissing(t *testing.T) {
	svc, filename := newLogTestService(t)

	logs, err := svc.GetLogs(10)
	require.NoError(t, err)
	assert.Empty(t, logs)

	require.NoError(t, os.WriteFile(filename, nil, 0644))
	logs, err = svc.GetLogs(10)
	require.NoError(t, err)
	assert.Empty(t, logs)
}
