package repository

import (
	"github.com/x-media/x-media-server/internal/model"
	"gorm.io/gorm"
)

// PipeRepository 管道仓储接口
type PipeRepository interface {
	Create(pipe *model.Pipe) error
	GetByID(id string) (*model.Pipe, error)
	GetAll() ([]model.Pipe, error)
	GetByInputID(inputID string) ([]model.Pipe, error)
	GetByOutputID(outputID string) ([]model.Pipe, error)
	Update(pipe *model.Pipe) error
	Delete(id string) error
	UpdateStatus(id string, status string) error
}

// PipeRepo 管道仓储实现
type PipeRepo struct {
	db *gorm.DB
}

// NewPipeRepo 创建管道仓储
func NewPipeRepo(db *gorm.DB) *PipeRepo {
	return &PipeRepo{db: db}
}

// Create 创建管道
func (r *PipeRepo) Create(pipe *model.Pipe) error {
	return r.db.Create(pipe).Error
}

// GetByID 根据ID获取管道
func (r *PipeRepo) GetByID(id string) (*model.Pipe, error) {
	var pipe model.Pipe
	if err := r.db.Where("id = ?", id).First(&pipe).Error; err != nil {
		return nil, err
	}
	return &pipe, nil
}

// GetAll 获取所有管道
func (r *PipeRepo) GetAll() ([]model.Pipe, error) {
	var pipes []model.Pipe
	if err := r.db.Find(&pipes).Error; err != nil {
		return nil, err
	}
	return pipes, nil
}

// GetByInputID 根据输入端ID获取管道
func (r *PipeRepo) GetByInputID(inputID string) ([]model.Pipe, error) {
	var pipes []model.Pipe
	if err := r.db.Where("input_id = ?", inputID).Find(&pipes).Error; err != nil {
		return nil, err
	}
	return pipes, nil
}

// GetByOutputID 根据输出端ID获取管道
func (r *PipeRepo) GetByOutputID(outputID string) ([]model.Pipe, error) {
	var pipes []model.Pipe
	if err := r.db.Where("output_id = ?", outputID).Find(&pipes).Error; err != nil {
		return nil, err
	}
	return pipes, nil
}

// Update 更新管道
func (r *PipeRepo) Update(pipe *model.Pipe) error {
	return r.db.Save(pipe).Error
}

// Delete 删除管道
func (r *PipeRepo) Delete(id string) error {
	return r.db.Where("id = ?", id).Delete(&model.Pipe{}).Error
}

// UpdateStatus 更新状态
func (r *PipeRepo) UpdateStatus(id string, status string) error {
	return r.db.Model(&model.Pipe{}).Where("id = ?", id).Update("status", status).Error
}
