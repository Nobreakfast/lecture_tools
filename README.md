# 课程助手（课堂统计与签到）

## 功能
- 固定学生入口 `/join`
- 管理员可随时开启/关闭新进入
- 学生无需账号，自动恢复答题进度（cookie 7 天有效 + localStorage 缓存）
- 同一学号自动复用进行中的答题会话，防止重复记录
- YAML 题库加载，支持图片路径
- 选择题实时保存，简答题提交时自动批量保存（无需手动保存）
- 同一学生支持按提交次数记录“第几次尝试”
- 结果页显示逐题反馈与 AI 学习建议
- 答案安全保护：学生答题过程中无法通过开发者工具查看正确答案
- CSV 导出精简格式（每题一列仅显示作答），带 BOM 头兼容 Excel

## 安全特性
- 防存储型 XSS：所有学生输入在管理员页面经过 HTML 转义
- 防 CSV 公式注入：导出单元格首字符为 `=+\-@` 时自动添加前缀
- 移除通配符 CORS，仅支持同源访问
- Cookie：`HttpOnly` + `SameSite=Lax`，有效期 7 天
- 同一学号自动复用进行中的答题会话，数据库唯一索引防重复
- SQLite 启用 WAL 模式 + 索引优化

## 启动
```bash
go mod tidy
go run ./cmd/server
```

## 编译
```bash
make build
```

编译产物会放到 `./bin` 目录：
- `bin/server`
- `bin/migrate`
- `bin/qrgen`

默认地址：
- 学生入口：自动使用本机局域网 IP（如 `http://192.168.x.x:8080/join`）
- 管理员：自动使用本机局域网 IP（如 `http://192.168.x.x:8080/admin`）
- 管理员密码：`admin123`

## 数据库迁移

从旧版本升级时，**先迁移再启动新版服务**：

```bash
# 在云服务器上执行，指定数据库文件路径
go run ./cmd/migrate/ ./data/app.db
```

迁移内容：
- 启用 SQLite WAL 模式
- 自动清理重复的 in_progress 记录（保留最新一条，其余标记为已提交）
- 创建查询索引和唯一约束索引

新部署无需迁移，`Init` 会自动完成。

## 命令行工具
网址转二维码工具：

```bash
go run ./cmd/qrgen -url "https://example.com" -out "./qrcode.png" -size 256
```

参数说明：
- `-url` 必填，且仅支持 `http/https`
- `-out` 可选，输出图片路径，默认 `qrcode.png`
- `-size` 可选，二维码尺寸，范围 `64-2048`

## 环境变量
- `APP_ADDR` 默认 `0.0.0.0:8080`
- `APP_BASE_URL` 默认自动推导为 `http://局域网IP:端口`
- `ADMIN_PASSWORD` 默认 `admin123`
- `DATA_DIR` 默认 `./data`
- `CERT_PATH` 可选，证书目录；设置后自动读取目录下首个 `*.pem` 与 `*.key` 启用 HTTPS，若证书不存在则自动回退 HTTP
- `AI_ENDPOINT` 可选，AI 服务地址
- `AI_API_KEY` 可选，AI 鉴权密钥
- `AI_MODEL` 可选，模型名

示例：
```bash
APP_ADDR=0.0.0.0:8080 \
APP_BASE_URL=https://192.168.1.10:8080 \
ADMIN_PASSWORD=你的管理员密码 \
CERT_PATH=./certs \
AI_ENDPOINT=https://your-api-endpoint \
AI_API_KEY=your_key \
AI_MODEL=your_model \
go run ./cmd/server
```

## 题库
参考 `examples/quiz.sample.yaml`。

题库规则：
- 顶层必须包含 `quiz_id`、`title`、`questions`
- 可选 `sampling.groups` 配置分组抽题（按 `tag` 抽 `pick` 道）
- 每题必须有唯一 `id`、`type`、`stem`
- `type` 仅支持：`single_choice`、`multi_choice`、`yes_no`、`survey`、`short_answer`
- `single_choice`/`multi_choice`/`yes_no`/`survey` 需配置 `options`（至少 2 个）
- `single_choice`/`yes_no` 必须配置 `correct_answer` 且命中选项
- `multi_choice` 必须配置 `correct_answer`，格式为英文逗号分隔的选项 key（如 `A,C,D`）
- 选项可配置 `image` 字段（可与文本同时存在，或仅图片）
- 每题可配置 `explanation` 作为结果页解析；`short_answer` 可额外配置 `reference_answer`
- `survey` 与 `short_answer` 不需要 `correct_answer`，不参与判分和 AI 总结
- 使用分组抽题时，题目可设置 `pool_tag` 归入题池；未设置 `pool_tag` 的题会固定出现
- 有图片时使用 `image` 字段，服务会优先读取 `QUIZ_ASSETS_DIR`（默认 `./quiz/assets`），其次读取 `DATA_DIR/assets`
- 系统会对选择题选项做“按学生会话稳定打乱”，同一学生重进页面顺序保持一致

## 管理员查看
- 管理员登录后可查看学生列表、状态和得分
- 学生记录支持展示“第几次尝试”（仅提交后计次）
- 点击“查看”可看每题学生选择、正确答案、是否答对
- 点击“导出全班 CSV”可做课后整理统计，并包含每题题干、选项内容与学生作答
- 点击“清空本次答题数据”仅清空当前已加载题库（当前 `quiz_id`）的数据

## 架构文档
- `docs/architecture.md`
- `docs/api-spec.md`
- `docs/adr/0001-tech-stack.md`

## Project Rules（Trae IDE）
- 规则文件：`.trae/rules.md`
- 所有新增功能或特色能力都要同步更新本 README。
- 任何实现都应遵循既有架构，不破坏初始设计思路。
