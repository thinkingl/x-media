package service

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/x-media/x-media-server/internal/media"
	"github.com/x-media/x-media-server/internal/model"
	"github.com/x-media/x-media-server/internal/repository"
	"github.com/x-media/x-media-server/pkg/errors"
	"github.com/x-media/x-media-server/pkg/logger"
	"github.com/x-media/x-media-server/pkg/utils"
)

// InputService 输入端服务
type InputService struct {
	repo   repository.InputRepository
	engine media.MediaEngine
}

// NewInputService 创建输入端服务
func NewInputService(repo repository.InputRepository, engine media.MediaEngine) *InputService {
	return &InputService{repo: repo, engine: engine}
}

// CreateInputRequest 创建输入端请求
type CreateInputRequest struct {
	Name   string `json:"name" binding:"required"`
	Type   string `json:"type" binding:"required"`
	Config string `json:"config" binding:"required"`
}

// Create 创建输入端
func (s *InputService) Create(req *CreateInputRequest) (*model.Input, error) {
	// 验证名称
	if req.Name == "" {
		return nil, errors.NewValidationError("名称不能为空")
	}

	// 验证类型
	if !isValidInputType(req.Type) {
		return nil, errors.NewValidationError(fmt.Sprintf("不支持的输入类型: %s", req.Type))
	}

	// 验证配置
	if err := validateInputConfig(req.Type, req.Config); err != nil {
		return nil, err
	}

	input := &model.Input{
		ID:     utils.GenerateID(),
		Name:   req.Name,
		Type:   req.Type,
		Config: req.Config,
		Status: model.InputStatusStopped,
	}

	if err := s.repo.Create(input); err != nil {
		return nil, errors.NewInternalError(err)
	}

	if req.Type == model.InputTypeFile {
		var config model.InputConfig
		if err := json.Unmarshal([]byte(req.Config), &config); err == nil && config.Path != "" {
			go s.probeAndSave(input.ID, config.Path)
		}
	}

	return input, nil
}

// GetByID 根据ID获取输入端
func (s *InputService) GetByID(id string) (*model.Input, error) {
	input, err := s.repo.GetByID(id)
	if err != nil {
		return nil, errors.NewNotFoundError("输入端", id)
	}
	return input, nil
}

// GetAll 获取所有输入端
func (s *InputService) GetAll() ([]model.Input, error) {
	return s.repo.GetAll()
}

// Update 更新输入端
func (s *InputService) Update(id string, req *CreateInputRequest) (*model.Input, error) {
	input, err := s.repo.GetByID(id)
	if err != nil {
		return nil, errors.NewNotFoundError("输入端", id)
	}

	// 验证类型
	if !isValidInputType(req.Type) {
		return nil, errors.NewValidationError(fmt.Sprintf("不支持的输入类型: %s", req.Type))
	}

	// 验证配置
	if err := validateInputConfig(req.Type, req.Config); err != nil {
		return nil, err
	}

	input.Name = req.Name
	input.Type = req.Type
	input.Config = req.Config

	if err := s.repo.Update(input); err != nil {
		return nil, errors.NewInternalError(err)
	}

	return input, nil
}

// Delete 删除输入端
func (s *InputService) Delete(id string) error {
	input, err := s.repo.GetByID(id)
	if err != nil {
		return errors.NewNotFoundError("输入端", id)
	}

	if input.Status == model.InputStatusRunning {
		return errors.NewValidationError("运行中的输入端不能删除")
	}

	// 从媒体引擎移除
	if err := s.engine.RemoveInput(id); err != nil {
		logger.Warnf("从媒体引擎移除输入端失败: %v", err)
	}

	return s.repo.Delete(id)
}

// Start 启动输入端
func (s *InputService) Start(id string) error {
	input, err := s.repo.GetByID(id)
	if err != nil {
		return errors.NewNotFoundError("输入端", id)
	}

	if input.Status == model.InputStatusRunning {
		return errors.NewValidationError("输入端已在运行中")
	}

	// 解析配置
	var config model.InputConfig
	if err := json.Unmarshal([]byte(input.Config), &config); err != nil {
		return errors.NewValidationError("配置格式错误")
	}

	// 创建媒体输入流
	inputConfig := &media.InputConfig{
		ID:        input.ID,
		Type:      input.Type,
		Path:      config.Path,
		URL:       config.URL,
		Loop:      utils.PtrBool(config.Loop, false),
		Speed:     config.Speed,
		Transport: config.Transport,
		Timeout:   config.TimeoutMs,
	}

	mediaInput, err := s.engine.CreateInput(inputConfig)
	if err != nil {
		return errors.NewInternalError(err)
	}

	// 启动输入流
	if err := mediaInput.Start(context.Background()); err != nil {
		return errors.NewInternalError(err)
	}

	// 更新状态
	return s.repo.UpdateStatus(id, model.InputStatusRunning)
}

// Stop 停止输入端
func (s *InputService) Stop(id string) error {
	input, err := s.repo.GetByID(id)
	if err != nil {
		return errors.NewNotFoundError("输入端", id)
	}

	if input.Status == model.InputStatusStopped {
		return errors.NewValidationError("输入端已停止")
	}

	// 从媒体引擎移除
	if err := s.engine.RemoveInput(id); err != nil {
		logger.Warnf("从媒体引擎移除输入端失败: %v", err)
	}

	// 更新状态
	return s.repo.UpdateStatus(id, model.InputStatusStopped)
}

// 辅助函数

func isValidInputType(inputType string) bool {
	validTypes := map[string]bool{
		model.InputTypeFile: true,
		model.InputTypeRTSP: true,
		model.InputTypeRTMP: true,
		model.InputTypeHLS:  true,
	}
	return validTypes[inputType]
}

func validateInputConfig(inputType string, configStr string) error {
	var config model.InputConfig
	if err := json.Unmarshal([]byte(configStr), &config); err != nil {
		return errors.NewValidationError("配置格式错误")
	}

	switch inputType {
	case model.InputTypeFile:
		if config.Path == "" {
			return errors.NewValidationError("文件路径不能为空")
		}
	case model.InputTypeRTSP:
		if config.URL == "" {
			return errors.NewValidationError("RTSP URL不能为空")
		}
	}

	return nil
}

func (s *InputService) probeAndSave(inputID, filePath string) {
	absPath := filePath
	if !filepath.IsAbs(absPath) {
		abs, err := filepath.Abs(absPath)
		if err == nil {
			absPath = abs
		}
	}
	probeResult, err := media.ProbeFile(absPath)
	if err != nil {
		logger.Warnf("probe file failed for input %s: %v", inputID, err)
		return
	}

	thumbDir := "./uploads/thumbnails"
	_ = os.MkdirAll(thumbDir, 0755)
	thumbPath := filepath.Join(thumbDir, inputID+".jpg")

	seekTime := 1.0
	if probeResult.Duration > 2 {
		seekTime = probeResult.Duration / 10
	}
	if err := media.ExtractThumbnail(filePath, thumbPath, seekTime); err != nil {
		logger.Warnf("extract thumbnail failed for input %s: %v", inputID, err)
		thumbPath = ""
	} else {
		probeResult.ThumbnailPath = thumbPath
	}

	infoJSON, err := json.Marshal(probeResult)
	if err != nil {
		logger.Warnf("marshal media info failed for input %s: %v", inputID, err)
		return
	}

	_ = s.repo.UpdateMediaInfo(inputID, string(infoJSON))
	logger.Infof("media info saved for input %s", inputID)
}

func (s *InputService) ProbeInput(id string) (*media.MediaInfo, error) {
	input, err := s.repo.GetByID(id)
	if err != nil {
		return nil, errors.NewNotFoundError("输入端", id)
	}

	if input.Type != model.InputTypeFile {
		return nil, errors.NewValidationError("只有文件类型输入端支持媒体探测")
	}

	var config model.InputConfig
	if err := json.Unmarshal([]byte(input.Config), &config); err != nil {
		return nil, errors.NewValidationError("配置格式错误")
	}

	filePath := config.Path
	if !filepath.IsAbs(filePath) {
		abs, err := filepath.Abs(filePath)
		if err == nil {
			filePath = abs
		}
	}

	probeResult, err := media.ProbeFile(filePath)
	if err != nil {
		return nil, errors.NewInternalError(err)
	}

	thumbDir := "./uploads/thumbnails"
	_ = os.MkdirAll(thumbDir, 0755)
	thumbPath := filepath.Join(thumbDir, id+".jpg")

	seekTime := 1.0
	if probeResult.Duration > 2 {
		seekTime = probeResult.Duration / 10
	}
	if err := media.ExtractThumbnail(filePath, thumbPath, seekTime); err != nil {
		logger.Warnf("extract thumbnail failed: %v", err)
		thumbPath = ""
	} else {
		probeResult.ThumbnailPath = thumbPath
	}

	infoJSON, _ := json.Marshal(probeResult)
	_ = s.repo.UpdateMediaInfo(id, string(infoJSON))

	return probeResult, nil
}
