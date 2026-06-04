package service

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/x-media/x-media-server/pkg/errors"
	"github.com/x-media/x-media-server/pkg/utils"
)

type FileService struct {
	uploadDir string
}

func NewFileService(uploadDir string) *FileService {
	return &FileService{uploadDir: uploadDir}
}

type FileEntry struct {
	Name  string `json:"name"`
	Path  string `json:"path"`
	IsDir bool   `json:"is_dir"`
	Size  int64  `json:"size"`
}

func (s *FileService) ListDir(dirPath string) ([]FileEntry, error) {
	if dirPath == "" {
		dirPath = "/"
	}

	info, err := os.Stat(dirPath)
	if err != nil {
		return nil, errors.NewNotFoundError("目录", dirPath)
	}
	if !info.IsDir() {
		return nil, errors.NewValidationError("路径不是目录")
	}

	entries, err := os.ReadDir(dirPath)
	if err != nil {
		return nil, errors.NewInternalError(err)
	}

	var result []FileEntry
	for _, e := range entries {
		name := e.Name()
		fullPath := filepath.Join(dirPath, name)

		// skip hidden files
		if strings.HasPrefix(name, ".") {
			continue
		}

		var size int64
		if !e.IsDir() {
			if info, err := e.Info(); err == nil {
				size = info.Size()
			}
		}

		result = append(result, FileEntry{
			Name:  name,
			Path:  fullPath,
			IsDir: e.IsDir(),
			Size:  size,
		})
	}

	return result, nil
}

func (s *FileService) Upload(fileName string, reader io.Reader) (string, error) {
	if err := os.MkdirAll(s.uploadDir, 0755); err != nil {
		return "", errors.NewInternalError(err)
	}

	id := utils.GenerateID()
	ext := filepath.Ext(fileName)
	saveName := id + ext
	savePath := filepath.Join(s.uploadDir, saveName)

	f, err := os.Create(savePath)
	if err != nil {
		return "", errors.NewInternalError(err)
	}
	defer f.Close()

	n, err := io.Copy(f, reader)
	if err != nil {
		os.Remove(savePath)
		return "", errors.NewInternalError(err)
	}

	_ = n // bytes written
	return savePath, nil
}

func (s *FileService) ValidateFilePath(path string) error {
	if path == "" {
		return errors.NewValidationError("文件路径不能为空")
	}

	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return errors.NewNotFoundError("文件", path)
		}
		return errors.NewInternalError(err)
	}

	if info.IsDir() {
		return errors.NewValidationError(fmt.Sprintf("路径是目录，不是文件: %s", path))
	}

	ext := strings.ToLower(filepath.Ext(path))
	allowed := map[string]bool{
		".mp4": true, ".flv": true, ".ts": true,
		".avi": true, ".mkv": true, ".mov": true,
	}
	if !allowed[ext] {
		return errors.NewValidationError(fmt.Sprintf("不支持的文件格式: %s", ext))
	}

	return nil
}
