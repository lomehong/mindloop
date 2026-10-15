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

/** 投影位置 → 段名（起步/同行/分身）。样张的阶段 chip 用这个——
 * chip 只给段名不给百分比（avatar.md §8.1 反游戏化；§10.1 投影
 * 非测量）。 */
export function segmentNameOf(pos: number): string {
  let acc = 0;
  for (const seg of CURVE_SEGMENTS) {
    acc += seg.width;
    if (pos < acc || seg === CURVE_SEGMENTS[CURVE_SEGMENTS.length - 1]) {
      return seg.key;
    }
  }
  return CURVE_SEGMENTS[0].key;
}

/** 授权档 → 阶段 chip 文案（段名 + 档位说明，无百分比）。 */
export function phaseChipOf(mode: PolicyMode): string {
  return `${segmentNameOf(curvePositionOf(mode))} · ${mode}`;
}

// ---- 三轴量条（样张视觉，真实读数填充）-------------------------------------
// 用户 2026-10-15 拍板：视觉对齐样张（彩色量条），填充比例全部来自
// 真实读数或公开映射，不引入虚构数值；量条是读数的图形化，不是经验条
//（avatar.md §8.1 修订记录见 docs/designs/avatar.md §10.1）。

export interface AxisMeter {
  label: string;
  /** 一句话读数（真实值）。 */
  text: string;
  /** 填充比例 0..1；null = 读数缺席（画空条）。 */
  ratio: number | null;
  /** 量条配色分类（step-colors 同族）：moss/lake/resin。 */
  tone: "moss" | "lake" | "resin";
}

export function axisMeters(f: {
  tasksDone: number | null;
  tasksTotal: number | null;
  policyMode: PolicyMode | null;
  memories: number | null;
}): AxisMeter[] {
  const abilityRatio =
    f.tasksTotal !== null && f.tasksTotal > 0
      ? (f.tasksDone ?? 0) / f.tasksTotal
      : null;
  const trustRatio = f.policyMode ? curvePositionOf(f.policyMode) : null;
  // 传承：在案记忆的相对厚度（20 条封顶作满）——读数是真实条数，
  // 比例只是展示刻度。
  const legacyRatio = f.memories === null ? null : Math.min(1, f.memories / 20);
  return [
    { label: "能力", text: axisAbilityText(f), ratio: abilityRatio, tone: "moss" },
    { label: "信任", text: axisTrustText(f), ratio: trustRatio, tone: "lake" },
    { label: "传承", text: axisLegacyText(f), ratio: legacyRatio, tone: "resin" },
  ];
}

function axisAbilityText(f: {
  tasksDone: number | null;
  tasksTotal: number | null;
}): string {
  if (f.tasksTotal === null) return "—";
  if (f.tasksTotal === 0) return "近 7 天无委托";
  return `近 7 天 ${f.tasksDone ?? 0}/${f.tasksTotal} 完成`;
}

function axisTrustText(f: { policyMode: PolicyMode | null }): string {
  switch (f.policyMode) {
    case "auto":
      return "auto · 分级放行";
    case "ask":
      return "ask · 全审";
    case "trusted":
      return "trusted · 直执行";
    case "deny":
      return "deny · 只思考";
    default:
      return "—";
  }
}

function axisLegacyText(f: { memories: number | null }): string {
  return f.memories === null ? "—" : `${f.memories} 条在案`;
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
  /** undefined=查询缺席；null=端点明示不封顶；数字=封顶值。 */
  dailyLimit: number | null | undefined;
  sensors: SensorsView | undefined;
  /** 近窗口被 learning-cap 压制的消息数；null = 轨迹窗口读数缺席。 */
  learningCapped: number | null;
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
  // 三态区分（审查修正）：undefined=查询缺席（读数缺席）；null=端点
  // 明示不封顶（budget PUT 的语义）；数字=封顶值。此前把 null 当缺席，
  // 未封顶被误标成"读数缺席"。
  rows.push({
    label: "每日总预算",
    value:
      input.dailyLimit === undefined
        ? "读数缺席"
        : input.dailyLimit !== null && input.dailyLimit > 0
          ? `${input.dailyLimit.toLocaleString()} tokens/日`
          : "未封顶",
    hint: "喂食在「食量」面 · 调整：/settings/config",
  });
  // 学习期口径（审查修正）：「条目显式配置了 learning_days」≠「在学习
  // 期」——LearningDays 带 omitempty（靠缺省学习期的新感官不吐字段，
  // 会漏报），且是否仍在期内需要感官出生时间（API 不暴露）。这里以
  // 轨迹实证为准：近窗口被 learning-cap 压制的消息数（调用方传入；
  // null = 轨迹窗口读数缺席），预演详单在注意力面。
  const capped = input.learningCapped;
  rows.push({
    label: "感官学习期",
    value:
      capped === null
        ? "机制开启（新感官缺省 3 天）· 轨迹窗口读数缺席"
        : capped > 0
          ? `生效中 · 近窗口压制 ${capped} 条 → 预演在注意力面`
          : "机制开启（新感官缺省 3 天）· 窗口内没有被压制的消息",
    hint: "是否仍在期内以轨迹 learning-cap 为准（出生时间不在读数里）",
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
