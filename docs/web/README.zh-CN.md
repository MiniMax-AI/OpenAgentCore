# Parsar Core Web

Core Web 是 Core 部署的管理员控制台。浏览器登录控制台后，通过管理接口操作；
应用使用自己的 Project API key 直接调用 Core 的公开 Agents API。

React 控制台（`apps/web`）按这套接口实现：浏览器只经控制台的同源管理路由，用
`AdminClient` 和沙箱管理客户端读写，不向 `/v1` 发任何请求。页面分三组：监控（概览、
Core 监控、Agent 监控、沙箱监控、Session 日志）、资源（Agent、环境模板、Skills、文件、
Vault）和平台（项目与 key、节点、系统）。缺失的数据显示为“—”，不显示为 0。

连接方式、权限边界和前端交接要求以[英文文档](README.md)为准。
