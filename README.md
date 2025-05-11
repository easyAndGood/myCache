# myCache
这是一个Golang分布式缓存框架。本框架的实现，参考了groupcache、memcached和GeeCache的开发思路和源代码。在此，我需要先向这些库的开发人员和相关贡献者表达我的敬意。
## 前言
> There are only two hard things in Computer Science: cache invalidation and naming things (计算科学中只有两件难事：缓存失效和命名。)
> <p align="right">– Phil Karlton</p>
在高并发的分布式的系统中，缓存是必不可少的一部分。<br>没有缓存对系统的加速和阻挡大量的请求直接落到系统的底层，系统是很难撑住高并发的冲击，所以分布式系统中缓存的设计是很重要的一环。Golang并没有自带的分布式缓存框架，比较流行的第三方框架有groupcache等。参考了相关框架的开发思路和源代码后，笔者开发了一个简单的分布式缓存框架——myCache，目前已具备了常见分布式缓存框架需要的基础功能。
<br>本框架中，键值需要是string类型，缓存值需要是[]byte类型（或可以转成为[]byte）。选择 byte 类型是为了能够支持任意的数据类型的存储，例如字符串、图片等。<br>
![image](https://github.com/Suuuuuu96/myCache/blob/main/img/Cache%20Aside%E6%A8%A1%E5%BC%8F-%E7%BC%93%E5%AD%98%E6%9B%B4%E6%96%B0%E5%9B%BE.png)
## 功能
* 缓存的分布式存储：本框架利用一致性哈希(consistent hashing)算法确定各键值的对应缓存节点（的IP地址），同时引入虚拟节点解决数据倾斜问题。
* 节点通讯：本框架中，每一个节点都同时是基于HTTP的服务端和客户端，能向其他节点发出请求、也能响应其他节点的请求。
* 缓存淘汰：本框架实现了LRU(Least Recently Used，最近最少使用)算法，及时淘汰不常用缓存数据，保证了一定容量下缓存的正常使用。
* 单机并发：本框架利用Go语言自带的互斥锁，保证了单机并发读写时的数据安全。
* Single Flight：本框架使用Single Flight机制，合并较短时间内相继达到的针对同一键值的请求，抑制重复的函数调用，防止缓存击穿。
* Protobuf 通信：本框架使用Protocol Buffers作为数据描述语言，显著地压缩了二进制数据传输的大小，降低了节点之间通讯的成本。
## 框架重要概念
* Group/组：同一个类型或领域的内容，由同一个Group来存储。一个节点可以存储多个Group的（部分）数据，一个Group的内容可以分布在多个节点。
* 节点：在本框架中，一个可以存储数据并使用HTTP通讯的主机就是一个节点。
* single flight：直译为单程飞行。短时间内，最早到来的请求将调用获得数据的函数。而其他请求不再调用该函数，而是等待着分享最早请求获得的数据。
* HTTPPool：所有节点之间的HTTP通讯任务，均由HTTPPool来负责。HTTPPool的方法ServeHTTP()负责响应其他节点的请求，HTTPPool的字段httpGetters负责向其他节点发出请求。
* consistent hash/一致性哈希：consistenthash.Map是基于一致性哈希的字典。功能：对于给定的key值，返回对应缓存节点（的IP地址）；或者添加节点。
* PeerGetter：【数据获得器】接口，实现该接口的结构体必须：能从指定group获得指定key对应的值，并返回。
* PeerPicker：【数据获得器的选择器】接口，实现该接口的结构体必须能根据传入的 key 找到并返回对应的PeerGetter【数据获得器】。
## 样例程序
```
package main

import (
	"fmt"
	"log"
	"net/http"

	"mycache"
)

var db = map[string]string{
	"Tom":  "630",
	"Jack": "1589",
	"Sam":  "12567",
}

func createGroup(order int) *mycache.Group {
	fullPersistentFile := ""
	if order == 4 {
		fullPersistentFile = fmt.Sprintf("../../myFile/persistence_path%d/scores/append.data.1746944363741", order)
	}
	conf := mycache.Conf{
		Name:               "scores",
		EnablePersistence:  true,
		PersistencePath:    fmt.Sprintf("../../myFile/persistence_path%d", order),
		LoadPersistentFile: false,
		FullPersistentFile: fullPersistentFile,
		// IncrPersistentFile: "",
	}
	return mycache.NewGroup(conf, 2<<10, mycache.GetterFunc(
		func(key string) ([]byte, error) {
			log.Println("[SlowDB] search key", key)
			if v, ok := db[key]; ok {
				return []byte(v), nil
			}
			return nil, fmt.Errorf("%s not exist", key)
		}))
}

func startCacheServer(addr string, addrs []string, g *mycache.Group) {
	peers := mycache.NewHTTPPool(addr)
	peers.Set(addrs...)
	g.RegisterPeers(peers)
	log.Println("mycache is running at", addr)
	log.Fatal(http.ListenAndServe(addr[7:], peers))
}

func startAPIServer(apiAddr string, g *mycache.Group) {
	http.Handle("/api", http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			key := r.URL.Query().Get("key")
			view, err := g.Get(key)
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			w.Header().Set("Content-Type", "application/octet-stream")
			w.Write(view.ByteSlice())
		}))
	log.Println("fontend server is running at", apiAddr)

	log.Fatal(http.ListenAndServe(apiAddr[7:], nil))
	// log.Fatal里面的内容出错则：打印输出内容；退出应用程序；defer函数不会执行。
}

func main() {
	apiAddr := "http://localhost:9999"
	addrMap := map[int]string{
		8001: "http://localhost:8001",
		8002: "http://localhost:8002",
		8003: "http://localhost:8003",
	}
	var addrs []string
	for _, v := range addrMap {
		addrs = append(addrs, v)
	}
	g := createGroup(0)
	g2 := createGroup(2)
	g3 := createGroup(3)
	g4 := createGroup(4)

	go startCacheServer(addrMap[8001], []string(addrs), g)
	go startCacheServer(addrMap[8002], []string(addrs), g2)
	go startCacheServer(addrMap[8003], []string(addrs), g3)
	startAPIServer(apiAddr, g4)
}

```

### 说明&注意事项
#### Group
一个 Group 可以认为是一个缓存的命名空间，每个 Group 拥有一个唯一的名称 name。比如可以创建三个 Group，缓存学生的成绩命名为 scores，缓存学生信息的命名为 info，
缓存学生课程的命名为 courses。<br>
从Group中进行查找的顺序：<br>
（1）查找mainCache；如果命中直接返回value；如果失败则进行（2）<br>
（2）查找key对应真实节点的名称。如果对应真实节点就是本节点，则进行（3）；如果是其他节点则进行（4）<br>
（3）通过getter获得key对应value，进行相关处理再返回。<br>
（4）请求其他节点返回结果。<br>
使用方法：<br>
（1）构造用户自定义的回调函数getter。用户对缓存进行设置，均需要通过getter。<br>
（2）使用NewGroup，新建一个Group，同时设置：名称name,最大容量cacheBytes,回调函数getter。<br>
（3）新建一个HTTPPool，并使用Group.RegisterPeers将该HTTPPool设置为Group.peers。<br>
（4）使用GetGroup(name)可以获得该name对应的Group的指针。<br>
（5）使用Group.Get(key)即可获得键值对应的value。<br>
注意程序中，GetterFunc是一个回调函数(callback)，在缓存不存在时，调用这个函数，得到源数据。定义一个函数类型 F，并且实现接口 A 的方法，然后在这个方法中调用自己。
这是 Go 语言中将其他函数（参数返回值定义与 F 一致）转换为接口 A 的常用技巧。GetterFunc类型的函数，均有名为Get的method，因此任意GetterFunc类型的函数都的Getter的实现。


#### single flight
single flight机制可以解决缓存击穿问题。<br><br>
常见的缓存使用问题包括：<br>
* 缓存雪崩：缓存在同一时刻全部失效，造成瞬时DB请求量大、压力骤增，引起雪崩。缓存雪崩通常因为缓存服务器宕机、缓存的 key 设置了相同的过期时间等引起。（多个key）<br>
* 缓存击穿：一个存在的key，在缓存过期的一刻，同时有大量的请求，这些请求都会击穿到 DB ，造成瞬时DB请求量大、压力骤增。（一个存在的key）<br>
* 缓存穿透：查询一个不存在的数据，因为不存在则不会写到缓存中，所以每次都会去请求 DB，如果瞬间流量过大，穿透到 DB，导致宕机。（一个不存在的key）<br>

<br>GroupCall 是 singleflight 的主数据结构，管理不同 key 的请求(call)。有两个方法：<br>
* Do方法，接受一个字符串Key和一个待调用的函数，会返回调用函数的结果和错误。使用Do方法的时候，它会根据提供的Key判断是否去真正调用fn函数。同一个 key，在同一时间只有第一次调用Do方法时才会去执行fn函数，其他并发的请求会等待调用的执行结果。<br>
* fn是一个能返回key对应值的函数。fn函数的具体内容：使用 PickPeer() 方法选择节点，若非本机节点，则调用 getFromPeer() 从远程获取。若是本机节点或远程获取失败，则回退到 getLocally()。

例子：<br>
第0秒协程A到来，第10秒协程B到来，第20秒协程C到来，这三个协程都是请求键值"张三"对应的值。<br>
第0秒，协程A到来，为了防止自己读取（及修改）GroupCall时GroupCall被别人修改，所以使用mu锁定。<br>
A查看GroupCall.m，发现并没有自己想要的值（的请求）。<br>
新建一个请求call，设置计数器=1（表示正在请求资源），登记到GroupCall.m（由于之后不再修改GroupCall，所以解锁mu）。<br>
调用fn()，向后方请求结果。（这里假定响应时间需要一分钟）<br>
第10秒，协程B到来，查看GroupCall.m，发现已经存在自己想要的值的请求c（call）。<br>
B调用c.wg.Wait()，发现计数器=1，也就是说：已经向后方进行了请求但还没有结果。于是B阻塞了。<br>
第20秒，协程C到来。与B相似的，C发现自己需要的值已经有人在请求了（计数器=1），但还没有结果。于是C阻塞了。<br>
第60秒，A得到了想要的结果（结果保存在call中），调用Done()使计数器变为0。<br>
计数器=0后，B和C相继被唤醒。<br>
B返回自己请求值对应call所保存的结果，结束任务。<br>
A删除了了m中key对应的call（只是删除了GroupCall与这个call的联系，实际上ABC共享这个的call依然存在），返回结果。<br>
C也返回自己请求值对应call所保存的结果，结束任务。<br>
说明：最早来到的请求，最早建立call。在call向后方请求数据的过程中，所有对同一个key的请求都会等待着这个call返回数据。<br>


#### HTTPPool
HTTPPool implements PeerPicker for a pool of HTTP peers.HTTPPool 只有 2 个参数，一个是 self，用来记录自己的地址，包括主机名/IP 和端口。
另一个是 basePath，作为节点间通讯地址的前缀，默认是 /mycache/，那么 http://example.com/mycache/ 开头的请求，就用于节点间的访问。
因为一个主机上还可能承载其他的服务，加一段 Path 是一个好习惯。比如，大部分网站的 API 接口，一般以 /api 作为前缀。<br>
HTTPPool的字段：<br>
```
self        string
basePath    string
mu          sync.Mutex 
peers       *consistenthash.Map 
httpGetters map[string]*httpGetter 
```
其中，peers是一致性哈希算法的字典，用来根据具体的 key 选择节点。peers是键值指向IP地址，如"小明"→"http://10.0.0.2:8008"。<br>
httpGetters是一个IP地址指向一个【数据获得器】。即每一个远程节点（的IP地址）指向一个 httpGetter。httpGetter 与远程节点的地址 baseURL 有关。<br>
HTTPPool的方法：<br>
```
ServeHTTP(w http.ResponseWriter, r *http.Request) ：响应其他节点的请求。
Set(peers ...string) ：传入所有节点（包括本节点）的IP地址的集合，设置同辈节点的信息。
PickPeer(key string) (PeerGetter, bool)： 返回键值对应的【数据获得器】。
```

#### consistent hash
结构体consistenthash.Map是基于一致性哈希的字典。功能：对于给定的key值，返回对应缓存节点（的名称）；或者添加节点。<br>
hash：哈希算法，默认是crc32.ChecksumIEEE<br>
replicas：虚拟节点倍数。<br>
keys：哈希环。节点的名称对应的哈希值。一个真实节点对应多个虚拟节点。<br>
hashMap：虚拟节点与真实节点的映射表 hashMap，键是虚拟节点的哈希值，值是真实节点的名称。<br>
节点名称哈希值到节点名称的映射。由于虚拟节点的存在，可能有多个哈希值对应一个真实节点。<br>
每个真实节点有一个唯一的名称作为标识符。<br>

#### PeerPicker与PeerGetter
PeerPicker：<br>
PeerPicker是一个【数据获得器的选择器】接口，实现该接口的结构体必须能根据传入的 key 找到并返回对应的【数据获得器】。PeerPicker里面需要有节点选择算法，如取余数法等等。本框架仅实现了基于一致性哈希算法，因此实现了PeerPicker的HTTPPool包含于一致性哈希字典。<br>
PeerGetter：<br>
PeerGetter是一个【数据获得器】接口，实现该接口的结构体必须：能从指定group获得指定key对应的值，并返回。由于是分布式框架，实现PeerGetter的结构体需要具有从别的节点获得数据的能力。不同节点之间的通信方式有很多种，比如蓝牙、直接使用USB数据线传输。<br>
本框架内仅实现了基于HTTP的【数据获得器】：httpGetter。每一个远程节点（一个IP地址）对应一个 httpGetter。不同的远程节点，使用的通信方式可能不一样，比如说有的节点之间使用蓝牙通信、有的是HTTP。但本框架仅支持不同节点使用HTTP通信。
