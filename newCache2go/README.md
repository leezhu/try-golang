
cache2go是一个并发安全的程序级缓存，项目简单易用，支持的功能足够丰富；整个项目代码很短
我尽量用自己的语言对每个部分进行了注释，并附带了自己的疑问点和可改进的地方；

此项目适合需要学习golang和cache的入门级同学，大佬请自行绕过；

针对cache2go作者似乎不怎么维护项目，我将fork这个项目列出需要改进点进行二次开发

## Installation

Make sure you have a working Go environment (Go 1.2 or higher is required).
See the [install instructions](http://golang.org/doc/install.html).

To install newCache2go, simply run:

    go get github.com/leezhu/try-golang/newCache2go

## Example
```go
package main

import (
	"github.com/leezhu/try-golang/newCache2go"
	"fmt"
	"time"
)

// Keys & values in cache2go can be of arbitrary types, e.g. a struct.
type myStruct struct {
	text     string
	moreData []byte
}

func main() {
	// Accessing a new cache table for the first time will create it.
	cache := cache2go.Cache("myCache")

	// We will put a new item in the cache. It will expire after
	// not being accessed via Value(key) for more than 5 seconds.
	val := myStruct{"This is a test!", []byte{}}
	cache.Add("someKey", 5*time.Second, &val)

	// Let's retrieve the item from the cache.
	res, err := cache.Value("someKey")
	if err == nil {
		fmt.Println("Found value in cache:", res.Data().(*myStruct).text)
	} else {
		fmt.Println("Error retrieving value from cache:", err)
	}

	// Wait for the item to expire in cache.
	time.Sleep(6 * time.Second)
	res, err = cache.Value("someKey")
	if err != nil {
		fmt.Println("Item is not cached (anymore).")
	}

	// Add another item that never expires.
	cache.Add("someKey", 0, &val)

	// cache2go supports a few handy callbacks and loading mechanisms.
	cache.SetAboutToDeleteItemCallback(func(e *cache2go.CacheItem) {
		fmt.Println("Deleting:", e.Key(), e.Data().(*myStruct).text, e.CreatedOn())
	})

	// Remove the item from the cache.
	cache.Delete("someKey")

	// And wipe the entire cache table.
	cache.Flush()
}
```

## 设计思路
### 架构
cacheTable -\> cacheItem；cacheTable就是对外使用的cache，可以在cache 里面加需要key

- 1.cache的实例新建，会两次加锁
- 2.源码很多读写地方都进行了加锁，是为了防止出现数据竞争吗？

### key过期设计
这个key过期清除设计的非常巧妙。

可以看下面这段代码，是添加k-v item的操作，新建一个item后，
接下来会调用addInternal函数，这个函数是触发添加调用的回调函数和过期检查函数；
```go
func (table *CacheTable) Add(key interface{}, lifeSpan time.Duration, data interface{}) *CacheItem {
	item := NewCacheItem(key, lifeSpan, data)

	// Add item to cache.
	table.Lock()
	table.addInternal(item)

	return item
}
```

```go
func (table *CacheTable) addInternal(item *CacheItem) {
	// Careful: do not run this method unless the table-mutex is locked!
	// It will unlock it for the caller before running the callbacks and checks
	table.log("Adding item with key", item.key, "and lifespan of", item.lifeSpan, "to table", table.name)
	table.items[item.key] = item

	// Cache values so we don't keep blocking the mutex.
	expDur := table.cleanupInterval
	addedItem := table.addedItem
	table.Unlock()

	// Trigger callback after adding an item to cache.
	if addedItem != nil {
		for _, callback := range addedItem {
			callback(item)
		}
	}

	// If we haven't set up any expiration check timer or found a more imminent item.
	if item.lifeSpan > 0 && (expDur == 0 || item.lifeSpan < expDur) {
		table.expirationCheck()
	}
}
```

可以直接看到，只要添加的item有过期时间，那么就会调用到expirationCheck函数，这个函数会先进行检查操作，如果整个cache里面还存在有过期时间的key，那么又会新起一个go 协程来检查；这样就能确保，当前cache过期检查能够一直持续下去；这貌似会引起另一个问题，如果cache有上千万级别的数量，这样一个主动删除操作，将会早就上千万的协程存在；类似于redis中的操作，是会存在一个被动删除的操作，如果每次访问时进行检查判断，过期了则直接删除，返回不存在；如果未过期则进行删除；


```go
....
	// Setup the interval for the next cleanup run.
	table.cleanupInterval = smallestDuration
	if smallestDuration > 0 {
		table.cleanupTimer = time.AfterFunc(smallestDuration, func() {
			go table.expirationCheck()
		})
	}
....
```
expirationCheck函数内容比较多，我只截选了比较难懂的部分；这个地方是设置了下一次清除的间隔启动时间，通过time.AfterFunc定时器实现；

**好奇的是**这个地方如果一直有未过期的k-v，那么就会在一个协程退出前，启动另一个协程；

**而且** 这个过期时间检查操作，是会锁住；

当一个cache里面存在10个k-v时，这是会产生10个协程检查10个k-v是否过期；感觉非常没必要；

可以设计成只有一个协程定期删除，访问前再删除；在千万级的k-v下，性能将会提升不少；不过这也需要性能测试后才知道；


### keepalive设计
保持存活的设计，不是简单的将过期时间增加，而且将item访问时间accessOn设置为当前时间；因为item过期检查用的就是now-accessOn >lifeSpan，而且还会将访问次数+1；这个访问次数用在后面的排序操作上；

### 删除所有item设计
新make一个item数组赋值给table,并且停掉过期检查的定时器；被删除的item数组等待gc回收

### 增加k-v的回调函数

可以看到下面的取值操作，先设置加载器，如果value无法取值
那么就可以用事先定义好的数据加载器进行生产值。
```go
table.SetDataLoader(func(key interface{}, args ...interface{}) *CacheItem {
		var item *CacheItem
		if key.(string) != "nil" {
			val := k + key.(string)
			i := NewCacheItem(key, 500*time.Millisecond, val)
			item = i
		}

		return item
	})
func (table *CacheTable) Value(key interface{}, args ...interface{}) (*CacheItem, error) {
	table.RLock()
	r, ok := table.items[key]
	loadData := table.loadData
	table.RUnlock()

	if ok {
		// Update access counter and timestamp.
		r.KeepAlive()
		return r, nil
	}

	// Item doesn't exist in cache. Try and fetch it with a data-loader.
	if loadData != nil {
		item := loadData(key, args...)
		if item != nil {
			table.Add(key, item.lifeSpan, item.data)
			return item, nil
		}

		return nil, ErrKeyNotFoundOrLoadable
	}

	return nil, ErrKeyNotFound
}
```

### 取前最多访问次数的item
作者是将所有table.items都进行排序后，再取前k个，这样操作的性能会
比较差，因为并不需要全部排序，可以用快速排序取出前k就行；



### 总结
cache2go实现还是非常简单的，读取的部分都加上了锁避免并发的安全。单数据量小的时候还是没什么问题，一旦内存使用量大还是存在非常多的性能问题，比如说flush时是直接make新的map，新加k-v item是直接new一个item，在出现同key时。是直接抛弃调的；还有foreach实现时会有dead-clock的bug，这些作者都没有维护了。如果cache2go是作为一个轻量级的内存缓存使用的话，还是有很多优良空间进行考虑的；

从整体来看内存的缓存都是基于map[interface{}]interface{}来做的；但是主要考虑以下几个点的实现：
* 写入
* 获取
* 过期设计
* 内存淘汰设计
* 写入、删除、过期时的回调函数
* 并发安全考虑

cache2go的内存淘汰设计是没有的，这会存在内存爆的情况；
待实现的点有：
* 内存淘汰设计
* 惰性过期删除；当访问不存在的时候进行删除
* 新增add item降低全局扫描过期检查的STW可能