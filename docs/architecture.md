# 课堂统计与签到系统架构说明

## 目标
- 固定学生入口 `/join`
- 管理员动态开启/关闭新进入
- 学生匿名会话并可恢复进度
- 实时保存答题
- 提交后不可修改
- 输出逐题反馈与 AI 学习建议

## 分层
- `cmd/server`: 进程入口与环境配置
- `internal/app`: HTTP 路由与用例编排
- `internal/domain`: 核心领域模型
- `internal/store`: 持久化接口与 SQLite 实现
- `internal/quiz`: YAML 题库解析与校验
- `internal/ai`: AI 总结适配层（含规则兜底）

## 核心状态
- 入口开关：`settings.entry_open`
- 学生作答：`attempts.status`
  - `in_progress`
  - `submitted`

## 关键约束
- 学生入口 URL 固定为 `/join`
- `entry_open=false` 时禁止新会话创建
- 已有会话可继续答题与提交
- `submitted` 状态禁止修改答案
- `survey` 题不参与判分与总结
- 同一学生（`quiz_id + student_no`）只允许一个 `in_progress` 状态的 attempt（数据库唯一索引保证）
- `/api/me` 不返回 `correct_answer`、`explanation`、`reference_answer` 等敏感字段，防止学生通过开发者工具查看答案
- 提交时自动保存所有未保存的简答题，无需手动保存

## 路由与资源

页面路由：
- `/`、`/join`：入口页
- `/quiz`：答题页
- `/result`：结果页
- `/admin`：管理后台
- `/pdf`：课件下载页

静态资源：
- `/assets/<path>`：题库图片资源。按顺序查找：
  - `QUIZ_ASSETS_DIR`（默认 `./quiz/assets`）
  - `${DATA_DIR}/assets`（默认 `./data/assets`）
- `/ppt/<folder>/<file>.pdf`：课件 PDF 文件，根目录为 `../ppt`（与 `DATA_DIR` 同级）
- `/uploads/<...>`：学生简答题图片上传后的访问路径，文件写入 `${DATA_DIR}/quiz/...`

## API 概览

学生接口：
- `POST /api/join`：加入课堂（仅入口开放时允许）
- `GET /api/entry-status`：查询入口是否开放
- `GET /api/me`：获取当前会话、题库（脱敏）与已保存答案
- `POST /api/answer`：保存答案（选择题实时保存；提交后禁止修改）
- `POST /api/answer-image`：上传简答题图片（仅 JPEG/PNG，提交后禁止）
- `POST /api/submit`：提交并锁定
- `GET /api/result`：获取逐题反馈与得分
- `POST /api/ai-summary`：生成/获取 AI 学习建议（可缓存）
- `POST /api/retry`：提交后发起“再做一次”（创建新的 `in_progress` attempt，并发新 cookie）

管理员接口（均需登录）：
- `POST /api/admin/login`：登录
- `GET /api/admin/state`：入口状态、人数统计、当前题库信息
- `POST /api/admin/entry`：开启/关闭入口
- `POST /api/admin/load-quiz`：加载题库（粘贴 YAML / 上传文件 / 指定服务器文件路径）
- `GET /api/admin/quiz-files`：列出服务器上的题库文件（用于下拉选择）
- `GET /api/admin/live`：SSE 实时推送人数与入口状态
- `GET /api/admin/attempts`：学生列表（按当前 `quiz_id` 过滤）
- `GET /api/admin/attempt-detail?id=...`：单个学生详情
- `GET /api/admin/export-csv`：导出 CSV
- `POST /api/admin/clear-attempts`：清空当前题库（当前 `quiz_id`）数据
- `POST /api/admin/shutdown`：安全关闭服务
- `GET /api/admin/ai-health`：AI 健康检查
- `POST /api/admin/ai-config`：保存 AI 配置（endpoint/key/model）
- `GET /api/admin/admin-summary` / `POST /api/admin/admin-summary`：获取/生成全班总结（可结合匹配 PDF 的文本作为上下文）
- `POST /api/admin/pdfs/upload`：上传课件 PDF
- `POST /api/admin/pdfs/delete`：删除课件 PDF
- `POST /api/admin/pdfs/rename`：重命名课件 PDF

## 会话恢复
- 学生 cookie（`student_token`）有效期 7 天，关闭浏览器后仍可恢复
- 学生信息（姓名、学号、班级）缓存在 localStorage，下次访问自动填充
- 同一学号重新加入时，服务器自动复用已有的 `in_progress` attempt，不会创建重复记录
- `/join` 页面加载时自动检测已有会话，有则直接跳转到 `/quiz` 或 `/result`

## 安全措施
- 学生提交的字段全部使用 HTML 转义（`escapeHTML`），防止存储型 XSS
- CSV 导出使用 `safeCSV` 防止公式注入（`=`、`+`、`-`、`@` 开头的单元格前缀 `'`）
- 移除了通配符 CORS（`Access-Control-Allow-Origin: *`），全部为同源访问
- Cookie 设置 `HttpOnly` + `SameSite=Lax`

## 数据库优化
- SQLite 启用 WAL 模式（`journal_mode=WAL`）+ `busy_timeout=5000`
- 索引：`idx_attempts_quiz_status`、`idx_attempts_lookup`、`idx_answers_attempt`
- 唯一约束索引：`idx_attempts_one_active`（防止同一学生同一题库同时存在多个进行中的 attempt）

## 数据流
1. 管理员登录并加载 YAML 题库
2. 管理员开启入口
3. 学生访问 `/join`，前端先检查已有会话（`/api/me`），无则填写信息加入
4. 服务器检查是否已有同学号的进行中 attempt，有则复用并更新 session token
5. 学生作答：选择题实时保存，简答题在提交时自动批量保存
6. 学生提交后状态锁定，查看结果并可导出 PDF/图片
7. 管理员可查看全班答题情况、导出精简 CSV

## 扩展原则
- 新题型：扩展 `domain.QuestionType` 与判分逻辑
- 新数据库：实现 `store.Store` 接口
- 新 AI 厂商：替换 `internal/ai` 的 HTTP 适配
- 多班级并行：当前为单活跃题库架构，如需多班同时答题需引入 `quiz_instance` 概念
