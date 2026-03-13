# 题库管理

## 目录结构

```
quiz/
├── assets/             ← 共享图片目录（服务器 QUIZ_ASSETS_DIR 默认指向此处）
│   └── *.svg           ← 各课程图片的符号链接
├── 最优化方法/
│   ├── assets/         ← 该课程原始图片
│   ├── tools/          ← 题目生成脚本
│   ├── week2_l1.yaml
│   └── week2_l2.yaml
├── 机器人控制技术/      ← 待添加
└── README.md
```

## 使用方式

1. 按课程名称创建子目录，YAML 题库文件放在对应课程目录下
2. 题目中用到的图片放在 `课程名/assets/` 下
3. 在 `quiz/assets/` 中创建符号链接指向课程图片，以便服务器能找到：
   ```bash
   cd quiz/assets
   ln -sf ../课程名/assets/图片文件.svg .
   ```
4. 管理员在后台加载 YAML 时，粘贴或上传对应课程的 YAML 文件即可

## 新课程添加示例

以"机器人控制技术"为例：

```bash
mkdir -p quiz/机器人控制技术/assets
# 编写题库
vim quiz/机器人控制技术/week1.yaml
# 如有图片，符号链接到共享目录
cd quiz/assets && ln -sf ../机器人控制技术/assets/*.svg .
```

## 题库规则

- 顶层必须包含 `quiz_id`、`title`、`questions`
- `quiz_id` 建议使用课程缩写+周次格式，如 `optim_week2_l1`、`robot_week1`
- 题型：`single_choice`、`multi_choice`、`yes_no`、`survey`、`short_answer`
- 图片文件名建议带课程前缀（如 `w2l2_epi_A.svg`），避免跨课程冲突
- 详细规则参见项目根目录 `README.md` 和 `examples/quiz.sample.yaml`
