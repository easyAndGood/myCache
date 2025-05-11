package mycache

import (
	"errors"
	"sync"

	"mycache/lru"
	"mycache/persistence"
)

// 带锁的缓存器
type cache struct {
	mu                 sync.RWMutex
	data               *lru.Cache
	cacheBytes         int64 // 最大容量
	groupName          string
	enablePersistence  bool                       // 是否开启持久化
	persistencePath    string                     // 持久化的路径，仅当enablePersistence为true时有效。例如"./persistence"，则相关文件会存储在"./persistence/{groupName}"下
	loadPersistentFile bool                       // 是否在初始化时加载持久化文件
	fullPersistentFile string                     // 初始化时加载的全量持久化文件，例如"./persistence/{groupName}/full.bin"
	incrPersistentFile string                     // 初始化时加载的增量持久化文件
	writeSequence      *persistence.WriteSequence // 持久化工具
}

type CacheInfo struct {
	CurrentCacheBytes int64 // 最大容量
	MaxCacheBytes     int64 // 当前容量
	KeysNum           int64 // 键值对数量
}

func (c *cache) init() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	keys := c.writeSequence.GetAllIndexKeys()
	for _, key := range keys {
		val, err := c.writeSequence.Get([]byte(key))
		if err == nil {
			value := ByteView{data: cloneBytes(val)}
			c.data.Add(key, value)
		}
	}
	return nil
}

func (c *cache) add(key string, val ByteView) error {
	if len(key) == 0 {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.enablePersistence && c.writeSequence != nil {
		err := c.writeSequence.Put([]byte(key), val.ByteSlice())
		if err != nil {
			return err
		}
	}
	c.data.Add(key, val)
	return nil
}

func (c *cache) get(key string) (val ByteView, ok bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if ans, ok := c.data.Get(key); ok {
		return ans.(ByteView), ok
	}
	return
}

func (c *cache) GetInfo() CacheInfo {
	c.mu.RLock()
	defer c.mu.RUnlock()
	ans := CacheInfo{
		CurrentCacheBytes: c.data.GetCurrentUsedBytes(),
		MaxCacheBytes:     int64(c.cacheBytes),
		KeysNum:           int64(c.data.Len()),
	}
	return ans
}

func (c *cache) delete(key string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.enablePersistence {
		err := c.writeSequence.Delete([]byte(key))
		if err != nil {
			return err
		}
	}
	c.data.Remove(key)
	return nil
}

func (c *cache) backup() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.enablePersistence {
		err := c.writeSequence.Merge()
		if err != nil {
			return err
		}
		err = c.writeSequence.Backup("")
		if err != nil {
			return err
		}
	} else {
		return errors.New("backup failed, persistence is not enabled")
	}
	return nil
}
