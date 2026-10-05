import type { PetMood, PetReaction } from "~/lib/pet-state";

// 圆润 SVG 小生物：果冻质感的圆润身体 + 大眼睛 + 头顶一株小苗。
// 表情与体态由 mood 驱动——颜色走 .pet-mood-* 的 CSS 变量，眼/嘴/苗的
// 形态切换走 .pet-mood-* 的变体规则（app.css），这里只出结构。
// 轨道粒子 = 忙碌思考者；涟漪 = 说话；pet-react-* = 一闪而过的微反应。
// （2026-10-04 用户实机体验后拍板：由初版"极简光点"重做为此形态。）

export function Creature({
  busy,
  speaking,
  reaction,
  reactionSeq,
  onToggleMenu,
  onOpenDashboard,
}: {
  busy: number;
  speaking: boolean;
  reaction: PetReaction | null;
  /** 每次微反应自增——作为 key 重挂 SVG，让 CSS 动画从头播。 */
  reactionSeq: number;
  onToggleMenu: () => void;
  onOpenDashboard: () => void;
}) {
  // 轨道粒子 = 忙碌思考者；最多画 5 颗。
  const motes = Math.min(busy, 5);
  return (
    <div
      key={reactionSeq}
      className={`pet-creature-wrap${reaction ? ` pet-react-${reaction}` : ""}`}
    >
      <svg
        data-testid="pet-orb"
        viewBox="0 0 120 120"
        className="pet-creature-svg"
        role="img"
        aria-label="mindloop 小生物"
        onClick={onToggleMenu}
        onDoubleClick={onOpenDashboard}
      >
        <defs>
          <radialGradient id="pet-body" cx="50%" cy="36%" r="75%">
            <stop offset="0%" stopColor="color-mix(in oklch, var(--pet-color) 45%, white)" />
            <stop offset="100%" stopColor="var(--pet-color)" />
          </radialGradient>
        </defs>

        {/* 微反应闪光环（平时透明） */}
        <circle
          className="pet-flash"
          cx="60"
          cy="68"
          r="44"
          fill="none"
          stroke="var(--pet-color)"
          strokeWidth="1.5"
        />
        {/* 影子 */}
        <ellipse className="pet-shadow" cx="60" cy="107" rx="25" ry="4.5" />

        <g className="pet-pulse">
        <g className="pet-breathe">
          {/* 头顶小苗：挺立=常态，垂头=病/盹/离线 */}
          <g className="pet-sprout">
            <path
              className="pet-sprout-stem"
              d="M60,44 C60,38 58,34 54,30"
              fill="none"
              stroke="var(--pet-color)"
              strokeWidth="2.4"
              strokeLinecap="round"
            />
            <ellipse
              className="pet-sprout-leaf"
              cx="51"
              cy="28"
              rx="6.2"
              ry="3.4"
              fill="var(--pet-color)"
              transform="rotate(-28 51 28)"
            />
          </g>

          {/* 身体：果冻感椭圆 + 顶缘高光 + 高光肚皮 + 腮红 */}
          <ellipse className="pet-body-shape" cx="60" cy="70" rx="30" ry="26" fill="url(#pet-body)" />
          <path className="pet-rim" d="M38,60 A26,26 0 0 1 55,45.5" />
          <ellipse className="pet-belly" cx="60" cy="81" rx="15" ry="8" fill="#fff" opacity="0.16" />
          <ellipse className="pet-cheek" cx="41.5" cy="74" rx="3.4" ry="2.1" />
          <ellipse className="pet-cheek" cx="78.5" cy="74" rx="3.4" ry="2.1" />

          {/* 眼睛：外层目光追随 → 内层周期眨眼；闭合弧线由 mood/摸头切换 */}
          <g className="pet-eyes-look">
            <g className="pet-eyes-open">
              <ellipse className="pet-eye" cx="49" cy="64" rx="5" ry="6.2" />
              <ellipse className="pet-eye" cx="71" cy="64" rx="5" ry="6.2" />
              <circle className="pet-eye-glint" cx="51" cy="61.5" r="1.7" />
              <circle className="pet-eye-glint" cx="73" cy="61.5" r="1.7" />
            </g>
            <g className="pet-eyes-closed">
              <path className="pet-eye-line" d="M43,64 Q49,69.5 55,64" />
              <path className="pet-eye-line" d="M65,64 Q71,69.5 77,64" />
            </g>
          </g>

          {/* 嘴：微笑（默认）/ 平直（停滞·不适）/ 张合（说话） */}
          <path className="pet-mouth-smile" d="M54,78 Q60,83 66,78" />
          <path className="pet-mouth-flat" d="M54.5,79.5 L65.5,79.5" />
          <ellipse className="pet-mouth-talk" cx="60" cy="80" rx="3.6" ry="3.2" />
        </g>
        </g>

        {/* 说话涟漪：身体外围两层错相外扩 */}
        {speaking && (
          <>
            <circle className="pet-ripple pet-ripple-a" cx="60" cy="70" r="40" fill="none" stroke="var(--pet-color)" strokeWidth="1" />
            <circle className="pet-ripple pet-ripple-b" cx="60" cy="70" r="40" fill="none" stroke="var(--pet-color)" strokeWidth="1" />
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
            <circle className="pet-mote" cx={60 + 43} cy="66" r="3" />
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
      </svg>
    </div>
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
