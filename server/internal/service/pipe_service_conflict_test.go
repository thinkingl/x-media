package service

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/x-media/x-media-server/internal/model"
	"github.com/x-media/x-media-server/pkg/errors"
	"gorm.io/gorm"
)

// TestCreatePipeConstraintConflictMapsTo409 验证查重 TOCTOU 被唯一索引
// 拦截后正确映射为 409（此前包成 500 内部错误）。
func TestCreatePipeConstraintConflictMapsTo409(t *testing.T) {
	pipeRepo := new(MockPipeRepo)
	inputRepo := new(MockInputRepo)
	outputRepo := new(MockOutputRepo)
	engine := new(MockMediaEngine)
	svc := NewPipeService(pipeRepo, inputRepo, outputRepo, engine)

	input := &model.Input{ID: "in1", Name: "输入", Type: "file"}
	output := &model.Output{ID: "out1", Name: "输出", Type: "rtmp"}
	// 预检全部通过（模拟并发窗口），但 Create 撞唯一索引
	inputRepo.On("GetByID", "in1").Return(input, nil)
	outputRepo.On("GetByID", "out1").Return(output, nil)
	pipeRepo.On("GetByInputID", "in1").Return([]model.Pipe{}, nil)
	pipeRepo.On("GetByOutputID", "out1").Return([]model.Pipe{}, nil)
	pipeRepo.On("Create", mock.Anything).Return(gorm.ErrDuplicatedKey)

	_, err := svc.Create(&CreatePipeRequest{InputID: "in1", OutputID: "out1"})
	require.Error(t, err)
	var appErr *errors.AppError
	require.ErrorAs(t, err, &appErr)
	assert.Equal(t, http.StatusConflict, appErr.Code,
		"唯一索引冲突应映射为 409 Conflict，实际: %d", appErr.Code)
}
