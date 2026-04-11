# 课堂答题、材料与作业提交系统架构说明

## 目标
- 固定学生答题入口 `/join`
- 固定学生作业入口 `/submit`
- 管理员动态开启/关闭新进入答题
- 学生匿名会话并可恢复进度
- 实时保存答题
- 提交答题后不可修改答案
- 按课程管理教学材料
- 按课程上传作业 PDF，并让学生按课程/作业提交文件
- 输出逐题反馈与 AI 学习建议

## 分层
- `cmd/server`：进程入口与环境配置
- `internal/app`：HTTP 路由与用例编排
- `internal/domain`：核心领域模型
- `internal/store`：持久化接口与 SQLite 实现
- `internal/quiz`：YAML 题库解析与校验
- `internal/ai`：AI 总结适配层（含规则兜底）

## 核心状态
- 入口开关：`settings.entry_open`
- 学生作答：`attempts.status`
  - `in_progress`
  - `submitted`
- 作业提交：`homework_submissions`
  - 不再使用 `finalized` 状态
  - 以 `(course, assignment_id, student_no)` 为唯一逻辑键
  - 始终保存当前最新上传文件

## 关键约束
- 学生答题入口 URL 固定为 `/join`
- 学生作业入口 URL 固定为 `/submit`
- `entry_open=false` 时禁止新会话创建
- 已有答题会话可继续答题与提交
- `submitted` 状态禁止修改答案
- `survey` 题不参与判分与总结
- 同一学生（`quiz_id + student_no`）只允许一个 `in_progress` 状态的 attempt（数据库唯一索引保证）
- `/api/me` 不返回 `correct_answer`、`explanation`、`reference_answer` 等敏感字段，防止学生通过开发者工具查看答案
- 提交时自动保存所有未保存的简答题，无需手动保存
- 作业系统与当前加载 quiz 解耦；不再依赖 `currentQuiz/currentCourse` 作为作业作用域
- 同一学生在同一 `course + assignment_id` 下只保留一条作业提交记录，后续进入时复用该记录
- 作业 PDF 的文件名（去掉 `.pdf`）就是 `assignment_id`
- 学生上传报告/代码后立即保存，最新上传覆盖旧文件；没有单独的 `/api/homework/finalize`
- `_homework` 目录中的作业 PDF 不进入普通 materials 列表，也不能通过通用 `/ppt/`、`/materials-files/` 学生路径直接访问

## 路由与资源

页面路由：
- `/`、`/join`：答题入口页
- `/submit`：作业提交页
- `/quiz`：答题页
- `/result`：结果页
- `/admin`：管理后台
- `/materials`：课程材料页（PDF 可预览，其他文件仅下载）
- `/pdf`：旧课件页入口，当前复用材料页

静态资源：
- `/assets/<path>`：题库图片资源。按顺序查找：
  - `QUIZ_ASSETS_DIR`（默认 `./quiz/assets`）
  - `${DATA_DIR}/assets`（默认 `./data/assets`）
- `/ppt/<folder>/<file>.pdf`：课程材料中的 PDF 预览路径，根目录为 `../ppt`
- `/materials-files/<folder>/<file>`：课程材料下载路径（PDF 也可通过该路径强制下载）
- `/uploads/<...>`：学生简答题图片上传后的访问路径，文件写入 `${DATA_DIR}/quiz/...`

作业相关资源：
- 管理员上传的作业 PDF 存储在 `../ppt/_homework/<course>/<assignment_id>.pdf`
- 学生通过 `/api/homework/assignment-pdf?course=...&assignment_id=...` 预览作业 PDF
- 学生作业文件存储在 `${DATA_DIR}/homework/<course>/<assignment_id>/<student_no>/<submission_id>/`
  - `report.pdf`
  - `code.zip`
- 管理员通过 `/api/admin/homework/report`、`/api/admin/homework/code`、`/api/admin/homework/archive` 读取学生提交文件

## API 概览

学生答题接口：
- `POST /api/join`：加入课堂（仅入口开放时允许）
- `GET /api/entry-status`：查询入口是否开放
- `GET /api/me`：获取当前会话、题库（脱敏）与已保存答案
- `POST /api/answer`：保存答案（选择题实时保存；提交后禁止修改）
- `POST /api/answer-image`：上传简答题图片（仅 JPEG/PNG，提交后禁止）
- `POST /api/submit`：提交并锁定答题记录
- `GET /api/result`：获取逐题反馈与得分
- `POST /api/ai-summary`：生成/获取 AI 学习建议（可缓存）
- `POST /api/retry`：提交后发起“再做一次”（创建新的 `in_progress` attempt，并发新 cookie）

学生作业接口：
- `GET /api/homework/courses`：列出当前已有作业 PDF 的课程
- `GET /api/homework/assignments?course=...`：列出该课程下可选作业
- `GET /api/homework/assignment-pdf?course=...&assignment_id=...`：预览作业 PDF
- `POST /api/homework/session`：进入/恢复某个课程作业提交记录
- `GET /api/homework/submission`：读取当前 `homework_token` 对应的作业提交状态
- `POST /api/homework/upload`：上传或替换作业文件槽位（`report` / `code`）
- `POST /api/homework/delete`：删除某个作业文件槽位

管理员接口（均需登录）：
- `POST /api/admin/login`：登录
- `GET /api/admin/state`：入口状态、人数统计、当前题库信息
- `POST /api/admin/entry`：开启/关闭入口
- `POST /api/admin/load-quiz`：加载题库（粘贴 YAML / 上传文件 / 指定服务器文件路径）
- `GET /api/admin/quiz-files`：列出服务器上的题库文件（用于下拉选择）
- `GET /api/admin/live`：SSE 实时推送人数与入口状态
- `GET /api/admin/attempts`：学生答题列表（按当前 `quiz_id` 过滤）
- `GET /api/admin/attempt-detail?id=...`：单个学生答题详情
- `GET /api/admin/export-csv`：导出 CSV
- `POST /api/admin/clear-attempts`：清空当前题库（当前 `quiz_id`）数据
- `POST /api/admin/shutdown`：安全关闭服务
- `GET /api/admin/ai-health`：AI 健康检查
- `POST /api/admin/ai-config`：保存 AI 配置（endpoint/key/model）
- `GET /api/admin/admin-summary` / `POST /api/admin/admin-summary`：获取/生成全班总结（可结合匹配 PDF 的文本作为上下文）
- `GET /api/materials`：列出课程材料分组（按 `folder + stem` 聚合）
- `GET /api/pdfs`：兼容旧页面的 PDF 扁平列表
- `POST /api/admin/pdfs/upload`：上传课程材料（支持多文件；部分成功时逐文件返回结果）
- `POST /api/admin/pdfs/delete`：删除单个课程材料文件
- `POST /api/admin/pdfs/rename`：重命名单个课程材料文件
- `POST /api/admin/pdfs/visibility`：切换课程材料是否对学生可见
- `GET /api/admin/homework/assignments`：按课程列出已启用作业 PDF
- `POST /api/admin/homework/assignments/upload`：按课程上传作业 PDF（文件名即作业编号）
- `POST /api/admin/homework/assignments/delete`：删除某个课程作业 PDF
- `GET /api/admin/homework/submissions`：按 `course`、`assignment_id` 筛选学生作业提交记录
- `GET /api/admin/homework/submission?id=...`：查看单个学生作业提交详情
- `GET /api/admin/homework/report?id=...`：查看或下载该学生的报告 PDF
- `GET /api/admin/homework/code?id=...`：下载该学生的代码 ZIP
- `GET /api/admin/homework/archive?id=...`：打包下载该学生的全部作业文件

## 会话恢复
- 学生答题 cookie（`student_token`）有效期 7 天，关闭浏览器后仍可恢复
- 学生信息（姓名、学号、班级）缓存在 localStorage，下次访问自动填充
- 同一学号重新加入时，服务器自动复用已有的 `in_progress` attempt，不会创建重复记录
- `/join` 页面加载时自动检测已有会话，有则直接跳转到 `/quiz` 或 `/result`

作业会话恢复：
- 作业系统使用独立 cookie：`homework_token`
- `/submit` 页面会缓存最近一次选择的课程、作业编号以及姓名/学号/班级
- 作业提交记录按 `(course, assignment_id, student_no)` 复用
- 如果学号相同但姓名/班级不匹配，且当前浏览器并不持有该提交记录的 `homework_token`，服务器会拒绝接管该记录

## 安全措施
- 学生提交的字段全部使用 HTML 转义（`escapeHTML`），防止存储型 XSS
- CSV 导出使用 `safeCSV` 防止公式注入（`=`、`+`、`-`、`@` 开头的单元格前缀 `'`）
- 移除了通配符 CORS（`Access-Control-Allow-Origin: *`），全部为同源访问
- Cookie 设置 `HttpOnly` + `SameSite=Lax`
- 作业 PDF 上传会校验文件内容是否为 PDF，而不只依赖扩展名
- 学生作业 ZIP 上传会校验文件内容是否为 ZIP，而不只依赖扩展名
- `_homework` 目录中的作业 PDF 不会被普通学生 materials 路由直接暴露

## 数据库优化
- SQLite 启用 WAL 模式（`journal_mode=WAL`）+ `busy_timeout=5000`
- 答题索引：`idx_attempts_quiz_status`、`idx_attempts_lookup`、`idx_answers_attempt`
- 答题唯一约束索引：`idx_attempts_one_active`
- 作业提交索引：`idx_homework_submissions_lookup`、`idx_homework_submissions_assignment`
- 作业提交唯一键：`UNIQUE(course, assignment_id, student_no)`

## 数据流
答题数据流：
1. 管理员登录并加载 YAML 题库
2. 管理员开启入口
3. 学生访问 `/join`，前端先检查已有会话（`/api/me`），无则填写信息加入
4. 服务器检查是否已有同学号的进行中 attempt，有则复用并更新 session token
5. 学生作答：选择题实时保存，简答题在提交时自动批量保存
6. 学生提交后状态锁定，查看结果并可导出 PDF/图片
7. 管理员可查看全班答题情况、导出精简 CSV

作业数据流：
1. 管理员在 `/admin` 的“作业提交”页签中选择课程并上传作业 PDF
2. 服务把作业 PDF 保存到 `../ppt/_homework/<course>/<assignment_id>.pdf`
3. 学生访问 `/submit`，先读取可用课程，再读取该课程下可用作业列表
4. 学生预览作业 PDF，填写姓名/学号/班级后进入该作业提交记录
5. 服务按 `(course, assignment_id, student_no)` 查找或创建 homework submission，并通过 `homework_token` 绑定浏览器会话
6. 学生上传报告 PDF 或代码 ZIP 时，服务立即覆盖固定槽位文件并更新数据库元数据
7. 管理员按课程/作业筛选学生提交，查看详情并下载 PDF、ZIP 或整包归档

## 文件系统布局（默认）
```text
./data/
  app.db
  assets/
  quiz/
    <class>/<quiz_id>/<name_studentNo>/...
  homework/
    <course>/<assignment_id>/<student_no>/<submission_id>/
      report.pdf
      code.zip
  autocert/

./ppt/
  <course>/...
  _homework/
    <course>/<assignment_id>.pdf

./quiz/
  assets/
  <course>/*.yaml
```

## 扩展原则
- 新题型：扩展 `domain.QuestionType` 与判分逻辑
- 新数据库：实现 `store.Store` 接口
- 新 AI 厂商：替换 `internal/ai` 的 HTTP 适配
- 多班级并行：当前为单活跃题库架构，如需多班同时答题需引入 `quiz_instance` 概念
- 若未来需要对作业增加截止时间、评分、版本历史或隐藏草稿，应在当前 `course + assignment_id + student_no` 模型之上增加显式作业元数据层，而不是重新耦合到当前 quiz 状态
