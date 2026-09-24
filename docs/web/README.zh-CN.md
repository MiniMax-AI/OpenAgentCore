# Parsar Core Web

Core Web 是 Core 部署的管理员控制台。浏览器登录控制台后，通过管理接口操作；
应用使用自己的 Project API key 直接调用 Core 的公开 Agents API。

管理服务和 `AdminClient` 已实现。React 页面与开发代理的迁移由前端同事负责；
现有执行页面、截图和 fixture 测试不代表新管理界面已经验收。

连接方式、权限边界和前端交接要求以[英文文档](README.md)为准。
