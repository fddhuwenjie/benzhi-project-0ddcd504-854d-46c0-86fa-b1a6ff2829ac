package domain

import "errors"

var ErrInvalid = errors.New("请求无效")
var ErrConflict = errors.New("修订冲突")
var ErrState = errors.New("当前状态不允许此操作")
var ErrNotFound = errors.New("未找到个案")
var ErrSealed = errors.New("个案已封存")
var ErrIntegrity = errors.New("完整性校验失败")
var ErrPersistence = errors.New("持久化失败")

// PersistenceError 保留稳定的 domain.ErrPersistence 类别，同时通过 Unwrap 链接底层根因，
// 供传输层用 errors.Is 既识别持久化失败类别又定位 syscall.EISDIR 等系统错误。
type PersistenceError struct {
	Operation string
	Cause     error
}

func (e *PersistenceError) Error() string {
	if e.Cause != nil {
		return e.Operation + ": " + e.Cause.Error()
	}
	return e.Operation
}

// Unwrap 同时暴露 domain.ErrPersistence 类别与底层根因，确保 errors.Is 可以识别两者。
func (e *PersistenceError) Unwrap() []error {
	if e.Cause != nil {
		return []error{ErrPersistence, e.Cause}
	}
	return []error{ErrPersistence}
}

// Persistence 包装持久化失败的操作与根因，保留完整错误链信息。
func Persistence(operation string, cause error) error {
	return &PersistenceError{Operation: operation, Cause: cause}
}

// DetailError 保留稳定错误类别，同时向 HTTP 层提供可操作的业务细节。
type DetailError struct {
	Kind    error
	Message string
	Details map[string]interface{}
}

func (e *DetailError) Error() string {
	if e.Message != "" {
		return e.Message
	}
	return e.Kind.Error()
}

func (e *DetailError) Unwrap() error { return e.Kind }

func Invalid(message string, details map[string]interface{}) error {
	return &DetailError{Kind: ErrInvalid, Message: message, Details: details}
}

func Conflict(message string, details map[string]interface{}) error {
	return &DetailError{Kind: ErrConflict, Message: message, Details: details}
}
