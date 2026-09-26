// model-config.tsx 模型档位区：三档（主/请求/摘要）各一行 =
// 档案选择 + 模型选择。绑定写入身份 providers.json 的 tiers（候选 =
// 档案的 models 清单；清单为空时回落常用建议）。显式环境变量
// （MINDLOOP_MODEL 等）优先于 JSON 绑定——被覆盖的行显示警告并提供
// 身份级清理（清理后回落到 providers.json 绑定）。
//
// 写语义（关键）：PUT 的 tiers 基线是 view.identity_tiers（身份文档
// 原始档位）——读面 tiers 是两级合并结果，拿它回写会把全局档位固化
// 进身份文档；profiles 只提交 origin=identity 的行。

import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Info, RotateCcw } from "lucide-react";
import { useMemo, useState } from "react";
import { toast } from "sonner";

import { Badge } from "~/components/ui/badge";
import { Button } from "~/components/ui/button";
import { Input } from "~/components/ui/input";
import { LoadingDots } from "~/components/ui/loading-dots";
import {
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from "~/components/ui/tooltip";
import { useControlsEnabled } from "~/components/thinker-controls";
import {
  deleteEnvVar,
  fetchLlmConfig,
  fetchLlmProviders,
  fetchOpenRouterModels,
  saveLlmProviders,
} from "~/lib/api";
import type {
  EnvEntry,
  IdentityEnv,
  LlmProviderProfileInput,
  LlmProviderProfileView,
  LlmTierBinding,
  LlmTierResolved,
} from "~/lib/types";

/* 三档旋钮语义对齐 providers.json（tiers）与 .env 旧键：env 键非空
 * 时优先于 JSON 绑定，与 CLI 解析器（config.ResolveTier）一致。 */
const TIER_KNOBS: { tier: string; envKey: string; label: string; tip: string }[] = [
  {
    tier: "think",
    envKey: "MINDLOOP_MODEL",
    label: "主模型",
    tip: "所有模型调用的默认：monolith 行动、responder 回复等。绑定档案走 providers.json；显式环境变量 MINDLOOP_MODEL 优先。",
  },
  {
    tier: "request",
    envKey: "MINDLOOP_REQUEST_MODEL",
    label: "请求档",
    tip: "反应式唤醒（人类来话、外部产物）用的模型；自发的空闲唤醒仍走主模型。未绑定或不可用时自动回落主模型。",
  },
  {
    tier: "summary",
    envKey: "MINDLOOP_SUMMARY_MODEL",
    label: "摘要档",
    tip: "recap 摘要与记忆归纳用的模型；未绑定或不可用时自动回落主模型。",
  },
];

/* 常用建议——档案未列模型清单时的兜底候选；任何模型都能经
 * "自定义"手动输入。 */
const MODEL_OPTIONS: { group: string; models: string[] }[] = [
  {
    group: "智谱（glm-* 自动端点）",
    models: ["glm-5", "glm-4.5-air", "glm-4-flash"],
  },
  {
    group: "Anthropic（claude-* 自动端点）",
    models: ["claude-sonnet-4-5", "claude-haiku-4-5"],
  },
  {
    group: "OpenRouter（vendor/model 形式）",
    models: [
      "openai/gpt-oss-120b",
      "openai/gpt-oss-20b",
      "anthropic/claude-sonnet-4.5",
      "google/gemini-2.5-flash",
    ],
  },
  {
    group: "本地占位（不联网，体验流程用）",
    models: ["echo"],
  },
];

const CUSTOM = "__custom__";
const OPENROUTER_DATALIST_ID = "openrouter-model-ids";

/** 档案视图 → PUT 输入（与 provider-profiles.tsx 的同名助手对齐：
 * 两处都只提交身份级档案，全局档案不回写）。 */
function viewToInput(p: LlmProviderProfileView): LlmProviderProfileInput {
  const input: LlmProviderProfileInput = { id: p.id, base_url: p.base_url };
  if (p.label) input.label = p.label;
  if (p.provider) input.provider = p.provider;
  if (p.api_key_env) input.api_key_env = p.api_key_env;
  if (p.models.length) input.models = p.models;
  return input;
}

function modelOptionLabel(model: { id: string }): string {
  return model.id;
}

function TierRow({
  knob,
  profiles,
  binding,
  inherited,
  resolved,
  identityEnvEntry,
  controlsEnabled,
  savePending,
  onBind,
  onClear,
  onCleanupEnv,
}: {
  knob: (typeof TIER_KNOBS)[number];
  profiles: LlmProviderProfileView[];
  binding?: LlmTierBinding;
  inherited?: LlmTierBinding;
  resolved?: LlmTierResolved;
  identityEnvEntry?: EnvEntry;
  controlsEnabled: boolean;
  savePending: boolean;
  onBind: (tier: string, binding: LlmTierBinding) => void;
  onClear: (tier: string) => void;
  onCleanupEnv: (key: string) => void;
}) {
  const [profileDraft, setProfileDraft] = useState<string | null>(null);
  const [customOpen, setCustomOpen] = useState(false);
  const [customDraft, setCustomDraft] = useState("");

  const profileId = profileDraft ?? binding?.profile ?? "";
  const profile = profiles.find((p) => p.id === profileId);
  // 候选：档案 models 清单；清单为空才回落常用建议（兜底）。
  const fallback = profile !== undefined && profile.models.length === 0;
  const groups =
    profile === undefined
      ? []
      : fallback
        ? MODEL_OPTIONS
        : [
            {
              group: `${profile.label || profile.id}（档案清单）`,
              models: profile.models,
            },
          ];
  const modelInCandidates =
    binding !== undefined &&
    binding.profile === profileId &&
    groups.some((g) => g.models.includes(binding.model));
  const currentModel = binding?.profile === profileId ? binding.model : "";

  return (
    <div className="flex flex-wrap items-center gap-2 border-b px-3 py-2 last:border-b-0">
      <div className="flex w-52 items-center gap-1.5">
        <span className="text-xs font-medium">{knob.label}</span>
        <span className="font-mono text-[10px] text-muted-foreground">
          {knob.envKey}
        </span>
        <Tooltip>
          <TooltipTrigger asChild>
            <Info className="size-3 shrink-0 cursor-help text-muted-foreground" />
          </TooltipTrigger>
          <TooltipContent className="max-w-xs text-xs">
            <b>{knob.label}</b>——{knob.tip}
          </TooltipContent>
        </Tooltip>
      </div>

      <div className="flex min-w-0 flex-1 flex-wrap items-center gap-2">
        <select
          aria-label={`${knob.label} 档案选择`}
          disabled={!controlsEnabled || savePending}
          value={profileId}
          onChange={(event) => {
            setProfileDraft(event.target.value);
            setCustomOpen(false);
          }}
          className="h-8 rounded-md border bg-transparent px-2 font-mono text-xs"
        >
          <option value="">（不绑定）</option>
          {profiles.map((p) => (
            <option key={p.id} value={p.id}>
              {p.label || p.id}
              {p.origin === "global" ? "（全局）" : ""}
            </option>
          ))}
        </select>

        {customOpen ? (
          <form
            className="flex items-center gap-2"
            onSubmit={(event) => {
              event.preventDefault();
              if (customDraft.trim() && profile) {
                onBind(knob.tier, {
                  profile: profile.id,
                  model: customDraft.trim(),
                });
                setCustomOpen(false);
                setCustomDraft("");
              }
            }}
          >
            <Input
              autoFocus
              aria-label={`${knob.label} 自定义模型`}
              value={customDraft}
              onChange={(event) => setCustomDraft(event.target.value)}
              placeholder="模型 id，如 glm-4.6"
              list={OPENROUTER_DATALIST_ID}
              className="h-8 w-56 font-mono text-xs"
            />
            <Button type="submit" size="sm" disabled={savePending}>
              保存
            </Button>
            <Button
              type="button"
              variant="ghost"
              size="sm"
              onClick={() => {
                setCustomOpen(false);
                setCustomDraft("");
              }}
            >
              取消
            </Button>
          </form>
        ) : (
          <select
            aria-label={`${knob.label} 模型选择`}
            disabled={!controlsEnabled || !profile || savePending}
            value={modelInCandidates ? currentModel : ""}
            onChange={(event) => {
              const value = event.target.value;
              if (value === CUSTOM) {
                setCustomOpen(true);
                setCustomDraft(currentModel);
                return;
              }
              if (value && profile) {
                onBind(knob.tier, { profile: profile.id, model: value });
              }
            }}
            className="h-8 min-w-56 rounded-md border bg-transparent px-2 font-mono text-xs"
          >
            <option value="" disabled>
              {profile ? "选择模型…" : "绑定档案后可选模型"}
            </option>
            {groups.map((group) => (
              <optgroup key={group.group} label={group.group}>
                {group.models.map((model) => (
                  <option key={model} value={model}>
                    {model}
                  </option>
                ))}
              </optgroup>
            ))}
            {profile && <option value={CUSTOM}>自定义…</option>}
          </select>
        )}

        {fallback && (
          <span className="text-[10px] text-muted-foreground">
            档案未列模型清单——显示常用建议
          </span>
        )}
        {!binding && inherited && (
          <span className="text-[10px] text-muted-foreground">
            继承全局绑定：{inherited.profile} / {inherited.model}
          </span>
        )}

        {resolved?.error ? (
          <span className="text-xs text-destructive">
            配置错误：{resolved.error}
          </span>
        ) : resolved?.source === "env" ? (
          <>
            <Tooltip>
              <TooltipTrigger asChild>
                <Badge variant="destructive" className="text-[10px]">
                  env 覆盖中
                </Badge>
              </TooltipTrigger>
              <TooltipContent className="max-w-xs text-xs">
                显式环境变量 {knob.envKey} 优先于 providers.json 绑定——
                {identityEnvEntry
                  ? "清除身份级覆盖后回落到 JSON 绑定。"
                  : "由进程环境或服务根 .env 提供，请在对应处移除。"}
              </TooltipContent>
            </Tooltip>
            <span className="font-mono text-[10px] text-muted-foreground">
              生效 {resolved.model}
            </span>
            {identityEnvEntry && (
              <Tooltip>
                <TooltipTrigger asChild>
                  <Button
                    variant="ghost"
                    size="icon-sm"
                    aria-label={`清理覆盖 ${knob.label}`}
                    disabled={savePending}
                    onClick={() => onCleanupEnv(knob.envKey)}
                  >
                    <RotateCcw className="size-3" />
                  </Button>
                </TooltipTrigger>
                <TooltipContent className="text-xs">
                  清除身份级 {knob.envKey}——回落到 providers.json 绑定
                </TooltipContent>
              </Tooltip>
            )}
          </>
        ) : resolved?.source === "providers.json" ? (
          <>
            <Badge variant="outline" className="text-[10px]">
              providers.json
            </Badge>
            <span className="font-mono text-[10px] text-muted-foreground">
              生效 {resolved.model}
            </span>
          </>
        ) : (
          <span className="text-[10px] text-muted-foreground">未配置</span>
        )}

        {binding && (
          <Tooltip>
            <TooltipTrigger asChild>
              <Button
                variant="ghost"
                size="sm"
                aria-label={`清除绑定 ${knob.label}`}
                disabled={savePending}
                onClick={() => onClear(knob.tier)}
              >
                清除绑定
              </Button>
            </TooltipTrigger>
            <TooltipContent className="text-xs">
              移除身份级 providers.json 的 {knob.tier} 档位条目
              {inherited ? `——回落到全局绑定（${inherited.profile}）` : ""}
            </TooltipContent>
          </Tooltip>
        )}
      </div>
    </div>
  );
}

/** 模型档位区：档位绑定写入身份 providers.json（与「提供商档案」区
 * 同一份文档、同一个 PUT 端点）。 */
export function ModelConfigSection({
  identityId,
  env,
}: {
  identityId: string;
  env: IdentityEnv;
}) {
  const controlsEnabled = useControlsEnabled();
  const queryClient = useQueryClient();

  const { data: view } = useQuery({
    queryKey: ["llm-providers", identityId],
    queryFn: () => fetchLlmProviders(identityId),
  });
  const { data: config } = useQuery({
    queryKey: ["llm-config", identityId],
    queryFn: () => fetchLlmConfig(identityId),
  });
  const { data: catalog } = useQuery({
    queryKey: ["openrouter-models"],
    queryFn: fetchOpenRouterModels,
    staleTime: 10 * 60 * 1000,
    retry: 1,
  });

  const invalidate = () => {
    queryClient.invalidateQueries({ queryKey: ["llm-providers", identityId] });
    queryClient.invalidateQueries({ queryKey: ["llm-config", identityId] });
    queryClient.invalidateQueries({ queryKey: ["env", identityId] });
  };

  // 写面基线是身份级原始档位：读面 tiers 是合并结果，不回写。
  const save = useMutation({
    mutationFn: (nextTiers: Record<string, LlmTierBinding>) =>
      saveLlmProviders(identityId, {
        version: view?.version ?? 1,
        profiles: (view?.profiles ?? [])
          .filter((p) => p.origin === "identity")
          .map(viewToInput),
        tiers: nextTiers,
      }),
    onSuccess: () => {
      toast.success("档位绑定已保存——重启思考者后生效");
      invalidate();
    },
    onError: (error: Error) => toast.error(error.message),
  });

  const cleanup = useMutation({
    mutationFn: (key: string) => deleteEnvVar(identityId, key),
    onSuccess: (entry) => {
      toast.success(`已清除 ${entry.key}`);
      invalidate();
    },
    onError: (error: Error) => toast.error(error.message),
  });

  const datalist = useMemo(
    () =>
      (catalog?.models ?? []).map((model) => (
        <option key={model.id} value={model.id} label={modelOptionLabel(model)} />
      )),
    [catalog]
  );

  if (!view) {
    return (
      <section className="mb-8">
        <div className="flex justify-center py-10">
          <LoadingDots />
        </div>
      </section>
    );
  }

  const identityTiers = view.identity_tiers;
  const catalogNote = catalog?.error
    ? "OpenRouter 目录不可达——自定义输入仍可用。"
    : catalog
      ? `自定义输入带 OpenRouter 公共目录 ${catalog.count} 个模型的自动补全。`
      : null;

  return (
    <section className="mb-8">
      <div className="mb-2 flex items-baseline gap-3">
        <h2 className="font-mono text-xs font-medium uppercase tracking-wider text-muted-foreground">
          模型档位
        </h2>
        <span className="text-[11px] text-muted-foreground">
          档位绑定写入身份 providers.json（候选来自「提供商档案」的模型清单）；
          显式环境变量（MINDLOOP_MODEL 等）优先。运行中的思考者保留启动时的配置——修改后需重启思考者。
          {catalogNote ? ` ${catalogNote}` : ""}
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
        {TIER_KNOBS.map((knob) => (
          <TierRow
            key={knob.tier}
            knob={knob}
            profiles={view.profiles}
            binding={identityTiers[knob.tier]}
            inherited={
              identityTiers[knob.tier] ? undefined : view.tiers[knob.tier]
            }
            resolved={config?.tiers[knob.tier as "think" | "request" | "summary"]}
            identityEnvEntry={env.env.find((e) => e.key === knob.envKey)}
            controlsEnabled={controlsEnabled}
            savePending={save.isPending || cleanup.isPending}
            onBind={(tier, binding) =>
              save.mutate({ ...identityTiers, [tier]: binding })
            }
            onClear={(tier) => {
              const next = { ...identityTiers };
              delete next[tier];
              save.mutate(next);
            }}
            onCleanupEnv={(key) => cleanup.mutate(key)}
          />
        ))}
      </div>
      <datalist id={OPENROUTER_DATALIST_ID}>{datalist}</datalist>
    </section>
  );
}
