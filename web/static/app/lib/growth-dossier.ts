// 成长档案（avatar.md §4 进化篇）的聚合逻辑层：同行曲线的档位投影、
// 自主权边界读数、传承清单。纯函数，数据全部来自既有 fetch（§0 后端
// 零改动）。
//
// 机制账裁决（avatar.md §10.1）：
//   - 曲线 marker 是**授权档的投影**（映射规则公开可查），不是成长
//     测量——阶段迁移的唯一机制触发是用户放权动作（§4.4.3 不自我加
//     冕），v1 没有放权动作的轨迹留痕，绝不显示"67%"这类假进度；
//   - 传承清单只列 preference 类记忆（记录在案、可点开看出处）+
//     taste 七天聚合（批改证据计数）；persona 自由文本无事件出处，
//     不纳入（§10 裁决 2）。

import type { MemoryInfo, SensorsView, TasteSummary } from "~/lib/types";
import { parsePolicyMode } from "~/lib/growth-ledger";

export type PolicyMode = NonNullable<ReturnType<typeof parsePolicyMode>["mode"]>;

// ---- 同行曲线（三段带）----------------------------------------------------

export interface CurveSegment {
  key: string;
  /** 段宽（0..1，原型 40/35/25 配比）。 */
  width: number;
  desc: string;
}

export const CURVE_SEGMENTS: CurveSegment[] = [
  { key: "起步期", width: 0.4, desc: "你多引导：批改 · 纠错 · 示范" },
  { key: "同行期", width: 0.35, desc: "并肩：委托 · 商量 · 分工" },
  { key: "分身期", width: 0.25, desc: "它多担当：替你出面 · 为你服务" },
];

/** 授权档 → 曲线上的投影位置（0..1）。规则公开：deny 几乎不放手、
 * ask 全审在起步段内、auto 分级放行正是起步→同行的边界、trusted 直
 * 执行已是分身段的信任形态。投影不是测量——marker 旁必须标注。 */
export function curvePositionOf(mode: PolicyMode): number {
  switch (mode) {
    case "deny":
      return 0.04;
    case "ask":
      return 0.18;
    case "auto":
      return 0.4;
    case "trusted":
      return 0.78;
  }
}

// ---- 自主权边界（只读读数）------------------------------------------------

export interface BoundaryRow {
  label: string;
  value: string;
  /** 调整去处的指引（v1 只读：调整走 CLI/env，调整本身有留痕）。 */
  hint?: string;
}

export function boundaryReadings(input: {
  env: Record<string, string>;
  dailyLimit: number | null;
  sensors: SensorsView | undefined;
}): BoundaryRow[] {
  const policy = parsePolicyMode(input.env["MINDLOOP_EXEC_POLICY"]);
  const policyText: Record<PolicyMode, string> = {
    auto: "auto · 分级放行（需审批类才等你）",
    ask: "ask · 全审（只读外都过你的手）",
    trusted: "trusted · 直执行（你显式选择）",
    deny: "deny · 只思考不执行",
  };
  const rows: BoundaryRow[] = [
    {
      label: "执行授权档",
      value: policy.mode ? policyText[policy.mode] : "读数缺席（env 不可达）",
      hint: policy.defaulted
        ? "未显式配置（缺省 auto） · 调整：MINDLOOP_EXEC_POLICY"
        : "调整：MINDLOOP_EXEC_POLICY",
    },
  ];
  const spont = input.env["MINDLOOP_SPONTANEOUS_TOKENS"];
  rows.push({
    label: "自发档日预算",
    value: spont ? `${Number(spont).toLocaleString()} tokens/日` : "未配置",
    hint: "调整：MINDLOOP_SPONTANEOUS_TOKENS",
  });
  rows.push({
    label: "每日总预算",
    value:
      input.dailyLimit === null
        ? "读数缺席"
        : input.dailyLimit > 0
          ? `${input.dailyLimit.toLocaleString()} tokens/日`
          : "未封顶",
    hint: "喂食在「食量」面 · 调整：/settings/config",
  });
  const learning = (input.sensors?.sensors ?? []).filter(
    (s) => s.learning_days !== undefined && s.learning_days > 0 && s.enabled !== false
  );
  rows.push({
    label: "感官学习期",
    value:
      learning.length === 0
        ? "没有在学习期的感官"
        : `${learning.length} 只在学习期 · 转正由你点头`,
    hint: learning.length > 0 ? "预演与放行：注意力面" : undefined,
  });
  const robotd = input.sensors?.robotd;
  if (robotd?.configured) {
    rows.push({
      label: "身（robotd）",
      value:
        robotd.mode === "action"
          ? "action · 已给 --window-allow"
          : "observe · 只许看不许动",
      hint: "调整：mcp.json 的 robotd 授权",
    });
  }
  return rows;
}

// ---- 传承清单（偏好类证据）------------------------------------------------

export interface LegacyEvidence {
  /** preference 类记忆（在案、未失效）。 */
  preferences: MemoryInfo[];
  /** 近 7 天批改证据（taste 聚合）；null = 读数缺席。 */
  touches: { approves: number; denies: number; undoes: number } | null;
}

export function legacyEvidence(
  memories: MemoryInfo[] | undefined,
  taste: TasteSummary | undefined
): LegacyEvidence {
  return {
    preferences: (memories ?? [])
      .filter(
        (m) =>
          m.type === "preference" && (!m.status || m.status === "active")
      )
      .sort((a, b) => (b.mtime ?? 0) - (a.mtime ?? 0)),
    touches: taste
      ? {
          approves: taste.Approves ?? 0,
          denies: taste.Denies ?? 0,
          undoes: taste.Undoes ?? 0,
        }
      : null,
  };
}
