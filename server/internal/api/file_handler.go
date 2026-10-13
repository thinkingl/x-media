package api

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/x-media/x-media-server/internal/service"
)

type FileHandler struct {
	fileSvc *service.FileService
}

func NewFileHandler(fileSvc *service.FileService) *FileHandler {
	return &FileHandler{fileSvc: fileSvc}
}

func (h *FileHandler) listDir(c *gin.Context) {
	dirPath := c.Query("path")
	entries, err := h.fileSvc.ListDir(dirPath)
	if err != nil {
		handleError(c, err)
		return
	}
	response(c, http.StatusOK, entries)
}

func (h *FileHandler) upload(c *gin.Context) {
	// 入口限制请求体大小：超大 body 直接拒绝，不进入 multipart 内存解析。
	limit := h.fileSvc.MaxUploadBytes
	if limit > 0 {
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, limit)
	}

	file, err := c.FormFile("file")
	if err != nil {
		// MaxBytesReader 超限时 FormFile 返回 "request body too large"
		if strings.Contains(err.Error(), "request body too large") {
			errorResponse(c, http.StatusRequestEntityTooLarge,
				fmt.Sprintf("上传内容超过大小限制（%d 字节）", limit))
			return
		}
		errorResponse(c, http.StatusBadRequest, "请选择要上传的文件")
		return
	}

	src, err := file.Open()
	if err != nil {
		errorResponse(c, http.StatusBadRequest, "无法读取上传的文件")
		return
	}
	defer src.Close()

	savePath, err := h.fileSvc.Upload(file.Filename, src)
	if err != nil {
		handleError(c, err)
		return
	}

	response(c, http.StatusOK, gin.H{
		"path": savePath,
	})
}
