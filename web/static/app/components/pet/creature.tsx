import { type CSSProperties, useId } from "react";

import type { PetMood, PetReaction } from "~/lib/pet-state";

// 圆润 SVG 小生物：果冻质感的圆润身体 + 大眼睛 + 头顶一株小苗。
// 表情与体态由 mood 驱动——颜色走 .pet-mood-* 的 CSS 变量，眼/嘴/苗的
// 形态切换走 .pet-mood-* 的变体规则（app.css），这里只出结构。
// 轨道粒子 = 忙碌思考者；涟漪 = 说话；pet-react-* = 一闪而过的微反应。
// （2026-10-04 用户实机体验后拍板：由初版"极简光点"重做为此形态。）
//
// v2.2 形态定稿（docs/designs/pet.md §0.0，2026-10-06 样张确认）：
//   - 双叶小苗（主叶 + 茎上小副叶，轮廓左右平衡）；眼睛放大一档 + 第二枚
//     微高光；补回 .pet-eyes-mood 层——「工作中眯眼 / 警情圆睁」等体态
//     此前因该层缺失而从未生效；
//   - 同一解剖、两种取景：≥96px 宽幅（页面形态，轨道/涟漪/气泡有空间），
//     以下近景（缩放轴 = 体心）——停靠 64 与全部缩略共用近景；
//   - 细节分层：<44px 走 pet-tier-lo（去腮红/微高光、加粗描边、收小跳动），
//     页面与停靠是全细节。

/** 全尺寸（宽幅取景）解剖常量；近景 = 以体心缩放的同一套坐标。 */
const BODY = { cx: 60, cy: 70, rx: 30, ry: 26 };
const EYE = { dx: 11.2, cy: 63.6, rx: 5.7, ry: 6.9 };
const GLINT = { dx: 2.3, dy: -2.8, r: 1.9, dx2: -2.1, dy2: 2.5, r2: 1.0, o2: 0.38 };
const CHEEK = { dx: 18.5, cy: 73.5, rx: 3.6, ry: 2.2 };
const SPROUT = {
  stem: "M60,44 C60.4,39 59.6,34.6 56.2,30",
  leaf1: { cx: 52.6, cy: 27.4, rx: 6.6, ry: 3.5, rot: -34 },
  leaf2: { cx: 63.6, cy: 34.4, rx: 4.3, ry: 2.3, rot: 28 },
};
const RIM = "M36.5,61.5 A26.5,26.5 0 0 1 55.5,44.5";
const BELLY = { cx: 60, cy: 80.5, rx: 15.5, ry: 8.2 };
const SMILE = "M53.4,78 Q60,83.6 66.6,78";
const FLAT = "M54.3,79.6 L65.7,79.6";
const TALK = { cx: 60, cy: 80, rx: 3.5, ry: 3.1 };
const EYES_CLOSED_Q = 5.8;
/** 倒影椭圆：宽幅一档；近景贴到放大后的身体下缘。 */
const SHADOW = {
  wide: { cx: 60, cy: 105.5, rx: 23, ry: 4.2 },
  close: { cx: 60, cy: 112, rx: 30, ry: 4.6 },
  closeSmall: { cx: 60, cy: 113.5, rx: 31, ry: 4.8 },
};

export interface CreatureSvgProps {
  /** 渲染边长（px）。取景与细节层不传时按此自动。 */
  size?: number;
  mood?: PetMood;
  /** 轨道粒子数（= 忙碌思考者），最多 5 颗。 */
  busy?: number;
  speaking?: boolean;
  reaction?: PetReaction | null;
  /** 每次微反应自增——作为 key 重挂 SVG，让 CSS 动画从头播。 */
  reactionSeq?: number;
  /** 取景：wide = 页面形态；close = 停靠/缩略。默认 ≥96 取 wide。 */
  frame?: "wide" | "close";
  /** 细节层：lo = 小尺寸减负（<44px 默认）。 */
  tier?: "full" | "lo";
  /** 动效相位（0..1）：同屏多实例错拍的负延迟（见 lib/pet-state phaseFor）。
   * 同一身份在页面各处应传同值——它们是同一只生物，同拍才对。 */
  phase?: number;
  /** 无障碍标签；不传视为装饰图（aria-hidden）。 */
  label?: string;
  onClick?: () => void;
  onDoubleClick?: () => void;
  testId?: string;
}

/** 共享生命体：页面形态、停靠、缩略共用这一份结构（同族同色）。 */
export function CreatureSvg({
  size = 128,
  mood = "idle",
  busy = 0,
  speaking = false,
  reaction = null,
  reactionSeq = 0,
  frame,
  tier,
  phase = 0,
  label,
  onClick,
  onDoubleClick,
  testId,
}: CreatureSvgProps) {
  const f = frame ?? (size >= 96 ? "wide" : "close");
  const lo = tier ? tier === "lo" : size < 44;
  const k = f === "close" ? (size < 40 ? 1.5 : 1.38) : 1;
  const motes = Math.min(busy, 5);
  const gid = `pet-body-${useId().replace(/[^a-zA-Z0-9-]/g, "")}`;
  const sh = f === "wide" ? SHADOW.wide : size < 40 ? SHADOW.closeSmall : SHADOW.close;
  const orbitR = f === "close" ? 34.5 : 43;
  const rippleR = f === "close" ? 33 : 40;
  const flashR = f === "close" ? 36 : 44;
  const zoom =
    f === "close"
      ? `translate(60,70) scale(${k}) translate(-60,-70)`
      : undefined;

  return (
    <div
      className={`pet-vars pet-mood-${mood}${lo ? " pet-tier-lo" : ""}`}
      style={{ "--pet-phase": phase } as CSSProperties}
    >
      <div
        key={reactionSeq}
        className={`pet-creature-wrap${reaction ? ` pet-react-${reaction}` : ""}`}
      >
        <svg
          data-testid={testId}
          viewBox="0 0 120 120"
          className="pet-creature-svg"
          style={{ width: size, height: size }}
          role={label ? "img" : undefined}
          aria-label={label}
          aria-hidden={label ? undefined : true}
          onClick={onClick}
          onDoubleClick={onDoubleClick}
        >
          <defs>
            <radialGradient id={gid} cx="50%" cy="36%" r="75%">
              <stop offset="0%" stopColor="color-mix(in oklch, var(--pet-color) 45%, white)" />
              <stop offset="100%" stopColor="var(--pet-color)" />
            </radialGradient>
          </defs>

          {/* 微反应闪光环（平时透明） */}
          <circle
            className="pet-flash"
            cx="60"
            cy="68"
            r={flashR}
            fill="none"
            stroke="var(--pet-color)"
            strokeWidth="1.5"
          />
          {/* 影子 */}
          <ellipse className="pet-shadow" cx={sh.cx} cy={sh.cy} rx={sh.rx} ry={sh.ry} />

          <g transform={zoom}>
            <g className="pet-pulse">
              <g className="pet-breathe">
                {/* 头顶小苗：挺立=常态，垂头=病/盹/离线 */}
                <g className="pet-sprout">
                  <path
                    className="pet-sprout-stem"
                    d={SPROUT.stem}
                    fill="none"
                    stroke="var(--pet-color)"
                    strokeWidth="2.5"
                    strokeLinecap="round"
                  />
                  <ellipse
                    className="pet-sprout-leaf"
                    cx={SPROUT.leaf1.cx}
                    cy={SPROUT.leaf1.cy}
                    rx={SPROUT.leaf1.rx}
                    ry={SPROUT.leaf1.ry}
                    fill="var(--pet-color)"
                    transform={`rotate(${SPROUT.leaf1.rot} ${SPROUT.leaf1.cx} ${SPROUT.leaf1.cy})`}
                  />
                  <ellipse
                    className="pet-sprout-leaf"
                    cx={SPROUT.leaf2.cx}
                    cy={SPROUT.leaf2.cy}
                    rx={SPROUT.leaf2.rx}
                    ry={SPROUT.leaf2.ry}
                    fill="var(--pet-color)"
                    transform={`rotate(${SPROUT.leaf2.rot} ${SPROUT.leaf2.cx} ${SPROUT.leaf2.cy})`}
                  />
                </g>

                {/* 身体：果冻感椭圆 + 顶缘高光 + 高光肚皮 + 腮红 */}
                <ellipse
                  className="pet-body-shape"
                  cx={BODY.cx}
                  cy={BODY.cy}
                  rx={BODY.rx}
                  ry={BODY.ry}
                  fill={`url(#${gid})`}
                />
                <path className="pet-rim" d={RIM} />
                <ellipse
                  className="pet-belly"
                  cx={BELLY.cx}
                  cy={BELLY.cy}
                  rx={BELLY.rx}
                  ry={BELLY.ry}
                  fill="#fff"
                  opacity="0.15"
                />
                <ellipse
                  className="pet-cheek"
                  cx={BODY.cx - CHEEK.dx}
                  cy={CHEEK.cy}
                  rx={CHEEK.rx}
                  ry={CHEEK.ry}
                />
                <ellipse
                  className="pet-cheek"
                  cx={BODY.cx + CHEEK.dx}
                  cy={CHEEK.cy}
                  rx={CHEEK.rx}
                  ry={CHEEK.ry}
                />

                {/* 眼睛：外层目光追随 → 心情变形 → 内层周期眨眼；
                    闭合弧线由 mood/摸头切换（分层原因见 app.css） */}
                <g className="pet-eyes-look">
                  <g className="pet-eyes-mood">
                    <g className="pet-eyes-open">
                      <ellipse className="pet-eye" cx={60 - EYE.dx} cy={EYE.cy} rx={EYE.rx} ry={EYE.ry} />
                      <ellipse className="pet-eye" cx={60 + EYE.dx} cy={EYE.cy} rx={EYE.rx} ry={EYE.ry} />
                      <circle className="pet-eye-glint" cx={60 - EYE.dx + GLINT.dx} cy={EYE.cy + GLINT.dy} r={GLINT.r} />
                      <circle className="pet-eye-glint" cx={60 + EYE.dx + GLINT.dx} cy={EYE.cy + GLINT.dy} r={GLINT.r} />
                      <circle className="pet-eye-glint g2" cx={60 - EYE.dx + GLINT.dx2} cy={EYE.cy + GLINT.dy2} r={GLINT.r2} opacity={GLINT.o2} />
                      <circle className="pet-eye-glint g2" cx={60 + EYE.dx + GLINT.dx2} cy={EYE.cy + GLINT.dy2} r={GLINT.r2} opacity={GLINT.o2} />
                    </g>
                    <g className="pet-eyes-closed">
                      <path
                        className="pet-eye-line"
                        d={`M${60 - EYE.dx - 6},${EYE.cy} Q${60 - EYE.dx},${EYE.cy + EYES_CLOSED_Q} ${60 - EYE.dx + 6},${EYE.cy}`}
                      />
                      <path
                        className="pet-eye-line"
                        d={`M${60 + EYE.dx - 6},${EYE.cy} Q${60 + EYE.dx},${EYE.cy + EYES_CLOSED_Q} ${60 + EYE.dx + 6},${EYE.cy}`}
                      />
                    </g>
                  </g>
                </g>

                {/* 嘴：微笑（默认）/ 平直（停滞·不适）/ 张合（说话） */}
                <path className="pet-mouth-smile" d={SMILE} />
                <path className="pet-mouth-flat" d={FLAT} />
                <ellipse className="pet-mouth-talk" cx={TALK.cx} cy={TALK.cy} rx={TALK.rx} ry={TALK.ry} />
              </g>
            </g>

            {/* 说话涟漪：身体外围两层错相外扩 */}
            {speaking && (
              <>
                <circle className="pet-ripple pet-ripple-a" cx="60" cy="70" r={rippleR} fill="none" stroke="var(--pet-color)" strokeWidth="1" />
                <circle className="pet-ripple pet-ripple-b" cx="60" cy="70" r={rippleR} fill="none" stroke="var(--pet-color)" strokeWidth="1" />
              </>
            )}

            {/* 思考者轨道粒子：一颗 = 一个忙碌思考者 */}
            {Array.from({ length: motes }, (_, i) => (
              <g
                key={i}
                className="pet-orbit"
                style={{
                  animationDuration: `${5.2 + i * 0.8}s`,
                  animationDelay: `${-i * 1.7}s`,
                }}
              >
                <circle className="pet-mote" cx={60 + orbitR} cy="66" r="3" />
              </g>
            ))}

            {/* 打盹的 Zzz */}
            <g className="pet-zzz">
              <text className="pet-z z1" x="82" y="48">z</text>
              <text className="pet-z z2" x="89" y="40">z</text>
              <text className="pet-z z3" x="96" y="32">Z</text>
            </g>

            {/* 摸头的小心心（仅 happy 微反应期间漂浮） */}
            <g className="pet-hearts">
              <path className="pet-heart h1" d="M30,44 c-2.5,-3.5 -7.5,-1 -5,3.5 l5,5.5 5,-5.5 c2.5,-4.5 -2.5,-7 -5,-3.5 z" />
              <path className="pet-heart h2" d="M40,34 c-2.5,-3.5 -7.5,-1 -5,3.5 l5,5.5 5,-5.5 c2.5,-4.5 -2.5,-7 -5,-3.5 z" />
              <path className="pet-heart h3" d="M49,44 c-2.5,-3.5 -7.5,-1 -5,3.5 l5,5.5 5,-5.5 c2.5,-4.5 -2.5,-7 -5,-3.5 z" />
            </g>
          </g>
        </svg>
      </div>
    </div>
  );
}

/** 页面形态（/pet）：宽幅取景 + 点击出菜单 / 双击开仪表盘。 */
export function Creature({
  mood,
  busy,
  speaking,
  reaction,
  reactionSeq,
  onToggleMenu,
  onOpenDashboard,
}: {
  mood: PetMood;
  busy: number;
  speaking: boolean;
  reaction: PetReaction | null;
  reactionSeq: number;
  onToggleMenu: () => void;
  onOpenDashboard: () => void;
}) {
  return (
    <CreatureSvg
      size={128}
      mood={mood}
      busy={busy}
      speaking={speaking}
      reaction={reaction}
      reactionSeq={reactionSeq}
      label="mindloop 小生物"
      testId="pet-orb"
      onClick={onToggleMenu}
      onDoubleClick={onOpenDashboard}
    />
  );
}

/** 小生物下的状态行：身份 · 心情（mono，数据味）。 */
export function PetStatusLine({
  text,
  mood,
}: {
  text: string;
  mood: PetMood;
}) {
  return (
    <div className="pet-status" data-testid="pet-status" data-mood={mood}>
      {text}
    </div>
  );
}
