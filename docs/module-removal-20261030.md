# 多 Agent 工作流移除记录

用户要求先整体移除工作流功能。2026-10-30 已完成源码清理、回归验证和 Docker Web 更新。

## 移除范围

- 删除 `internal/workflow/`、`internal/api/workflow_module{,_test}.go`、`web/src/features/workflow/`、`web/scripts/workflow/`。
- 撤销 API 初始化、路由、备份恢复协调以及 Linux/Windows 的模块生命周期回调。
- 删除控制台主导航、手机导航、侧栏、内容面板和旧工作流路由；恢复原有终端和远程桌面的显示条件。
- 清理示例环境变量、两个 Compose 配置、飞牛配置写入、README 功能说明及工作流测试脚本项。
- 原工作流开发、接入、使用和验收文档随模块源码一起归档。

## 保留与备份

- 删除前的模块源码、四份开发文档及 13 个接入文件原件保存在 `artifacts/source-archives/workflow-module-20261030-before-removal.tar.gz`，不包含 `.env`、运行数据或剪贴板文件。
- `data/workflow/` 中既有数据库、密钥和备份仍保留，新程序不会加载或调度该模块。
- 未连接远程 SSH 执行项目清理，也未删除远程 worktree、代码分支、归档或项目文件。
- 终端、远程桌面、文件管理、原有 AI 助手及其代理设置保留。

## 验证与交付

- Go 全量测试、vet、前端单元检查和生产构建通过；Linux amd64 Docker 构建及 Linux arm64 交叉编译通过。
- 浏览器回归全部通过：SSH 连接进度与指纹、右键复制；标签与分屏关闭；RDP 闲置恢复与切换；弹窗；主题灰阶、历史滚动、剪贴板及普通/tmux 的 Ctrl+Enter，包含真实 tmux/Bash 验证。
- 生产前端的 47 个 HTML/JS/CSS 均无工作流入口、文案和接口引用。
- Web 版本 `v0.3.48+terminal.20261030.4`，镜像 `sha256:c15dbb5062872295ffbe4291179614de1fc14691751a6fed6a37e50af9728447`。容器 healthy、readiness 返回 ok，线上 47 个资源与构建逐字节一致，原三个工作流 GET 接口返回 404。
- 内网访问地址：`http://192.168.0.111:8377`。仅更新 velin，guacd 沿用原镜像和启动时间。
- Windows 单 EXE：`artifacts/windows-v0.3.48/velin-web-ssh-windows-gui-amd64-v0.3.48-no-workflow.exe`，38.02 MiB，SHA-256 `4cbcbe2ac01f5bdd727641cda2779a383da112c07a0d591c6667aefef5934068`。已校验 x64 GUI、版本、工作流接口/包缺席及全部 47 个资源内嵌；未进行 Windows 实机验证。
