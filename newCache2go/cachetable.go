package cache2go

import (
	"log"
	"sort"
	"sync"
	"time"
)

//CacheTable 是管理cacheItem的结构
type CacheTable struct {
	sync.RWMutex //这里定义变量，是因为RWMutex是结构体，直接继承

	name string //cache的缓存名
	//存储k-v数据，item里面存了实际k-v
	//原以为item只需要存value就行？
	//TODO 为什么item还需要存key
	items map[interface{}]*CacheItem

	//是一个清除过期item的定时器
	//主要用于当删除所有item时，要停止这个定时器，防止定时器报错
	cleanupTimer *time.Timer

	//当前定时器的间隔时间，初始化为0，如果非0，说明
	//正存在等待执行的过期检查定时任务
	cleanupInterval time.Duration

	//日志模块，可定制
	logger *log.Logger

	//当试图加载不存在的key数据时，执行的回调函数
	loadData func(key interface{}, args ...interface{}) *CacheItem

	//添加新item时的回调函数
	addedItem []func(item *CacheItem)

	//删除item时的回调函数
	aboutToDeleteItem []func(item *CacheItem)
}

//Count 计算item的数量
func (table *CacheTable) Count() int {
	table.RLock()
	defer table.RUnlock()
	return len(table.items)
}

//Foreach 使用传入的f方法进行遍历所有item
//这样可以自行定制如何显示
func (table *CacheTable) Foreach(trans func(key interface{}, item *CacheItem)) {
	table.RLock()
	defer table.RUnlock()

	for k, v := range table.items {
		trans(k, v)
	}
}

//SetDataLoader 数据加载器函数，赋值
func (table *CacheTable) SetDataLoader(f func(interface{}, ...interface{}) *CacheItem) {
	table.Lock()
	defer table.Unlock()
	table.loadData = f
}

//SetAddedItemCallback 初始化数据加载器;当加载器还存在时，就移除置为空；
//当访问到不存在的数据时，可以使用这个
//加载器将数据写进去，这个是不是应该叫InitDataLoader更好
func (table *CacheTable) SetAddedItemCallback(f func(*CacheItem)) {
	if len(table.addedItem) > 0 {
		table.RemoveAddedItemCallbacks()
	}
	table.Lock()
	defer table.Unlock()
	table.addedItem = append(table.addedItem, f)
}

//AddAddedItemCallback 增加添加item回调函数
func (table *CacheTable) AddAddedItemCallback(f func(*CacheItem)) {
	table.Lock()
	defer table.Unlock()
	table.addedItem = append(table.addedItem, f)
}

//RemoveAddedItemCallbacks 置为空
func (table *CacheTable) RemoveAddedItemCallbacks() {
	table.Lock()
	defer table.Unlock()
	table.addedItem = nil
}

//下面是删除回调函数部分，与添加回调函数异曲同工，就不多讲了
func (table *CacheTable) SetAboutToDeleteItemCallback(f func(*CacheItem)) {
	if len(table.aboutToDeleteItem) > 0 {
		table.RemoveAboutToDeleteItemCallback()
	}
	table.Lock()
	defer table.Unlock()
	table.aboutToDeleteItem = append(table.aboutToDeleteItem, f)
}

// AddAboutToDeleteItemCallback appends a new callback to the AboutToDeleteItem queue
func (table *CacheTable) AddAboutToDeleteItemCallback(f func(*CacheItem)) {
	table.Lock()
	defer table.Unlock()
	table.aboutToDeleteItem = append(table.aboutToDeleteItem, f)
}

// RemoveAboutToDeleteItemCallback empties the about to delete item callback queue
func (table *CacheTable) RemoveAboutToDeleteItemCallback() {
	table.Lock()
	defer table.Unlock()
	table.aboutToDeleteItem = nil
}

//SetLogger 设置日志logger
func (table *CacheTable) SetLogger(logger *log.Logger) {
	table.Lock()
	defer table.Unlock()
	table.logger = logger
}

//过期检查，清除；这个部分可以说是整个设计的精髓
//会循环进行检查，并且利用定时器进行控制
//每天添加新的item会触发检查，如果没有新的item，会有
//定时的过期检查启动
func (table *CacheTable) expirationCheck() {
	//锁住当前部分
	table.Lock()
	//如果过期清除定时器已存在，那么将旧的停止；新的检查触发
	if table.cleanupTimer != nil {
		table.cleanupTimer.Stop()
	}
	if table.cleanupInterval > 0 {
		table.log("Expiration check triggered after", table.cleanupInterval)
	} else {
		table.log("Expiration check installed for table", table.name)
	}

	//全局找到接下来最小的将要过期时间
	now := time.Now()
	smallestDuration := 0 * time.Second
	for key, item := range table.items {
		item.RLock()
		lifeSpan := item.lifeSpan
		accessOn := item.accessdOn
		item.RUnlock()

		if lifeSpan == 0 {
			continue
		}
		if now.Sub(accessOn) >= lifeSpan {
			//说明已经过期f
			table.deleteInternal(key)
		} else {
			if smallestDuration == 0 || lifeSpan-now.Sub(accessOn) < smallestDuration {
				smallestDuration = lifeSpan - now.Sub(accessOn)
			}
		}
	}

	//将找到最小间隔时间作为下一次启动的时间
	table.cleanupInterval = smallestDuration
	if smallestDuration > 0 {
		table.cleanupTimer = time.AfterFunc(smallestDuration, func() {
			go table.expirationCheck()
		})
	}
	table.Unlock()

}

//Add 添加k-v item到cacheTable中。每个add操作都会新增
//一个item，出现相同k,不同value时，会直接使用新的item进行覆盖
//TODO 如果业务代码没有进行k判断是否存在，一直Add，那么会存在item不断增大的情况
//这里有必要实现一个如果key存在，那么可以复用item的操作；直接修改item的存活时间和值
func (table *CacheTable) Add(key interface{}, lifeSpan time.Duration, data interface{}) *CacheItem {
	item := NewCacheItem(key, lifeSpan, data)

	//添加item
	table.Lock()
	table.addInternal(item)

	return item
}

//addInternal 将cacheItem添加到table map中
func (table *CacheTable) addInternal(item *CacheItem) {
	table.log("Adding item with key", item.key, "and lifespan of", item.lifeSpan, "to table", table.name)
	//注意:在使用addInternal时的函数已经加上了lock锁，所以这里可以直接覆值不会出现并发问题
	//为什么要这么设计：是因为此函数是必定要加锁的，但是调用此函数的函数如果也需要加锁，那么
	//势必过程就是，加锁->解锁->加锁->解锁。为了减少一次加解锁过程，我们就把加解锁跨越两个函数，只用一次加解锁
	//就能实现。但是这样也带来可读性的麻烦，需要注意
	table.items[item.key] = item

	expDur := table.cleanupInterval
	addedItem := table.addedItem
	table.Unlock()

	//添加item的回调函数触发
	if addedItem != nil {
		for _, callback := range addedItem {
			callback(item)
		}
	}

	//如果当前的item是有过期时间的，那么就可能需要启动一个协程进行过期检查
	//如果定时检查器的时间为0，或者当前的生命周期小于最小的定时启动时间，那么就需要重新进行
	//过期检查
	if item.lifeSpan > 0 && (expDur == 0 || item.lifeSpan < expDur) {
		//这个地方会扫描所有的item，可以认为是STW，当批量写入数据时
		//可能会造成频繁的扫描全表，造成时延期变高
		//TODOSTW
		table.expirationCheck()
	}
}

//deleteInternal删除操作
func (table *CacheTable) deleteInternal(key interface{}) (*CacheItem, error) {
	r, ok := table.items[key]
	if !ok {
		return nil, ErrKeyNotFound
	}

	aboutToDeleteItem := table.aboutToDeleteItem
	//下面操作不需要再访问table的时候，就可以解锁
	table.Unlock()

	if aboutToDeleteItem != nil {
		for _, callback := range aboutToDeleteItem {
			callback(r)
		}
	}

	//item级别的删除回调函数
	r.RLock()
	defer r.RUnlock()
	if r.aboutToExpire != nil {
		for _, callback := range r.aboutToExpire {
			callback(key)
		}
	}

	//删除item操作需要加锁
	table.Lock()
	table.log("Deleting item with key", key)
	delete(table.items, key)

	return r, nil
}

//Delete 删除k-v
func (table *CacheTable) Delete(key interface{}) (*CacheItem, error) {
	table.Lock()
	defer table.Unlock()
	return table.deleteInternal(key)
}

//判断key是否存在
func (table *CacheTable) Exists(key interface{}) bool {
	table.RLock()
	defer table.RUnlock()

	_, ok := table.items[key]
	return ok
}

//NotFoundAdded 如果没找到那么就新增
func (table *CacheTable) NotFoundAdded(key interface{}, lifeSpan time.Duration, data ...interface{}) bool {
	table.Lock()

	if _, ok := table.items[key]; ok {
		table.Unlock()
		return false
	}

	item := NewCacheItem(key, lifeSpan, data)
	table.addInternal(item)

	return true
}

//Value返回存在的k-v内容，并且会刷新v的存活时间
func (table *CacheTable) Value(key interface{}, args ...interface{}) (*CacheItem, error) {
	table.RLock()
	r, ok := table.items[key]
	loadData := table.loadData
	table.Unlock()

	if ok {
		r.KeepAlive()
		return r, nil
	}

	//如果item不存在，那么调用加载函数进行存入
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

//Flush 删除cache表中所有的item
//注意在刷新的过程中，需要检查是否正在等待执行的过期定时任务，
//需要进行停止
func (table *CacheTable) Flush() {
	table.Lock()
	defer table.Unlock()

	table.log("Flushing table", table.name)

	//直接新make一个map，原有的等待回收；这样gc会不会压力很大
	table.items = make(map[interface{}]*CacheItem)
	table.cleanupInterval = 0
	if table.cleanupTimer != nil {
		table.cleanupTimer.Stop()
	}
}

//对k-v的item进行排序，要利用sort的排序接口
//就必须实现swap,len，less那几个接口才行
//这里相当于保存了k，和要对比的访问次数值
type CacheItemPair struct {
	Key         interface{}
	AccessCount int64
}
type CacheItemPairList []CacheItemPair

func (p CacheItemPairList) Swap(i, j int)      { p[i], p[j] = p[j], p[i] }
func (p CacheItemPairList) Len() int           { return len(p) }
func (p CacheItemPairList) Less(i, j int) bool { return p[i].AccessCount < p[j].AccessCount }

//访问最多的前count项item
//作者实现的是按照全部排序后取前k项，这样势必造成了
//运算的浪费呀，如果我只是。还是要根据实际的业务场景走
//如果一般经常只是取第一个那么，每次遍历一遍取就行，而不是要o(n^2)的复杂度
func (table *CacheTable) MostAccessed(count int64) []*CacheItem {
	table.RLock()
	defer table.RUnlock()

	//进行排序
	p := make(CacheItemPairList, len(table.items))
	i := 0
	for k, v := range table.items {
		p[i] = CacheItemPair{k, v.accessdCount}
		i++
	}
	sort.Sort(p)

	//取出前k个item
	var r []*CacheItem
	c := int64(0) //64位int
	for _, v := range p {
		if c >= count {
			break
		}

		item, ok := table.items[v.Key]
		if ok {
			r = append(r, item)
		}
		c++
	}

	return r
}

//内部的日志打印函数，便于查看
func (table *CacheTable) log(v ...interface{}) {
	if table.logger == nil {
		return
	}

	table.logger.Println(v...)
}
