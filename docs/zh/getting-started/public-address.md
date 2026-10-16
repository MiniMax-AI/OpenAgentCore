---
title: "配置公开地址"
source: docs/getting-started/public-address.md
source_hash: 18cf3655776a7bd22a09023a7764abf17461570371b846ef5ce4d1caa3d13a6f
---

设置浏览器、节点、沙箱和应用访问 Core 所用的唯一源地址，并在其前面放置带 HTTPS 的反向代理。本指南面向将 Core 暴露到其所在机器之外的运维人员。

## 选择公开 URL {#choose-the-public-url}

`OAC_PUBLIC_URL` 是应用、节点、沙箱和自托管执行器使用的源地址。Web 只接受其中的主机。Core 从中派生守护进程 WebSocket URL、自托管 `remote_url` 和每个沙箱的连接地址。它是不含路径的 http 或 https 源地址。

| 场景 | 公开 URL |
| --- | --- |
| 在本机试用 | `http://localhost:8080`，`--host` 不是 `0.0.0.0` 或主机没有私有地址时的默认值 |
| 本地网络 | `http://<private address>:8080`，`--host` 为 `0.0.0.0` 且默认路由带有私有网络地址时由安装程序选用 |
| 本地网络之外 | 反向代理提供的 HTTPS 源地址，例如 `https://core.example` |
| E2B 提供商 | 可从互联网访问且不是回环地址的 HTTPS 源地址，因为 E2B 沙箱会从 E2B 云端调用 Core |

安装时用 `--public-url` 传入该源地址，或按[更改公开 URL](#changing-the-public-url)稍后修改。使用选项安装，参阅[安装选项](./install-options.md#pass-flags)。

## 监听器与访问 {#listeners-and-access}

默认安装在 `--host` `0.0.0.0` 的 `--web-port`（8080）上发布 Web。Core 和 PostgreSQL 不发布端口。`--host` 是不含端口、协议或区域的 IPv4 或 IPv6 地址。请在浏览器中使用服务器的具体 IP，而不是通配地址。

| 服务 | 外部可访问性 |
| --- | --- |
| Web | `OAC_HOST` 上的 `OAC_WEB_PORT`。使用反向代理时，用 `--host 127.0.0.1` 安装 |
| Core | 仅通过 Web。`/v1`、`/api/v1` 和 `/docs` 原样透传；已登录的控制台 `/core/v1` 请求带有 Core 密钥。其余由 Web 自己提供；参阅[请求边界](../web/console-server.md#request-boundary) |
| PostgreSQL | 否 |

沙箱是隔离边界：参阅 [Runtime 与外层隔离](../concepts.md#runtime-and-outer-isolation)。

### 端口 {#ports}

安装程序在下载镜像之前检查 `--web-port`。

- `--host` 是该端口绑定的地址。`0.0.0.0` 在所有 IPv4 接口发布 Web。`127.0.0.1` 只在本机发布 Web。
- 端口被占用时安装停止，不会改用其他端口。

`OAC_HOST` 或 `OAC_WEB_PORT` 变化时，`oac apply` 会重新创建 Web。

## 将 Core 置于 HTTPS 之后 {#https-and-the-reverse-proxy}

用 `--host 127.0.0.1` 安装，并把反向代理指向 Web，默认是 `127.0.0.1:8080`。反向代理必须：

- **保留 Host。** Web 仅接受 `OAC_PUBLIC_URL` 中的主机。
- **在 `/api/v1` 上传递 WebSocket 升级请求。** 节点、沙箱 Runtime（包括 E2B）和自托管执行器通过它连接。
- **不对流进行缓冲或设置超时。** `/v1` 会流式传输 Session 事件。
- **接受大文件上传。** 源文件最大可达 512 MiB；具体限制由 Core 执行。

::: code-group

```caddyfile [Caddy]
core.example {
	reverse_proxy 127.0.0.1:8080
}
```

```nginx [nginx]
map $http_upgrade $connection_upgrade {
	default upgrade;
	''      close;
}

server {
	listen 443 ssl;
	server_name core.example;
	ssl_certificate     /etc/ssl/core.example/fullchain.pem;
	ssl_certificate_key /etc/ssl/core.example/privkey.pem;

	client_max_body_size 513m;

	location / {
		proxy_pass http://127.0.0.1:8080;
		proxy_http_version 1.1;
		proxy_set_header Host $http_host;
		proxy_set_header Upgrade $http_upgrade;
		proxy_set_header Connection $connection_upgrade;
		proxy_buffering off;
		proxy_read_timeout 1h;
		proxy_send_timeout 1h;
	}
}
```

:::

Caddy 会自行获取证书，默认传递 Host 和 WebSockets。nginx 的 server 块放在 `http` 上下文中，并需要你提供证书。

然后在 `~/.oac/core/.env` 中设置 `OAC_PUBLIC_URL=https://core.example`，并运行 `~/.oac/core/oac apply`。

### 检查路由 {#verify-the-routing}

```sh
curl -s -o /dev/null -w '%{http_code}\n' -H 'OpenAI-Beta: agents=v1' https://core.example/v1/agents
```

`401` 表示 `/v1` 已到达 Core，Core 正在要求提供密钥。`404` 表示请求已到达 Web：请修复反向代理，否则应用调用和每个节点连接都会失败。节点记录 `HTTP 400 on the WebSocket upgrade`，说明其前面的代理没有在 `/api/v1` 上传递升级请求；参阅[故障排查](./troubleshooting.md#nodes)。

所有位置都必须保持启用 TLS 验证。使用私有证书颁发机构时，节点主机、自托管机器和 Runtime 镜像必须信任该机构。

### 使用快速隧道进行本地试用 {#try-it-locally-with-a-quick-tunnel}

[Cloudflare 快速隧道](https://developers.cloudflare.com/cloudflare-one/connections/connect-networks/do-more-with-tunnels/trycloudflare/)会为使用外部入口的试用安装提供一个临时公共 HTTPS 地址。它只会转发到一个端口，因此需要在前面放置一个具有相同路由的本地代理：

```caddyfile
http://:8443 {
	bind 127.0.0.1
	reverse_proxy 127.0.0.1:8080
}
```

1. 启动代理：`caddy run --config Caddyfile`。
2. 启动隧道：`cloudflared tunnel --url http://127.0.0.1:8443`。它会打印一个地址，例如 `https://random-words.trycloudflare.com`。
3. 在 `~/.oac/core/.env` 中将该地址设置为 `OAC_PUBLIC_URL`，并运行 `~/.oac/core/oac apply`。

每当 `cloudflared` 重启时，该地址都会改变；随后必须重新添加绑定到旧地址的节点。吞吐量较低，因此节点首次下载 Runtime（约 500 MB）时可能很慢；请参阅[慢速链接](./nodes.md#rerun-expiry-and-slow-links)。

## 更改公开 URL {#changing-the-public-url}

先把反向代理指向新地址，然后编辑 `~/.oac/core/.env` 中的 `OAC_PUBLIC_URL` 并运行 `oac apply`。之后：

- 使用旧地址的节点不会再获得新沙箱：请在 Web 中移除这些节点，然后重新添加。
- 只有在旧地址仍可访问此 Core 时，现有沙箱和执行器才会继续工作。
- 自托管执行器必须使用新的 `remote_url` 重启，并且其安装程序会拒绝为旧地址进行的安装：请创建新的自托管 Session，然后重新连接其主机。

该设置列在[配置参考](../configuration.md#settings)中。
