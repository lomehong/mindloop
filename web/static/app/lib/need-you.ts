// 「需要你」信号聚合（theater.md §5）：从 home 的 attention 区升格为
// 全局组件的数据层——四类信号（待审批/预算≥80%/熔断/停滞），顺序即
// 文档顺序。纯函数：输入既有 API 读数，输出行动条载荷（去处理去处
// 由回归单测盯着：待审批必须落在桌面任务 tab）。

import { fmtDuration } from "~/components/activity-badge";
import { formatClock, formatCount } from "~/lib/format";
import type { Identity, IdentityActivity, PendingApproval, Usage } from "~/lib/types";

export interface SignalRow {
  key: string;
  name: string;
  text: string;
  to: string;
}

function identityPath(id: string, rest = ""): string {
  return `/i/${encodeURIComponent(id)}${rest}`;
}

/** 单具身份的四类信号，顺序即文档顺序：待审批 → 预算 → 熔断 → 停滞。 */
export function signalsFor(
  identity: Identity,
  usage: Usage | undefined,
  approvals: PendingApproval[] | undefined,
  activity: IdentityActivity | undefined
): SignalRow[] {
  const rows: SignalRow[] = [];
  const push = (kind: string, text: string, to: string) =>
    rows.push({ key: `${identity.id}:${kind}`, name: identity.name, text, to });
  const base = identityPath(identity.id);

  if (approvals && approvals.length > 0) {
    const oldest = approvals.reduce((a, b) =>
      Date.parse(a.created) <= Date.parse(b.created) ? a : b
    );
    const ageH = (Date.now() - Date.parse(oldest.created)) / 3_600_000;
    const wait =
      Number.isFinite(ageH) && ageH >= 1
        ? `已等待 ${Math.floor(ageH)} 小时`
        : "运行等待授权";
    push(
      "approval",
      `${approvals.length} 条脚本待审批 · ${wait}`,
      `${base}/tasks`
    );
  }

  const admission = usage?.admission;
  if (admission && admission.daily_limit > 0) {
    const pct = (admission.used_today / admission.daily_limit) * 100;
    if (pct >= 80) {
      push(
        "budget",
        `今日预算已用 ${Math.round(pct)}% · ${formatCount(admission.used_today)}/${formatCount(admission.daily_limit)} tokens`,
        `${base}/run/usage`
      );
    }
  }
  if (admission?.cooling_until && Date.parse(admission.cooling_until) > Date.now()) {
    push("cooling", `熔断冷却中 · 至 ${formatClock(admission.cooling_until)}`, `${base}/run/usage`);
  }
  if (activity?.state === "stalled") {
    push(
      "stalled",
      `忙碌但安静 ${fmtDuration(activity.last_step_age_s) ?? "?"} · 可能停滞`,
      `${base}/run/health`
    );
  }
  return rows;
}
