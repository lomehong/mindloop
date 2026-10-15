// 命令面板（theater.md §16.8 引 cmdk；avatar.md §6.1 升格为正式一级
// 入口）：全部引擎路由（含运行四页/设置三页）+ 身份切换 + 动作
// （新建委托/喂食）。砍 tab 行后这是低频管理面的第一入口；条目控制
// 在几十个（无虚拟化），全文搜索仍走各页搜索框（§4 职责表）。

import { Command } from "cmdk";
import { useNavigate } from "react-router";
import { useState } from "react";

import { useQuery } from "@tanstack/react-query";

import { NewTaskDialog } from "~/components/new-task-dialog";
import { fetchIdentities } from "~/lib/api";

/** 引擎室全部路由（avatar.md §6.1：⌘K 覆盖全部面板——面板消失，
 * 入口必须在）。label 保持既有页面名；avatar B 期统一换轴时改这里。 */
const ROUTES = [
  { label: "舞台", path: "" },
  { label: "轨迹 · 泳道", path: "/traj" },
  { label: "轨迹 · 日志流", path: "/log" },
  { label: "轨迹 · 分集", path: "/recap" },
  { label: "回看", path: "/review" },
  { label: "任务", path: "/tasks" },
  { label: "对话", path: "/chat" },
  { label: "记忆", path: "/memories" },
  { label: "感知", path: "/sensors" },
  { label: "运行 · 思考者", path: "/run/thinkers" },
  { label: "运行 · 日程", path: "/run/schedule" },
  { label: "运行 · 健康", path: "/run/health" },
  { label: "运行 · 用量", path: "/run/usage" },
  { label: "设置 · 配置", path: "/settings/config" },
  { label: "设置 · 技能", path: "/settings/skills" },
  { label: "设置 · 连接", path: "/settings/connections" },
];

const itemClass =
  "flex cursor-pointer items-center gap-2.5 rounded-lg px-3 py-2 text-[13px] data-[selected=true]:bg-primary/10";

export function CommandPalette({
  open,
  onOpenChange,
  identityId,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  /** 身份上下文（心智页传入）：有则展示"本身份"分组并把动作落到
   * 该身份；工作台不传——只有全局项与新建委托。 */
  identityId?: string;
}) {
  const navigate = useNavigate();
  const [taskOpen, setTaskOpen] = useState(false);
  const { data: identities } = useQuery({
    queryKey: ["identities"],
    queryFn: fetchIdentities,
    enabled: open,
    refetchInterval: 0,
  });

  // 路由组的目标身份：心智页传自身份；工作台无上下文时落第一个
  // 运行中的身份（无则第一个）——砍 tab 后工作台也必须有全路由入口。
  const routesFor =
    identityId ??
    (identities ?? []).find((it) => it.live)?.id ??
    (identities ?? [])[0]?.id;
  const routesLabel =
    identityId === undefined && routesFor !== undefined
      ? `前往 · ${
          (identities ?? []).find((it) => it.id === routesFor)?.name ?? routesFor
        }`
      : "本身份";

  const go = (path: string) => {
    onOpenChange(false);
    navigate(path);
  };
  const goIdentity = (id: string, rest = "") => {
    onOpenChange(false);
    navigate(`/i/${encodeURIComponent(id)}${rest}`);
  };

  return (
    <>
      <Command.Dialog
        open={open}
        onOpenChange={onOpenChange}
        label="命令面板"
        className="fixed left-1/2 top-[14vh] z-[80] w-[520px] max-w-[92vw] -translate-x-1/2 overflow-hidden rounded-2xl border border-line bg-card shadow-[0_30px_80px_rgba(0,0,0,0.6)] backdrop-blur-[14px]"
        shouldFilter
      >
        <Command.Input
          autoFocus
          placeholder="跳转视图 / 切换身份 / 托付任务…"
          className="w-full border-b border-line bg-transparent px-[18px] py-[15px] text-[14px] text-foreground outline-none placeholder:text-faint"
        />
        <Command.List className="max-h-[380px] overflow-y-auto p-1.5">
          <Command.Empty className="px-3 py-2 text-[12.5px] text-faint">
            无匹配项
          </Command.Empty>
          {routesFor !== undefined && (
            <Command.Group heading={routesLabel} className="text-[11px] text-faint">
              {ROUTES.map((s) => (
                <Command.Item
                  key={s.path}
                  value={`${routesLabel} ${s.label}`}
                  onSelect={() => goIdentity(routesFor, s.path)}
                  className={itemClass}
                >
                  <span className="w-5 font-mono text-[11px] text-faint">↳</span>
                  {s.label}
                </Command.Item>
              ))}
            </Command.Group>
          )}
          {(identities ?? []).length > 0 && (
            <Command.Group heading="身份切换" className="text-[11px] text-faint">
              {(identities ?? [])
                .filter((it) => it.id !== identityId)
                .map((it) => (
                  <Command.Item
                    key={it.id}
                    value={`切换身份 ${it.name}`}
                    onSelect={() => goIdentity(it.id)}
                    className={itemClass}
                  >
                    <span className="w-5 font-mono text-[11px] text-faint">⇄</span>
                    {it.name}
                    <span className="ml-auto font-mono text-[10px] text-faint">
                      {it.live ? "运行中" : "已停"}
                    </span>
                  </Command.Item>
                ))}
            </Command.Group>
          )}
          <Command.Group heading="动作" className="text-[11px] text-faint">
            <Command.Item
              value="动作 新建委托 任务"
              onSelect={() => {
                onOpenChange(false);
                setTaskOpen(true);
              }}
              className={itemClass}
            >
              <span className="w-5 font-mono text-[11px] text-faint">＋</span>
              新建委托（共同的验收标准）
            </Command.Item>
            {routesFor !== undefined && (
              <Command.Item
                value="动作 喂食 预算 调每日预算"
                onSelect={() => goIdentity(routesFor, "/settings/config")}
                className={itemClass}
              >
                <span className="w-5 font-mono text-[11px] text-faint">◍</span>
                喂食 · 调每日预算
              </Command.Item>
            )}
            <Command.Item
              value="全局 工作台 同行面"
              onSelect={() => go("/")}
              className={itemClass}
            >
              <span className="w-5 font-mono text-[11px] text-faint">⌂</span>
              工作台 · 同行面
            </Command.Item>
          </Command.Group>
        </Command.List>
      </Command.Dialog>
      {/* 动作「新建委托」的真身：面板关→对话框开。defaultIdentityId
          取当前身份上下文；工作台入口留空（选运行中的一个）。 */}
      <NewTaskDialog
        open={taskOpen}
        onOpenChange={setTaskOpen}
        defaultIdentityId={identityId}
      />
    </>
  );
}
