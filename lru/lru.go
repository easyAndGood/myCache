package lru

import (
	"container/list"
)

// 链表一个很大的优点：插入快，删除快。而数组的有优点就是遍历快，索引快。
// list适合于那些频繁插入删除操作的场景。 而slice适合于那些多次查询的场景。
// 所以缓存的数据应该使用list。

// 无锁的缓存器结构体的定义
type Cache struct {
	maxBytes  int64                                   // 允许使用的最大内存。0表示没有限制
	nbytes    int64                                   // 当前已使用的内存
	ll        *list.List                              // 用于储存数据的双向链表
	cache     map[string]*list.Element                // 键是字符串，值是双向链表中对应节点的指针。
	OnEvicted func(key string, value ComputableValue) // 某条记录被移除时的回调函数，可以为 nil。
}

// 键值对，用于存放实际数据。也就是list.Element里面的值。
type entry struct {
	key         string
	insideValue ComputableValue
}

// 可计算大小的值
type ComputableValue interface {
	Len() int64
}

func New(maxBytes int64, onEvicted func(string, ComputableValue)) *Cache {
	return &Cache{
		maxBytes:  maxBytes,
		ll:        list.New(),
		cache:     make(map[string]*list.Element),
		OnEvicted: onEvicted,
	}
}

func (c *Cache) Get(key string) (temp ComputableValue, ok bool) {
	if val, ok := c.cache[key]; ok { // val是*list.Element
		c.ll.MoveToFront(val)     // 约定 front 为队尾
		ele := val.Value.(*entry) // list.Element的值（Value）是接口，需要转换成*entry
		return ele.insideValue, true
	}
	return nil, false
}

func (c *Cache) RemoveOldest() {
	ele := c.ll.Back()
	if ele != nil {
		c.ll.Remove(ele)
		p := ele.Value.(*entry)
		delete(c.cache, p.key)
		c.nbytes -= int64(len(p.key)) + p.insideValue.Len()
		if c.OnEvicted != nil {
			c.OnEvicted(p.key, p.insideValue)
		}
	}
}

func (c *Cache) GetCurrentUsedBytes() int64 {
	return c.nbytes
}

func (c *Cache) GetMaxUsedBytes() int64 {
	return c.maxBytes
}

func (c *Cache) Len() int {
	return c.ll.Len()
}

func (c *Cache) Add(key string, val ComputableValue) bool {
	// 插入成功则返回true，否则false
	if c.maxBytes > 0 && int64(len(key))+val.Len() > c.maxBytes {
		return false
	}
	if value0, ok := c.cache[key]; ok {
		c.ll.PushFront(value0)
		temp := value0.Value.(*entry)
		c.nbytes += temp.insideValue.Len() - val.Len()
		temp.insideValue = val
	} else {
		ele := c.ll.PushFront(&entry{key, val})
		c.cache[key] = ele
		c.nbytes += int64(len(key)) + val.Len()
	}
	for c.maxBytes > 0 && c.nbytes > c.maxBytes {
		c.RemoveOldest()
	}
	return true
}

func (c *Cache) Remove(key string) {
	if len(key) == 0 {
		return
	}
	if ele, ok := c.cache[key]; ok {
		p := ele.Value.(*entry)
		delete(c.cache, p.key)
		c.ll.Remove(ele)
		c.nbytes -= int64(len(p.key)) + p.insideValue.Len()
		if c.OnEvicted != nil {
			c.OnEvicted(p.key, p.insideValue)
		}
	}
}
