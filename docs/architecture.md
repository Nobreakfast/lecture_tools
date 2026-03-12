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

## 数据流
1. 管理员登录并加载 YAML 题库
2. 管理员开启入口
3. 学生访问 `/join` 进入，创建匿名会话并写入 cookie
4. 学生作答实时保存到 `answers`
5. 学生提交后写入 `summaries`，状态锁定
6. 学生查看结果并导出

## 扩展原则
- 新题型：扩展 `domain.QuestionType` 与判分逻辑
- 新数据库：实现 `store.Store` 接口
- 新 AI 厂商：替换 `internal/ai` 的 HTTP 适配
