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

type CosmosData = {
  thinkers: Array<{ name: string; state: string }>;
  budgetRatio: number | null; // 0..1；null=未设预算
  working: boolean;
};

const COSMOS_POLL_MS = 30_000;

async function fetchCosmosData(identityId: string): Promise<CosmosData> {
  const headers = { Accept: "application/json" };
  const [thinkersRes, usageRes, activityRes] = await Promise.all([
    fetch(`/api/identities/${encodeURIComponent(identityId)}/thinkers`, { headers }).catch(() => null),
    fetch(`/api/identities/${encodeURIComponent(identityId)}/usage`, { headers }).catch(() => null),
    fetch(`/api/identities/${encodeURIComponent(identityId)}/activity`, { headers }).catch(() => null),
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
  if (usageRes?.ok) {
    const j = (await usageRes.json().catch(() => null)) as {
      admission?: { daily_limit?: number; used_today?: number };
    } | null;
    const a = j?.admission;
    if (a && a.daily_limit && a.daily_limit > 0) {
      budgetRatio = Math.min(1, (a.used_today ?? 0) / a.daily_limit);
    }
  }
  let working = false;
  if (activityRes?.ok) {
    const j = (await activityRes.json().catch(() => null)) as { state?: string } | null;
    working = j?.state === "working";
  }
  return { thinkers, budgetRatio, working };
}

export function SceneCosmos({ identityId }: { identityId: string }) {
  const canvasRef = useRef<HTMLCanvasElement | null>(null);
  // WebGL 失败的降级位：CSS 星空（静态、零依赖），舞台不破。
  const [degraded] = useState(
    () => typeof window !== "undefined" && !window.WebGLRenderingContext
  );

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
      atmo(1.78, 0x4ade80, 0.05, true);
      atmo(1.66, 0x86efac, 0.035, false);

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
      let dataTimer: ReturnType<typeof setInterval> | undefined;
      const poll = async () => {
        try {
          const d = await fetchCosmosData(identityId);
          if (disposed) return;
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
        } catch {
          /* 数据失败保持上一态 */
        }
      };
      void poll();
      dataTimer = setInterval(poll, COSMOS_POLL_MS);

      // —— 渲染循环：可见即动，隐藏即停 ——
      let raf = 0;
      let running = false;
      let t0 = performance.now();
      const frame = (now: number) => {
        raf = requestAnimationFrame(frame);
        const dt = (now - t0) / 1000;
        t0 = now;
        core.rotation.y += dt * 0.12;
        stars.rotation.y += dt * 0.008;
        arcGroup.rotation.y += dt * 0.1; // 任务弧整组慢旋：推进中的观感
        // 滚轮缩放平滑收敛（限幅 4.8–11，构图预算外留电影式余量）
        const cur = camera.position.length();
        if (Math.abs(camDistTarget - cur) > 0.01) {
          const nd = cur + (camDistTarget - cur) * Math.min(1, dt * 5);
          camera.position.multiplyScalar(nd / cur);
          camera.lookAt(0, 0, 0);
        }
        for (const b of thinkerBodies.values()) {
          b.phase += dt * b.speed;
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
