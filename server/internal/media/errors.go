package media

import "errors"

var (
	// ErrUnsupportedType 不支持的类型
	ErrUnsupportedType = errors.New("不支持的类型")
	
	// ErrInputNotFound 输入流不存在
	ErrInputNotFound = errors.New("输入流不存在")
	
	// ErrOutputNotFound 输出流不存在
	ErrOutputNotFound = errors.New("输出流不存在")
	
	// ErrStreamNotRunning 流未运行
	ErrStreamNotRunning = errors.New("流未运行")
	
	// ErrInvalidConfig 无效配置
	ErrInvalidConfig = errors.New("无效配置")
	
	// ErrConnectionFailed 连接失败
	ErrConnectionFailed = errors.New("连接失败")
	
	// ErrTimeout 超时
	ErrTimeout = errors.New("超时")
)
