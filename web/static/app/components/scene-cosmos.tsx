// 心智舞台宇宙（Scene Protocol 渲染端扩展，参考 BaiLongma
// hotspot-earth 的工程纪律）：Three.js 渲染的氛围层 + 活动星图。
//
// 两层数据：
//   氛围层——星空粒子 + 微光核心球 + 双层大气辉光（程序化贴图，零图片
//   资源；断 WebGL 降级为 CSS 星空，舞台永不因渲染失败而破）。
//   数据层——思考者=绕轨星体（active 亮/idle 暗）、进行中任务=轨道弧、
//   今日预算=核心外环亮度（未设预算为默认微光）。数据来自既有 API
//   （thinkers/usage），零新增抓取。
//
// 工程纪律（BaiLongma hotspot-earth 实证，全部照抄）：
//   powerPreference low-power（装饰场景不唤醒独显）、pixelRatio ≤2、
//   页面隐藏即暂停 rAF、visibility 恢复即续、卸载全链 dispose、
//   动态 import three（主包零增重）、prefers-reduced-motion 静帧。

import { useEffect, useRef, useState } from "react";

import { apiRequest } from "~/lib/api";
import { MIND_STATE_PARAMS, mindStateOf, type MindState } from "~/lib/theater";

/** 舞台特效信号（theater.md §11/§13）：E1 推卡前兆脉冲、E4 审批
 * 决定环、E2 收工巡视、在听辉光。由 scene-layer 从场景流与对话条
 * 派生（真实交互派生，无装饰性假状态），结构兼容即可——渲染端只读
 * t0/dir/quadrant。 */
export interface CosmosFx {
  pulse: { t0: number; quadrant: "center" | "right" } | null;
  decision: { dir: "allow" | "deny"; t0: number } | null;
  completion: { t0: number } | null;
  listening: boolean;
}

type CosmosData = {
  thinkers: Array<{ name: string; state: string }>;
  budgetRatio: number | null; // 0..1；null=未设预算
  working: boolean;
  state: MindState;
  /** 今日真实收口的任务数——年轮刻线（§13.2）的派生源。 */
  doneToday: number;
};

const COSMOS_POLL_MS = 30_000;

async function fetchCosmosData(identityId: string): Promise<CosmosData> {
  const [thinkersRes, usageRes, activityRes, approvalsRes, tasksRes] = await Promise.all([
    apiRequest(`/api/identities/${encodeURIComponent(identityId)}/thinkers`).catch(() => null),
    apiRequest(`/api/identities/${encodeURIComponent(identityId)}/usage`).catch(() => null),
    apiRequest(`/api/identities/${encodeURIComponent(identityId)}/activity`).catch(() => null),
    apiRequest(`/api/identities/${encodeURIComponent(identityId)}/approvals`).catch(() => null),
    apiRequest(`/api/identities/${encodeURIComponent(identityId)}/tasks`).catch(() => null),
  ]);
  const thinkers: Array<{ name: string; state: string }> = [];
  if (thinkersRes?.ok) {
    const j = (await thinkersRes.json().catch(() => null)) as {
      thinkers?: Array<{ name?: string; state?: string }>;
    } | null;
    for (const t of j?.thinkers ?? []) {
      if (t.name) thinkers.push({ name: t.name, state: t.state ?? "idle" });
    }
  }
  let budgetRatio: number | null = null;
  let coolingUntil: string | null = null;
  if (usageRes?.ok) {
    const j = (await usageRes.json().catch(() => null)) as {
      admission?: { daily_limit?: number; used_today?: number; cooling_until?: string };
    } | null;
    const a = j?.admission;
    coolingUntil = a?.cooling_until ?? null;
    if (a && a.daily_limit && a.daily_limit > 0) {
      budgetRatio = Math.min(1, (a.used_today ?? 0) / a.daily_limit);
    }
  }
  let activity: { state?: string; dispatcher_running?: boolean } | null = null;
  if (activityRes?.ok) {
    activity = (await activityRes.json().catch(() => null)) as
      | { state?: string; dispatcher_running?: boolean }
      | null;
  }
  let pendingApprovals = 0;
  if (approvalsRes?.ok) {
    const j = (await approvalsRes.json().catch(() => null)) as
      | Array<{ hash?: string }>
      | { approvals?: Array<{ hash?: string }> }
      | null;
    const list = Array.isArray(j) ? j : (j?.approvals ?? []);
    pendingApprovals = list.length;
  }
  let doneToday = 0;
  if (tasksRes?.ok) {
    const j = (await tasksRes.json().catch(() => null)) as
      | Array<{ status?: string; updated_at?: string }>
      | null;
    const dayStart = new Date();
    dayStart.setHours(0, 0, 0, 0);
    for (const t of j ?? []) {
      if (
        t.status === "succeeded" &&
        t.updated_at &&
        Date.parse(t.updated_at) >= dayStart.getTime()
      ) {
        doneToday += 1;
      }
    }
  }
  const state = mindStateOf({
    activity: {
      state: (activity?.state ?? "idle") as never,
      dispatcher_running: activity?.dispatcher_running ?? false,
    },
    pendingApprovals,
    coolingUntil,
    nowMs: Date.now(),
  });
  return {
    thinkers,
    budgetRatio,
    working: state === "working",
    state,
    doneToday,
  };
}

export function SceneCosmos({ identityId, fx }: { identityId: string; fx?: CosmosFx }) {
  const canvasRef = useRef<HTMLCanvasElement | null>(null);
  // WebGL 失败的降级位：CSS 星空（静态、零依赖），舞台不破。
  const [degraded] = useState(
    () => typeof window !== "undefined" && !window.WebGLRenderingContext
  );
  // 特效信号经 ref 进渲染循环：props 变化不重建整个 three.js 场景。
  const fxRef = useRef<CosmosFx>({
    pulse: null,
    decision: null,
    completion: null,
    listening: false,
  });
  useEffect(() => {
    if (fx) fxRef.current = fx;
  }, [fx]);

  useEffect(() => {
    if (degraded) return;
    const canvas = canvasRef.current;
    if (!canvas) return;
    let disposed = false;
    let cleanup = () => {};

    (async () => {
      const THREE = await import("three");
      if (disposed) return;

      // —— 基础：渲染器（low-power：装饰场景不唤醒独显）——
      let renderer: import("three").WebGLRenderer;
      try {
        renderer = new THREE.WebGLRenderer({
          canvas,
          antialias: true,
          alpha: true,
          powerPreference: "low-power",
        });
      } catch {
        degradedFallback();
        return;
      }
      const size = () => ({
        w: canvas.clientWidth || 800,
        h: canvas.clientHeight || 400,
      });
      const { w, h } = size();
      renderer.setPixelRatio(Math.min(window.devicePixelRatio, 2));
      renderer.setSize(w, h, false);

      const scene = new THREE.Scene();
      // 宇宙永远是深夜色：舞台是"舷窗"，不随亮暗主题漂移——深空底
      // 让星空/辉光在两种主题下都有对比（UI 铬件仍走主题色）。
      scene.background = new THREE.Color(0x081712);
      const camera = new THREE.PerspectiveCamera(45, w / h, 0.1, 200);
      // 构图预算：相机距离按"核心球+最外轨道环"整体入画取值——
      // 半高 = tan(fov/2)×dist ≈ 3.2，须 ≥ 最外轨道延伸（~3.0）。
      // 此前只对心不拉远，宽屏窄高比下轨道环与星体上下出画（实测）。
      // 滚轮缩放：操作员可推近/拉远（限幅+平滑，BaiLongma 地球同款）。
      camera.position.set(0, 1.4, 7.6);
      camera.lookAt(0, 0, 0);
      let camDistTarget = camera.position.length();

      scene.add(new THREE.AmbientLight(0xdfffe8, 0.5));
      const key = new THREE.PointLight(0x9fffcf, 40, 60);
      key.position.set(6, 5, 8);
      scene.add(key);

      // —— 氛围层：微光核心球（程序化贴图：年轮质感的斑驳绿核）——
      // 装饰性随机用种子 LCG（确定性星空；非加密用途，勿用 crypto 语义误读）
      let seed = 0x9e3779b9;
      const rnd = () => {
        seed = (seed * 1664525 + 1013904223) % 4294967296;
        return seed / 4294967296;
      };
      const coreTexCanvas = document.createElement("canvas");
      coreTexCanvas.width = 512;
      coreTexCanvas.height = 256;
      const c2 = coreTexCanvas.getContext("2d");
      if (c2) {
        const grad = c2.createLinearGradient(0, 0, 0, 256);
        grad.addColorStop(0, "#0d2b1e");
        grad.addColorStop(0.5, "#123526");
        grad.addColorStop(1, "#0a2318");
        c2.fillStyle = grad;
        c2.fillRect(0, 0, 512, 256);
        // 年轮斑驳：随机半透明弧线
        for (let i = 0; i < 60; i++) {
          c2.strokeStyle = `rgba(120, 220, 170, ${0.03 + rnd() * 0.07})`;
          c2.lineWidth = 1 + rnd() * 2.5;
          c2.beginPath();
          c2.ellipse(256, 128, 40 + rnd() * 200, 20 + rnd() * 90, rnd() * Math.PI, 0, Math.PI * 2);
          c2.stroke();
        }
      }
      const coreTex = new THREE.CanvasTexture(coreTexCanvas);
      const core = new THREE.Mesh(
        new THREE.SphereGeometry(1.55, 48, 48),
        new THREE.MeshPhongMaterial({
          map: coreTex,
          emissive: new THREE.Color(0x1d5c3f),
          emissiveIntensity: 0.55,
          shininess: 12,
        })
      );
      scene.add(core);

      // 双层大气辉光
      const atmo = (radius: number, color: number, opacity: number, back: boolean) => {
        const m = new THREE.Mesh(
          new THREE.SphereGeometry(radius, 32, 32),
          new THREE.MeshBasicMaterial({
            color, transparent: true, opacity,
            side: back ? THREE.BackSide : THREE.FrontSide,
            depthWrite: false,
          })
        );
        scene.add(m);
        return m;
      };
      const atmoOuter = atmo(1.78, 0x4ade80, 0.05, true);
      const atmoInner = atmo(1.66, 0x86efac, 0.035, false);
      // 色温覆层（§10）：等审批树脂慢闪 / 出错陶土 tint——语义色不
      // 常驻发红，动画里按态启停。
      const tintAtmo = new THREE.Mesh(
        new THREE.SphereGeometry(1.72, 32, 32),
        new THREE.MeshBasicMaterial({
          color: 0xd9a441, transparent: true, opacity: 0,
          side: THREE.FrontSide, depthWrite: false,
        })
      );
      scene.add(tintAtmo);
      // E4 决定共振环 + E2 收工巡视光点（§11）：一次性事件动画的载体。
      const fxRing = new THREE.Mesh(
        new THREE.TorusGeometry(2.15, 0.03, 8, 96),
        new THREE.MeshBasicMaterial({ color: 0x86efac, transparent: true, opacity: 0 })
      );
      fxRing.rotation.x = Math.PI / 2.4;
      scene.add(fxRing);
      // 预算环上的事件坐标系（刻线/巡视光点共用）：与预算环同倾角。
      const ringPlane = new THREE.Group();
      ringPlane.rotation.x = Math.PI / 2.4;
      scene.add(ringPlane);
      const patrolDot = new THREE.Mesh(
        new THREE.SphereGeometry(0.06, 12, 12),
        new THREE.MeshBasicMaterial({ color: 0xbbf7d0, transparent: true, opacity: 0 })
      );
      ringPlane.add(patrolDot);
      // 年轮刻线组：今日真实收口的任务数（§13.2——从 tasks API 派生，
      // 不做本地假计数）。
      const tickGroup = new THREE.Group();
      ringPlane.add(tickGroup);
      const ensureTicks = (count: number) => {
        const capped = Math.min(count, 12);
        while (tickGroup.children.length > capped) {
          const m = tickGroup.children.pop() as import("three").Mesh;
          tickGroup.remove(m);
          m.geometry.dispose();
        }
        while (tickGroup.children.length < capped) {
          const i = tickGroup.children.length;
          const a = -Math.PI / 2 + (i + 1) * 0.16;
          const m = new THREE.Mesh(
            new THREE.SphereGeometry(0.035, 8, 8),
            new THREE.MeshBasicMaterial({ color: 0xbbf7d0, transparent: true, opacity: 0.9 })
          );
          m.position.set(Math.cos(a) * 2.15, Math.sin(a) * 2.15, 0.02);
          tickGroup.add(m);
        }
      };

      // —— 星空粒子（同一种子 LCG：装饰性坐标）——
      const starVerts: number[] = [];
      for (let i = 0; i < 1800; i++) {
        const theta = rnd() * Math.PI * 2;
        const phi = Math.acos(2 * rnd() - 1);
        const r = 24 + rnd() * 16;
        starVerts.push(
          r * Math.sin(phi) * Math.cos(theta),
          r * Math.cos(phi),
          r * Math.sin(phi) * Math.sin(theta)
        );
      }
      const starGeo = new THREE.BufferGeometry();
      starGeo.setAttribute("position", new THREE.Float32BufferAttribute(starVerts, 3));
      const stars = new THREE.Points(
        starGeo,
        new THREE.PointsMaterial({ color: 0xcfe8d8, size: 0.05, sizeAttenuation: true })
      );
      scene.add(stars);

      // —— 数据层容器：思考者星体 / 任务轨道弧 / 预算外环 ——
      const dataGroup = new THREE.Group();
      scene.add(dataGroup);
      const thinkerBodies = new Map<string, { mesh: import("three").Mesh; orbit: import("three").Group; speed: number; phase: number }>();
      let budgetRing: import("three").Mesh | null = null;

      const ORBIT_PALETTE = [0x86efac, 0x93c5fd, 0xfcd34d, 0xf9a8d4];
      function ensureThinkerBodies(names: string[]) {
        // 移除已消失的
        for (const [name, b] of thinkerBodies) {
          if (!names.includes(name)) {
            dataGroup.remove(b.orbit);
            b.mesh.geometry.dispose();
            thinkerBodies.delete(name);
          }
        }
        names.forEach((name, i) => {
          if (thinkerBodies.has(name)) return;
          const orbit = new THREE.Group();
          orbit.rotation.x = 0.42 + i * 0.5;
          orbit.rotation.z = i * 0.35;
          const color = ORBIT_PALETTE[i % ORBIT_PALETTE.length];
          // 轨道细环（半径与相机半高 3.2 联动：最外 2.85+星体 ≤ 3.0）
          const ring = new THREE.Mesh(
            new THREE.TorusGeometry(2.35 + i * 0.5, 0.006, 8, 96),
            new THREE.MeshBasicMaterial({ color, transparent: true, opacity: 0.22 })
          );
          ring.rotation.x = Math.PI / 2;
          orbit.add(ring);
          // 星体
          const body = new THREE.Mesh(
            new THREE.SphereGeometry(0.14, 20, 20),
            new THREE.MeshBasicMaterial({ color })
          );
          orbit.add(body);
          dataGroup.add(orbit);
          thinkerBodies.set(name, {
            mesh: body, orbit,
            speed: 0.25 + i * 0.11,
            phase: rnd() * Math.PI * 2,
          });
        });
      }
      // 任务轨道弧：心智 working 时的活动轨迹。固定对称相位（此前
      // 随机朝向+残缺形态看着像画错了），整组绕核慢旋表达"推进中"。
      const arcGroup = new THREE.Group();
      dataGroup.add(arcGroup);
      function ensureTaskArcs(count: number) {
        while (arcGroup.children.length > count) {
          const line = arcGroup.children.pop() as import("three").Line;
          line.geometry.dispose();
        }
        while (arcGroup.children.length < count) {
          const i = arcGroup.children.length;
          const geo = new THREE.BufferGeometry().setFromPoints(
            new THREE.EllipseCurve(0, 0, 3.05 + i * 0.4, 3.05 + i * 0.4, 0, Math.PI * 0.62)
              .getPoints(64)
              .map((p) => new THREE.Vector3(p.x, 0, p.y))
          );
          const line = new THREE.Line(
            geo,
            new THREE.LineBasicMaterial({ color: 0x9fffcf, transparent: true, opacity: 0.3 })
          );
          line.rotation.set(1.05 + i * 0.5, i * Math.PI, 0); // 镜像对称
          arcGroup.add(line);
        }
      }
      function ensureBudgetRing(ratio: number | null) {
        const want = ratio === null ? null : Math.max(ratio, 0.08);
        if (want === null) {
          if (budgetRing) {
            dataGroup.remove(budgetRing);
            budgetRing.geometry.dispose();
            budgetRing = null;
          }
          return;
        }
        if (!budgetRing) {
          budgetRing = new THREE.Mesh(
            new THREE.TorusGeometry(2.15, 0.02, 8, 96),
            new THREE.MeshBasicMaterial({ color: 0xfcd34d, transparent: true, opacity: 0.1 })
          );
          budgetRing.rotation.x = Math.PI / 2.4;
          dataGroup.add(budgetRing);
        }
        const mat = budgetRing.material as import("three").MeshBasicMaterial;
        mat.opacity = 0.12 + want * 0.5;
        mat.color.setHex(want > 0.8 ? 0xf87171 : 0xfcd34d);
      }

      // —— 数据轮询（轻：30s；失败静默保持上一态）——
      let mindState: MindState = "idle";
      let tickCount = -1;

      // E3 错误碎粒（theater.md §11）：陶土色碎粒自核心近轨弹出又坠
      // 回（抛物线 700ms）——"疼了一下"而非"坏了"。
      const debris: Array<{ mesh: import("three").Mesh; vel: import("three").Vector3 }> = [];
      let debrisT0 = -1e9;
      const spawnDebris = () => {
        if (disposed) return;
        // 清上一轮（触发间隔 > 动画时长，理论不会重叠——防御性清理）。
        for (const d of debris) {
          scene.remove(d.mesh);
          d.mesh.geometry.dispose();
          (d.mesh.material as import("three").Material).dispose();
        }
        debris.length = 0;
        for (let i = 0; i < 4; i++) {
          const mesh = new THREE.Mesh(
            new THREE.SphereGeometry(0.055, 10, 10),
            new THREE.MeshBasicMaterial({ color: 0xcf6f5a, transparent: true })
          );
          const a = (i / 4) * Math.PI * 2 + rnd() * 0.6;
          debris.push({
            mesh,
            vel: new THREE.Vector3(
              Math.cos(a) * (0.9 + rnd() * 0.4),
              1.5 + rnd() * 0.5,
              Math.sin(a) * (0.9 + rnd() * 0.4)
            ),
          });
          mesh.position.set(0, 1.65, 0);
          scene.add(mesh);
        }
        debrisT0 = performance.now();
      };

      const poll = async () => {
        try {
          const d = await fetchCosmosData(identityId);
          if (disposed) return;
          // E3 错误碎粒：mindState 转入 error 的瞬间（边沿，非持续）
          // ——伤害是事件不是状态（theater.md §11）。
          if (d.state === "error" && mindState !== "error") {
            spawnDebris();
          }
          mindState = d.state;
          ensureThinkerBodies(d.thinkers.map((t) => t.name));
          for (const t of d.thinkers) {
            const b = thinkerBodies.get(t.name);
            if (b) {
              (b.mesh.material as import("three").MeshBasicMaterial).color.setHex(
                t.state === "active" ? 0xbbf7d0 : 0x475569
              );
            }
          }
          ensureTaskArcs(d.working ? 2 : 0);
          ensureBudgetRing(d.budgetRatio);
          if (d.doneToday !== tickCount) {
            tickCount = d.doneToday;
            ensureTicks(tickCount);
          }
        } catch {
          /* 数据失败保持上一态 */
        }
      };
      void poll();
      const dataTimer = setInterval(poll, COSMOS_POLL_MS);

      // —— 渲染循环：可见即动，隐藏即停 ——
      // 五态插值状态（§10）：亮度/转速/呼吸向目标参数指数阻尼收敛
      // （1−exp(−λ·dt)，帧率无关，≈800ms 与设计契约一致）。
      const cur: { glow: number; spin: number; hz: number } = {
        glow: MIND_STATE_PARAMS.idle.glow,
        spin: MIND_STATE_PARAMS.idle.spin,
        hz: MIND_STATE_PARAMS.idle.hz,
      };
      let breathPhase = 0;
      let lastDecisionT0 = 0;
      let lastDecisionDir: "allow" | "deny" = "allow";
      let lastCompletionT0 = 0;
      // 闲置微翻身（§13.1）：idle 时每 30–90s 短促一晃——睡着的人会翻身。
      let nextRollAt = performance.now() + 25000 + rnd() * 40000;
      let rollStartAt = -1e9;
      let raf = 0;
      let running = false;
      let t0 = performance.now();
      const frame = (now: number) => {
        raf = requestAnimationFrame(frame);
        const dt = Math.min(0.1, (now - t0) / 1000);
        t0 = now;
        const t = now / 1000;
        const P = MIND_STATE_PARAMS[mindState];
        const k = 1 - Math.exp(-dt * 4.5);
        cur.glow += (P.glow - cur.glow) * k;
        cur.spin += (P.spin - cur.spin) * k;
        cur.hz += (P.hz - cur.hz) * k;
        breathPhase += dt * cur.hz * Math.PI * 2;
        const fx = fxRef.current;
        // 五态应用：亮度=能量（含在听 +0.08，§13.3），转速=推进，
        // awaiting 自转停（spin→0，思考者与任务弧同步屏息）。
        const breath = Math.sin(breathPhase);
        const coreMat = core.material as import("three").MeshPhongMaterial;
        coreMat.emissiveIntensity =
          0.3 + cur.glow * 0.45 + breath * 0.1 + (fx.listening ? 0.08 : 0);
        (atmoOuter.material as import("three").MeshBasicMaterial).opacity =
          0.03 + cur.glow * 0.06;
        (atmoInner.material as import("three").MeshBasicMaterial).opacity =
          0.02 + cur.glow * 0.04;
        const tintMat = tintAtmo.material as import("three").MeshBasicMaterial;
        if (P.tint !== null) {
          tintMat.color.setHex(P.tint);
          tintMat.opacity = P.blink
            ? 0.06 + 0.05 * (0.5 + 0.5 * Math.sin(t * 1.8))
            : 0.15;
        } else {
          tintMat.opacity = Math.max(0, tintMat.opacity - dt * 0.5);
        }
        core.rotation.y += dt * 0.12 * cur.spin;
        // E1 推卡前兆（theater.md §11）：核心球朝卡片象限单侧脉冲
        // +0.1（120ms）——说话有身体前兆，卡才是"它推的"。
        if (fx.pulse && now - fx.pulse.t0 < 120) {
          const env = 1 - (now - fx.pulse.t0) / 120;
          coreMat.emissiveIntensity += 0.1 * env;
          core.position.x = (fx.pulse.quadrant === "right" ? 0.07 : 0) * env;
        } else {
          core.position.x = 0;
        }
        // E3 碎粒动画：抛物线 700ms（上抛+重力回落），尾段淡出。
        if (debris.length > 0) {
          const age = now - debrisT0;
          if (age >= 700) {
            for (const d of debris) {
              scene.remove(d.mesh);
              d.mesh.geometry.dispose();
              (d.mesh.material as import("three").Material).dispose();
            }
            debris.length = 0;
          } else {
            const s = age / 1000;
            for (const d of debris) {
              d.mesh.position.set(
                d.vel.x * s,
                1.65 + d.vel.y * s - 4.5 * s * s,
                d.vel.z * s
              );
              (d.mesh.material as import("three").MeshBasicMaterial).opacity =
                Math.max(0, 1 - age / 700);
            }
          }
        }
        stars.rotation.y += dt * 0.008;
        arcGroup.rotation.y += dt * 0.1 * cur.spin;
        // 闲置微翻身：短促一晃后归位
        if (mindState === "idle" && now > nextRollAt) {
          rollStartAt = now;
          nextRollAt = now + 30000 + rnd() * 60000;
        }
        const rollP = (now - rollStartAt) / 1200;
        core.rotation.z =
          rollP >= 0 && rollP <= 1 ? Math.sin(rollP * Math.PI * 2) * 0.02 * (1 - rollP) : 0;
        // E4 审批决定（§11）：allow 外扩顺扫 / deny 收拢，方向语义可断言
        if (fx.decision && fx.decision.t0 !== lastDecisionT0) {
          lastDecisionT0 = fx.decision.t0;
          lastDecisionDir = fx.decision.dir;
        }
        const fxRingMat = fxRing.material as import("three").MeshBasicMaterial;
        const decAge = lastDecisionT0 ? (now - lastDecisionT0) / 300 : 2;
        if (decAge >= 0 && decAge < 1) {
          const s = lastDecisionDir === "allow" ? 0.92 + decAge * 0.3 : 1.2 - decAge * 0.3;
          fxRing.scale.setScalar(s);
          fxRingMat.color.setHex(lastDecisionDir === "allow" ? 0x86efac : 0xcf6f5a);
          fxRingMat.opacity = 0.7 * (1 - decAge);
        } else {
          fxRingMat.opacity = 0;
        }
        // E2 收工（§11）：光点沿预算环巡视一圈后熄灭——「收工」句法
        if (fx.completion && fx.completion.t0 !== lastCompletionT0) {
          lastCompletionT0 = fx.completion.t0;
        }
        const patAge = lastCompletionT0 ? (now - lastCompletionT0) / 900 : 2;
        if (patAge >= 0 && patAge < 1) {
          const a = -Math.PI / 2 + patAge * Math.PI * 2;
          patrolDot.position.set(Math.cos(a) * 2.15, Math.sin(a) * 2.15, 0.03);
          (patrolDot.material as import("three").MeshBasicMaterial).opacity =
            0.9 * (1 - patAge);
        } else {
          (patrolDot.material as import("three").MeshBasicMaterial).opacity = 0;
        }
        // 滚轮缩放平滑收敛（限幅 4.8–11，构图预算外留电影式余量）
        const camCur = camera.position.length();
        if (Math.abs(camDistTarget - camCur) > 0.01) {
          const nd = camCur + (camDistTarget - camCur) * Math.min(1, dt * 5);
          camera.position.multiplyScalar(nd / camCur);
          camera.lookAt(0, 0, 0);
        }
        for (const b of thinkerBodies.values()) {
          b.phase += dt * b.speed * cur.spin;
          const r = 2.35 + (thinkerBodies.size > 1 ? [...thinkerBodies.values()].indexOf(b) * 0.5 : 0);
          b.mesh.position.set(Math.cos(b.phase) * r, 0, Math.sin(b.phase) * r);
        }
        renderer.render(scene, camera);
      };
      const start = () => {
        if (!running && !disposed) {
          running = true;
          t0 = performance.now();
          raf = requestAnimationFrame(frame);
        }
      };
      const stop = () => {
        if (running) {
          running = false;
          cancelAnimationFrame(raf);
        }
      };
      const onVis = () => (document.hidden ? stop() : start());
      document.addEventListener("visibilitychange", onVis);
      const onResize = () => {
        const { w, h } = size();
        camera.aspect = w / h;
        camera.updateProjectionMatrix();
        renderer.setSize(w, h, false);
      };
      const ro = new ResizeObserver(onResize);
      ro.observe(canvas);
      // 滚轮缩放：挂在舞台根（canvas 的父元素）而非 canvas 本身——
      // canvas 是 absolute 底层，被 z-10 的头部/内容层整个盖住，
      // 事件目标永远不是 canvas，挂在它上等于收不到（实测：滚轮完全
      // 不响应）。下列情况放行不拦截：指针落在卡片上（[data-scene-id]）
      // 或落在可滚动容器里（卡片列表要能滚）。
      const host = canvas.parentElement ?? canvas;
      const scrollableAncestor = (el: Element | null): boolean => {
        for (let n = el; n && n !== host; n = n.parentElement) {
          const s = getComputedStyle(n);
          if (/(auto|scroll)/.test(s.overflowY) && n.scrollHeight > n.clientHeight + 1) {
            return true;
          }
        }
        return false;
      };
      const onWheel = (e: WheelEvent) => {
        const target = e.target as Element | null;
        if (target?.closest("[data-scene-id]") || scrollableAncestor(target)) return;
        e.preventDefault();
        camDistTarget = Math.min(11, Math.max(4.8, camDistTarget + e.deltaY * 0.004));
      };
      host.addEventListener("wheel", onWheel, { passive: false });
      // 相机距离探针（E2E 用：滚轮缩放可被机械验证，不靠肉眼比图）。
      (canvas as unknown as { __camDist?: () => number }).__camDist = () =>
        camera.position.length();
      // prefers-reduced-motion：只渲一帧静场
      if (window.matchMedia("(prefers-reduced-motion: reduce)").matches) {
        renderer.render(scene, camera);
      } else {
        start();
      }

      function degradedFallback() {
        // WebGL 建失败时的降级底色（canvas 已在 effect 顶部做过空值守卫）。
        const el = canvasRef.current;
        if (!el) return;
        el.style.background =
          "radial-gradient(ellipse at 30% 40%, rgba(34,84,58,0.35), transparent 60%), radial-gradient(ellipse at 70% 60%, rgba(20,60,42,0.3), transparent 55%), #0a1a12";
      }

      cleanup = () => {
        disposed = true;
        stop();
        if (dataTimer) clearInterval(dataTimer);
        document.removeEventListener("visibilitychange", onVis);
        host.removeEventListener("wheel", onWheel);
        ro.disconnect();
        renderer.dispose();
        coreTex.dispose();
        scene.traverse((o) => {
          const mesh = o as import("three").Mesh;
          if (mesh.geometry) mesh.geometry.dispose();
          const mat = mesh.material as import("three").Material | undefined;
          if (mat) mat.dispose();
        });
      };
    })();

    return () => {
      disposed = true;
      cleanup();
    };
  }, [identityId, degraded]);

  if (degraded) {
    return (
      <div
        aria-hidden
        className="absolute inset-0"
        style={{
          background:
            "radial-gradient(ellipse at 30% 40%, rgba(34,84,58,0.35), transparent 60%), radial-gradient(ellipse at 70% 60%, rgba(20,60,42,0.3), transparent 55%), #0a1a12",
        }}
      />
    );
  }
  return (
    <canvas
      ref={canvasRef}
      aria-hidden
      className="absolute inset-0 size-full"
    />
  );
}
