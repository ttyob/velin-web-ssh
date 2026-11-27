import { StreamLanguage, HighlightStyle, syntaxHighlighting } from "@codemirror/language";
import { tags } from "@lezer/highlight";
import { EditorView } from "@codemirror/view";
import { json } from "@codemirror/lang-json";
import { markdown } from "@codemirror/lang-markdown";
import { javascript, typescript } from "@codemirror/legacy-modes/mode/javascript";
import { python } from "@codemirror/legacy-modes/mode/python";
import { go } from "@codemirror/legacy-modes/mode/go";
import { css } from "@codemirror/legacy-modes/mode/css";
import { xml } from "@codemirror/legacy-modes/mode/xml";
import { c, cpp, java, csharp } from "@codemirror/legacy-modes/mode/clike";
import { rust } from "@codemirror/legacy-modes/mode/rust";
import { standardSQL } from "@codemirror/legacy-modes/mode/sql";
import { shell } from "@codemirror/legacy-modes/mode/shell";
import { yaml } from "@codemirror/legacy-modes/mode/yaml";
import { properties } from "@codemirror/legacy-modes/mode/properties";
import { ruby } from "@codemirror/legacy-modes/mode/ruby";

export function codeLanguage(path: string) {
  const name = path.split("/").at(-1)?.toLowerCase() || "";
  const extension = name.split(".").at(-1) || "";
  if (["json", "jsonc"].includes(extension)) return { name: "JSON", extension: json() };
  if (["md", "markdown"].includes(extension)) return { name: "Markdown", extension: markdown() };
  const modes = {
    js: javascript, mjs: javascript, cjs: javascript, jsx: javascript,
    ts: typescript, tsx: typescript, py: python, go, css, scss: css, less: css,
    html: xml, htm: xml, xml, svg: xml, vue: xml,
    c, h: c, cpp, hpp: cpp, java, cs: csharp, rs: rust,
    sql: standardSQL, sh: shell, bash: shell, zsh: shell,
    yaml, yml: yaml, ini: properties, env: properties, conf: properties,
    cfg: properties, properties, rb: ruby,
  };
  const mode = modes[extension as keyof typeof modes];
  if (name === "dockerfile") return { name: "Dockerfile", extension: StreamLanguage.define(shell) };
  if (name === ".env" || name.startsWith(".env.")) return { name: "Properties", extension: StreamLanguage.define(properties) };
  return { name: mode ? extension.toUpperCase() : "纯文本", extension: mode ? StreamLanguage.define(mode) : [] };
}

export const workbenchEditorTheme = [
  EditorView.theme({
    "&": { height: "100%", color: "var(--text)", backgroundColor: "var(--bg)", fontSize: "13px" },
    ".cm-scroller": { overflow: "auto", fontFamily: "var(--font-mono, 'Cascadia Code', Consolas, monospace)", lineHeight: "1.65" },
    ".cm-content": { padding: "10px 0 60px", caretColor: "var(--text)" },
    ".cm-gutters": { color: "var(--muted)", backgroundColor: "var(--bg)", borderRight: "1px solid var(--line)" },
    ".cm-activeLine, .cm-activeLineGutter": { backgroundColor: "var(--surface-2)" },
    ".cm-cursor": { borderLeftColor: "var(--text)" },
    ".cm-selectionBackground, &.cm-focused .cm-selectionBackground": { backgroundColor: "var(--accent-soft, #6383a733) !important" },
    ".cm-panels, .cm-tooltip": { color: "var(--text)", backgroundColor: "var(--surface)", borderColor: "var(--line)" },
    ".cm-searchMatch": { backgroundColor: "#b39a7240", outline: "1px solid #b39a7280" },
    ".cm-searchMatch-selected": { backgroundColor: "#b39a7270" },
  }),
  syntaxHighlighting(HighlightStyle.define([
    { tag: tags.comment, color: "var(--code-comment, #7d8c7a)" },
    { tag: [tags.keyword, tags.modifier], color: "var(--code-keyword, #8da9bf)" },
    { tag: [tags.string, tags.regexp], color: "var(--code-string, #b6a17c)" },
    { tag: [tags.number, tags.bool, tags.null], color: "var(--code-number, #b88e7a)" },
    { tag: [tags.typeName, tags.className, tags.tagName], color: "var(--code-type, #8da992)" },
    { tag: [tags.function(tags.variableName), tags.attributeName], color: "var(--code-function, #a7abb7)" },
  ])),
];
