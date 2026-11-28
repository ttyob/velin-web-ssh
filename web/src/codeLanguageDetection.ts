export type CodeLanguageId =
  | "json" | "markdown" | "php" | "javascript" | "typescript" | "python" | "go"
  | "css" | "sass" | "xml" | "c" | "cpp" | "java" | "csharp" | "scala" | "kotlin"
  | "objective-c" | "dart" | "rust" | "sql" | "shell" | "yaml" | "properties"
  | "ruby" | "dockerfile" | "toml" | "lua" | "perl" | "r" | "swift" | "powershell"
  | "cmake" | "protobuf" | "nginx" | "diff" | "groovy" | "haskell" | "clojure"
  | "common-lisp" | "scheme" | "erlang" | "fortran" | "pascal" | "vb" | "verilog"
  | "vhdl" | "coffeescript" | "elm" | "pug" | "jinja2" | "gherkin" | "makefile"
  | "plaintext";

const extensions: Record<string, CodeLanguageId> = {
  json: "json", jsonc: "json", json5: "json", md: "markdown", markdown: "markdown", mdx: "markdown",
  php: "php", php3: "php", php4: "php", php5: "php", php8: "php", phtml: "php", phps: "php",
  js: "javascript", mjs: "javascript", cjs: "javascript", jsx: "javascript",
  ts: "typescript", mts: "typescript", cts: "typescript", tsx: "typescript",
  py: "python", pyw: "python", go: "go", css: "css", scss: "sass", sass: "sass", less: "css", styl: "css",
  html: "xml", htm: "xml", xhtml: "xml", xml: "xml", svg: "xml", vue: "xml", svelte: "xml",
  c: "c", h: "c", cpp: "cpp", cc: "cpp", cxx: "cpp", hpp: "cpp", hh: "cpp", hxx: "cpp",
  java: "java", cs: "csharp", scala: "scala", kt: "kotlin", kts: "kotlin", m: "objective-c", mm: "objective-c",
  dart: "dart", rs: "rust", sql: "sql", sh: "shell", bash: "shell", zsh: "shell", fish: "shell",
  yaml: "yaml", yml: "yaml", ini: "properties", env: "properties", conf: "properties", cfg: "properties",
  properties: "properties", rb: "ruby", toml: "toml", lua: "lua", pl: "perl", pm: "perl", r: "r",
  swift: "swift", ps1: "powershell", psm1: "powershell", psd1: "powershell", cmake: "cmake",
  proto: "protobuf", diff: "diff", patch: "diff", gradle: "groovy", groovy: "groovy", gvy: "groovy",
  hs: "haskell", lhs: "haskell", clj: "clojure", cljs: "clojure", cljc: "clojure", edn: "clojure",
  lisp: "common-lisp", lsp: "common-lisp", cl: "common-lisp", scm: "scheme", ss: "scheme",
  erl: "erlang", hrl: "erlang", f: "fortran", f90: "fortran", f95: "fortran", f03: "fortran",
  pas: "pascal", pp: "pascal", vb: "vb", vbs: "vb", v: "verilog", vh: "verilog", sv: "verilog",
  svh: "verilog", vhd: "vhdl", vhdl: "vhdl", coffee: "coffeescript", litcoffee: "coffeescript",
  elm: "elm", pug: "pug", jade: "pug", j2: "jinja2", jinja: "jinja2", jinja2: "jinja2",
  feature: "gherkin", mk: "makefile",
};

export function codeLanguageId(path: string): CodeLanguageId {
  const normalized = path.replaceAll("\\", "/").toLowerCase();
  const name = normalized.split("/").at(-1) || "";
  const extension = name.includes(".") ? name.split(".").at(-1) || "" : "";
  if (name === "dockerfile" || name.startsWith("dockerfile.")) return "dockerfile";
  if (["makefile", "gnumakefile", "bsdmakefile"].includes(name)) return "makefile";
  if (name === "cmakelists.txt") return "cmake";
  if (name === "nginx.conf" || normalized.includes("/nginx/") && extension === "conf") return "nginx";
  if (["gemfile", "rakefile", "guardfile", "podfile"].includes(name)) return "ruby";
  if (name === "jenkinsfile") return "groovy";
  if (name === ".env" || name.startsWith(".env.")) return "properties";
  return extensions[extension] || "plaintext";
}
