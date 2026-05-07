package repository

import (
	"github.com/x-media/x-media-server/internal/model"
	"gorm.io/gorm"
)

// InputRepository 输入端仓储接口
type InputRepository interface {
	Create(input *model.Input) error
	GetByID(id string) (*model.Input, error)
	GetAll() ([]model.Input, error)
	Update(input *model.Input) error
	Delete(id string) error
	UpdateStatus(id string, status string) error
}

// InputRepo 输入端仓储实现
type InputRepo struct {
	db *gorm.DB
}

// NewInputRepo 创建输入端仓储
func NewInputRepo(db *gorm.DB) *InputRepo {
	return &InputRepo{db: db}
}

// Create 创建输入端
func (r *InputRepo) Create(input *model.Input) error {
	return r.db.Create(input).Error
}

// GetByID 根据ID获取输入端
func (r *InputRepo) GetByID(id string) (*model.Input, error) {
	var input model.Input
	if err := r.db.Where("id = ?", id).First(&input).Error; err != nil {
		return nil, err
	}
	return &input, nil
}

// GetAll 获取所有输入端
func (r *InputRepo) GetAll() ([]model.Input, error) {
	var inputs []model.Input
	if err := r.db.Find(&inputs).Error; err != nil {
		return nil, err
	}
	return inputs, nil
}

// Update 更新输入端
func (r *InputRepo) Update(input *model.Input) error {
	return r.db.Save(input).Error
}

// Delete 删除输入端
func (r *InputRepo) Delete(id string) error {
	return r.db.Where("id = ?", id).Delete(&model.Input{}).Error
}

// UpdateStatus 更新状态
func (r *InputRepo) UpdateStatus(id string, status string) error {
	return r.db.Model(&model.Input{}).Where("id = ?", id).Update("status", status).Error
}
