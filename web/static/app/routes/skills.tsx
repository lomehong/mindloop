import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Download, RefreshCw, Trash2 } from "lucide-react";
import { useState } from "react";
import { useParams } from "react-router";
import { toast } from "sonner";

import { IdentityTabs } from "~/components/identity-tabs";
import { useControlsEnabled } from "~/components/thinker-controls";
import { ConfirmDialog } from "~/components/confirm-dialog";
import { Badge } from "~/components/ui/badge";
import { Button } from "~/components/ui/button";
import { Input } from "~/components/ui/input";
import { LoadingDots } from "~/components/ui/loading-dots";
import { fetchIdentityStatus, fetchSkills, installSkill, removeSkill } from "~/lib/api";
import {
  SKILLS_POLL_MS,
  STATUS_BACKGROUND_POLL_MS,
} from "~/lib/polling";

export function meta() {
  return [{ title: "mindloop · 技能" }];
}

// source 徽标文案。
const SOURCE_LABEL: Record<string, string> = {
  identity: "身份级",
  global: "全局",
};

export default function SkillsPage() {
  const { identityId = "" } = useParams();
  const controlsEnabled = useControlsEnabled();
  const queryClient = useQueryClient();
  const [source, setSource] = useState("");
  // 待确认删除的技能名；null = 对话框关闭。
  const [skillToRemove, setSkillToRemove] = useState<string | null>(null);

  const { data: status } = useQuery({
    queryKey: ["status", identityId],
    queryFn: () => fetchIdentityStatus(identityId),
    refetchInterval: STATUS_BACKGROUND_POLL_MS,
  });

  const { data: view, isLoading } = useQuery({
    queryKey: ["skills", identityId],
    queryFn: () => fetchSkills(identityId),
    refetchInterval: SKILLS_POLL_MS,
  });

  const install = useMutation({
    mutationFn: () => installSkill(identityId, source.trim()),
    onSuccess: (result) => {
      toast.success(`已安装：${result.installed.join(", ")}`);
      setSource("");
      queryClient.invalidateQueries({ queryKey: ["skills", identityId] });
    },
    onError: (error: Error) => toast.error(error.message),
  });

  const remove = useMutation({
    mutationFn: (name: string) => removeSkill(identityId, name),
    onSuccess: (result) => {
      toast.success(`已删除 ${result.removed}`);
      queryClient.invalidateQueries({ queryKey: ["skills", identityId] });
    },
    onError: (error: Error) => toast.error(error.message),
  });

  if (isLoading || !view) {
    return (
      <div className="flex justify-center py-20">
        <LoadingDots />
      </div>
    );
  }

  return (
    <div className="mx-auto w-full max-w-7xl">
      <IdentityTabs
        identityId={identityId}
        live={status?.live ?? false}
        active="skills"
        name={view.identity?.name}
      />
      <div className="mx-auto w-full max-w-4xl space-y-6 pb-10">
        <div className="flex items-center gap-3">
          <h2 className="font-mono text-xs font-medium uppercase tracking-wider text-muted-foreground">
            技能（Agent Skills 标准）
          </h2>
          <span className="text-[11px] text-muted-foreground">
            每个技能是一个含 SKILL.md 的目录：索引进 ada
            的系统提示，正文按需读取。身份级遮蔽全局同名技能。
          </span>
          {controlsEnabled && (
            <Button
              variant="outline"
              size="sm"
              className="ml-auto"
              onClick={() =>
                queryClient.invalidateQueries({
                  queryKey: ["skills", identityId],
                })
              }
            >
              <RefreshCw className="size-3" />
              刷新
            </Button>
          )}
        </div>

        {controlsEnabled && (
          <form
            className="flex items-center gap-2"
            onSubmit={(event) => {
              event.preventDefault();
              if (source.trim() && !install.isPending) install.mutate();
            }}
          >
            <Input
              value={source}
              onChange={(event) => setSource(event.target.value)}
              placeholder="安装源：本地目录路径，或 owner/repo（GitHub）"
              className="h-9 flex-1 font-mono text-xs"
            />
            <Button
              type="submit"
              size="sm"
              disabled={!source.trim() || install.isPending}
            >
              <Download className="size-3.5" />
              安装
            </Button>
          </form>
        )}

        {view.problems.length > 0 && (
          <div className="rounded-lg border border-amber-300 bg-amber-50 px-3 py-2 text-xs text-amber-900 dark:border-amber-800 dark:bg-amber-950 dark:text-amber-200">
            {view.problems.map((p, idx) => (
              <div key={idx}>⚠ {p}</div>
            ))}
          </div>
        )}

        {view.skills.length === 0 ? (
          <p className="py-10 text-center text-sm text-muted-foreground">
            还没有技能——用上方表单安装，或 mindloop skills init
            脚手架一个。
          </p>
        ) : (
          <div className="space-y-3">
            {view.skills.map((skill) => (
              <div key={skill.name} className="rounded-lg border p-3">
                <div className="flex flex-wrap items-baseline gap-2">
                  <span className="font-mono text-sm font-medium">
                    {skill.name}
                  </span>
                  <Badge variant="outline" className="text-[10px]">
                    {SOURCE_LABEL[skill.source] ?? skill.source}
                  </Badge>
                  {controlsEnabled && skill.source === "identity" && (
                    <Button
                      variant="ghost"
                      size="icon-sm"
                      className="ml-auto"
                      title={`删除 ${skill.name}`}
                      aria-label={`删除 ${skill.name}`}
                      disabled={remove.isPending}
                      onClick={() => setSkillToRemove(skill.name)}
                    >
                      <Trash2 className="size-3" />
                    </Button>
                  )}
                </div>
                <p className="mt-1 text-sm text-muted-foreground">
                  {skill.description}
                </p>
                <p className="mt-1 font-mono text-[10px] text-muted-foreground/70">
                  {skill.dir}
                </p>
              </div>
            ))}
          </div>
        )}
      </div>
      <ConfirmDialog
        open={skillToRemove !== null}
        onOpenChange={(open) => {
          if (!open) setSkillToRemove(null);
        }}
        tone="danger"
        title={skillToRemove ? `删除技能 ${skillToRemove}？` : "删除技能？"}
        description="将从该身份的技能目录中删除此技能。"
        confirmText="删除"
        onConfirm={() => {
          if (skillToRemove) remove.mutate(skillToRemove);
        }}
      />
    </div>
  );
}
