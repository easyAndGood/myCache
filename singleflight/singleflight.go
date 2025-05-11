package singleflight // single flight 单程飞行。提供了一种抑制重复函数调用的机制。

/*
	缓存雪崩：缓存在同一时刻全部失效，造成瞬时DB请求量大、压力骤增，引起雪崩。缓存雪崩通常因为缓存服务器宕机、缓存的 key 设置了相同的过期时间等引起。
	缓存击穿：一个存在的key，在缓存过期的一刻，同时有大量的请求，这些请求都会击穿到 DB ，造成瞬时DB请求量大、压力骤增。
	缓存穿透：查询一个不存在的数据，因为不存在则不会写到缓存中，所以每次都会去请求 DB，如果瞬间流量过大，穿透到 DB，导致宕机。
	如果请求是串行的，即一个请求得到满足后另一个请求才到来，则singleflight不起作用。
	仅当多个（针对同一个key的）请求同时出现时（最后一个请求应该在第一个请求获得数据之前达到），
	singleflight起作用：最早到来的请求将进行获得数据的操作（即调用fn函数）。
	而其他请求不再调用fn函数，而是等待着分享最早请求获得的数据。
	当这些针对同一个key的请求全部结束后，GroupCall将不再保存对应的缓存值，关于key的缓存告一段落。
*/

import (
	"sync"
)

/*
call 代表正在进行中，或已经结束的请求。使用 sync.WaitGroup 锁避免重入。
如果wg的计数器大于等于1，说明正在向后方请求结果。如果计数器等于0，说明已经得到结果。
新建sync.WaitGroup（等待组）时设置一个计数器，初始化为0。
wg.Add(n) 计数器加n。
wg.Done() 计数器减1。
wg.Wait() 当计数器不等于 0 时阻塞，直到变 0。
*/
type call struct {
	wg  sync.WaitGroup
	val interface{}
	err error
}

// Call的Group。
// Group 是 singleflight 的主数据结构，管理不同 key 的请求(call)。
type GroupCall struct {
	mu sync.Mutex       // 互斥锁，用于保护m。
	m  map[string]*call // 一个键值，指向一个请求（call）。
}

/*
Do方法，接受一个字符串Key和一个待调用的函数，会返回调用函数的结果和错误。
使用Do方法的时候，它会根据提供的Key判断是否去真正调用fn函数。
同一个 key，在同一时间只有第一次调用Do方法时才会去执行fn函数，其他并发的请求会等待调用的执行结果。
fn是一个能返回key对应值的函数。fn函数的具体内容：
使用 PickPeer() 方法选择节点，若非本机节点，则调用 getFromPeer() 从远程获取。
若是本机节点或远程获取失败，则回退到 getLocally()。
*/
func (g *GroupCall) Do(key string, fn func() (interface{}, error)) (interface{}, error) {
	g.mu.Lock()
	if g.m == nil {
		g.m = make(map[string]*call)
	}
	if c, ok := g.m[key]; ok {
		g.mu.Unlock()
		c.wg.Wait()
		return c.val, c.err
	}
	c := new(call)
	c.wg.Add(1)
	g.m[key] = c
	g.mu.Unlock()

	c.val, c.err = fn()
	c.wg.Done()

	g.mu.Lock()
	delete(g.m, key)
	g.mu.Unlock()

	return c.val, c.err
}

/*
	例子：
	第0秒协程A到来，第10秒协程B到来，第20秒协程C到来，这三个协程都是请求键值"张三"对应的值。
	第0秒，协程A到来，为了防止自己读取（及修改）GroupCall时GroupCall被别人修改，所以使用mu锁定。
	A查看GroupCall.m，发现并没有自己想要的值（的请求）。
	新建一个请求call，设置计数器=1（表示正在请求资源），登记到GroupCall.m（由于之后不再修改GroupCall，所以解锁mu）。
	调用fn()，向后方请求结果。（这里假定响应时间需要一分钟）
	第10秒，协程B到来，查看GroupCall.m，发现已经存在自己想要的值的请求c（call）。
	B调用c.wg.Wait()，发现计数器=1，也就是说：已经向后方进行了请求但还没有结果。于是B阻塞了。
	第20秒，协程C到来。与B相似的，C发现自己需要的值已经有人在请求了（计数器=1），但还没有结果。于是C阻塞了。
	第60秒，A得到了想要的结果（结果保存在call中），调用Done()使计数器变为0。
	计数器=0后，B和C相继被唤醒。
	B返回自己请求值对应call所保存的结果，结束任务。
	A删除了了m中key对应的call（只是删除了GroupCall与这个call的联系，实际上ABC共享这个的call依然存在），返回结果。
	C也返回自己请求值对应call所保存的结果，结束任务。
	说明：最早来到的请求，最早建立call。在call向后方请求数据的过程中，所有对同一个key的请求都会等待着这个call返回数据。
*/
