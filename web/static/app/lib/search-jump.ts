/** 搜索命中直达的目标换算（identity-shell 的 MindlogSearch onJump）。
 * 独立纯函数：跳转目标曾被写死 `${base}?step=`——舞台成为 index 路由
 * 后该参数无人消费，命中直达静默失效（2026-10-14 审查 #7）。目标
 * 规则：轨迹区 → 泳道（/traj，?step= 由 TimelineView 解析：窗口内
 * 滚动+闪烁+开详情，窗口外退化取单步弹窗）；其余区 → 日志流定位。 */
export function jumpTargetFor(
  section: string,
  base: string,
  stepId: string
): string {
  const q = `?step=${encodeURIComponent(stepId)}`;
  return section === "traj" ? `${base}/traj${q}` : `${base}/log${q}`;
}
