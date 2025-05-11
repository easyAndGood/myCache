package mycache

import (
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
	"sync"

	"github.com/golang/protobuf/proto"

	"mycache/consistenthash"
	pb "mycache/mycachepb"
)

const (
	defaultBasePath  = "/_mycache/"
	internalBasePath = "/_mycache_internal/"
	defaultReplicas  = 50
)

/*
HTTPPool implements PeerPicker for a pool of HTTP peers.
HTTPPool 只有 2 个参数，一个是 self，用来记录自己的地址，包括主机名/IP 和端口。
另一个是 basePath，作为节点间通讯地址的前缀，默认是 /_mycache/，
那么 http://example.com/_mycache/ 开头的请求，就用于节点间的访问。
因为一个主机上还可能承载其他的服务，加一段 Path 是一个好习惯。
比如，大部分网站的 API 接口，一般以 /api 作为前缀。
方法：
ServeHTTP(w http.ResponseWriter, r *http.Request) ：响应其他节点的请求。
Set(peers ...string) ：传入所有节点（包括本节点）的IP地址的集合，设置同辈节点的信息。
PickPeer(key string) (PeerGetter, bool)： 返回键值对应的【数据获得器】。
*/
type HTTPPool struct {
	self        string // 本服务节点的URL, e.g. "https://example.net:8000"
	basePath    string
	mu          sync.Mutex             // guards peers and httpGetters
	peers       *consistenthash.Map    // 一致性哈希算法的字典，用来根据具体的 key 选择节点。peers是键值指向IP地址，如"小明"→"http://10.0.0.2:8008"。
	httpGetters map[string]*httpGetter // keyed by e.g. "http://10.0.0.2:8008"。httpGetters是一个IP地址指向一个【数据获得器】。
	// 即每一个远程节点（的IP地址）指向一个 httpGetter。httpGetter 与远程节点的地址 baseURL 有关。
}

// NewHTTPPool initializes an HTTP pool of peers.
func NewHTTPPool(self string) *HTTPPool {
	return &HTTPPool{
		self:     self, // 如"localhost:9999"
		basePath: defaultBasePath,
	}
}

// Log info with server name
func (p *HTTPPool) Log(format string, v ...any) {
	log.Printf("[Server %s] %s", p.self, fmt.Sprintf(format, v...))
}

// 服务器部分：
// ServeHTTP handle all http requests
func (p *HTTPPool) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !strings.HasPrefix(r.URL.Path, p.basePath) && !strings.HasPrefix(r.URL.Path, internalBasePath) { // strings.Hasprefix(s, prefix)返回s是否以prefix开头
		panic("HTTPPool serving unexpected path: " + r.URL.Path) // 如果r.URL.Path不以"/_mycache/"等开头
	}
	p.Log("%s %s", r.Method, r.URL.Path)

	if internalBasePath == string(r.URL.Path[:len(internalBasePath)]) {
		parts := strings.SplitN(r.URL.Path[len(internalBasePath):], "/", 2)
		// parts[0]是scores
		if len(parts) <= 0 || len(parts) >= 3 {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		groupName := parts[0]
		if len(parts) == 1 && r.Method == "GET" {
			p.ServeInternalInfo(w, groupName)
		} else if len(parts) == 2 && parts[1] == "backup" && r.Method == "POST" {
			p.ServeInternalBackup(w, groupName)
		} else {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		return
	}
	/*
		p.basePath是/_mycache/
		r.URL.Path是/_mycache/scores/Tom
		r.URL.Path[len(p.basePath):]是scores/Tom
		/<basepath>/<groupname>/<key> required
	*/
	parts := strings.SplitN(r.URL.Path[len(p.basePath):], "/", 2) // parts[0]是scores，parts[1]是Tom
	if len(parts) != 2 {
		http.Error(w, "bad request", http.StatusBadRequest) // 如果parts不是group+key，则非法
		return
	}
	groupName := parts[0]
	key := parts[1]
	p.ServeKey(w, r, groupName, key)
}

func (p *HTTPPool) ServeKey(w http.ResponseWriter, r *http.Request, groupName, key string) {
	group := GetGroup(groupName)
	if group == nil {
		http.Error(w, "no such group: "+groupName, http.StatusNotFound)
		return
	}
	if r.Method == "GET" {
		view, err := group.Get(key)
		body, err := proto.Marshal(&pb.KVResponse{Value: view.ByteSlice()})
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Write(body) // 按事先协商好：body必须是序列化的pb.KVResponse格式的数据。
		return
	}
	if r.Method == "DELETE" {
		fmt.Println("delete key: ", key)
		err := group.Delete(key)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		return
	}
}

func (p *HTTPPool) ServeInternalInfo(w http.ResponseWriter, groupName string) {
	group := GetGroup(groupName)
	if group == nil {
		http.Error(w, "no such group: "+groupName, http.StatusNotFound)
		return
	}
	info := group.GetCacheInfo()
	response := &pb.InfoResponse{
		KeysNum:          info.KeysNum,
		CurrentUsedBytes: info.CurrentCacheBytes,
		MaxUsedBytes:     info.MaxCacheBytes,
	}
	body, err := proto.Marshal(response)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Write(body)
	fmt.Println(groupName, " CacheInfo info: ", info)
}

func (p *HTTPPool) ServeInternalBackup(w http.ResponseWriter, groupName string) {
	group := GetGroup(groupName)
	if group == nil {
		http.Error(w, "no such group: "+groupName, http.StatusNotFound)
		return
	}
	err := group.Backup()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	return
}

// 客户端部分：

func (p *HTTPPool) Set(peerIPs ...string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.peers = consistenthash.New(defaultReplicas, nil) // 新建一个一致性哈希字典。
	p.peers.Add(peerIPs...)
	p.httpGetters = make(map[string]*httpGetter, len(peerIPs))
	for _, peer := range peerIPs { // peers是所有节点（包括本节点）的IP地址的集合
		p.httpGetters[peer] = &httpGetter{baseURL: peer + p.basePath} // 一个IP地址，指向一个数据获得器。
	}
}

/*
先通过p.peers的一致性哈希获得键值key对应的节点的IP地址，然后返回该IP地址对应的数据获得器httpGetters。
peer是按一致性哈希字典得到的IP地址，如"https://example.net:8000"
*/
func (p *HTTPPool) PickPeer(key string) (PeerGetter, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if peer := p.peers.Get(key); peer != "" && peer != p.self {
		p.Log("Pick peer %s", peer)
		return p.httpGetters[peer], true // 如果peer不是空且不是本节点，则返回peer对应的【数据获得器】。
	}
	return nil, false
}

var _ PeerPicker = (*HTTPPool)(nil) // 检查HTTPPool是否实现了【数据获得器的选择器】PeerPicker接口

// 创建 httpGetter，实现 PeerGetter 接口。——基于HTTP的【数据获得器】。
type httpGetter struct {
	baseURL string // 表示将要访问的远程节点的地址，例如 http://example.com/_mycache/。
}

func (h *httpGetter) Get(in *pb.Request, out *pb.KVResponse) error {
	u := fmt.Sprintf(
		"%v%v/%v",
		h.baseURL,
		url.QueryEscape(in.GetGroup()),
		url.QueryEscape(in.GetKey()),
		// func QueryEscape(s string) string ：该函数对s进行转码使之可以安全的用在URL查询里。
		// url.QueryEscape("http://images.com /cat.png")的结果是"http%3A%2F%2Fimages.com+%2Fcat.png"
	)
	res, err := http.Get(u)
	if err != nil {
		return err
	}
	defer res.Body.Close()

	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("server returned: %v", res.Status)
	}
	// 需要事先协商好：传来的数据必须是序列化的pb.KVResponse格式的数据。
	bytes, err := io.ReadAll(res.Body) // ReadAll() 是一次读取所有数据
	if err = proto.Unmarshal(bytes, out); err != nil {
		return fmt.Errorf("decoding response body: %v", err)
	}
	return nil
}

var _ PeerGetter = (*httpGetter)(nil)
