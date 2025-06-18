![](https://raw.githubusercontent.com/shiyanhui/dht/master/doc/screen-shot.png)

在这个视频上你可以看到爬取效果[Youtube](https://www.youtube.com/watch?v=AIpeQtw22kc).

## 项目概述 (Project Overview)

本仓库是 BitTorrent DHT 协议的 Go 语言实现。其主要目标有两个方面：

*   作为标准 DHT 网络节点运行，遵循 BitTorrent DHT 协议规范。
*   作为底层库，用以构建强大的 DHT 网络爬虫，高效收集 torrent 元数据，为 BT 搜索引擎等应用提供支持。

[bthub.io](http://bthub.io) 是一个基于本仓库爬虫模式构建的 BT 搜索引擎，可作为 BTDigg 的替代品。

## 核心功能 (Core Features)

1.  **DHT 协议实现:**
    *   支持节点发现、路由表管理、以及在 DHT 网络中宣告信息。
    *   目前支持的 BEPs (BitTorrent Enhancement Proposals) 包括:
        *   [BEP-3 (部分)](http://www.bittorrent.org/beps/bep_0003.html): BitTorrent 协议规范 (DHT 相关方面)。
        *   [BEP-5](http://www.bittorrent.org/beps/bep_0005.html): DHT 协议 (核心操作: `ping`, `find_node`, `get_peers`, `announce_peer`)。
        *   [BEP-9](http://www.bittorrent.org/beps/bep_0009.html): 用于 torrent 元数据文件交换的扩展。
        *   [BEP-10](http://www.bittorrent.org/beps/bep_0010.html): 握手协议的扩展。
2.  **双重工作模式:**
    *   **标准模式 (Standard Mode):** 作为符合规范的 DHT 节点运行。此模式下，程序严格遵守 DHT 协议，用于加入现有的 DHT 网络、查找其他节点、宣告自身信息等标准 DHT 操作。
    *   **爬虫模式 (Crawling Mode):** 特化为高效发现和下载 torrent 元数据而设计。在此模式下，为了最大化信息获取效率，程序可能不完全遵循 BEP 的所有方面。这是构建 BT 搜索引擎（如 `bthub.io`）的基础。
3.  **元数据获取:**
    *   包含一个 `downloader` 组件 (通过 `dht.NewWire` 初始化)，该组件利用 BEP-9 (元数据交换协议) 来获取 torrent 的元数据信息。
4.  **网络交互:**
    *   处理 KRPC 协议消息 (用于 DHT 节点间的通信)。
    *   高效的路由表管理机制。
    *   内置 IP 黑名单功能，以屏蔽不良节点。

## 主要用途 (Main Use Cases)

*   **参与 DHT 网络:** 允许 Go 应用程序作为标准 DHT 节点加入 BitTorrent DHT 网络。
*   **构建 BT 搜索引擎:** 提供核心的元数据爬取能力，可用于搭建类似 `bthub.io` 的 BT 搜索引擎。
*   **学术研究与网络分析:** 可用于分析 DHT 网络的拓扑结构、节点行为、信息传播等特性。

## Installation

    go get github.com/lostmaniac/dht

## Example

下面是一个简单的爬虫例子，你可以到[这里](https://github.com/lostmaniac/dht/blob/master/sample)看完整的Demo。

```go
import (
    "fmt"
    "github.com/lostmaniac/dht"
)

func main() {
    downloader := dht.NewWire(65536)
    go func() {
        // once we got the request result
        for resp := range downloader.Response() {
            fmt.Println(resp.InfoHash, resp.MetadataInfo)
        }
    }()
    go downloader.Run()

    config := dht.NewCrawlConfig()
    config.OnAnnouncePeer = func(infoHash, ip string, port int) {
        // request to download the metadata info
        downloader.Request([]byte(infoHash), ip, port)
    }
    d := dht.New(config)

    d.Run()
}
```

## Download

这个是已经编译好的Demo二进制文件，你可以到这里[下载](https://github.com/lostmaniac/dht/files/407021/spider.zip)。

## 当前限制 (Current Limitations)

*   **NAT 穿透:** 目前版本在 NAT 网络环境下可能存在连接问题，尚未完全实现 NAT 穿透。
*   **内存消耗:** 默认的爬虫配置 (尤其在爬虫模式下) 需要大约 300MB 内存。用户可以根据服务器的可用内存调整 `MaxNodes` 和 `BlackListMaxSize` 参数以优化内存使用。

## 未来工作 (Future Work)

*   [ ] **NAT 穿透:** 实现更完善的 NAT 穿透机制，使得程序在局域网或复杂网络环境中也能稳定运行。
*   [ ] **完整 BEP-3 实现:** 完整实现 BEP-3 中定义的资源下载功能，不仅仅局限于元数据。
*   [ ] **性能优化:** 持续优化代码，提高运行效率和降低资源消耗。

## Blog

你可以在[这里](https://github.com/lostmaniac/dht/wiki)看到DHT Spider教程。

## License

[MIT](https://github.com/lostmaniac/dht/blob/master/LICENSE)
