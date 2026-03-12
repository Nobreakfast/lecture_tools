# 课程助手（课堂统计与签到）

## 功能
- 固定学生入口 `/join`
- 管理员可随时开启/关闭新进入
- 学生无需账号，自动恢复答题进度
- YAML 题库加载，支持图片路径
- 实时保存答题并在提交后锁定
- 同一学生支持按提交次数记录“第几次尝试”
- 结果页显示逐题反馈与 AI 学习建议

## 启动
```bash
go mod tidy
go run ./cmd/server
```

默认地址：
- 学生入口：自动使用本机局域网 IP（如 `http://192.168.x.x:8080/join`）
- 管理员：自动使用本机局域网 IP（如 `http://192.168.x.x:8080/admin`）
- 管理员密码：`admin123`

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
- `AI_ENDPOINT` 可选，AI 服务地址
- `AI_API_KEY` 可选，AI 鉴权密钥
- `AI_MODEL` 可选，模型名

示例：
```bash
APP_ADDR=0.0.0.0:8080 \
APP_BASE_URL=http://192.168.1.10:8080 \
ADMIN_PASSWORD=你的管理员密码 \
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
