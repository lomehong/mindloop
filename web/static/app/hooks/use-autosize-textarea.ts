import { useEffect, useRef } from "react";

/**
 * Grows a textarea to fit its content (up to CSS max-height, where it
 * switches to internal scrolling) by measuring scrollHeight on each change.
 * Plain CSS `field-sizing: content` would do this alone, but isn't
 * supported on older Safari/iOS, which the chat composers need to target.
 */
export function useAutosizeTextarea(value: string) {
  const ref = useRef<HTMLTextAreaElement>(null);

  useEffect(() => {
    const el = ref.current;
    if (!el) return;
    el.style.height = "auto";
    const styles = window.getComputedStyle(el);
    const borderHeight =
      (Number.parseFloat(styles.borderTopWidth) || 0) +
      (Number.parseFloat(styles.borderBottomWidth) || 0);
    el.style.height = `${el.scrollHeight + borderHeight}px`;
  }, [value]);

  return ref;
}
