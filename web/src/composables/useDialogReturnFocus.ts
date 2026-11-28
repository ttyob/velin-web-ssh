import { watch } from 'vue';

// Restore the exact triggering control without scrolling its parent list.
export function useDialogReturnFocus(open: () => boolean) {
  let trigger: HTMLElement | undefined;
  watch(open, value => {
    if (value && document.activeElement instanceof HTMLElement && document.activeElement !== document.body) trigger = document.activeElement;
  }, { flush: 'sync', immediate: true });
  function restoreFocus(event?: Event) {
    if (!trigger?.isConnected || !trigger.getClientRects().length || trigger.matches(':disabled')) return;
    event?.preventDefault();
    trigger.focus({ preventScroll: true });
  }
  return { restoreFocus };
}
