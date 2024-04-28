# eidola

## 介绍

Eidola 是一个用Go语言编写的微服务框架，专用于解决在微服务体系结构中常见的一些问题，包括服务注册、服务发现，以及简化微服务之间的通信。

## 特色
- **服务注册**：允许服务实例在启动时自动注册到服务中心。
- **服务发现**：客户端能够发现网络中可用的服务实例，并进行连接。
- **负载均衡**：内置了多种负载均衡策略，可根据需求进行选择和定制。
- **服务限流**：防止过载，保证服务质量(SLA)的机制。
- **服务路由**：根据客户端请求的特性路由请求到合适的服务实例。
- **分布式跟踪：** 提供了强大的API来为您的应用程序添加对分布式跟踪的支持。

## 快速开始

### 安装

首先，确保您已安装Go。然后执行以下命令：

```bash
go github.com/dormoron/eidola
```

### 创建服务端
``` go
package main

import (
	"github.com/dormoron/eidola"
	"github.com/dormoron/eidola/registry/etcd"
	clientv3 "go.etcd.io/etcd/client/v3"
	"time"
)

func main() {
	// 连接etcd
	etcdClient, err := clientv3.New(clientv3.Config{
		Endpoints: []string{"localhost:2379"},
	})
	r, err := etcd.NewRegistry(etcdClient)
	// 创建server
	server, err := eidola.NewServer("your_server_name", eidola.ServerWithRegistry(r),
		eidola.ServerWithRegisterTimeout(time.Second*10))
	// 启动服务	
	err = server.Start(":8081")
	if err != nil {
	    // 处理错误
	}
}
```

### 创建客户端
``` go
package main

import (
	"context"
	"github.com/dormoron/eidola"
	"github.com/dormoron/eidola/registry/etcd"
	clientv3 "go.etcd.io/etcd/client/v3"
	"time"
)

func main() {
	// 连接etcd
	etcdClient, err := clientv3.New(clientv3.Config{
		Endpoints: []string{"82.156.204.14:2379"},
	})
	r, err := etcd.NewRegistry(etcdClient)
	// 创建客户端实例
	client, err := eidola.NewClient(eidola.ClientInsecure(), eidola.ClientWithResolver(r, time.Second*3))
	if err != nil {
	    // 处理错误
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second*10)
	defer cancel()
	// 连接到服务
	conn, err := client.Dial(ctx, "your_server_name")
	if err != nil {
	    // 处理错误
	}
	
	// 现在你可以使用conn来进行RPC调用
}
```

## 贡献
欢迎任何形式的贡献，包括报告bug，提出新功能，以及直接向代码库提交代码。

## 许可证
Mist是在MIT许可下发行的。有关详细信息，请查阅 [LICENSE](LICENSE) 文件。