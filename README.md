# 课程助手（课堂答题、材料与作业提交）

面向课堂现场的轻量教学辅助系统：固定入口、可控放行、匿名恢复、YAML 题库、课程材料管理、作业提交与 AI 学习建议。

## 设计灵感
- 教室里最需要的是“稳定 + 可控”：入口固定、老师随时开关、学生无需注册即可继续作答或提交作业
- 题库要能被老师自己维护：YAML 可读可改，可复制粘贴导入
- 教学材料和作业说明要能按课程管理，不依赖额外前端构建或对象存储
- 答案要尽量“看不到”：学生端接口不下发正确答案字段，降低通过开发者工具偷看风险
- 部署要足够轻：Go + SQLite 单机运行，不依赖外部数据库或前端构建流程

## 功能
- 固定学生答题入口 `/join`
- 固定学生作业入口 `/submit`
- 管理员可随时开启/关闭新进入答题
- 学生无需账号，自动恢复答题进度（cookie 7 天有效 + localStorage 缓存）
- 同一学号自动复用进行中的答题会话，防止重复记录
- YAML 题库加载，支持图片路径
- 选择题实时保存，简答题提交时自动批量保存（无需手动保存）
- 简答题支持按 `short_answer_mode` 显式区分：`text`（纯文本）、`image`（仅图像上传）、`code`（代码文本）
- 简答题图像上传支持 JPEG/PNG
- 同一学生支持按提交次数记录“第几次尝试”
- 结果页显示逐题反馈与 AI 学习建议（可缓存）
- 全班总结：管理端可生成课程整体复盘（可选结合匹配 PDF 的文本上下文）
- CSV 导出精简格式（每题一列仅显示作答），带 BOM 头兼容 Excel
- 课程材料页 `/materials`：从 `../ppt/<course>/` 读取材料；仅 PDF 支持预览，其他文件仅下载
- 管理员材料上传支持多文件、部分成功、单文件重命名/删除，以及按文件控制“学生可见/隐藏”
- 作业系统支持按课程上传作业 PDF：文件名去掉 `.pdf` 后即为作业编号
- 学生在 `/submit` 中按“课程 + 作业编号”选择作业，可先预览作业 PDF，再上传报告 PDF 与代码 ZIP
- 作业上传即保存为当前最新版本，不需要额外点击“确认提交”
- 支持 `golang.org/x/crypto/acme/autocert` 自动签发与续签 HTTPS 证书

## 页面与主要入口
- 学生答题入口：`/join`
- 学生作业入口：`/submit`
- 课程材料页：`/materials`
- 管理后台：`/admin`

默认地址：
- 学生答题：自动使用本机局域网 IP（如 `http://192.168.x.x:8080/join`）
- 学生作业：自动使用本机局域网 IP（如 `http://192.168.x.x:8080/submit`）
- 课程材料：自动使用本机局域网 IP（如 `http://192.168.x.x:8080/materials`）
- 管理员：自动使用本机局域网 IP（如 `http://192.168.x.x:8080/admin`）
- 管理员密码：默认 `admin123`（课堂外网/线上部署务必修改 `ADMIN_PASSWORD`）

## 作业提交流程
- 管理员先在 `/admin` 的“作业提交”页签中选择课程，并上传作业 PDF
- 上传后的 PDF 文件名（去掉 `.pdf` 扩展名）就是作业编号，例如 `task_1.pdf` -> `task_1`
- 学生进入 `/submit` 后，先选择课程，再从服务器返回的可用作业列表中选择作业编号
- 学生可直接预览该作业 PDF，然后填写姓名、学号、班级并进入当前作业
- 学生上传报告 PDF 会覆盖当前 `report.pdf`，上传代码 ZIP 会覆盖当前 `code.zip`
- 同一学生在同一 `课程 + 作业编号` 下始终复用同一条提交记录；系统保存的是当前最新文件

## 安全特性
- 防存储型 XSS：所有学生输入在管理员页面经过 HTML 转义
- 防 CSV 公式注入：导出单元格首字符为 `=+\-@` 时自动添加前缀
- 移除通配符 CORS，仅支持同源访问
- Cookie：`HttpOnly` + `SameSite=Lax`，有效期 7 天
- 同一学号自动复用进行中的答题会话，数据库唯一索引防重复
- SQLite 启用 WAL 模式 + 索引优化
- 作业 PDF 上传校验文件内容是否为 PDF，不只依赖扩展名
- 作业代码上传校验文件内容是否为 ZIP，不只依赖扩展名
- `_homework` 目录中的作业 PDF 不会出现在普通 materials 学生页面中，也不能通过通用材料下载路径直接访问

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

## 文件存储位置

默认情况下，应用主要会把文件写到 `DATA_DIR`（默认 `./data`）及其同级的 `ppt` 目录：

- SQLite 数据库：`${DATA_DIR}/app.db`
- 题库图片静态资源：
  - 优先读取 `QUIZ_ASSETS_DIR`（默认 `./quiz/assets`）
  - 其次读取 `${DATA_DIR}/assets`
- 学生简答题上传图片：`${DATA_DIR}/quiz/<class>/<quiz_id>/<name_studentNo>/...`
- 课程材料：`../ppt/<course>/...`
- 作业 PDF（老师上传供学生预览）：`../ppt/_homework/<course>/<assignment_id>.pdf`
- 学生作业提交文件：`${DATA_DIR}/homework/<course>/<assignment_id>/<student_no>/<submission_id>/`
  - 报告固定保存为 `report.pdf`
  - 代码固定保存为 `code.zip`
- `autocert` 证书缓存（若启用）：`${DATA_DIR}/autocert`

其中：
- `/uploads/...` 对应的是学生简答题图片访问路径，磁盘文件位于 `${DATA_DIR}/quiz/...`
- `/ppt/...` 与 `/materials-files/...` 对应的是课程材料目录 `../ppt/...`
- `../ppt/_homework/...` 下的作业 PDF 不会出现在普通 `/materials` 列表中，也不能被学生通过通用 `/ppt/`、`/materials-files/` 直接访问

## 数据库迁移

从旧版本升级时，**先迁移再启动新版服务**：

```bash
# 在云服务器上执行，指定数据库文件路径
go run ./cmd/migrate/ ./data/app.db
```

当前 `cmd/migrate` 主要处理的是答题记录（attempts）相关迁移，内容包括：
- 启用 SQLite WAL 模式
- 自动清理重复的 `in_progress` 记录（保留最新一条，其余标记为已提交）
- 创建查询索引和唯一约束索引

说明：
- 新部署无需执行该命令，直接启动服务即可，`Init` 会自动创建所需表结构
- 新版作业提交表结构（`homework_submissions`）由服务启动时自动初始化/切换，不依赖这个独立迁移命令

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
- `APP_ADDR` 默认 `0.0.0.0:8080`；若检测到证书并启用 HTTPS，默认改为 `0.0.0.0:443`
- `APP_BASE_URL` 默认自动推导；HTTPS 模式下为 `https://局域网IP:端口`
- `ADMIN_PASSWORD` 默认 `admin123`
- `DATA_DIR` 默认 `./data`
- `QUIZ_ASSETS_DIR` 默认 `./quiz/assets`（题库图片目录）
- `CERT_PATH` 可选，证书目录；设置后自动读取目录下首个 `*.pem` 与 `*.key` 启用 HTTPS，若证书不存在则自动回退 HTTP
- `AUTOCERT_ENABLE` 可选，`true/1/yes/on` 启用 `autocert` 自动签发与续签；启用后使用 443 监听 HTTPS
- `AUTOCERT_HOSTS` 可选，逗号分隔域名白名单（如 `a.example.com,b.example.com`）；未设置时尝试从 `APP_BASE_URL` 自动提取
- `AUTOCERT_EMAIL` 可选，证书注册邮箱
- `AUTOCERT_CACHE_DIR` 可选，证书缓存目录；默认 `${DATA_DIR}/autocert`
- `APP_HTTP_REDIRECT_ADDR` 可选，仅在 HTTPS 启用时生效；手动证书默认 `:8080`，`autocert` 默认 `:80`，自动 301 跳转到 HTTPS
- `AUTOCERT_ENABLE=true` 且域名不可签发（如 `localhost`、`127.0.0.1`、局域网 IP）时，服务会自动回退到原有 HTTP/手动证书策略
- `AI_ENDPOINT` 可选，AI 服务地址
- `AI_API_KEY` 可选，AI 鉴权密钥
- `AI_MODEL` 可选，模型名

示例：
```bash
APP_ADDR=0.0.0.0:443 \
APP_BASE_URL=https://192.168.1.10:443 \
ADMIN_PASSWORD=你的管理员密码 \
CERT_PATH=./certs \
APP_HTTP_REDIRECT_ADDR=:8080 \
AI_ENDPOINT=https://your-api-endpoint \
AI_API_KEY=your_key \
AI_MODEL=your_model \
go run ./cmd/server
```

`autocert` 示例：
```bash
APP_ADDR=0.0.0.0:443 \
APP_BASE_URL=https://your.domain.com \
AUTOCERT_ENABLE=true \
AUTOCERT_HOSTS=your.domain.com \
AUTOCERT_EMAIL=you@example.com \
APP_HTTP_REDIRECT_ADDR=:80 \
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
- `survey` 可选 `allow_multiple: true` 表示问卷多选（不判分）
- `single_choice`/`yes_no` 必须配置 `correct_answer` 且命中选项
- `multi_choice` 必须配置 `correct_answer`，格式为英文逗号分隔的选项 key（如 `A,C,D`）
- 选项可配置 `image` 字段（可与文本同时存在，或仅图片）
- 每题可配置 `explanation` 作为结果页解析；`short_answer` 可额外配置 `reference_answer`
- `short_answer` 可选 `short_answer_mode`：`text`（仅文本）、`image`（仅图片上传）、`code`（仅代码文本）；不填时保持旧规则兼容
- `survey` 与 `short_answer` 不需要 `correct_answer`，不参与判分和 AI 总结
- 使用分组抽题时，题目可设置 `pool_tag` 归入题池；未设置 `pool_tag` 的题会固定出现
- 有图片时使用 `image` 字段，服务会优先读取 `QUIZ_ASSETS_DIR`（默认 `./quiz/assets`），其次读取 `DATA_DIR/assets`
- 系统会对选择题选项做“按学生会话稳定打乱”，同一学生重进页面顺序保持一致

## 题库目录建议（可选）
- 推荐结构：`quiz/课程名/*.yaml`
- 推荐把图片放到 `QUIZ_ASSETS_DIR`（默认 `./quiz/assets`）或 `${DATA_DIR}/assets`，在 YAML 中写 `image: 文件名.svg`
- 若希望每门课单独管理图片，可把 `QUIZ_ASSETS_DIR` 指向某门课的 `assets` 目录（例如 `./quiz/最优化方法/assets`）

## 管理员查看
- 管理员登录后可查看学生列表、状态和得分
- 学生记录支持展示“第几次尝试”（仅提交后计次）
- 点击“查看”可看每题学生选择、正确答案、是否答对
- 点击“导出全班 CSV”可做课后整理统计，并包含每题题干、选项内容与学生作答
- 点击“清空本次答题数据”仅清空当前已加载题库（当前 `quiz_id`）的数据
- 管理员可在“材料管理”中按课程上传材料、控制学生可见性、并对单个材料执行重命名/删除
- 管理员可在“作业提交”中按课程上传作业 PDF、查看已启用作业，以及按课程/作业筛选学生提交记录
- 管理员可查看单个学生提交详情，并下载该学生的报告 PDF、代码 ZIP 或打包下载全部文件

## 架构文档
- `docs/architecture.md`（含 API 概览与文件存储布局）
- `docs/adr/0001-tech-stack.md`
- `docs/quiz-prep-step-by-step.md`（题库准备流程）

## 题库准备（AI 工作流）
- 目标：你只描述每道题“想测什么/想问什么”，Trae/Cursor 直接生成可导入 YAML
- 推荐先看：`docs/quiz-prep-step-by-step.md`
- 最常用规则：
  - 判分类题目（`single_choice`/`multi_choice`/`yes_no`）必须有 `correct_answer`
  - 问卷题用 `survey`；多选问卷用 `allow_multiple: true`（不判分）
  - 反馈题用 `short_answer`（不判分）

## Project Rules（Trae IDE）
- 规则文件：`.trae/rules/requirements.md`
- 所有新增功能或特色能力都要同步更新本 README
- 任何实现都应遵循既有架构，不破坏初始设计思路
