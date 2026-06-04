package api

import (
	"net/http"

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
	file, err := c.FormFile("file")
	if err != nil {
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
