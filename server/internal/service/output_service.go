package service

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/x-media/x-media-server/internal/media"
	"github.com/x-media/x-media-server/internal/model"
	"github.com/x-media/x-media-server/internal/repository"
	"github.com/x-media/x-media-server/pkg/errors"
	"github.com/x-media/x-media-server/pkg/logger"
	"github.com/x-media/x-media-server/pkg/utils"
)

// OutputService 输出端服务
type OutputService struct {
	repo   repository.OutputRepository
	engine media.Engine
}

// NewOutputService 创建输出端服务
func NewOutputService(repo repository.OutputRepository, engine media.Engine) *OutputService {
	return &OutputService{repo: repo, engine: engine}
}

// CreateOutputRequest 创建输出端请求
type CreateOutputRequest struct {
	Name   string `json:"name" binding:"required"`
	Type   string `json:"type" binding:"required"`
	Config string `json:"config" binding:"required"`
}

// Create 创建输出端
func (s *OutputService) Create(req *CreateOutputRequest) (*model.Output, error) {
	// 验证类型
	if !isValidOutputType(req.Type) {
		return nil, errors.NewValidationError(fmt.Sprintf("不支持的输出类型: %s", req.Type))
	}

	// 验证配置
	if err := validateOutputConfig(req.Type, req.Config); err != nil {
		return nil, err
	}

	output := &model.Output{
		ID:     utils.GenerateID(),
		Name:   req.Name,
		Type:   req.Type,
		Config: req.Config,
		Status: model.OutputStatusStopped,
	}

	if err := s.repo.Create(output); err != nil {
		return nil, errors.NewInternalError(err)
	}

	return output, nil
}

// GetByID 根据ID获取输出端
func (s *OutputService) GetByID(id string) (*model.Output, error) {
	output, err := s.repo.GetByID(id)
	if err != nil {
		return nil, errors.NewNotFoundError("输出端", id)
	}
	return output, nil
}

// GetAll 获取所有输出端
func (s *OutputService) GetAll() ([]model.Output, error) {
	return s.repo.GetAll()
}

// Update 更新输出端
func (s *OutputService) Update(id string, req *CreateOutputRequest) (*model.Output, error) {
	output, err := s.repo.GetByID(id)
	if err != nil {
		return nil, errors.NewNotFoundError("输出端", id)
	}

	// 验证类型
	if !isValidOutputType(req.Type) {
		return nil, errors.NewValidationError(fmt.Sprintf("不支持的输出类型: %s", req.Type))
	}

	// 验证配置
	if err := validateOutputConfig(req.Type, req.Config); err != nil {
		return nil, err
	}

	output.Name = req.Name
	output.Type = req.Type
	output.Config = req.Config

	if err := s.repo.Update(output); err != nil {
		return nil, errors.NewInternalError(err)
	}

	return output, nil
}

// Delete 删除输出端
func (s *OutputService) Delete(id string) error {
	output, err := s.repo.GetByID(id)
	if err != nil {
		return errors.NewNotFoundError("输出端", id)
	}

	if output.Status == model.OutputStatusRunning {
		return errors.NewValidationError("运行中的输出端不能删除")
	}

	// 从媒体引擎移除
	if err := s.engine.RemoveOutput(id); err != nil {
		logger.Warnf("从媒体引擎移除输出端失败: %v", err)
	}

	return s.repo.Delete(id)
}

// Start 启动输出端
func (s *OutputService) Start(id string) error {
	output, err := s.repo.GetByID(id)
	if err != nil {
		return errors.NewNotFoundError("输出端", id)
	}

	if output.Status == model.OutputStatusRunning {
		return errors.NewValidationError("输出端已在运行中")
	}

	// 解析配置
	var config model.OutputConfig
	if err := json.Unmarshal([]byte(output.Config), &config); err != nil {
		return errors.NewValidationError("配置格式错误")
	}

	// 创建媒体输出流
	outputConfig := &media.OutputConfig{
		ID:        output.ID,
		Type:      output.Type,
		URL:       config.URL,
		Addr:      config.Addr,
		Mode:      config.Mode,
		Transport: config.Transport,
	}

	mediaOutput, err := s.engine.CreateOutput(outputConfig)
	if err != nil {
		return errors.NewInternalError(err)
	}

	// 启动输出流
	if err := mediaOutput.Start(context.Background()); err != nil {
		return errors.NewInternalError(err)
	}

	// 更新状态
	return s.repo.UpdateStatus(id, model.OutputStatusRunning)
}

// Stop 停止输出端
func (s *OutputService) Stop(id string) error {
	output, err := s.repo.GetByID(id)
	if err != nil {
		return errors.NewNotFoundError("输出端", id)
	}

	if output.Status == model.OutputStatusStopped {
		return errors.NewValidationError("输出端已停止")
	}

	// 从媒体引擎移除
	if err := s.engine.RemoveOutput(id); err != nil {
		logger.Warnf("从媒体引擎移除输出端失败: %v", err)
	}

	// 更新状态
	return s.repo.UpdateStatus(id, model.OutputStatusStopped)
}

// 辅助函数

func isValidOutputType(outputType string) bool {
	validTypes := map[string]bool{
		model.OutputTypeRTMP:    true,
		model.OutputTypeRTSP:    true,
		model.OutputTypeHTTPFLV: true,
		model.OutputTypeHLS:     true,
	}
	return validTypes[outputType]
}

func validateOutputConfig(outputType string, configStr string) error {
	var config model.OutputConfig
	if err := json.Unmarshal([]byte(configStr), &config); err != nil {
		return errors.NewValidationError("配置格式错误")
	}

	switch outputType {
	case model.OutputTypeRTMP:
		if config.URL == "" {
			return errors.NewValidationError("RTMP URL不能为空")
		}
	case model.OutputTypeRTSP:
		if config.Mode == "" {
			return errors.NewValidationError("RTSP模式不能为空")
		}
		if config.Mode == "push" && config.URL == "" {
			return errors.NewValidationError("推流模式URL不能为空")
		}
		if config.Mode == "server" && config.Addr == "" {
			return errors.NewValidationError("服务模式地址不能为空")
		}
	case model.OutputTypeHTTPFLV:
		if config.Addr == "" {
			return errors.NewValidationError("HTTP-FLV地址不能为空")
		}
	}

	return nil
}
