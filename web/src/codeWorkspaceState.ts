import type { CodeLanguageId } from "./codeLanguageDetection";
import type { EditorIndent } from "./editorPreferences";
import type { FloatingPanelBounds } from "./floatingPanel";
export type WorkspaceDocumentState = { path: string; line: number; column: number; scroll: number; language?: CodeLanguageId; indent?: EditorIndent; draft?: string; saved?: string; version?: string };
export type WorkspaceState = { documents: WorkspaceDocumentState[]; activePath: string; panel: 'files' | 'search' | 'git'; panelOpen: boolean; width: number; gitView: 'list' | 'tree'; expanded: string[]; terminalOpen: boolean; terminalHeight: number; terminalBounds?: FloatingPanelBounds; commitMessage: string };
export function workspaceStateKey(userID: string, hostID: string, root: string) { return `velin-code-workspace:v2:${encodeURIComponent(userID)}:${encodeURIComponent(hostID)}:${encodeURIComponent(root)}`; }
export function loadWorkspaceState(key: string): Partial<WorkspaceState> {
  try {
    const value = JSON.parse(localStorage.getItem(key) || '{}');
    if (!value || typeof value !== 'object') return {};
    const number = (input: unknown, fallback: number) => typeof input === 'number' && Number.isFinite(input) && input >= 0 ? input : fallback;
    value.documents = Array.isArray(value.documents) ? value.documents.filter((doc: WorkspaceDocumentState) => doc && typeof doc.path === 'string' && doc.path.startsWith('/')).map((doc: WorkspaceDocumentState) => ({ path: doc.path, line: number(doc.line, 1), column: number(doc.column, 1), scroll: number(doc.scroll, 0), ...(typeof doc.language === "string" ? { language: doc.language } : {}), ...(["2", "4", "8", "tab"].includes(doc.indent || "") ? { indent: doc.indent } : {}), ...(typeof doc.draft === 'string' ? { draft: doc.draft, saved: typeof doc.saved === 'string' ? doc.saved : '', version: typeof doc.version === 'string' ? doc.version : '' } : {}) })) : [];
    value.width = number(value.width, 260); value.terminalHeight = number(value.terminalHeight, 240);
    if (value.terminalBounds && typeof value.terminalBounds === 'object') {
      const rect = value.terminalBounds;
      value.terminalBounds = { width: number(rect.width, 640), height: number(rect.height, 300), right: number(rect.right, 12), bottom: number(rect.bottom, 12) };
    } else delete value.terminalBounds;
    value.expanded = Array.isArray(value.expanded) ? value.expanded.filter((path: unknown) => typeof path === 'string') : [];
    value.commitMessage = typeof value.commitMessage === 'string' ? value.commitMessage : '';
    return value;
  } catch { return {}; }
}
export function saveWorkspaceState(key: string, value: WorkspaceState) {
  // Keep every draft; report storage failures to the caller instead of dropping data.
  localStorage.setItem(key, JSON.stringify(value));
}
