# 课程助手

面向课堂现场的轻量教学辅助系统：**多教师、多课程、多学生**，支持随堂测验、课程资料、作业提交、AI 学习建议、教师独立文档页与系统快照恢复。系统管理员可在 `/admin` 查看教师/课程/学生/答题总量与在线人数，并直接看到“是否适合升级/重启/恢复”的维护建议。教师端小测成绩会按“同一学生 + 同一题库”自动保留最高分，并支持重复记录检查；作业下载文件名统一为 `班级_作业编号_姓名_学号`。Go + SQLite 单机运行，单个二进制部署。

## 设计原则

- 教室里最需要的是"稳定 + 可控"：邀请码即可进入，教师随时开关测验入口
- 学生无需注册；会话用 cookie 自动恢复，简答题图片、作业文件本地持久化
- 学生在答题页可返回课程主页且保留进度，也可主动退出当前答题会话
- 题库用 YAML 管理；图片与 YAML 同目录
- 每个教师的每门课数据完全隔离在 `metadata/{teacher}/{course}/` 下
- 单二进制 + SQLite；没有前端构建、没有外部依赖
- 系统每天凌晨 3 点自动生成快照，默认保留 14 天；管理员可下载或恢复

## 角色与入口

| 角色       | 入口               | 说明                                                |
| ---------- | ------------------ | --------------------------------------------------- |
| 学生       | `/`                | 输入 6 位邀请码进入课程，做测验 / 看资料 / 提交作业 |
| 教师       | `/t` 或 `/teacher` | 登录后管理自己的课程；右上角可新开使用文档页        |
| 系统管理员 | `/admin`           | 负责教师账号、AI 配置、系统概览与在线人数判断       |

短链：`/s/ABC123` → 自动跳转到 `/?code=ABC123`。

## 快速开始

### 启动

```bash
go mod tidy
go run ./cmd/server
```

默认监听 `0.0.0.0:8080`，服务会把**局域网 IP** 打印到日志，方便学生扫码或手动输入。

### 首次部署（从零开始）

新库启动时会自动建表。接下来需要创建首位管理员教师：

```bash
go run ./cmd/migrate upgrade \
  --db ./data/app.db \
  --teacher-id SC25132 \
  --teacher-name "赵浩诚" \
  --password '请改真实密码'
```

然后登录 `/admin`，在"教师管理"Tab 里添加更多教师，或者登录 `/teacher` 开始建课。

### 从 main 分支升级

见 [docs/migration-from-main.md](docs/migration-from-main.md)。

## 编译

```bash
make build
```

产物：
- `bin/server` — 主服务
- `bin/migrate` — 迁移工具
- `bin/qrgen` — 二维码生成

## 文件布局

```
metadata/
  {teacher_id}/
    {course_slug}/
      quiz/
        {quiz_id}/
          {quiz_id}.yaml        # 题库
          *.png, *.svg          # 题图
          submissions/
            {student_no}/*.jpg  # 学生简答题图片
      assignment/
        {assignment_id}/
          *.pdf, *.docx         # 作业说明
          submissions/
            {student_no}/{submission_id}/
              report.pdf
              code.ipynb
              extra.zip
      materials/
        *                       # 课程资料（PDF/代码/任意）

snapshots/
  snapshot_2026-04-25_030000_scheduled_lite.tar.gz
  snapshot_2026-04-25_104233_manual_lite.tar.gz
```

数据库：`./data/app.db`（SQLite + WAL）。

## 环境变量

| 变量                     | 默认           | 说明                                       |
| ------------------------ | -------------- | ------------------------------------------ |
| `APP_ADDR`               | `0.0.0.0:8080` | 监听地址；HTTPS 默认改为 `:443`            |
| `APP_BASE_URL`           | 自动           | 外部访问的基础 URL                         |
| `DATA_DIR`               | `./data`       | SQLite + 缓存目录                          |
| `METADATA_DIR`           | `./metadata`   | 按教师/课程组织的文件根                    |
| `SNAPSHOT_DIR`           | `./snapshots`  | 系统快照目录（自动快照、恢复上传临时文件） |
| `AI_ENDPOINT`            | -              | AI 服务地址                                |
| `AI_API_KEY`             | -              | AI 鉴权密钥                                |
| `AI_MODEL`               | -              | 模型名                                     |
| `CERT_PATH`              | -              | HTTPS 证书目录（`*.pem` + `*.key`）        |
| `AUTOCERT_ENABLE`        | -              | 启用 `autocert` 自动证书                   |
| `AUTOCERT_HOSTS`         | -              | 域名白名单                                 |
| `APP_HTTP_REDIRECT_ADDR` | -              | HTTP→HTTPS 301 监听                        |

> **注意**：`ADMIN_PASSWORD` 环境变量已**不再生效**。管理员登录必须用 `teachers` 表里 role=admin 的账号（通过 `cmd/migrate upgrade --teacher-id ...` 创建）。

## 数据契约（关键）

- `attempts.course_id` **必填 > 0**；唯一约束 `(quiz_id, student_no, course_id) WHERE status='in_progress'`
- `SubmitAttempt` 按 `(course_id, quiz_id, student_no)` 计算 `attempt_no`，两门课共用 YAML 不再串号
- `admin_summaries` 主键 = `(course_id, quiz_id)` 复合；不同课程的同一 YAML 有独立总结
- `homework_submissions.course_id` 为权威字段；`course` 列保留 slug 仅用于展示
- `courses.display_name` 保存教师输入的英文展示名（空格版）；`courses.internal_name` 保存自动转换后的内部名（空格转下划线）；`courses.slug` 继续兼容旧逻辑并镜像 `internal_name`
- 建课接口会对英文名做 `trim + collapse spaces + replace spaces with "_"` 处理；历史课程不会被强制改写展示名

## 路由表

| URL                                                                    | 视图/Handler                                                              | 权限        |
| ---------------------------------------------------------------------- | ------------------------------------------------------------------------- | ----------- |
| `/`                                                                    | 学生入口（邀请码 + Tab：测验/资料/作业）                                  | 无          |
| `/s/:code`                                                             | 301 → `/?code=:code`                                                      | 无          |
| `/join`                                                                | 301 → `/`（旧 QR 码兼容）                                                 | 无          |
| `/t` 或 `/teacher`                                                     | 教师面板                                                                  | 教师 cookie |
| `/teacher/docs` 或 `/t/docs`                                           | 教师使用文档页（Markdown 渲染，适合单独打开对照操作）                     | 教师 cookie |
| `/admin`                                                               | 系统管理                                                                  | role=admin  |
| `/static/*`                                                            | 共享 CSS/JS                                                               | 无          |
| `/api/auth/*`                                                          | 统一登录 / 登出 / me                                                      | —           |
| `/api/system/*`                                                        | 系统管理 API（教师、AI、统计）                                            | role=admin  |
| `/api/teacher/courses/*`                                               | 教师课程 API                                                              | 教师 cookie |
| `/api/course?code=`                                                    | 邀请码解析（返回 `id/name/display_name/internal_name/slug/teacher_name`） | 无          |
| `/api/join` / `/api/entry-status?course_id=N` / `/api/student-signout` | 学生入场与退出当前答题会话                                                | —           |
| `/api/admin/*`（教学类）                                               | 410 Gone + 指向新路由                                                     | —           |

教师课程 API 约定：
- `POST /api/teacher/courses`：请求体中的 `slug` 字段可直接填写带空格英文名，例如 `Machine Learning Intro`
- 服务端自动生成 `internal_name=Machine_Learning_Intro`，并同时返回 `display_name`、`internal_name` 与兼容字段 `slug`
- 文件路径、课程目录、作业目录等内部引用统一使用 `internal_name`
- `GET /api/teacher/courses/attempts`、`/attempts-check`、`/export-csv` 会按“同一学生 + 同一题库”自动去重，保留最高分；教师页可查看重复检查结果
- 教师端作业下载（单个 PDF、单个学生压缩包、批量压缩包内学生目录/文件）统一使用 `班级_作业编号_姓名_学号` 命名

## 题库 YAML

参考 [examples/quiz.sample.yaml](examples/quiz.sample.yaml)。最常用规则：

- 顶层必须有 `quiz_id`、`title`、`questions`
- 每题必须有 `id`、`type`、`stem`；类型限 `single_choice`/`multi_choice`/`yes_no`/`survey`/`short_answer`
- 判分类（single/multi/yes_no）必须有 `correct_answer`
- `short_answer.short_answer_mode`：`text` / `image` / `code` / `text_image`（省略则根据题干自动检测）
- `fixed_position: true`：固定该题位置，不参与随机排序
- 图片放在 YAML 同目录，YAML 里写 `image: foo.svg`
- **上传 YAML 时以文件名为准**：`week7_l1.yaml` → 目录 `week7_l1`，运行时 `quiz_id = week7_l1`

详见 [docs/quiz-prep-step-by-step.md](docs/quiz-prep-step-by-step.md)。

## 安全特性

- 所有学生输入经 HTML 转义后再呈现给管理员
- CSV 导出自动前缀 `=+\-@` 防公式注入
- 同源 CORS；Cookie 带 `HttpOnly` + `SameSite=Lax`
- 单一学号在同一课程内唯一进行中 attempt
- SQLite WAL + 复合索引
- 作业 PDF / ZIP 上传会校验文件**内容签名**，非扩展名
- 作业 PDF 不会出现在 `/materials` 或通用下载路径下
- 系统快照恢复采用“写入恢复任务 -> 重启 -> 启动前应用快照”流程，避免在线覆盖活跃 SQLite 数据

## 系统快照

- 自动快照：服务每天凌晨 `03:00` 生成一次“轻量快照”并保存在 `SNAPSHOT_DIR`
- 轻量快照内容：`app.db` 一致性副本、`metadata/*/*/quiz/**`、`metadata/*/*/assignment/*/submissions/**`、`manifest.json`
- 轻量快照不会打包课程资料 `materials/` 与作业说明文件，因此体积更小，适合日常保留与下载
- 完整快照：仅管理员在 `/admin` 的“系统快照”Tab 中手动生成并直接下载，不在服务器长期保留
- 保留策略：服务器端自动清理 14 天前的轻量快照
- 管理入口：`/admin` 的“系统快照”Tab，可查看历史轻量快照、下载轻量快照、按历史快照恢复、上传快照恢复
- 恢复方式：管理员提交恢复请求后，服务会写入待恢复任务并自动重启；若你不是用 `systemd` / `launchd` 托管，请手动重新启动服务

## 开发

### 运行测试

```bash
go test ./...
```

### 维护教师文档截图

```bash
make docs-screenshots
```

该命令会调用项目内置的教师文档截图脚本，重新生成 `internal/app/web/static/docs/screenshots/` 下的说明图片，适合在更新 `teacher-guide.md` 后重新产出配套截图。

### 目录

```
cmd/
  server/    # 主进程入口
  migrate/   # 升级 & 初始化工具（含 upgrade 子命令）
  qrgen/     # 二维码
internal/
  ai/        # AI 客户端 + 总结 prompt
  app/       # HTTP handlers + 模板
    web/     # HTML 模板 (embedded)
      static/  # 共享 CSS/JS (embedded)
  domain/    # 领域类型
  quiz/      # YAML parser
  store/     # SQLite
  pdftext/   # PDF 文本提取
```

### 贡献指南

- 新增功能同步更新本 README 与 `docs/architecture.md`
- `internal/app/web/` 下的页面优先复用 `static/app.css` 与 `App.*` JS helper；避免重复实现 toast / confirm / fetch 逻辑
- 新增数据契约要在 `cmd/migrate/upgrade.go` 加入幂等迁移步骤

## 文档

- [docs/architecture.md](docs/architecture.md) — 架构与 API 概览
- [docs/migration-from-main.md](docs/migration-from-main.md) — 从 main 分支升级的完整指南
- [docs/quiz-prep-step-by-step.md](docs/quiz-prep-step-by-step.md) — 题库准备流程
- [docs/adr/0001-tech-stack.md](docs/adr/0001-tech-stack.md) — 技术栈决策
- [teacher-guide.md](internal/app/web/static/docs/teacher-guide.md) — 教师端图文使用说明，对应页面 `/teacher/docs`
