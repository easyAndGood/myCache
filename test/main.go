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

/*
测试的命令

查询
curl http://localhost:9999/api?key=Tom
curl http://localhost:8001/_mycache/scores/Tom
curl http://localhost:8001/_mycache/scores/Jack
curl http://localhost:8001/_mycache/scores/Sam

删除
curl -X DELETE http://localhost:8001/_mycache/scores/Tom
curl -X DELETE http://localhost:8001/_mycache/scores/Sam

信息
curl http://localhost:8001/_mycache_internal/scores

备份
curl -X POST http://localhost:8001/_mycache_internal/scores/backup
*/
