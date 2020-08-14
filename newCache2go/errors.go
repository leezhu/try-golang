// errors的错误定义
package cache2go

import "github.com/pkg/errors"

var (
	ErrKeyNotFound           = errors.New("Key not found in cache")
	ErrKeyNotFoundOrLoadable = errors.New("Key not found in cache and can not be loaded in ")
)
