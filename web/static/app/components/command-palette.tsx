// 命令面板（theater.md §16.8 定案引库 cmdk）：Ctrl+K 全局——分区
// 导航 + 身份切换 + 工作台。WAI-ARIA combobox 由 cmdk 提供；条目
// 控制在几百内（无虚拟化），全文搜索仍走各页搜索框（§4 职责表）。

import { Command } from "cmdk";
import { useNavigate } from "react-router";

import { useQuery } from "@tanstack/react-query";

import { fetchIdentities } from "~/lib/api";

const SECTIONS = [
  { label: "舞台", path: "" },
  { label: "轨迹", path: "/traj" },
  { label: "任务", path: "/tasks" },
  { label: "记忆", path: "/memories" },
  { label: "回看", path: "/review" },
  { label: "感知", path: "/sensors" },
  { label: "运行 · 思考者", path: "/run/thinkers" },
  { label: "运行 · 用量", path: "/run/usage" },
  { label: "设置 · 配置", path: "/settings/config" },
];

export function CommandPalette({
  open,
  onOpenChange,
  identityId,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  identityId: string;
}) {
  const navigate = useNavigate();
  const { data: identities } = useQuery({
    queryKey: ["identities"],
    queryFn: fetchIdentities,
    enabled: open,
    refetchInterval: 0,
  });

  const go = (path: string) => {
    onOpenChange(false);
    navigate(path);
  };

  return (
    <Command.Dialog
      open={open}
      onOpenChange={onOpenChange}
      label="命令面板"
      className="fixed left-1/2 top-[14vh] z-[80] w-[520px] max-w-[92vw] -translate-x-1/2 overflow-hidden rounded-2xl border border-line bg-card shadow-[0_30px_80px_rgba(0,0,0,0.6)] backdrop-blur-[14px]"
      shouldFilter
    >
      <Command.Input
        autoFocus
        placeholder="跳转视图 / 切换身份…"
        className="w-full border-b border-line bg-transparent px-[18px] py-[15px] text-[14px] text-foreground outline-none placeholder:text-faint"
      />
      <Command.List className="max-h-[320px] overflow-y-auto p-1.5">
        <Command.Empty className="px-3 py-2 text-[12.5px] text-faint">
          无匹配项
        </Command.Empty>
        <Command.Group heading="本身份" className="text-[11px] text-faint">
          {SECTIONS.map((s) => (
            <Command.Item
              key={s.path}
              value={`本身份 ${s.label}`}
              onSelect={() => go(`/i/${encodeURIComponent(identityId)}${s.path}`)}
              className="flex cursor-pointer items-center gap-2.5 rounded-lg px-3 py-2 text-[13px] data-[selected=true]:bg-primary/10"
            >
              <span className="w-5 font-mono text-[11px] text-faint">↳</span>
              {s.label}
            </Command.Item>
          ))}
        </Command.Group>
        {(identities ?? []).filter((it) => it.id !== identityId).length > 0 && (
          <Command.Group heading="切换身份" className="text-[11px] text-faint">
            {(identities ?? [])
              .filter((it) => it.id !== identityId)
              .map((it) => (
                <Command.Item
                  key={it.id}
                  value={`切换身份 ${it.name}`}
                  onSelect={() => go(`/i/${encodeURIComponent(it.id)}`)}
                  className="flex cursor-pointer items-center gap-2.5 rounded-lg px-3 py-2 text-[13px] data-[selected=true]:bg-primary/10"
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
        <Command.Group heading="全局" className="text-[11px] text-faint">
          <Command.Item
            value="全局 工作台"
            onSelect={() => go("/")}
            className="flex cursor-pointer items-center gap-2.5 rounded-lg px-3 py-2 text-[13px] data-[selected=true]:bg-primary/10"
          >
            <span className="w-5 font-mono text-[11px] text-faint">⌂</span>
            工作台
          </Command.Item>
        </Command.Group>
      </Command.List>
    </Command.Dialog>
  );
}
