package cache2go

import "sync"

var (
	cache = make(map[string]*CacheTable)
	mutex sync.RWMutex
)

//Cache 返回一个已存在同名的table,如果不存在则新建
func Cache(table string) *CacheTable {
	mutex.RLock() //访问的时候进行加锁
	t, ok := cache[table]
	mutex.RUnlock()

	if !ok {
		mutex.Lock() //写的时候准备加锁
		t, ok = cache[table]
		//双重检测是否已经存在
		if !ok {
			t = &CacheTable{
				name:  table,
				items: make(map[interface{}]*CacheItem),
			}
			cache[table] = t
		}
		mutex.Unlock()
	}

	return t
}
