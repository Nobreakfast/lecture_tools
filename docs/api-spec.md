# API 说明

## 学生接口
- `POST /api/join`
  - body: `name, student_no, class_name`
  - 仅在入口开放时可创建会话
- `GET /api/entry-status`
  - 返回入口是否开放
- `GET /api/me`
  - 返回当前学生、题库、已有答案
- `POST /api/answer`
  - body: `question_id, answer`
  - 提交后返回禁止修改
- `POST /api/submit`
  - 结束作答并锁定
- `GET /api/result`
  - 获取逐题反馈与 AI 建议

## 管理员接口
- `POST /api/admin/login`
  - body: `password`
- `GET /api/admin/state`
  - 返回入口状态、人数统计、当前题库标题
- `POST /api/admin/entry`
  - body: `open: bool`
- `POST /api/admin/load-quiz`
  - JSON: `yaml` 或 multipart 文件上传
- `GET /api/admin/live`
  - SSE 实时推送状态与人数
- `GET /api/admin/attempts`
  - 返回学生列表、状态、得分
- `GET /api/admin/attempt-detail?id=...`
  - 返回单个学生逐题答案与正误
- `GET /api/admin/export-csv`
  - 下载课后整理 CSV
