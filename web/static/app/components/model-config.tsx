import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Info, RotateCcw } from "lucide-react";
import { useMemo, useState } from "react";
import { toast } from "sonner";

import { Badge } from "~/components/ui/badge";
import { Button } from "~/components/ui/button";
import { Input } from "~/components/ui/input";
import {
  Select,
  SelectContent,
  SelectGroup,
  SelectItem,
  SelectLabel,
  SelectTrigger,
  SelectValue,
} from "~/components/ui/select";
import {
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from "~/components/ui/tooltip";
import { useControlsEnabled } from "~/components/thinker-controls";
import { deleteEnvVar, fetchOpenRouterModels, putEnvVar } from "~/lib/api";
import type { IdentityEnv, OpenRouterModels } from "~/lib/types";

/* 旋钮语义对齐 mindloop 的真实配置面（.env.example）——这些键由
 * mind run / chat 启动时加载的身份 .env 提供值。 */
const MODEL_KNOBS: { key: string; label: string; tip: string }[] = [
  {
    key: "MINDLOOP_MODEL",
    label: "主模型",
    tip: "所有模型调用的默认：monolith 行动、responder 回复、recap 摘要都用它。claude-* 自动走 Anthropic；echo 是本地占位（不联网）；glm-* 自动落到智谱端点，其余走 openai-compatible。",
  },
  {
    key: "MINDLOOP_REQUEST_MODEL",
    label: "请求档模型",
    tip: "反应式唤醒（人类来话、外部产物）用的模型；自发的空闲唤醒仍走主模型——分层后安静日的模型开销可降一大截。未设置或不可用时自动回落主模型。",
  },
];

/* 常用选择——可自由编辑；任何模型都能经"自定义"手动输入。
 * 供应商标识按模型名自动推断（glm-* → 智谱、claude-* → Anthropic）。 */
const MODEL_OPTIONS: { group: string; models: string[] }[] = [
  {
    group: "智谱（直接，glm-* 自动端点）",
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
const ALL_OPTION_VALUES = new Set(
  MODEL_OPTIONS.flatMap((g) => g.models)
);
const OPENROUTER_DATALIST_ID = "openrouter-model-ids";

// 来源徽标的中文文案。
const SOURCE_LABELS: Record<string, string> = {
  identity: "身份级",
  inherited: "继承",
  default: "默认",
};

function ModelRow({
  identityId,
  knob,
  env,
  catalog,
}: {
  identityId: string;
  knob: (typeof MODEL_KNOBS)[number];
  env: IdentityEnv;
  catalog?: OpenRouterModels;
}) {
  const controlsEnabled = useControlsEnabled();
  const queryClient = useQueryClient();
  const invalidate = () =>
    queryClient.invalidateQueries({ queryKey: ["env", identityId] });

  const identityEntry = env.env.find((e) => e.key === knob.key);
  const inheritedEntry = env.inherited.find((e) => e.key === knob.key);
  const effective = identityEntry?.value ?? inheritedEntry?.value ?? "";
  const source = identityEntry
    ? "identity"
    : inheritedEntry
      ? "inherited"
      : "default";

  const [customDraft, setCustomDraft] = useState<string | null>(null);

  // 只有目录是按 key 过滤的列表时才有意义：配置了的 OpenRouter 形
  // 式模型（vendor/name）不在列表里就不可用。
  const unavailable =
    catalog?.source === "key" &&
    effective.includes("/") &&
    !catalog.models.some((m) => m.id === effective);

  const save = useMutation({
    mutationFn: (value: string) => putEnvVar(identityId, knob.key, value),
    onSuccess: (entry) => {
      toast.success(`已保存 ${entry.key}——重启思考者后生效`);
      setCustomDraft(null);
      invalidate();
    },
    onError: (error: Error) => toast.error(error.message),
  });
  const remove = useMutation({
    mutationFn: () => deleteEnvVar(identityId, knob.key),
    onSuccess: () => {
      toast.success(`已清除 ${knob.key}`);
      invalidate();
    },
    onError: (error: Error) => toast.error(error.message),
  });

  return (
    <div className="flex flex-wrap items-center gap-2 border-b px-3 py-2 last:border-b-0">
      <div className="flex w-56 items-center gap-1.5">
        <span className="font-mono text-xs font-medium">{knob.key}</span>
        <Tooltip>
          <TooltipTrigger asChild>
            <Info className="size-3 shrink-0 cursor-help text-muted-foreground" />
          </TooltipTrigger>
          <TooltipContent className="max-w-xs text-xs">
            <b>{knob.label}</b>——{knob.tip}
          </TooltipContent>
        </Tooltip>
      </div>

      <div className="flex min-w-0 flex-1 items-center gap-2">
        {customDraft !== null ? (
          <form
            className="flex flex-1 items-center gap-2"
            onSubmit={(event) => {
              event.preventDefault();
              if (customDraft.trim()) save.mutate(customDraft.trim());
            }}
          >
            <Input
              autoFocus
              value={customDraft}
              onChange={(event) => setCustomDraft(event.target.value)}
              placeholder={
                catalog?.source === "key"
                  ? `输入以搜索此 key 可用的 ${catalog.count} 个模型…`
                  : "vendor/model 或 claude-…"
              }
              list={OPENROUTER_DATALIST_ID}
              className="h-8 flex-1 font-mono text-xs"
            />
            <Button type="submit" size="sm" disabled={save.isPending}>
              保存
            </Button>
            <Button
              type="button"
              variant="ghost"
              size="sm"
              onClick={() => setCustomDraft(null)}
            >
              取消
            </Button>
          </form>
        ) : (
          <>
            <Select
              /* key 在值变化时强制重挂载——否则 Radix 会在 value 回到
               * undefined 时保留上次的选择，清除后显示过期模型。 */
              key={effective}
              disabled={!controlsEnabled || save.isPending}
              value={ALL_OPTION_VALUES.has(effective) ? effective : undefined}
              onValueChange={(value) => {
                if (value === CUSTOM) setCustomDraft(effective);
                else if (value !== effective) save.mutate(value);
              }}
            >
              <SelectTrigger size="sm" className="min-w-56 font-mono text-xs">
                <SelectValue
                  placeholder={
                    effective || "（未设置——用内置默认）"
                  }
                />
              </SelectTrigger>
              <SelectContent>
                {MODEL_OPTIONS.map((group) => (
                  <SelectGroup key={group.group}>
                    <SelectLabel className="text-[11px]">{group.group}</SelectLabel>
                    {group.models
                      // key 过滤目录下，剔除该 key 用不了的 OpenRouter
                      // 条目（vendor/name）。claude-* 不归 OpenRouter 管。
                      .filter(
                        (model) =>
                          catalog?.source !== "key" ||
                          !model.includes("/") ||
                          catalog.models.some((m) => m.id === model)
                      )
                      .map((model) => (
                        <SelectItem
                          key={model}
                          value={model}
                          className="font-mono text-xs"
                        >
                          {model}
                        </SelectItem>
                      ))}
                  </SelectGroup>
                ))}
                <SelectItem value={CUSTOM} className="text-xs">
                  自定义…
                </SelectItem>
              </SelectContent>
            </Select>
            <Badge variant="outline" className="text-[10px]">
              {SOURCE_LABELS[source] ?? source}
            </Badge>
            {unavailable && (
              <Tooltip>
                <TooltipTrigger asChild>
                  <Badge variant="destructive" className="text-[10px]">
                    此 key 不可用
                  </Badge>
                </TooltipTrigger>
                <TooltipContent className="max-w-xs text-xs">
                  此 OpenRouter key 的模型列表里没有 {effective}
                  ——检查模型 id，或组织层的模型/供应商设置。
                </TooltipContent>
              </Tooltip>
            )}
            {controlsEnabled && identityEntry && (
              <Tooltip>
                <TooltipTrigger asChild>
                  <Button
                    variant="ghost"
                    size="icon-sm"
                    disabled={remove.isPending}
                    onClick={() => remove.mutate()}
                  >
                    <RotateCcw className="size-3" />
                  </Button>
                </TooltipTrigger>
                <TooltipContent className="text-xs">
                  清除身份级覆盖——回落到{" "}
                  {inheritedEntry
                    ? `继承值（${inheritedEntry.value}）`
                    : "内置默认"}
                </TooltipContent>
              </Tooltip>
            )}
          </>
        )}
      </div>
    </div>
  );
}

/** 快速模型配置：最常改的模型旋钮 + 常用候选，新身份不必手写
 * 环境变量。写入的正是下方表格编辑的同一份身份 .env。 */
/* datalist 的 label 必须以 id 开头：Firefox 的弹出层只显示 label，
 * 详见原实现注释。 */
function modelOptionLabel(model: OpenRouterModels["models"][number]): string {
  const parts: string[] = [model.id];
  if (model.prompt_usd_per_m != null && model.completion_usd_per_m != null)
    parts.push(
      `$${model.prompt_usd_per_m}/M 入 · $${model.completion_usd_per_m}/M 出`
    );
  if (model.context_length)
    parts.push(`${Math.round(model.context_length / 1000)}k 上下文`);
  return parts.join(" — ");
}

export function ModelConfigSection({
  identityId,
  env,
}: {
  identityId: string;
  env: IdentityEnv;
}) {
  const { data: catalog } = useQuery({
    queryKey: ["openrouter-models"],
    queryFn: fetchOpenRouterModels,
    staleTime: 10 * 60 * 1000,
    retry: 1,
  });

  const datalist = useMemo(
    () =>
      (catalog?.models ?? []).map((model) => (
        <option
          key={model.id}
          value={model.id}
          label={modelOptionLabel(model)}
        />
      )),
    [catalog]
  );

  const catalogNote =
    catalog?.source === "key"
      ? `此 key 可用 ${catalog.count} 个 OpenRouter 模型。`
      : catalog?.source === "public"
        ? `OpenRouter 公共目录共 ${catalog.count} 个模型（未配 key——可用性未校验）。`
        : catalog?.error
          ? "OpenRouter 目录不可达——仍可在“自定义”里手动输入。"
          : null;

  return (
    <section className="mb-8">
      <div className="mb-2 flex items-baseline gap-3">
        <h2 className="font-mono text-xs font-medium uppercase tracking-wider text-muted-foreground">
          模型
        </h2>
        <span className="text-[11px] text-muted-foreground">
          常用模型配置（写入身份 .env，mind run / chat 启动时加载；显式环境变量优先）。
          运行中的思考者保留启动时的环境——修改后需重启思考者。{catalogNote ? ` ${catalogNote}` : ""}
        </span>
      </div>
      <div className="rounded-lg border">
        {MODEL_KNOBS.map((knob) => (
          <ModelRow
            key={knob.key}
            identityId={identityId}
            knob={knob}
            env={env}
            catalog={catalog}
          />
        ))}
      </div>
      <datalist id={OPENROUTER_DATALIST_ID}>{datalist}</datalist>
    </section>
  );
}
