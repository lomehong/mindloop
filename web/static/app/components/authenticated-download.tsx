import { useRef, useState } from "react";
import type { ComponentProps } from "react";
import { Button } from "~/components/ui/button";
import { downloadFile } from "~/lib/api";

type Props = Pick<ComponentProps<typeof Button>, "children" | "variant" | "size" | "title" | "className"> & {
  url: string;
  filename: string;
};

/** 所有下载入口共用认证、等待状态及错误展示，不导航到裸 API 地址。 */
export function AuthenticatedDownload({ url, filename, children, ...props }: Props) {
  const busy = useRef(false);
  const [pending, setPending] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const download = async () => {
    if (busy.current) return;
    busy.current = true;
    setPending(true);
    setError(null);
    try {
      await downloadFile(url, filename);
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      busy.current = false;
      setPending(false);
    }
  };
  return (
    <span className="inline-flex flex-wrap items-center gap-2">
      <Button {...props} type="button" disabled={pending} onClick={() => void download()}>
        {pending ? "下载中…" : children}
      </Button>
      {error && <span role="alert" className="text-xs text-destructive">{error}</span>}
    </span>
  );
}
