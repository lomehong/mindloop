import { Bell, BellOff, BellRing } from "lucide-react";
import { useEffect, useState } from "react";
import { toast } from "sonner";

import { fetchPushKey, subscribePush, unsubscribePush } from "~/lib/api";

type PushState = "unsupported" | "loading" | "off" | "on" | "denied";

function applicationServerKey(b64url: string): ArrayBuffer {
  const padded = b64url.padEnd(b64url.length + ((4 - (b64url.length % 4)) % 4), "=");
  const raw = atob(padded.replace(/-/g, "+").replace(/_/g, "/"));
  const buffer = new ArrayBuffer(raw.length);
  const view = new Uint8Array(buffer);
  for (let i = 0; i < raw.length; i++) view[i] = raw.charCodeAt(i);
  return buffer;
}

function supported(): boolean {
  return (
    "serviceWorker" in navigator &&
    "PushManager" in window &&
    "Notification" in window
  );
}

/** Bell in the chat header: tap to enable/disable push notifications for
 * this device. Hidden where push can't work (e.g. iOS outside the
 * installed app). The tap doubles as the user gesture iOS requires for
 * the permission prompt. */
export function PushBell({ name }: { name: string }) {
  const [state, setState] = useState<PushState>("loading");

  useEffect(() => {
    let cancelled = false;
    (async () => {
      if (!supported()) return setState("unsupported");
      if (Notification.permission === "denied") return setState("denied");
      const reg = await navigator.serviceWorker.ready;
      const sub = await reg.pushManager.getSubscription();
      if (sub) {
        // Re-register on every launch: upsert is idempotent, and it heals
        // the box after a prune, rebuild, or subscription rotation.
        subscribePush(name, sub.toJSON()).catch(() => {});
      }
      if (!cancelled) setState(sub ? "on" : "off");
    })().catch(() => !cancelled && setState("unsupported"));
    return () => {
      cancelled = true;
    };
  }, [name]);

  const enable = async () => {
    setState("loading");
    try {
      const permission = await Notification.requestPermission();
      if (permission !== "granted") {
        setState(permission === "denied" ? "denied" : "off");
        if (permission === "denied") {
          toast.error("Notifications are blocked for this app in system settings");
        }
        return;
      }
      const reg = await navigator.serviceWorker.ready;
      const { key } = await fetchPushKey();
      const sub = await reg.pushManager.subscribe({
        userVisibleOnly: true,
        applicationServerKey: applicationServerKey(key),
      });
      await subscribePush(name, sub.toJSON());
      setState("on");
      toast.success("This phone will buzz when someone messages you back");
    } catch (error) {
      setState("off");
      toast.error((error as Error).message);
    }
  };

  const disable = async () => {
    setState("loading");
    try {
      const reg = await navigator.serviceWorker.ready;
      const sub = await reg.pushManager.getSubscription();
      if (sub) {
        await unsubscribePush(sub.endpoint);
        await sub.unsubscribe();
      }
      setState("off");
    } catch (error) {
      setState("on");
      toast.error((error as Error).message);
    }
  };

  if (state === "unsupported") return null;
  const icon =
    state === "on" ? (
      <BellRing className="size-4" />
    ) : state === "denied" ? (
      <BellOff className="size-4 opacity-40" />
    ) : (
      <Bell className="size-4 opacity-60" />
    );
  return (
    <button
      type="button"
      className="flex h-9 w-9 items-center justify-center rounded-full active:bg-accent disabled:opacity-40"
      disabled={state === "loading" || state === "denied"}
      title={
        state === "on"
          ? "Notifications on — tap to disable"
          : state === "denied"
            ? "Notifications blocked in system settings"
            : "Notify this phone when you get a reply"
      }
      aria-label="Toggle push notifications"
      onClick={state === "on" ? disable : enable}
    >
      {icon}
    </button>
  );
}
