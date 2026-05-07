package errors

import (
	"fmt"
	"net/http"
)

// AppError 应用错误
type AppError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Err     error  `json:"-"`
}

// Error 实现error接口
func (e *AppError) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("%s: %v", e.Message, e.Err)
	}
	return e.Message
}

// Unwrap 解包错误
func (e *AppError) Unwrap() error {
	return e.Err
}

// NewAppError 创建应用错误
func NewAppError(code int, message string, err error) *AppError {
	return &AppError{
		Code:    code,
		Message: message,
		Err:     err,
	}
}

// 预定义错误
var (
	ErrNotFound = &AppError{
		Code:    http.StatusNotFound,
		Message: "资源不存在",
	}

	ErrBadRequest = &AppError{
		Code:    http.StatusBadRequest,
		Message: "请求参数错误",
	}

	ErrInternal = &AppError{
		Code:    http.StatusInternalServerError,
		Message: "内部服务器错误",
	}

	ErrConflict = &AppError{
		Code:    http.StatusConflict,
		Message: "资源冲突",
	}
)

// NewNotFoundError 创建未找到错误
func NewNotFoundError(resource string, id string) *AppError {
	return &AppError{
		Code:    http.StatusNotFound,
		Message: fmt.Sprintf("%s不存在: %s", resource, id),
	}
}

// NewValidationError 创建验证错误
func NewValidationError(message string) *AppError {
	return &AppError{
		Code:    http.StatusBadRequest,
		Message: message,
	}
}

// NewInternalError 创建内部错误
func NewInternalError(err error) *AppError {
	return &AppError{
		Code:    http.StatusInternalServerError,
		Message: "内部服务器错误",
		Err:     err,
	}
}
