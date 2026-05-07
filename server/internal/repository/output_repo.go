package repository

import (
	"github.com/x-media/x-media-server/internal/model"
	"gorm.io/gorm"
)

// OutputRepository 输出端仓储接口
type OutputRepository interface {
	Create(output *model.Output) error
	GetByID(id string) (*model.Output, error)
	GetAll() ([]model.Output, error)
	Update(output *model.Output) error
	Delete(id string) error
	UpdateStatus(id string, status string) error
}

// OutputRepo 输出端仓储实现
type OutputRepo struct {
	db *gorm.DB
}

// NewOutputRepo 创建输出端仓储
func NewOutputRepo(db *gorm.DB) *OutputRepo {
	return &OutputRepo{db: db}
}

// Create 创建输出端
func (r *OutputRepo) Create(output *model.Output) error {
	return r.db.Create(output).Error
}

// GetByID 根据ID获取输出端
func (r *OutputRepo) GetByID(id string) (*model.Output, error) {
	var output model.Output
	if err := r.db.Where("id = ?", id).First(&output).Error; err != nil {
		return nil, err
	}
	return &output, nil
}

// GetAll 获取所有输出端
func (r *OutputRepo) GetAll() ([]model.Output, error) {
	var outputs []model.Output
	if err := r.db.Find(&outputs).Error; err != nil {
		return nil, err
	}
	return outputs, nil
}

// Update 更新输出端
func (r *OutputRepo) Update(output *model.Output) error {
	return r.db.Save(output).Error
}

// Delete 删除输出端
func (r *OutputRepo) Delete(id string) error {
	return r.db.Where("id = ?", id).Delete(&model.Output{}).Error
}

// UpdateStatus 更新状态
func (r *OutputRepo) UpdateStatus(id string, status string) error {
	return r.db.Model(&model.Output{}).Where("id = ?", id).Update("status", status).Error
}
