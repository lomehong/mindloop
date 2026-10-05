// 新建任务对话框：「新的任务」的唯一实现（左栏入口与身份任务页共用）。
// 选择身份、写下要求，经与 PWA 同一份任务 API 提交——带幂等键（失败
// 重发复用同键，服务端返回原任务而不是再落一份）；成功后跳该身份的
// 任务面看真实状态，不做乐观推断。

import * as DialogPrimitive from "@radix-ui/react-dialog";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useEffect, useRef, useState } from "react";
import { useNavigate } from "react-router";
import { toast } from "sonner";

import { Button } from "~/components/ui/button";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "~/components/ui/select";
import { Textarea } from "~/components/ui/textarea";
import { fetchIdentities, submitTask } from "~/lib/api";
import { newClientMessageId } from "~/lib/use-chat";

export function NewTaskDialog({
  open,
  onOpenChange,
  defaultIdentityId,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  /** 预选身份（身份任务页传自身份；左栏缺省选运行中的一个）。 */
  defaultIdentityId?: string;
}) {
  const navigate = useNavigate();
  const queryClient = useQueryClient();
  const [content, setContent] = useState("");
  const [picked, setPicked] = useState<string | null>(null);
  const submitRef = useRef<{ content: string; clientMessageId: string } | null>(
    null
  );

  const { data: identities } = useQuery({
    queryKey: ["identities"],
    queryFn: fetchIdentities,
    enabled: open,
  });
  const list = identities ?? [];
  const selected =
    picked ??
    defaultIdentityId ??
    list.find((it) => it.live)?.id ??
    list[0]?.id ??
    "";

  // 每次打开重置：空白草稿 + 清幂等键（上一单的键不跨单复用）。
  useEffect(() => {
    if (open) {
      setContent("");
      setPicked(null);
      submitRef.current = null;
    }
  }, [open]);

  const mutation = useMutation({
    mutationFn: (input: {
      identityId: string;
      content: string;
      clientMessageId: string;
    }) =>
      submitTask(input.identityId, {
        content: input.content,
        clientMessageId: input.clientMessageId,
      }),
    onSuccess: (task) => {
      submitRef.current = null;
      onOpenChange(false);
      const name =
        list.find((it) => it.id === task.identity_id)?.name ?? task.identity_id;
      toast.success(`已交给 ${name} 执行`);
      void queryClient.invalidateQueries({
        queryKey: ["tasks", task.identity_id],
      });
      navigate(`/i/${encodeURIComponent(task.identity_id)}/tasks`);
    },
    onError: (error: Error) => toast.error(error.message),
  });

  const submit = () => {
    const trimmed = content.trim();
    if (!trimmed || !selected || mutation.isPending) return;
    const prev = submitRef.current;
    const clientMessageId =
      prev && prev.content === trimmed
        ? prev.clientMessageId
        : newClientMessageId();
    submitRef.current = { content: trimmed, clientMessageId };
    mutation.mutate({ identityId: selected, content: trimmed, clientMessageId });
  };

  return (
    <DialogPrimitive.Root open={open} onOpenChange={onOpenChange}>
      <DialogPrimitive.Portal>
        <DialogPrimitive.Overlay className="fixed inset-0 z-50 bg-black/45 transition-opacity data-[state=closed]:opacity-0 data-[state=open]:opacity-100" />
        <DialogPrimitive.Content className="fixed left-1/2 top-1/2 z-50 w-[min(92vw,30rem)] -translate-x-1/2 -translate-y-1/2 rounded-2xl border border-border bg-card p-5 shadow-xl outline-none transition-all data-[state=closed]:scale-[0.98] data-[state=closed]:opacity-0 data-[state=open]:scale-100 data-[state=open]:opacity-100">
          <DialogPrimitive.Title className="text-[15px] font-semibold">
            新的任务
          </DialogPrimitive.Title>
          <DialogPrimitive.Description className="mt-1 text-xs text-muted-foreground">
            作为显式委托交给某个身份执行——它会按任务推进，过程与结果留底在它的轨迹里。
          </DialogPrimitive.Description>
          {list.length === 0 ? (
            <div className="py-8 text-center text-sm text-muted-foreground">
              还没有身份。先去工作台新建一个，再回来派任务。
            </div>
          ) : (
            <div className="mt-4 space-y-3">
              <div className="flex items-center gap-2.5">
                <span className="shrink-0 text-xs text-muted-foreground">
                  交给
                </span>
                <Select value={selected} onValueChange={setPicked}>
                  <SelectTrigger size="sm" className="min-w-44">
                    <SelectValue placeholder="选择身份" />
                  </SelectTrigger>
                  <SelectContent>
                    {list.map((it) => (
                      <SelectItem key={it.id} value={it.id}>
                        {it.name}
                        {it.live ? " · 运行中" : ""}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
                <span className="text-[11px] text-muted-foreground/70">
                  身份会在自己的任务队列里看到这条委托
                </span>
              </div>
              <Textarea
                autoFocus
                rows={4}
                value={content}
                onChange={(event) => setContent(event.target.value)}
                placeholder="写下要它做的事——完整、可执行的任务描述（不是聊天消息）"
                className="max-h-60 min-h-24 resize-none"
                onKeyDown={(event) => {
                  if (
                    (event.metaKey || event.ctrlKey) &&
                    event.key === "Enter"
                  ) {
                    event.preventDefault();
                    submit();
                  }
                }}
              />
            </div>
          )}
          <div className="mt-4 flex justify-end gap-2">
            <Button
              variant="ghost"
              size="sm"
              onClick={() => onOpenChange(false)}
            >
              取消
            </Button>
            <Button
              size="sm"
              disabled={mutation.isPending || !content.trim() || !selected}
              onClick={submit}
            >
              {mutation.isPending ? "提交中…" : "交给它执行"}
            </Button>
          </div>
        </DialogPrimitive.Content>
      </DialogPrimitive.Portal>
    </DialogPrimitive.Root>
  );
}
