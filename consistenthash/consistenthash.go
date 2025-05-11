package consistenthash

import (
	"hash/crc32"
	"sort"
	"strconv"
)

type Hash func(data []byte) uint32

/*
基于一致性哈希的字典。功能：对于给定的key值，返回对应缓存节点（的名称）；或者添加节点。
hash：哈希算法，默认是crc32.ChecksumIEEE
replicas：虚拟节点倍数。
keys：哈希环。节点的名称对应的哈希值。一个真实节点对应多个虚拟节点。
hashMap：虚拟节点与真实节点的映射表 hashMap，键是虚拟节点的哈希值，值是真实节点的名称。
节点名称哈希值到节点名称的映射。由于虚拟节点的存在，可能有多个哈希值对应一个真实节点。
每个真实节点有一个唯一的名称作为标识符。
*/
type Map struct {
	hash     Hash
	replicas int
	keys     []int
	hashMap  map[int]string
}

func New(replicas int, fn Hash) *Map {
	m := &Map{
		replicas: replicas,
		hash:     fn,
		hashMap:  make(map[int]string),
	}
	if m.hash == nil {
		m.hash = crc32.ChecksumIEEE
	}
	return m
}

// Add 函数允许传入0或多个真实节点的名称（或IP地址），并将这些节点追加到哈希环上m.keys。
func (m *Map) Add(keys ...string) {
	for _, key := range keys {
		for i := 0; i < m.replicas; i++ {
			hash := int(m.hash([]byte(strconv.Itoa(i) + key)))
			m.keys = append(m.keys, hash)
			m.hashMap[hash] = key
		}
	}
	sort.Ints(m.keys)
	/*
		需要对环上的哈希值进行排序，后面才能进行二分查找。
		如key为"localhost:8001"、replicas是3。
		则h1="1localhost:8001"的哈希值，则h2="2localhost:8001"的哈希值，
		则h3="3localhost:8001"的哈希值，且hashMap[h1]=hashMap[h2]=hashMap[h3]="localhost:8001"
	*/
}

func (m *Map) Get(key string) string {
	if len(m.keys) == 0 { // 如果一个节点都没有则返回""
		return ""
	}
	hash := int(m.hash([]byte(key)))
	/*
		func Search(n int, f func(int) bool) int
		search使用二分法进行查找，Search()方法回使用“二分查找”算法来搜索某指定切片[0:n]，
		并返回能够使f(i)=true的最小的i（0<=i<n）值。
		简单地说，Search返回[0,n)之间满足f的最小值。如果一个都没有，则返回n。
	*/
	idx := sort.Search(len(m.keys), func(i int) bool {
		return m.keys[i] >= hash
	})

	return m.hashMap[m.keys[idx%len(m.keys)]]
}

/*
缓存雪崩：缓存在同一时刻全部失效，造成瞬时DB请求量大、压力骤增，引起雪崩。
常因为缓存服务器宕机，或缓存设置了相同的过期时间引起。
*/
