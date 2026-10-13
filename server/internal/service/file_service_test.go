package service

import (
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/x-media/x-media-server/pkg/errors"
)

// newTestFileService 构造一个以 tmp 为唯一媒体根目录的 FileService。
// 测试内统一走该构造，避免依赖具体根目录默认值。
func newTestFileService(t *testing.T) (*FileService, string) {
	t.Helper()
	root := t.TempDir()
	return NewFileService(filepath.Join(root, "uploads"), []string{root}), root
}

// TestListDirRejectsEscape 复现任意目录遍历：
// 修复前 ListDir("/etc") / ListDir("<root>/..") 都会成功返回宿主机目录列表。
func TestListDirRejectsEscape(t *testing.T) {
	svc, root := newTestFileService(t)

	for _, p := range []string{
		"/etc",
		root + "/../..",
		filepath.Dir(root),
	} {
		entries, err := svc.ListDir(p)
		require.Error(t, err, "path=%q 必须被拒绝", p)
		assert.Nil(t, entries, "path=%q 越界时不得返回目录内容", p)
		var appErr *errors.AppError
		require.ErrorAs(t, err, &appErr, "path=%q 应返回统一的 AppError", p)
		assert.Equal(t, http.StatusBadRequest, appErr.Code, "path=%q 应为 400 校验错误", p)
	}
}

// TestListDirEmptyPathReturnsRoot 复现缺省路径问题：
// 修复前 dirPath=="" 会落到 "/"，即列出整个根文件系统。
func TestListDirEmptyPathReturnsRoot(t *testing.T) {
	svc, root := newTestFileService(t)
	// 根目录放一个可见条目，便于断言列出的确实是媒体根内容
	require.NoError(t, os.WriteFile(filepath.Join(root, "marker.txt"), []byte("x"), 0644))

	entries, err := svc.ListDir("")
	require.NoError(t, err)

	// 根目录下不应出现宿主机系统目录，所有条目必须位于媒体根内
	require.NotEmpty(t, entries, "空路径应返回媒体根目录内容")
	for _, e := range entries {
		assert.True(t,
			filepath.IsAbs(e.Path),
			"条目路径应为绝对路径: %s", e.Path)
	}
	_, err = os.Stat(filepath.Join(root, entries[0].Name))
	assert.NoError(t, err, "空路径列出的内容应真实存在于媒体根目录内")

	// "/" 与空路径等价，同样映射到媒体根
	entriesRoot, err := svc.ListDir("/")
	require.NoError(t, err)
	assert.Equal(t, entries, entriesRoot, "\"/\" 应与空路径一样映射到媒体根目录")
}

// TestListDirAllowsRootAndSubdir 保护合法用法：根目录自身与其子目录必须可浏览。
func TestListDirAllowsRootAndSubdir(t *testing.T) {
	svc, root := newTestFileService(t)
	sub := filepath.Join(root, "media")
	require.NoError(t, os.MkdirAll(sub, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(sub, "a.mp4"), []byte("x"), 0644))

	// 根目录自身
	entries, err := svc.ListDir(root)
	require.NoError(t, err)
	foundSub := false
	for _, e := range entries {
		if e.Name == "media" && e.IsDir {
			foundSub = true
		}
	}
	assert.True(t, foundSub, "根目录应能列出其子目录")

	// 子目录
	entries, err = svc.ListDir(sub)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	assert.Equal(t, "a.mp4", entries[0].Name)
	assert.Equal(t, filepath.Join(sub, "a.mp4"), entries[0].Path)
}

// TestListDirMultipleRoots 验证配置了多个媒体根时全部可浏览。
func TestListDirMultipleRoots(t *testing.T) {
	rootA := t.TempDir()
	rootB := t.TempDir()
	svc := NewFileService(filepath.Join(rootA, "uploads"), []string{rootA, rootB})

	_, err := svc.ListDir(rootA)
	require.NoError(t, err)
	_, err = svc.ListDir(rootB)
	require.NoError(t, err)
}
