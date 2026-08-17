package service

import (
	"context"
	"encoding/json"

	"github.com/x-media/x-media-server/internal/media"
	"github.com/x-media/x-media-server/internal/model"
	"github.com/x-media/x-media-server/internal/repository"
	"github.com/x-media/x-media-server/pkg/errors"
	"github.com/x-media/x-media-server/pkg/logger"
	"github.com/x-media/x-media-server/pkg/utils"
)

// hasRunningPipeByInput 判断 inputID 是否有运行中的管道引用。
func hasRunningPipeByInput(pipeRepo repository.PipeRepository, inputID string) bool {
	pipes, err := pipeRepo.GetByInputID(inputID)
	if err != nil {
		logger.Warnf("查询输入管道失败 %s: %v", inputID, err)
		return true // 保守：查询失败视为仍有引用，避免误停
	}
	for _, p := range pipes {
		if p.Status == model.PipeStatusRunning {
			return true
		}
	}
	return false
}

// hasRunningPipeByOutput 判断 outputID 是否有运行中的管道引用。
func hasRunningPipeByOutput(pipeRepo repository.PipeRepository, outputID string) bool {
	pipes, err := pipeRepo.GetByOutputID(outputID)
	if err != nil {
		logger.Warnf("查询输出管道失败 %s: %v", outputID, err)
		return true
	}
	for _, p := range pipes {
		if p.Status == model.PipeStatusRunning {
			return true
		}
	}
	return false
}

// stopOrphanedInput 停止无任何运行管道引用的输入（engine 移除 + DB 置 stopped），
// 保证 DB 状态与引擎真实状态一致。
func stopOrphanedInput(engine media.Engine, inputRepo repository.InputRepository, pipeRepo repository.PipeRepository, inputID string) {
	if hasRunningPipeByInput(pipeRepo, inputID) {
		return
	}
	if err := engine.RemoveInput(inputID); err != nil && err != media.ErrInputNotFound {
		logger.Warnf("停止孤儿输入失败 %s: %v", inputID, err)
		return
	}
	if err := inputRepo.UpdateStatus(inputID, model.InputStatusStopped); err != nil {
		logger.Warnf("更新孤儿输入状态失败 %s: %v", inputID, err)
	}
	logger.Infof("输入已停止(无管道引用): %s", inputID)
}

// stopOrphanedOutput 停止无任何运行管道引用的输出。
func stopOrphanedOutput(engine media.Engine, outputRepo repository.OutputRepository, pipeRepo repository.PipeRepository, outputID string) {
	if hasRunningPipeByOutput(pipeRepo, outputID) {
		return
	}
	if err := engine.RemoveOutput(outputID); err != nil && err != media.ErrOutputNotFound {
		logger.Warnf("停止孤儿输出失败 %s: %v", outputID, err)
		return
	}
	if err := outputRepo.UpdateStatus(outputID, model.OutputStatusStopped); err != nil {
		logger.Warnf("更新孤儿输出状态失败 %s: %v", outputID, err)
	}
	logger.Infof("输出已停止(无管道引用): %s", outputID)
}

// startInputOnly 仅启动一个输入 source 并同步状态（不涉及管道）。
func startInputOnly(engine media.Engine, repo repository.InputRepository, id string) error {
	input, err := repo.GetByID(id)
	if err != nil {
		return err
	}
	var config model.InputConfig
	if err := json.Unmarshal([]byte(input.Config), &config); err != nil {
		return errors.NewValidationError("配置格式错误")
	}
	inputConfig := &media.InputConfig{
		ID:        input.ID,
		Type:      input.Type,
		Path:      config.Path,
		URL:       config.URL,
		Loop:      utils.PtrBool(config.Loop, false),
		Speed:     config.Speed,
		Transport: config.Transport,
		Timeout:   config.TimeoutMs,
		TimestampGrid: &media.TrackGridConfig{
			Video: config.TimestampGrid != nil && config.TimestampGrid.Video != nil && *config.TimestampGrid.Video,
			Audio: config.TimestampGrid != nil && config.TimestampGrid.Audio != nil && *config.TimestampGrid.Audio,
		},
	}
	mi, err := engine.CreateInput(inputConfig)
	if err != nil {
		return err
	}
	if err := mi.Start(context.Background()); err != nil {
		return err
	}
	return repo.UpdateStatus(id, model.InputStatusRunning)
}

// startOutputOnly 仅启动一个输出 sink 并同步状态。
func startOutputOnly(engine media.Engine, repo repository.OutputRepository, id string) error {
	output, err := repo.GetByID(id)
	if err != nil {
		return err
	}
	var config model.OutputConfig
	if err := json.Unmarshal([]byte(output.Config), &config); err != nil {
		return errors.NewValidationError("配置格式错误")
	}
	outputConfig := &media.OutputConfig{
		ID:        output.ID,
		Type:      output.Type,
		URL:       config.URL,
		Addr:      config.Addr,
		Mode:      config.Mode,
		Transport: config.Transport,
	}
	mo, err := engine.CreateOutput(outputConfig)
	if err != nil {
		return err
	}
	if err := mo.Start(context.Background()); err != nil {
		return err
	}
	return repo.UpdateStatus(id, model.OutputStatusRunning)
}
