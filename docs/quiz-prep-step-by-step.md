# 题库准备 Step-by-Step（给老师 + Trae/Cursor）

本文档用于把“我描述每道题要求”快速转成可导入的 YAML 题库。

## 0) 先确认本节课目标

- 是`知识检测`（需要判分）还是`课堂调研`（不判分）。
- 题目数量、课程名、周次（例如 `week4_l1`）。

## 1) 选对题型（最重要）

- `single_choice`：单选，**必须**有 `correct_answer`
- `multi_choice`：多选，**必须**有 `correct_answer`（如 `A,C,D`）
- `yes_no`：是非题，**必须**有 `correct_answer`
- `survey`：问卷题，默认单选；可加 `allow_multiple: true` 变成多选问卷（不判分）
- `short_answer`：简答题，不判分，可选 `reference_answer`；可选 `short_answer_mode`：`text`（仅文字）/ `image`（仅上传图片）/ `code`（仅代码）/ `text_image`（文字+图片），省略则根据题干自动检测

## 2) 先写“自然语言需求”

按下面模板把每题意图告诉 Cursor（不必先写 YAML）：

- 课程：`最优化方法`
- 文件名：`week4_l1.yaml`
- 第1题：题型 + 题干 + 选项方向 + 是否判分
- 第2题：...

建议每题至少给出：

- 出题目的（想测什么/想调查什么）
- 选项风格（偏理论、偏应用、偏职业等）
- 是否允许多选

## 3) 让 AI 直接生成 YAML

生成目标目录：

- `quiz/课程名/weekX_lY.yaml`

基础结构：

- 顶层：`quiz_id`、`title`、`questions`
- 每题：`id`、`type`、`stem`
- 选择类题目：`options`（至少 2 个）

## 4) 用规则做一次“出题体检”

生成后逐项检查：

- 题型与字段是否匹配（尤其 `correct_answer` 是否应存在）
- `correct_answer` 是否命中选项 key
- `id` 是否唯一（建议 `课程缩写_wXlY_qN`）
- 问卷题是否错误地写成判分题

## 5) 管理端导入前的快速校验

- 本地打开 YAML，确认缩进与引号正确
- 管理端加载题库（支持：粘贴 YAML / 上传文件 / 选择服务器上的题库文件），检查是否报字段错误
- 学生端试答 1 次，确认单选/多选交互正常

## 6) 课堂使用建议

- 一套题中混合：`判分题 + survey + short_answer`
- 最后一题固定留一个开放反馈，便于课后改进
- 职业/兴趣调研优先用 `survey`，避免误计分

## 7) 变更同步

- 只要题型规则或能力变化，更新：
  - `README.md`（题库规则）
  - 本文档（操作流程）

## 图片与资源路径（容易踩坑）

题目与选项的 `image` 字段使用“相对文件名/相对路径”，服务端通过 `/assets/<image>` 提供访问，并按顺序查找：

1. `QUIZ_ASSETS_DIR`（默认 `./quiz/assets`，服务启动时会自动创建）
2. `${DATA_DIR}/assets`（默认 `./data/assets`，服务启动时会自动创建）

建议做法：
- 若图片属于题库的一部分且希望纳入版本管理：放到 `./quiz/assets/`，在 YAML 中写 `image: 文件名.svg`
- 若图片属于运行期材料或不希望进仓库：放到 `./data/assets/`，同样写 `image: 文件名.svg`

## 8) 常用提示词（可直接复制给 Cursor）

```text
我要为《最优化方法》生成 week4_l1.yaml。
我只描述每道题意图，你负责：
1) 选择正确题型并生成合法 YAML
2) 严格满足本项目题库规则（correct_answer、allow_multiple 等）
3) 输出到 quiz/最优化方法/week4_l1.yaml
4) 若我的描述和规则冲突，请先按规则修正并说明原因
```
