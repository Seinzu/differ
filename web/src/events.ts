export type ServerEvent =
  | { type: "comments" | "reconnected" }
  | { type: "navigate"; tab: string; query: string };

function newTabId() {
  return Math.random().toString(36).slice(2) + Date.now().toString(36);
}

// Identifies this tab to the server, which sends agents' show_diff requests
// to the tab in use. It survives the reload that navigation causes.
export const tabId = (() => {
  try {
    const saved = sessionStorage.getItem("differ:tab");
    if (saved) return saved;
    const id = newTabId();
    sessionStorage.setItem("differ:tab", id);
    return id;
  } catch {
    return newTabId();
  }
})();

// One event stream per tab, shared by every listener. EventSource reconnects
// by itself; listeners hear "reconnected" so they can catch up.
let source: EventSource | undefined;
const listeners = new Set<(event: ServerEvent) => void>();
export function onServerEvent(listener: (event: ServerEvent) => void) {
  if (!source && typeof EventSource !== "undefined") {
    let opened = false;
    source = new EventSource(
      `/api/events?${new URLSearchParams({ tab: tabId })}`,
    );
    source.onopen = () => {
      if (opened) listeners.forEach((l) => l({ type: "reconnected" }));
      opened = true;
    };
    source.onmessage = (message) => {
      try {
        const event = JSON.parse(message.data) as ServerEvent;
        listeners.forEach((l) => l(event));
      } catch {
        // Ignore events this version does not understand.
      }
    };
  }
  listeners.add(listener);
  return () => void listeners.delete(listener);
}
