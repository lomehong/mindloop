import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import {
  Download,
  Eye,
  Pencil,
  RefreshCw,
  Save,
  Trash2,
} from "lucide-react";
import { useEffect, useState } from "react";
import { useParams } from "react-router";
import { toast } from "sonner";

import { IdentityTabs } from "~/components/identity-tabs";
import { useControlsEnabled } from "~/components/thinker-controls";
import { ConfirmDialog } from "~/components/confirm-dialog";
import { Badge } from "~/components/ui/badge";
import { Button } from "~/components/ui/button";
import { Input } from "~/components/ui/input";
import { LoadingDots } from "~/components/ui/loading-dots";
import { Textarea } from "~/components/ui/textarea";
import {
  fetchIdentityStatus,
  fetchSkillContent,
  fetchSkills,
  installSkill,
  removeSkill,
  updateSkill,
} from "~/lib/api";
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
  // 正在编辑的技能；readOnly=true 是全局层只读查看。非 null 时整页
  // 切换到编辑器视图（长文编辑需要空间，不用弹窗）。
  const [editing, setEditing] = useState<{
    name: string;
    readOnly: boolean;
  } | null>(null);
  // 编辑草稿；null = 尚未从服务端读到正文。
  const [draft, setDraft] = useState<string | null>(null);

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

  // 编辑器的正文按需取（打开时拉最新——避免拿到列表轮询缓存的旧文）。
  const { data: skillContent, isLoading: contentLoading } = useQuery({
    queryKey: ["skill-content", identityId, editing?.name],
    queryFn: () => fetchSkillContent(identityId, editing!.name),
    enabled: editing !== null,
  });

  useEffect(() => {
    if (skillContent) setDraft(skillContent.content);
  }, [skillContent]);

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

  const save = useMutation({
    mutationFn: () =>
      updateSkill(identityId, editing!.name, draft ?? ""),
    onSuccess: (result) => {
      toast.success(`已保存 ${result.saved}`);
      setEditing(null);
      setDraft(null);
      queryClient.invalidateQueries({ queryKey: ["skills", identityId] });
    },
    onError: (error: Error) => toast.error(error.message),
  });

  const closeEditor = () => {
    setEditing(null);
    setDraft(null);
  };

  // ---------- 编辑器视图 ----------
  if (editing) {
    const unchanged = draft === null || draft === skillContent?.content;
    return (
      <div className="mx-auto w-full max-w-7xl">
        <IdentityTabs
          identityId={identityId}
          live={status?.live ?? false}
          active="skills"
          name={view?.identity?.name}
        />
        <div className="mx-auto w-full max-w-4xl space-y-3 pb-10">
          <div className="flex flex-wrap items-center gap-2">
            <Button
              variant="ghost"
              size="sm"
              onClick={closeEditor}
              disabled={save.isPending}
            >
              ← 返回
            </Button>
            <span className="font-mono text-sm font-medium">
              {editing.name}
            </span>
            <Badge variant="outline" className="text-[10px]">
              {SOURCE_LABEL[skillContent?.source ?? "identity"] ?? ""}
            </Badge>
            {editing.readOnly && (
              <span className="text-xs text-muted-foreground">
                全局层共享技能，只读；修改请经 CLI
              </span>
            )}
            {!editing.readOnly && (
              <div className="ml-auto flex items-center gap-2">
                <span className="text-[11px] text-muted-foreground">
                  {draft?.length ?? 0} 字符 · 保存时校验 frontmatter，
                  校验失败不落盘
                </span>
                <Button
                  size="sm"
                  disabled={
                    unchanged || contentLoading || save.isPending
                  }
                  onClick={() => save.mutate()}
                >
                  <Save className="size-3.5" />
                  {save.isPending ? "保存中…" : "保存"}
                </Button>
              </div>
            )}
          </div>
          {contentLoading || draft === null ? (
            <div className="flex justify-center py-20">
              <LoadingDots />
            </div>
          ) : (
            <Textarea
              value={draft}
              readOnly={editing.readOnly}
              onChange={(event) => setDraft(event.target.value)}
              spellCheck={false}
              className="min-h-[65vh] resize-y font-mono text-xs leading-relaxed"
            />
          )}
        </div>
      </div>
    );
  }

  // ---------- 列表视图 ----------
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
                      title={`编辑 ${skill.name}`}
                      aria-label={`编辑 ${skill.name}`}
                      onClick={() => {
                        setDraft(null);
                        setEditing({ name: skill.name, readOnly: false });
                      }}
                    >
                      <Pencil className="size-3" />
                    </Button>
                  )}
                  {skill.source === "global" && (
                    <Button
                      variant="ghost"
                      size="icon-sm"
                      className="ml-auto"
                      title={`查看 ${skill.name}（只读）`}
                      aria-label={`查看 ${skill.name}`}
                      onClick={() => {
                        setDraft(null);
                        setEditing({ name: skill.name, readOnly: true });
                      }}
                    >
                      <Eye className="size-3" />
                    </Button>
                  )}
                  {controlsEnabled && skill.source === "identity" && (
                    <Button
                      variant="ghost"
                      size="icon-sm"
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
