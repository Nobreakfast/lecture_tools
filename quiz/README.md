# week2_l1 题库说明

- 题库文件：`quiz/week2_l1.yaml`
- 生成脚本：`quiz/tools/w2_l1/generate_week2_l1_quiz.py`
- 当前题库包含 8 个题型池，每池 10 道变式，系统按配置每池抽 1 题，共 8 题
- 题干与选项可写 LaTeX（推荐 `\( \)` 或 `$ $`）
- 已内置 2 道固定 `short_answer` 交流题，固定出现在第 9、10 题，不参与判分
- 若要额外加入交流题，可在 `questions` 末尾追加 `short_answer` 且不设置 `pool_tag`
- 追加的不带 `pool_tag` 题目会固定展示，不占用抽题名额
- 题图素材建议放在 `quiz/assets/`，题干图用 `image`，选项图用 `options[].image`（如 `w2l2_epi_A.svg`）
