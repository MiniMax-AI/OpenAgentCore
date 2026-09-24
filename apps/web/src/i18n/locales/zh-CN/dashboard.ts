export const dashboard = {
  trends: {
    title: "资源趋势", retained: "保留采样 · 持久历史", local: "浏览器本地采样 · 重新加载后重置", source: "运行时指标数据源", live: "实时", history: "历史",
    historyStale: "历史数据已过期", historyLoading: "历史 · 加载中", durableResolution: "持久数据 · {{seconds}}秒", liveRetrying: "实时 · 正在重试历史数据", liveUnavailable: "实时 · 历史数据不可用", liveLoading: "实时 · 正在加载历史数据", staleRetrying: "已过期 · 正在重试", liveInterval: "实时 · {{seconds}}秒",
    durableStatus: "{{status}}；{{count}} 个 运行时目标", retainedFailure: "运行时采样刷新失败，正在显示保留采样", samplingInterval: "运行时每 {{seconds}} 秒实时采样一次",
    bucket: "时间桶", buckets: "时间桶", sample: "个采样", samples: "个采样", observations: "{{actual}}/{{expected}} 个观测", durableRange: "运行时持久时间范围", liveRange: "运行时实时时间范围",
    refreshFailed: "持久历史刷新失败：{{error}}", notConfigured: "未配置持久历史；实时采样仍可用。", requestFailed: "请求 运行时持久历史失败。",
  },
  charts: {
    collecting: "正在收集实时采样", durableHistory: "持久历史", live: "实时", retainedBuckets: "保留时间桶", liveSamples: "实时采样", chartLabel: "{{title}} {{source}} 图表",
    hideSeries: "隐藏 {{series}} 序列", showSeries: "显示 {{series}} 序列", hide: "隐藏 {{series}}", show: "显示 {{series}}", resetZoom: "重置缩放",
    instructions: "将指针移到图表上可查看精确值。水平拖动可选择并缩放时间范围。双击或使用“重置缩放”可恢复完整范围。单击可固定时间点，使用左右方向键移动，按 Esc 清除。",
    pinned: "已固定", hover: "悬停", unavailable: "不可用", allHidden: "已隐藏所有序列", sparse: "采样稀疏", showLegend: "使用图例显示序列", sparseDetail: "{{count}} 个有效点 · 绘制连线需要连续时间桶", emptyDetail: "{{valid}}/2 个有效点 · {{count}} 个快照 · 不会合成历史数据",
    table: { series: "序列", latest: "最新值", missing: "缺失采样" }, runtime: "运行时", usage: "使用率", used: "已使用", configuredLimit: "配置上限", input: "输入", output: "输出", gridDurable: "运行时持久历史图表", gridLive: "运行时实时窗口图表",
    cpu: { title: "CPU 使用率", withoutData: "{{count}} 个运行时目标在此时间段内没有 CPU 数据", durable: "按时间桶聚合的累计差值使用率 · 持久历史", live: "已报告或累计差值使用率 · 实时窗口", empty: "无保留的 CPU 采样" },
    memory: { title: "内存使用", durable: "已观测沙箱 聚合 / 配置上限 · 持久历史", live: "已观测沙箱 工作集 / 配置上限 · 实时窗口", empty: "无保留的已观测内存采样" },
    active: { series: "活跃", sandboxTitle: "活跃沙箱", runtimeTitle: "运行时活跃状态", sumDurable: "每个保留时间桶中的已观测分配数 · 持久历史", sumLive: "每个快照中生命周期状态为活动的分配数 · 实时窗口", binaryDurable: "保留时间桶中存在已观测分配 · 1 活动 / 0 非活跃", binaryLive: "生命周期状态为活动 · 1 活动 / 0 非活跃", active: "活跃", inactive: "非活跃", empty: "无保留的活跃沙箱 采样" },
    tokens: { title: "Token 吞吐量", durable: "规范 Session 用量差值 · 持久历史", live: "Session 用量差值 · 排除缺失用量", empty: "无保留的 Token 采样", perMinute: "{{value}}/分钟" },
  },
} as const;
