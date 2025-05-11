package mycache

import pb "mycache/mycachepb"

/*
节点选择器。
PeerPicker是一个【数据获得器的选择器】接口，实现该接口的结构体必须能根据传入的 key 找到并返回对应的【数据获得器】。
PeerPicker里面需要有节点选择算法，如取余数法等等。本框架仅实现了基于一致性哈希算法，因此实现了PeerPicker的HTTPPool包含于一致性哈希字典。
*/
type PeerPicker interface {
	PickPeer(key string) (peer PeerGetter, ok bool) // 根据传入的 key 选择相应节点 PeerGetter。
}

/*
PeerGetter是一个【数据获得器】接口，实现该接口的结构体必须：能从指定group获得指定key对应的值，并返回。
由于是分布式框架，实现PeerGetter的结构体需要具有从别的节点获得数据的能力。
不同节点之间的通信方式有很多种，比如蓝牙、直接使用USB数据线传输。
本框架内仅实现了基于HTTP的【数据获得器】：httpGetter。
每一个远程节点（一个IP地址）对应一个 httpGetter。
不同的远程节点，使用的通信方式可能不一样，比如说有的节点之间使用蓝牙通信、有的是HTTP。但本框架仅支持不同节点使用HTTP通信。
*/
type PeerGetter interface {
	Get(in *pb.Request, out *pb.KVResponse) error
}
