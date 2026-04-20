# 课程助手

面向课堂现场的轻量教学辅助系统：**多教师、多课程、多学生**，支持随堂测验、课程资料、作业提交与 AI 学习建议。Go + SQLite 单机运行，单个二进制部署。

## 设计原则

- 教室里最需要的是"稳定 + 可控"：邀请码即可进入，教师随时开关测验入口
- 学生无需注册；会话用 cookie 自动恢复，简答题图片、作业文件本地持久化
- 题库用 YAML 管理；图片与 YAML 同目录
- 每个教师的每门课数据完全隔离在 `metadata/{teacher}/{course}/` 下
- 单二进制 + SQLite；没有前端构建、没有外部依赖

## 角色与入口

| 角色 | 入口 | 说明 |
|---|---|---|
| 学生 | `/` | 输入 6 位邀请码进入课程，做测验 / 看资料 / 提交作业 |
| 教师 | `/t` 或 `/teacher` | 登录后管理自己的课程 |
| 系统管理员 | `/admin` | 仅负责教师账号、AI 配置、系统概览 |

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
```

数据库：`./data/app.db`（SQLite + WAL）。

## 环境变量

| 变量 | 默认 | 说明 |
|---|---|---|
| `APP_ADDR` | `0.0.0.0:8080` | 监听地址；HTTPS 默认改为 `:443` |
| `APP_BASE_URL` | 自动 | 外部访问的基础 URL |
| `DATA_DIR` | `./data` | SQLite + 缓存目录 |
| `METADATA_DIR` | `./metadata` | 按教师/课程组织的文件根 |
| `AI_ENDPOINT` | - | AI 服务地址 |
| `AI_API_KEY` | - | AI 鉴权密钥 |
| `AI_MODEL` | - | 模型名 |
| `CERT_PATH` | - | HTTPS 证书目录（`*.pem` + `*.key`） |
| `AUTOCERT_ENABLE` | - | 启用 `autocert` 自动证书 |
| `AUTOCERT_HOSTS` | - | 域名白名单 |
| `APP_HTTP_REDIRECT_ADDR` | - | HTTP→HTTPS 301 监听 |

> **注意**：`ADMIN_PASSWORD` 环境变量已**不再生效**。管理员登录必须用 `teachers` 表里 role=admin 的账号（通过 `cmd/migrate upgrade --teacher-id ...` 创建）。

## 数据契约（关键）

- `attempts.course_id` **必填 > 0**；唯一约束 `(quiz_id, student_no, course_id) WHERE status='in_progress'`
- `SubmitAttempt` 按 `(course_id, quiz_id, student_no)` 计算 `attempt_no`，两门课共用 YAML 不再串号
- `admin_summaries` 主键 = `(course_id, quiz_id)` 复合；不同课程的同一 YAML 有独立总结
- `homework_submissions.course_id` 为权威字段；`course` 列保留 slug 仅用于展示

## 路由表

| URL | 视图/Handler | 权限 |
|---|---|---|
| `/` | 学生入口（邀请码 + Tab：测验/资料/作业） | 无 |
| `/s/:code` | 301 → `/?code=:code` | 无 |
| `/join` | 301 → `/`（旧 QR 码兼容） | 无 |
| `/t` 或 `/teacher` | 教师面板 | 教师 cookie |
| `/admin` | 系统管理 | role=admin |
| `/static/*` | 共享 CSS/JS | 无 |
| `/api/auth/*` | 统一登录 / 登出 / me | — |
| `/api/system/*` | 系统管理 API（教师、AI、统计） | role=admin |
| `/api/teacher/courses/*` | 教师课程 API | 教师 cookie |
| `/api/course?code=` | 邀请码解析（返回 `id/name/slug/teacher_name`） | 无 |
| `/api/join` / `/api/entry-status?course_id=N` | 学生入场 | — |
| `/api/admin/*`（教学类） | 410 Gone + 指向新路由 | — |

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

## 开发

### 运行测试

```bash
go test ./...
```

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
