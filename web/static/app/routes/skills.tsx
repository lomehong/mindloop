import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Download, FileCode, RefreshCw, Save } from "lucide-react";
import { useEffect, useState } from "react";
import { useParams } from "react-router";
import { toast } from "sonner";

import { useControlsEnabled } from "~/components/thinker-controls";
import { ConfirmDialog } from "~/components/confirm-dialog";
import { QueryErrorBanner } from "~/components/query-error-banner";
import { Button } from "~/components/ui/button";
import {
  Empty,
  EmptyDescription,
  EmptyHeader,
  EmptyMedia,
  EmptyTitle,
} from "~/components/ui/empty";
import { Input } from "~/components/ui/input";
import { Skeleton } from "~/components/ui/loading-skeleton";
import { Textarea } from "~/components/ui/textarea";
import {
  fetchSkillContent,
  fetchSkills,
  installSkill,
  removeSkill,
  updateSkill,
} from "~/lib/api";
import { SKILLS_POLL_MS } from "~/lib/polling";

export function meta() {
  return [{ title: "mindloop · 技能" }];
}

// source 徽标文案。
const SOURCE_LABEL: Record<string, string> = {
  identity: "身份级",
  global: "全局",
};

/** 归属/元信息胶囊（mockup .mbadge）。 */
const MBADGE =
  "rounded-full border border-line-strong px-1.5 py-px font-mono text-[9.5px] font-normal leading-[1.6] text-muted-foreground";

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

  const {
    data: view,
    isLoading,
    isError,
    error,
    refetch,
  } = useQuery({
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
      <div className="mx-auto w-full max-w-7xl 2xl:max-w-[1600px]">
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
            <span className="font-mono text-[13px] font-medium">
              {editing.name}
            </span>
            <span className={MBADGE}>
              {SOURCE_LABEL[skillContent?.source ?? "identity"] ?? ""}
            </span>
            {editing.readOnly && (
              <span className="text-[11px] text-faint">
                全局层共享技能，只读；修改请经 CLI
              </span>
            )}
            {!editing.readOnly && (
              <div className="ml-auto flex items-center gap-2">
                <span className="text-[11px] text-faint">
                  {draft?.length ?? 0} 字符 · 保存时校验 frontmatter，
                  校验失败不落盘
                </span>
                <Button
                  size="sm"
                  className="h-7 px-2.5 text-[12px]"
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
            <Skeleton className="h-[65vh] w-full rounded-xl" />
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
  if (isError) {
    return (
      <div className="mx-auto w-full max-w-7xl 2xl:max-w-[1600px]">
        <div className="mx-auto w-full max-w-4xl pb-10">
          <QueryErrorBanner error={error} onRetry={() => void refetch()} />
        </div>
      </div>
    );
  }

  if (isLoading || !view) {
    return (
      <div className="mx-auto w-full max-w-7xl 2xl:max-w-[1600px]">
        <div className="mx-auto w-full max-w-4xl space-y-4 pb-10">
          <Skeleton className="h-7 w-44" />
          <Skeleton className="h-9 w-full" />
          <Skeleton className="h-16 w-full rounded-xl" />
          <Skeleton className="h-16 w-full rounded-xl" />
        </div>
      </div>
    );
  }

  return (
    <div className="mx-auto w-full max-w-7xl 2xl:max-w-[1600px]">
      <div className="mx-auto w-full max-w-4xl space-y-4 pb-10">
        <div className="flex flex-wrap items-baseline gap-3">
          <h1 className="font-note text-[22px] font-semibold tracking-[0.01em]">
            技能
          </h1>
          <span className="text-[13px] text-muted-foreground">
            Agent Skills 标准 · 索引进系统提示，正文按需读取
          </span>
          {controlsEnabled && (
            <Button
              variant="outline"
              size="sm"
              className="ml-auto self-center"
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
            className="flex items-center gap-2.5"
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
              className="h-8 px-3 text-[12px]"
              disabled={!source.trim() || install.isPending}
            >
              <Download className="size-3.5" />
              安装
            </Button>
          </form>
        )}

        {view.problems.length > 0 && (
          <div className="rounded-lg border border-resin/40 bg-resin/[0.06] px-3 py-2 text-xs text-resin">
            {view.problems.map((p, idx) => (
              <div key={idx}>⚠ {p}</div>
            ))}
          </div>
        )}

        {view.skills.length === 0 ? (
          <Empty>
            <EmptyHeader>
              <EmptyMedia variant="icon">
                <FileCode className="size-5" />
              </EmptyMedia>
              <EmptyTitle className="text-base">还没有技能</EmptyTitle>
              <EmptyDescription>
                <div>
                  用上方表单安装（本地目录或 owner/repo），或
                  mindloop skills init 脚手架一个。
                </div>
                <div className="mt-2 break-all rounded-md bg-muted px-3 py-2 text-left font-mono text-[11px]">
                  mindloop skills init {view.identity?.id ?? identityId} greet
                </div>
              </EmptyDescription>
            </EmptyHeader>
          </Empty>
        ) : (
          <div className="overflow-hidden rounded-xl border border-line bg-card">
            {view.skills.map((skill) => (
              <div
                key={skill.name}
                className="grid grid-cols-1 items-center gap-x-3.5 gap-y-1.5 border-b border-line px-4 py-2.5 last:border-b-0 hover:bg-muted/40 md:grid-cols-[minmax(170px,230px)_1fr_auto]"
              >
                <div className="flex min-w-0 flex-wrap items-center gap-2">
                  <span className="font-mono text-[13px]">{skill.name}</span>
                  <span className={MBADGE}>
                    {SOURCE_LABEL[skill.source] ?? skill.source}
                  </span>
                </div>
                <div className="min-w-0">
                  <p className="text-[12.5px] leading-relaxed text-muted-foreground">
                    {skill.description}
                  </p>
                  <p className="break-all font-mono text-[10.5px] text-faint">
                    {skill.dir}
                  </p>
                </div>
                <div className="flex justify-end gap-1">
                  {controlsEnabled && skill.source === "identity" && (
                    <Button
                      variant="ghost"
                      size="sm"
                      className="h-6 px-2 text-[11px] text-muted-foreground"
                      title={`编辑 ${skill.name}`}
                      aria-label={`编辑 ${skill.name}`}
                      onClick={() => {
                        setDraft(null);
                        setEditing({ name: skill.name, readOnly: false });
                      }}
                    >
                      编辑
                    </Button>
                  )}
                  {skill.source === "global" && (
                    <Button
                      variant="ghost"
                      size="sm"
                      className="h-6 px-2 text-[11px] text-muted-foreground"
                      title={`查看 ${skill.name}（只读）`}
                      aria-label={`查看 ${skill.name}`}
                      onClick={() => {
                        setDraft(null);
                        setEditing({ name: skill.name, readOnly: true });
                      }}
                    >
                      查看（只读）
                    </Button>
                  )}
                  {controlsEnabled && skill.source === "identity" && (
                    <Button
                      variant="ghost"
                      size="sm"
                      className="h-6 px-2 text-[11px] text-muted-foreground hover:text-clay"
                      title={`删除 ${skill.name}`}
                      aria-label={`删除 ${skill.name}`}
                      disabled={remove.isPending}
                      onClick={() => setSkillToRemove(skill.name)}
                    >
                      删除
                    </Button>
                  )}
                </div>
              </div>
            ))}
          </div>
        )}

        {view.skills.length > 0 && (
          <div className="px-0.5 text-[12px] leading-relaxed text-muted-foreground">
            身份级遮蔽全局同名技能；保存时校验 frontmatter，校验失败不落盘。
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
