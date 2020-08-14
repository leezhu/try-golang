// 一个简单具有过期功能的缓存item

package cache2go

import (
	"sync"
	"time"
)

//CacheItem 是一个独立的缓存item，是包含元数据的最小单位
//可以理解成存的实际k-v数据实体
type CacheItem struct {
	sync.RWMutex //加锁

	key      interface{}   //key
	data     interface{}   //数据value
	lifeSpan time.Duration //生命周期，如果没有访问，则会被移除

	createdOn    time.Time //创建的时间戳
	accessdOn    time.Time //最近访问的时间戳
	accessdCount int64     //访问的次数

	//在移除这个缓存的时候进行的回调函数，可以存多个
	aboutToExpire []func(key interface{})
}

//NewCacheItem 会访问一个新的item实体
func NewCacheItem(key interface{}, lifeSpan time.Duration, data interface{}) *CacheItem {
	t := time.Now()
	return &CacheItem{
		key:           key,
		data:          data,
		lifeSpan:      lifeSpan,
		createdOn:     t,
		accessdOn:     t,
		accessdCount:  0,
		aboutToExpire: nil,
	}
}

//KeepAlive, 刷新item的近期访问时间，并且次数++
// accessOn的访问时间是后续移除item算lifespan的重要依据
func (item *CacheItem) KeepAlive() {
	item.Lock()
	defer item.Unlock() //进行读写就得加锁，defer避免处理失败必须解锁

	item.accessdOn = time.Now()
	item.accessdCount++
}

//LifeSpan 给出当前item的生命周期，因为定义的cacheItem元素都是私有化，
//需要返回内容就得实现类似于java中的getData方法
func (item *CacheItem) LifeSpan() time.Duration {
	return item.lifeSpan
}

//AccessOn 返回item的访问时间，因为accessOn新建后会更新，
//为了并发安全需要加锁，只是读，所以加上读锁，lifeSpan只有初始化
//才设置，后续无更新所以无需加锁
func (item *CacheItem) AccessOn() time.Time {
	item.RLock()
	defer item.RUnlock()
	return item.accessdOn
}

//AccessdCount 类似的，访问次数返回也需要加读锁
func (item *CacheItem) AccessdCount() int64 {
	item.RLock()
	defer item.RUnlock()
	return item.accessdCount
}

//Key 返回不可变的key
func (item *CacheItem) Key() interface{} {
	return item.key
}

//Data 返回data，这里估计很多人好奇为什么不加锁
//理论上我们想的都是map下k-v，v可更新；但实际作者
//在cacheTable中没有复用已存在的item而是每次都新建item，
//这样能使得不必考虑v更新带来的数据和并发安全问题，但是同时
//也带来了缓存利用率不高、gc压力等问题；对于性能高的使用有必要
//对这个地方进行改写，复用已存在的item
//TODO 可复用item
func (item *CacheItem) Data() interface{} {
	return item.data
}

//SetAboutToExpireCallback 重置移除item回调函数的列表，将原有滞空
//当回调函数比较多的时候，可以考虑使用make,并复用slice
func (item *CacheItem) SetAboutToExpireCallback(f func(key interface{})) {
	if len(item.aboutToExpire) > 0 {
		item.RemoveAboutToExpireCallback()
	}
	item.Lock()
	defer item.Unlock()

	//slice=nil，len=0,cap=0,也可以进行append，可研究下append函数
	item.aboutToExpire = append(item.aboutToExpire, f)
}

//RemoveAboutToExpireCallback 移除已存在的回调函数，置为空
func (item *CacheItem) RemoveAboutToExpireCallback() {
	item.Lock()
	defer item.Unlock()
	item.aboutToExpire = nil
}

//AddAboutToExpireCallback 新增回调函数
func (item *CacheItem) AddAboutToExpireCallback(f func(key interface{})) {
	item.Lock()
	defer item.Unlock()
	item.aboutToExpire = append(item.aboutToExpire, f)
}
