// provider-profiles.tsx 提供商档案区：providers.json 的连接与模型
// 清单在这里管理（CRUD + 从提供商拉取目录）。密钥只以"输入 →
// 分流写入身份 .env"的形态经过请求体——providers.json 与页面回显
// 永不出现密钥字面量。
//
// 两级合并的写语义（关键）：PUT 只提交 origin=identity 的档案
// （纯全局档案保持只读，避免保存动作把它们复制进身份级）；删除
// 档案时一并清理引用它的档位绑定。

import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Plus, RefreshCw } from "lucide-react";
import { useState } from "react";
import { toast } from "sonner";

import { ConfirmDialog } from "~/components/confirm-dialog";
import { Badge } from "~/components/ui/badge";
import { Button } from "~/components/ui/button";
import { Input } from "~/components/ui/input";
import { LoadingDots } from "~/components/ui/loading-dots";
import { fetchLlmModels, fetchLlmProviders, saveLlmProviders } from "~/lib/api";
import type {
  LlmTierBinding,
  LlmProviderProfileInput,
  LlmProviderProfileView,
} from "~/lib/types";

const NEW_PROFILE = "__new__";

interface ProfileDraft {
  id: string;
  label: string;
  provider: string;
  base_url: string;
  api_key_env: string;
  api_key: string;
  models: string; // 每行一个
}

function draftFromProfile(p: LlmProviderProfileView): ProfileDraft {
  return {
    id: p.id,
    label: p.label,
    provider: p.provider,
    base_url: p.base_url,
    api_key_env: p.api_key_env,
    api_key: "",
    models: p.models.join("\n"),
  };
}

function emptyDraft(): ProfileDraft {
  return {
    id: "",
    label: "",
    provider: "",
    base_url: "",
    api_key_env: "",
    api_key: "",
    models: "",
  };
}

function draftToInput(d: ProfileDraft): LlmProviderProfileInput {
  const models = d.models
    .split("\n")
    .map((line) => line.trim())
    .filter(Boolean);
  const input: LlmProviderProfileInput = {
    id: d.id.trim(),
    base_url: d.base_url.trim(),
  };
  if (d.label.trim()) input.label = d.label.trim();
  if (d.provider) input.provider = d.provider;
  if (d.api_key_env.trim()) input.api_key_env = d.api_key_env.trim();
  if (models.length) input.models = models;
  if (d.api_key) input.api_key = d.api_key;
  return input;
}

function viewToInput(p: LlmProviderProfileView): LlmProviderProfileInput {
  const input: LlmProviderProfileInput = { id: p.id, base_url: p.base_url };
  if (p.label) input.label = p.label;
  if (p.provider) input.provider = p.provider;
  if (p.api_key_env) input.api_key_env = p.api_key_env;
  if (p.models.length) input.models = p.models;
  return input;
}

function keyStatus(p: LlmProviderProfileView): string {
  if (!p.has_key) return `未配置 · ${p.key_env_name}`;
  return p.key_source === "env"
    ? "密钥已配置（环境变量）"
    : "密钥已配置（身份 .env）";
}

export function ProviderProfilesSection({ identityId }: { identityId: string }) {
  const queryClient = useQueryClient();
  const [editingId, setEditingId] = useState<string | null>(null);
  const [draft, setDraft] = useState<ProfileDraft | null>(null);
  const [probeError, setProbeError] = useState<string | null>(null);
  const [confirmDelete, setConfirmDelete] = useState<string | null>(null);

  const { data: view, isLoading } = useQuery({
    queryKey: ["llm-providers", identityId],
    queryFn: () => fetchLlmProviders(identityId),
  });

  const invalidate = () =>
    queryClient.invalidateQueries({ queryKey: ["llm-providers", identityId] });

  const save = useMutation({
    mutationFn: (doc: {
      profiles: LlmProviderProfileInput[];
      tiers: Record<string, LlmTierBinding>;
    }) => saveLlmProviders(identityId, { version: view?.version ?? 1, ...doc }),
    onSuccess: () => {
      toast.success("提供商档案已保存");
      setEditingId(null);
      setDraft(null);
      setProbeError(null);
      invalidate();
    },
    onError: (error: Error) => toast.error(error.message),
  });

  const probe = useMutation({
    mutationFn: (profileId: string) => fetchLlmModels(identityId, profileId, true),
    onSuccess: (result) => {
      if (result.error) {
        setProbeError(result.error);
        return;
      }
      setProbeError(null);
      setDraft((d) => (d ? { ...d, models: result.models.join("\n") } : d));
      toast.success(`已拉取 ${result.models.length} 个模型——保存后生效`);
    },
    onError: (error: Error) => setProbeError(error.message),
  });

  if (isLoading || !view) {
    return (
      <div className="flex justify-center py-10">
        <LoadingDots />
      </div>
    );
  }

  // 写面基线是身份文档的原始档位（identity_tiers）：读面 tiers 是两级
  // 合并结果，拿它回写会把全局档位固化进身份文档。
  const identityTiers = view.identity_tiers;
  const identityProfiles = view.profiles.filter((p) => p.origin === "identity");

  const updateDraft = (patch: Partial<ProfileDraft>) =>
    setDraft((d) => (d ? { ...d, ...patch } : d));

  const submitDraft = () => {
    if (!draft) return;
    const id = draft.id.trim();
    if (!/^[a-z0-9][a-z0-9_-]{0,63}$/.test(id)) {
      toast.error("档案 ID 需小写字母/数字/_/-（首字符为字母或数字）");
      return;
    }
    if (!draft.base_url.trim()) {
      toast.error("Base URL 必填");
      return;
    }
    let profiles: LlmProviderProfileInput[];
    if (editingId === NEW_PROFILE) {
      profiles = [...identityProfiles.map(viewToInput), draftToInput(draft)];
    } else {
      profiles = identityProfiles.map((p) =>
        p.id === editingId ? draftToInput(draft) : viewToInput(p)
      );
    }
    save.mutate({ profiles, tiers: identityTiers });
  };

  const deleteProfile = (id: string) => {
    const profiles = identityProfiles
      .filter((p) => p.id !== id)
      .map(viewToInput);
    const nextTiers = Object.fromEntries(
      Object.entries(identityTiers).filter(([, binding]) => binding.profile !== id)
    ) as Record<string, LlmTierBinding>;
    save.mutate({ profiles, tiers: nextTiers });
  };

  return (
    <section className="mb-8">
      <div className="mb-2 flex items-baseline gap-3">
        <h2 className="font-mono text-xs font-medium uppercase tracking-wider text-muted-foreground">
          提供商档案
        </h2>
        <span className="text-[11px] text-muted-foreground">
          providers.json 管连接与模型清单；密钥只存身份 .env（"API Key"
          输入后保存时自动分流）。运行中的思考者保留启动时的配置——修改后需重启思考者。
        </span>
      </div>

      {view.error && (
        <p
          role="alert"
          className="mb-2 rounded-md border border-destructive/40 bg-destructive/5 px-3 py-2 text-xs text-destructive"
        >
          无法读取现有配置：{view.error}
        </p>
      )}

      <div className="rounded-lg border">
        {view.profiles.length === 0 && (
          <p className="px-3 py-6 text-center text-sm text-muted-foreground">
            还没有提供商档案。添加一个——下方模型档位的候选就来自档案的模型清单。
          </p>
        )}
        {view.profiles.map((p) => (
          <div
            key={p.id}
            className="flex flex-wrap items-center gap-3 border-b px-3 py-2 last:border-b-0"
          >
            <div className="min-w-0 flex-1">
              <div className="flex flex-wrap items-center gap-2">
                <span className="text-sm font-medium">{p.label || p.id}</span>
                <span className="font-mono text-xs text-muted-foreground">
                  {p.id}
                </span>
                <Badge variant="outline" className="text-[10px]">
                  {p.origin === "identity" ? "身份级" : "全局级"}
                </Badge>
              </div>
              <div className="flex flex-wrap items-center gap-2 font-mono text-xs text-muted-foreground">
                <span>{p.base_url}</span>
                <span>· {p.models.length} 个模型</span>
                <span>· {p.provider || "自动推断"}</span>
              </div>
              <span className="text-xs text-muted-foreground">
                {keyStatus(p)}
              </span>
            </div>
            {p.origin === "identity" ? (
              <div className="flex gap-1">
                <Button
                  variant="ghost"
                  size="sm"
                  aria-label={`编辑 ${p.id}`}
                  onClick={() => {
                    setEditingId(p.id);
                    setDraft(draftFromProfile(p));
                    setProbeError(null);
                  }}
                >
                  编辑
                </Button>
                <Button
                  variant="ghost"
                  size="sm"
                  aria-label={`删除 ${p.id}`}
                  onClick={() => setConfirmDelete(p.id)}
                >
                  删除
                </Button>
              </div>
            ) : (
              <span className="text-[11px] text-muted-foreground">
                在全局 providers.json 中管理
              </span>
            )}
          </div>
        ))}
      </div>

      {editingId === null ? (
        <div className="mt-3">
          <Button
            variant="outline"
            size="sm"
            onClick={() => {
              setEditingId(NEW_PROFILE);
              setDraft(emptyDraft());
              setProbeError(null);
            }}
          >
            <Plus className="size-3" />
            添加档案
          </Button>
        </div>
      ) : (
        draft && (
          <form
            className="mt-3 flex flex-col gap-3 rounded-lg border p-3"
            onSubmit={(event) => {
              event.preventDefault();
              submitDraft();
            }}
          >
            <div className="grid grid-cols-2 gap-3">
              <label className="flex flex-col gap-1 text-xs text-muted-foreground">
                ID
                <Input
                  value={draft.id}
                  disabled={editingId !== NEW_PROFILE}
                  onChange={(event) => updateDraft({ id: event.target.value })}
                  placeholder="glm"
                  pattern="[a-z0-9][a-z0-9_-]*"
                  title="小写字母/数字/_/-，首字符为字母或数字"
                  className="h-8 font-mono text-xs"
                />
              </label>
              <label className="flex flex-col gap-1 text-xs text-muted-foreground">
                名称
                <Input
                  value={draft.label}
                  onChange={(event) => updateDraft({ label: event.target.value })}
                  placeholder="智谱"
                  className="h-8 text-xs"
                />
              </label>
              <label className="flex flex-col gap-1 text-xs text-muted-foreground">
                供应商
                <select
                  value={draft.provider}
                  onChange={(event) =>
                    updateDraft({ provider: event.target.value })
                  }
                  className="h-8 rounded-md border bg-transparent px-2 text-xs"
                >
                  <option value="">自动推断（按模型名）</option>
                  <option value="openai-compatible">openai-compatible</option>
                  <option value="anthropic">anthropic</option>
                  <option value="echo">echo</option>
                </select>
              </label>
              <label className="flex flex-col gap-1 text-xs text-muted-foreground">
                Base URL
                <Input
                  value={draft.base_url}
                  onChange={(event) =>
                    updateDraft({ base_url: event.target.value })
                  }
                  placeholder="https://open.bigmodel.cn/api/paas/v4"
                  className="h-8 font-mono text-xs"
                />
              </label>
              <label className="flex flex-col gap-1 text-xs text-muted-foreground">
                密钥引用（api_key_env）
                <Input
                  value={draft.api_key_env}
                  onChange={(event) =>
                    updateDraft({ api_key_env: event.target.value })
                  }
                  placeholder="留空 = 约定键 MINDLOOP_PROFILE_<ID>_API_KEY"
                  className="h-8 font-mono text-xs"
                />
              </label>
              <label className="flex flex-col gap-1 text-xs text-muted-foreground">
                API Key
                <Input
                  type="password"
                  autoComplete="off"
                  value={draft.api_key}
                  onChange={(event) =>
                    updateDraft({ api_key: event.target.value })
                  }
                  placeholder="保存时写入身份 .env；留空不改动"
                  className="h-8 font-mono text-xs"
                />
              </label>
            </div>
            <label className="flex flex-col gap-1 text-xs text-muted-foreground">
              模型清单（每行一个）
              <textarea
                value={draft.models}
                onChange={(event) => updateDraft({ models: event.target.value })}
                rows={4}
                placeholder={"glm-4.6\nglm-4.5-air"}
                className="rounded-md border bg-transparent px-2 py-1.5 font-mono text-xs"
              />
            </label>
            {probeError && (
              <p role="alert" className="text-xs text-destructive">
                拉取失败：{probeError}
              </p>
            )}
            <div className="flex items-center gap-2">
              <Button
                type="button"
                variant="outline"
                size="sm"
                disabled={probe.isPending || !draft.id.trim()}
                onClick={() => probe.mutate(draft.id.trim())}
              >
                {probe.isPending ? (
                  <LoadingDots text="拉取中" />
                ) : (
                  <>
                    <RefreshCw className="size-3" />
                    从提供商拉取
                  </>
                )}
              </Button>
              <Button
                type="submit"
                size="sm"
                disabled={save.isPending || !draft.id.trim() || !draft.base_url.trim()}
              >
                保存
              </Button>
              <Button
                type="button"
                variant="ghost"
                size="sm"
                onClick={() => {
                  setEditingId(null);
                  setDraft(null);
                  setProbeError(null);
                }}
              >
                取消
              </Button>
            </div>
          </form>
        )
      )}

      <ConfirmDialog
        open={confirmDelete !== null}
        onOpenChange={(open) => {
          if (!open) setConfirmDelete(null);
        }}
        tone="danger"
        title={`删除档案 ${confirmDelete ?? ""}？`}
        description="将从身份级 providers.json 移除该档案；引用它的身份级档位绑定一并移除，对应密钥键会从身份 .env 清理。"
        confirmText="删除"
        onConfirm={() => {
          if (confirmDelete) deleteProfile(confirmDelete);
          setConfirmDelete(null);
        }}
      />
    </section>
  );
}
