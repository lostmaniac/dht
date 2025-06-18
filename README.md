![](https://raw.githubusercontent.com/shiyanhui/dht/master/doc/screen-shot.png)

See the video on [YouTube](https://www.youtube.com/watch?v=AIpeQtw22kc).

[中文版README (Chinese Version)](https://github.com/lostmaniac/dht/blob/master/README_CN.md)

## Project Overview

This repository provides a Go language implementation of the BitTorrent DHT (Distributed Hash Table) protocol. Its main objectives are:

*   To function as a standard DHT network node, adhering to BitTorrent DHT protocol specifications.
*   To serve as a foundational library for building powerful DHT network crawlers designed to efficiently collect torrent metadata.

This implementation supports dual modes of operation, catering to different needs within the BitTorrent ecosystem.

## Core Features

1.  **DHT Protocol Implementation:**
    *   Facilitates essential DHT operations such as node discovery, routing table management, and information announcement within the network.
    *   Supports the following BEPs (BitTorrent Enhancement Proposals):
        *   [BEP-3 (The BitTorrent Protocol Specification)](http://www.bittorrent.org/beps/bep_0003.html) - Partial implementation, focusing on DHT-related aspects.
        *   [BEP-5 (DHT Protocol)](http://www.bittorrent.org/beps/bep_0005.html) - Implements core operations like `ping`, `find_node`, `get_peers`, and `announce_peer`.
        *   [BEP-9 (Extension for Peers to Send Metadata Files)](http://www.bittorrent.org/beps/bep_0009.html) - Enables the exchange of torrent metadata files.
        *   [BEP-10 (Extension Protocol)](http://www.bittorrent.org/beps/bep_0010.html) - Supports protocol extensions for enhanced handshake capabilities.
2.  **Dual Operating Modes:**
    *   **Standard Mode:** Operates as a compliant DHT node. In this mode, the software strictly follows DHT protocol specifications, suitable for joining the existing DHT network, discovering peers, and announcing its presence like any standard DHT client.
    *   **Crawling Mode:** A specialized mode optimized for efficiently discovering and downloading torrent metadata. To maximize information retrieval, this mode may not strictly adhere to all aspects of BEPs. It forms the foundational technology for BT search engines.
3.  **Metadata Harvesting:**
    *   Includes a `downloader` component (instantiated via `dht.NewWire`) that utilizes BEP-9 (Extension for Peers to Send Metadata Files) to fetch metadata information for torrents.
4.  **Network Interaction:**
    *   Effectively handles KRPC (Kademlia Remote Procedure Call) protocol messages, which are used for communication between DHT nodes.
    *   Manages an internal routing table to maintain information about other known DHT nodes.
    *   Features an IP blacklist capability to ignore requests from or avoid interaction with potentially problematic IP addresses.

## Main Use Cases

*   **DHT Network Participation:** Allows Go applications to join the BitTorrent DHT network as standard, compliant nodes.
*   **Building BT Search Engines:** The crawling mode provides the core metadata collection capabilities required for developing BT search engine services, similar to [bthub.io](http://bthub.io) (which is built using this library).
*   **Academic Research & Network Analysis:** Can be employed as a tool for academic research to study the characteristics of the DHT network, such as its topology, node behavior, and information propagation patterns.

## Installation

    go get github.com/lostmaniac/dht

## Example

Below is a simple spider. You can find more samples [here](https://github.com/lostmaniac/dht/blob/master/sample).

```go
import (
    "fmt"
    "github.com/lostmaniac/dht"
)

func main() {
    downloader := dht.NewWire(65535)
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

You can download the demo compiled binary file [here](https://github.com/lostmaniac/dht/files/407021/spider.zip).

## Current Limitations

*   **NAT Traversal:** The current version may experience connectivity issues when operating behind NAT (Network Address Translation) devices, as comprehensive NAT traversal mechanisms are not yet fully implemented. This can affect its ability to run effectively in some LAN environments.
*   **Memory Consumption:** The default configuration, especially in crawling mode, can consume approximately 300MB of RAM. Users can adjust the `MaxNodes` and `BlackListMaxSize` parameters to better suit their available system memory.

## Future Work

*   [ ] **NAT Traversal:** Implement robust NAT traversal techniques to ensure reliable operation in various network environments, including LANs.
*   [ ] **Full BEP-3 Implementation:** Extend BEP-3 support to include resource downloading, not just metadata.
*   [ ] **Optimization:** Continuously optimize the codebase for improved performance and reduced resource consumption.

## FAQ

#### Why it is slow compared to other spiders ?

Well, maybe there are several reasons.

- DHT aims to implement the standard BitTorrent DHT protocol, not born for crawling the DHT network. (This is a design choice, with crawling as a secondary, specialized mode).
- NAT Traversal issue. You run the crawler in a local network. (Covered in "Current Limitations").
- It will block ip which looks like bad and a good ip may be mis-judged. (The IP blacklist is a feature; potential misjudgment is an operational aspect).

## License

MIT, read more [here](https://github.com/lostmaniac/dht/blob/master/LICENSE)
