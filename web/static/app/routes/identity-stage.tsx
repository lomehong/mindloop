// 舞台落地页（theater-stage.html 完整还原）：心智页的默认视图。
// 宇宙氛围层 + 活动星图铺满整窗；卡片右上玻璃列；隔夜账卡左上；
// 线索行左下；底部心跳行 + 对话条（对话单真相源）。
//
// 本组件是 identity-shell 的 index 路由——/i/:id 裸入口即舞台
//（用户 2026-10-14 拍板：心智页 = 剧场完整还原，工作台保留原样）。
// 自持场景流与特效状态；对话展开经 ?chat URL 状态与壳层浮层共享。

import { useCallback, useEffect, useRef, useState } from "react";
import { useParams } from "react-router";
import { parseAsString, useQueryState } from "nuqs";

import { SceneStage, useSceneStream } from "~/components/scene-layer";
import type { LeavingCard } from "~/components/scene-layer";
import type { SceneStreamEvent, SceneSurface } from "~/lib/scene";

export default function IdentityStage() {
  const { identityId = "" } = useParams();
  const [, setChatOpen] = useQueryState("chat", parseAsString.withDefault(""));

  const [pulse, setPulse] = useState<{
    t0: number;
    quadrant: "center" | "right";
  } | null>(null);
  const [leaving, setLeaving] = useState<LeavingCard[]>([]);
  const [sedimented, setSedimented] = useState<
    Array<{ surface: SceneSurface }>
  >([]);

  // 退场动画的挂起定时器：卸载时清掉（setState-after-unmount 卫生，
  // 2026-10-14 审查 #11）。
  const leaveTimersRef = useRef<Set<ReturnType<typeof setTimeout>>>(new Set());
  useEffect(() => {
    const timers = leaveTimersRef.current;
    return () => {
      for (const t of timers) clearTimeout(t);
      timers.clear();
    };
  }, []);

  const onSceneEvent = useCallback((ev: SceneStreamEvent) => {
    if (ev.type === "upsert") {
      setPulse({
        t0: ev.t0,
        quadrant:
          ev.surface.intent === "confront" || ev.surface.focus
            ? "center"
            : "right",
      });
      return;
    }
    if (ev.reason === "ttl") {
      setSedimented((prev) => [...prev.slice(-5), { surface: ev.surface }]);
      return;
    }
    setLeaving((prev) => [
      ...prev.slice(-4),
      { surface: ev.surface, reason: ev.reason, key: ev.t0 },
    ]);
    const t = setTimeout(
      () => {
        leaveTimersRef.current.delete(t);
        setLeaving((prev) => prev.filter((l) => l.key !== ev.t0));
      },
      600
    );
    leaveTimersRef.current.add(t);
  }, []);

  const scene = useSceneStream(identityId, onSceneEvent);

  return (
    <div className="absolute inset-0">
      <SceneStage
        surfaces={scene.surfaces}
        identityId={identityId}
        pulse={pulse}
        leaving={leaving}
        sedimented={sedimented}
        onExpand={() => setChatOpen("1")}
      />
    </div>
  );
}
