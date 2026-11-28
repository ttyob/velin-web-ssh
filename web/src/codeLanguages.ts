import { StreamLanguage, HighlightStyle, syntaxHighlighting } from "@codemirror/language";
import { tags } from "@lezer/highlight";
import { EditorView } from "@codemirror/view";
import { json } from "@codemirror/lang-json";
import { markdown } from "@codemirror/lang-markdown";
import { php } from "@codemirror/lang-php";
import { javascript, typescript } from "@codemirror/legacy-modes/mode/javascript";
import { python } from "@codemirror/legacy-modes/mode/python";
import { go } from "@codemirror/legacy-modes/mode/go";
import { css } from "@codemirror/legacy-modes/mode/css";
import { xml } from "@codemirror/legacy-modes/mode/xml";
import { c, cpp, java, csharp, scala, kotlin, objectiveC, dart } from "@codemirror/legacy-modes/mode/clike";
import { rust } from "@codemirror/legacy-modes/mode/rust";
import { standardSQL } from "@codemirror/legacy-modes/mode/sql";
import { shell } from "@codemirror/legacy-modes/mode/shell";
import { yaml } from "@codemirror/legacy-modes/mode/yaml";
import { properties } from "@codemirror/legacy-modes/mode/properties";
import { ruby } from "@codemirror/legacy-modes/mode/ruby";
import { dockerFile } from "@codemirror/legacy-modes/mode/dockerfile";
import { toml } from "@codemirror/legacy-modes/mode/toml";
import { lua } from "@codemirror/legacy-modes/mode/lua";
import { perl } from "@codemirror/legacy-modes/mode/perl";
import { r } from "@codemirror/legacy-modes/mode/r";
import { swift } from "@codemirror/legacy-modes/mode/swift";
import { powerShell } from "@codemirror/legacy-modes/mode/powershell";
import { cmake } from "@codemirror/legacy-modes/mode/cmake";
import { protobuf } from "@codemirror/legacy-modes/mode/protobuf";
import { nginx } from "@codemirror/legacy-modes/mode/nginx";
import { diff } from "@codemirror/legacy-modes/mode/diff";
import { groovy } from "@codemirror/legacy-modes/mode/groovy";
import { haskell } from "@codemirror/legacy-modes/mode/haskell";
import { clojure } from "@codemirror/legacy-modes/mode/clojure";
import { commonLisp } from "@codemirror/legacy-modes/mode/commonlisp";
import { scheme } from "@codemirror/legacy-modes/mode/scheme";
import { erlang } from "@codemirror/legacy-modes/mode/erlang";
import { fortran } from "@codemirror/legacy-modes/mode/fortran";
import { pascal } from "@codemirror/legacy-modes/mode/pascal";
import { vb } from "@codemirror/legacy-modes/mode/vb";
import { verilog } from "@codemirror/legacy-modes/mode/verilog";
import { vhdl } from "@codemirror/legacy-modes/mode/vhdl";
import { coffeeScript } from "@codemirror/legacy-modes/mode/coffeescript";
import { elm } from "@codemirror/legacy-modes/mode/elm";
import { pug } from "@codemirror/legacy-modes/mode/pug";
import { jinja2 } from "@codemirror/legacy-modes/mode/jinja2";
import { gherkin } from "@codemirror/legacy-modes/mode/gherkin";
import { sass } from "@codemirror/legacy-modes/mode/sass";
import { codeLanguageId, type CodeLanguageId } from "./codeLanguageDetection";

export function codeLanguage(path: string, override?: CodeLanguageId) {
  const id = override || codeLanguageId(path);
  if (id === "json") return { name: "JSON", extension: json() };
  if (id === "markdown") return { name: "Markdown", extension: markdown() };
  if (id === "php") return { name: "PHP", extension: php() };
  const modes = {
    javascript, typescript, python, go, css, sass, xml, c, cpp, java, csharp, scala, kotlin,
    "objective-c": objectiveC, dart, rust, sql: standardSQL, shell, yaml, properties, ruby,
    dockerfile: dockerFile, toml, lua, perl, r, swift, powershell: powerShell, cmake, protobuf,
    nginx, diff, groovy, haskell, clojure, "common-lisp": commonLisp, scheme, erlang, fortran,
    pascal, vb, verilog, vhdl, coffeescript: coffeeScript, elm, pug, jinja2, gherkin,
    makefile: shell,
  } satisfies Partial<Record<CodeLanguageId, Parameters<typeof StreamLanguage.define>[0]>>;
  const labels: Partial<Record<CodeLanguageId, string>> = {
    javascript: "JavaScript", typescript: "TypeScript", python: "Python", go: "Go", css: "CSS",
    sass: "Sass / SCSS", xml: "HTML / XML", c: "C", cpp: "C++", java: "Java", csharp: "C#",
    scala: "Scala", kotlin: "Kotlin", "objective-c": "Objective-C", dart: "Dart", rust: "Rust",
    sql: "SQL", shell: "Shell", yaml: "YAML", properties: "Properties", ruby: "Ruby",
    dockerfile: "Dockerfile", toml: "TOML", lua: "Lua", perl: "Perl", r: "R", swift: "Swift",
    powershell: "PowerShell", cmake: "CMake", protobuf: "Protocol Buffers", nginx: "Nginx",
    diff: "Diff", groovy: "Groovy", haskell: "Haskell", clojure: "Clojure", "common-lisp": "Common Lisp",
    scheme: "Scheme", erlang: "Erlang", fortran: "Fortran", pascal: "Pascal", vb: "Visual Basic",
    verilog: "Verilog", vhdl: "VHDL", coffeescript: "CoffeeScript", elm: "Elm", pug: "Pug",
    jinja2: "Jinja2", gherkin: "Gherkin", makefile: "Makefile",
  };
  const mode = modes[id as keyof typeof modes];
  return { name: labels[id] || "纯文本", extension: mode ? StreamLanguage.define(mode) : [] };
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

export const codeLanguageOptions = (["plaintext", "json", "markdown", "php", "javascript", "typescript", "python", "go", "css", "sass", "xml", "c", "cpp", "java", "csharp", "scala", "kotlin", "objective-c", "dart", "rust", "sql", "shell", "yaml", "properties", "ruby", "dockerfile", "toml", "lua", "perl", "r", "swift", "powershell", "cmake", "protobuf", "nginx", "diff", "groovy", "haskell", "clojure", "common-lisp", "scheme", "erlang", "fortran", "pascal", "vb", "verilog", "vhdl", "coffeescript", "elm", "pug", "jinja2", "gherkin", "makefile"] as CodeLanguageId[]).map(id => ({ id, name: codeLanguage("", id).name }));
