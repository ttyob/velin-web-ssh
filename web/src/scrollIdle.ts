// While a list scrolls, the pointer is effectively stationary but the content
// moves under it, so the browser re-hit-tests and re-evaluates :hover (and
// repaints hover backgrounds) for the rows on every frame. Marking the scroller
// as "scrolling" lets CSS switch pointer events off for its descendants until
// scrolling settles.
//
// Removing the class must be hysteretic. Each add/remove restyles the whole
// list, so toggling between discrete wheel ticks (or across a slow frame) costs
// more than it saves and makes the jank self-reinforcing. The class is therefore
// removed only after scrolling has been quiet for a hold period: `scrollend`
// starts a short hold, any further scroll cancels it, and a long safety timer
// covers browsers (or cases) where `scrollend` never arrives.
const SCROLLERS =
  ".resource-list,.web-service-list,.session-dialog-list,.sp-main,.file-manager-list,.docker-list,.el-dialog__body,.git-history-list,.monitor-body";
const SCROLLING = "is-scrolling";
const HOLD_AFTER_END_MS = 150;
const SAFETY_MS = 400;
const timers = new WeakMap<Element, number>();
// Drag-and-drop (e.g. reordering hosts) auto-scrolls the list; rows must stay
// valid drop targets for that whole time, so suppression is skipped while dragging.
let dragging = false;

function scroller(event: Event) {
  const target = event.target;
  return target instanceof Element && target.matches(SCROLLERS) ? target : null;
}

function releaseIn(target: Element, delay: number) {
  window.clearTimeout(timers.get(target));
  timers.set(
    target,
    window.setTimeout(() => target.classList.remove(SCROLLING), delay),
  );
}

export function installScrollIdle() {
  const setDragging = (value: boolean) => () => {
    dragging = value;
  };
  document.addEventListener("dragstart", setDragging(true), true);
  document.addEventListener("dragend", setDragging(false), true);
  document.addEventListener("drop", setDragging(false), true);
  document.addEventListener(
    "scroll",
    (event) => {
      const target = scroller(event);
      if (!target || dragging) return;
      if (!target.classList.contains(SCROLLING)) target.classList.add(SCROLLING);
      releaseIn(target, SAFETY_MS);
    },
    { capture: true, passive: true },
  );
  if ("onscrollend" in window)
    document.addEventListener(
      "scrollend",
      (event) => {
        const target = scroller(event);
        if (target) releaseIn(target, HOLD_AFTER_END_MS);
      },
      { capture: true, passive: true },
    );
}
