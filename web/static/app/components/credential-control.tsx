import { useQueryClient } from "@tanstack/react-query";
import { KeyRound } from "lucide-react";
import { useId, useState, useSyncExternalStore } from "react";
import {
  AlertDialog,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "~/components/ui/alert-dialog";
import { Button } from "~/components/ui/button";
import { Input } from "~/components/ui/input";
import { authRequired, setWebToken, subscribeAuth } from "~/lib/api";

export function CredentialControl() {
  const required = useSyncExternalStore(subscribeAuth, authRequired, () => false);
  const [open, setOpen] = useState(false);
  const [draft, setDraft] = useState("");
  const inputId = useId();
  const client = useQueryClient();
  const save = (token: string) => {
    setWebToken(token);
    setDraft("");
    setOpen(false);
    // 仅恢复查询；不重放曾被拒绝的消息、上传或控制操作。
    void client.resetQueries();
  };

  return (
    <div className="flex flex-wrap items-center justify-end gap-2">
      {required && <span role="alert" className="text-xs text-destructive">请更新访问凭据</span>}
      <Button variant="ghost" size="sm" aria-label="访问凭据" onClick={() => { setDraft(""); setOpen(true); }}>
        <KeyRound className="size-3" />
        访问凭据
      </Button>
      <AlertDialog open={open} onOpenChange={(next) => { setOpen(next); if (!next) setDraft(""); }}>
        <AlertDialogContent>
          <form className="space-y-4" onSubmit={(event) => { event.preventDefault(); if (draft.trim()) save(draft); }}>
            <AlertDialogHeader>
              <AlertDialogTitle>更新 Web 访问凭据</AlertDialogTitle>
              <AlertDialogDescription>
                输入 mindloop web 的访问 Token，不是模型 API Key。凭据仅保存在当前浏览器；禁止本地存储时仅在本次会话有效。
              </AlertDialogDescription>
            </AlertDialogHeader>
            <div className="space-y-2">
              <label htmlFor={inputId} className="text-sm font-medium">Web 访问 Token</label>
              <Input id={inputId} type="password" autoComplete="off" value={draft} onChange={(event) => setDraft(event.target.value)} />
            </div>
            <AlertDialogFooter>
              <Button type="button" variant="ghost" onClick={() => save("")}>清除凭据</Button>
              <AlertDialogCancel type="button">取消</AlertDialogCancel>
              <Button type="submit" disabled={!draft.trim()}>保存并重新连接</Button>
            </AlertDialogFooter>
          </form>
        </AlertDialogContent>
      </AlertDialog>
    </div>
  );
}
