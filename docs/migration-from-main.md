# 从 main 分支升级到多租户版

本指南针对**正在线上运行 main 分支**、希望升级到新的多租户 + 去 legacy 版本的部署。迁移工具会把旧数据库与旧文件布局原地升级到新 schema，**保留**所有学生答题记录、图片与作业提交文件。

## 变更概要

- 新增 `teachers` / `courses` / `course_state` 表；`attempts.course_id`、`homework_submissions.course_id`、`admin_summaries.course_id` 列
- 所有学生入口统一在 `/`（旧 `/join`、`/student` 均 301 到根路径）
- 系统管理 `/admin` 仅保留「教师管理 / AI 配置 / 系统概览」；所有教学事务改由 `/teacher` 负责
- 旧的 `/api/admin/*` 教学类 API 全部返回 410 Gone（附迁移提示）
- 删除 `ADMIN_PASSWORD` 兜底登录；管理员必须通过 `teachers` 表条目登录
- 所有文件迁入 `./metadata/{teacher_id}/{course_slug}/` 新布局

## 升级步骤

> **先做快照**：停机前务必备份整台服务器（云厂商的快照/镜像即可）。迁移工具也会自动产生一份 `data/app.db.prelegacy-*.bak`。

### 1. 停机

```bash
systemctl stop course-assistant  # 或你的进程管理器
```

### 2. 切分支并编译

```bash
cd /path/to/course-assistant
git fetch origin
git checkout dev_multi       # 或对应发布 tag
make build                   # 产物在 ./bin/
```

### 3. 一键升级

```bash
./bin/migrate upgrade \
  --db ./data/app.db \
  --teacher-id SC25132 \
  --teacher-name "赵浩诚" \
  --password '请改成真实密码' \
  --course-slug default \
  --course-name "默认课程"
```

工具会**按顺序**执行（每一步幂等）：

1. 备份 `./data/app.db` 到 `./data/app.db.prelegacy-<timestamp>.bak`
2. 识别源 schema（main / dev_multi / empty）
3. 建立新表 / 补全缺失列（含 `course_id`）
4. 创建默认教师（role=admin）与默认课程
5. 把所有旧 attempts / homework 的 `course_id` 回填为默认课程
6. 按 `(course_id, quiz_id, student_no)` 重算 `attempt_no`，修复 main 分支的跨课串号问题
7. 清理重复的 `in_progress` attempt
8. 建立新索引（含 `(course_id, quiz_id)` 复合索引）
9. 把 `./data/quiz/`、`./data/homework/`、`./ppt/` 的文件**复制**到 `./metadata/{teacher}/{course}/...`
10. 改写 `answers` 里的图片 URL 为新路径
11. 统一 YAML `quiz_id = 目录名`（并同步数据库）
12. 删除 legacy 全局设置（`settings.quiz_yaml` / `settings.entry_open`）
13. 把报告写到 `./data/migration_report.json`，含每一步的行数/文件数统计

### 4. 核对迁移报告

```bash
jq . data/migration_report.json | less
```

重点看：
- `attempts_updated` 与你数据库里原有 attempts 总数一致
- `homework_updated` 与原有 homework_submissions 总数一致
- `files_copied` 中 `materials / assignments / quiz_yaml / answer_images / homework_submissions` 的计数合理
- `quiz_ids_renamed` 是否命中你预期的 YAML 文件
- `warnings` 为空（否则具体查看）

### 5. 启动新版本

```bash
./bin/server
# 或 systemctl start course-assistant
```

启动日志应显示：
- 监听地址
- 学生入口：`/`
- 教师入口：`/teacher` 或 `/t`
- 系统管理：`/admin`

### 6. 冒烟测试清单

- 访问 `/admin` → 用默认教师账号登录 → 三个 Tab 均可正常加载
- 访问 `/teacher` → 用同一教师登录 → 能看到默认课程 → 进入「答题记录」应能看到历史 attempts
- 访问 `/` → 输入默认课程邀请码（登录教师面板即可看到）→ 填信息 → 进入测验
- 旧 QR 码（`/join?code=XXX`）应 301 到 `/?code=XXX`
- 几个随机历史答题记录，进入「查看详情」后图片应能正常加载（路径已被改写）

### 7. 清理旧目录（可选）

**确认新版本稳定运行几天后**，可以删除旧布局：

```bash
rm -rf ./data/quiz ./data/homework ./ppt ./quiz/assets ./quiz/*/
```

## 回滚

若升级后出现问题：

```bash
systemctl stop course-assistant
cp ./data/app.db.prelegacy-<timestamp>.bak ./data/app.db
git checkout main
make build
systemctl start course-assistant
```

文件部分未被修改（迁移是复制而不是移动），旧目录仍完好。

## 常见问题

**Q: 迁移后第一次登录 `/admin`，密码是什么？**  
A: `--password` 参数指定的值。如果没传，工具会在终端打印一次随机密码（只显示一次）。

**Q: 我有多位老师共享一个旧部署，如何把 attempts 分到各自名下？**  
A: 本次迁移把所有旧数据挂到「默认教师 + 默认课程」下。之后在 `/admin` 里创建其他教师账号，登录 `/teacher` 创建自己的课程，然后用数据库手动迁移（未来版本会提供 UI 拆分工具）。

**Q: 学生 cookie 还有效吗？**  
A: `student_token` cookie 未动；迁移工具已把对应 attempt 的 `course_id` 设为默认课程，学生刷新页面即可继续作答或查看结果。

**Q: `ADMIN_PASSWORD` 环境变量还有用吗？**  
A: 不再生效。管理员登录只认 `teachers` 表里 role=admin 的账号。

**Q: 再次运行升级工具安全吗？**  
A: 安全。每一步都有幂等保护。迁移会生成一份新的备份并重新计算统计。

## 旧命令兼容

旧的位置参数命令仍然可以用，但不再推荐：

```bash
# 旧：go run ./cmd/migrate ./data/app.db admin SC25132 赵浩诚 admin123
# 新：go run ./cmd/migrate upgrade --db ./data/app.db --teacher-id SC25132 --teacher-name 赵浩诚 --password admin123
```
