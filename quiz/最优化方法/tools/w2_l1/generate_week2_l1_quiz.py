from pathlib import Path


ROOT = Path(__file__).resolve().parents[3]
OUTPUT = ROOT / "quiz" / "week2_l1.yaml"


def yq(text):
    return "'" + str(text).replace("'", "''") + "'"


def item(qid, tag, knowledge_tag, stem, options, correct):
    return {
        "id": qid,
        "pool_tag": tag,
        "knowledge_tag": knowledge_tag,
        "type": "single_choice",
        "stem": stem,
        "options": options,
        "correct_answer": correct,
    }


def build_questions():
    questions = []
    thetas = [r"\frac{1}{10}", r"\frac{2}{10}", r"\frac{3}{10}", r"\frac{4}{10}", r"\frac{5}{10}", r"\frac{6}{10}", r"\frac{7}{10}", r"\frac{8}{10}", r"\frac{9}{10}", r"\frac{1}{2}"]
    for i, theta in enumerate(thetas, 1):
        questions.append(
            item(
                f"cset_{i:02d}",
                "凸集定义判定",
                "凸集",
                rf"若对任意 \(x_1,x_2\in S\)，都有 \(\theta x_1+(1-\theta)x_2\in S\)（其中 \(\theta={theta}\) 只是特例），该性质刻画的是哪一类集合？",
                [("A", "凸集"), ("B", "仿射集"), ("C", "锥"), ("D", "超平面")],
                "A",
            )
        )
    for i in range(1, 11):
        questions.append(
            item(
                f"aff_{i:02d}",
                "仿射与凸的区别",
                "仿射集",
                rf"关于 \(x=\theta x_1+(1-\theta)x_2\)，下列哪项正确描述了仿射集与凸集在 \(\theta\) 取值上的区别（变式 {i}）？",
                [
                    ("A", r"凸集: \(\theta\in[0,1]\)，仿射集: \(\theta\in\mathbb{R}\)"),
                    ("B", r"凸集: \(\theta\in\mathbb{R}\)，仿射集: \(\theta\in[0,1]\)"),
                    ("C", r"两者都要求 \(\theta\in[0,1]\)"),
                    ("D", r"两者都要求 \(\theta\in\mathbb{R}\)"),
                ],
                "A",
            )
        )
    ks = [3, 4, 5, 6, 7, 8, 9, 10, 4, 6]
    for i, k in enumerate(ks, 1):
        questions.append(
            item(
                f"comb_{i:02d}",
                "凸组合条件",
                "凸组合",
                rf"设 \(x=\sum_{{i=1}}^{{{k}}}\theta_i x_i\)，且 \(\sum_{{i=1}}^{{{k}}}\theta_i=1\)。若再要求 \(\theta_i\ge 0\)，则该 \(x\) 是什么？",
                [("A", "凸组合"), ("B", "仿射组合"), ("C", "线性组合"), ("D", "锥组合")],
                "A",
            )
        )
    for i in range(1, 11):
        questions.append(
            item(
                f"hull_{i:02d}",
                "凸包概念",
                "凸包",
                rf"集合 \(S\) 的所有凸组合构成的集合通常记作什么（变式 {i}）？",
                [("A", r"\(\mathrm{conv}(S)\)"), ("B", r"\(\mathrm{aff}(S)\)"), ("C", r"\(\mathrm{cone}(S)\)"), ("D", r"\(\mathbb{{R}}^n\)")],
                "A",
            )
        )
    for i in range(1, 11):
        questions.append(
            item(
                f"affh_{i:02d}",
                "仿射包概念",
                "仿射包",
                rf"集合 \(S\) 的所有仿射组合构成的最小仿射集通常记作什么（变式 {i}）？",
                [("A", r"\(\mathrm{aff}(S)\)"), ("B", r"\(\mathrm{conv}(S)\)"), ("C", r"\(\mathrm{cone}(S)\)"), ("D", r"\(\mathcal{{H}}(S)\)")],
                "A",
            )
        )
    lambdas = [2, 3, 4, 5, 6, 7, 8, 9, 10, 12]
    for i, lam in enumerate(lambdas, 1):
        questions.append(
            item(
                f"cone_{i:02d}",
                "锥定义判定",
                "锥",
                rf"若 \(S\neq\emptyset\)，且对任意 \(x\in S\) 与任意 \(\lambda>0\)（如 \(\lambda={lam}\)），都有 \(\lambda x\in S\)，则 \(S\) 至少满足哪一性质？",
                [("A", "锥"), ("B", "凸集"), ("C", "仿射集"), ("D", "多面体")],
                "A",
            )
        )
    for i in range(1, 11):
        questions.append(
            item(
                f"ccone_{i:02d}",
                "凸锥判定",
                "凸锥",
                rf"若集合 \(S\) 同时满足“凸集”与“锥”两个条件，那么 \(S\) 应称为（变式 {i}）？",
                [("A", "凸锥"), ("B", "仿射包"), ("C", "超平面"), ("D", "半空间")],
                "A",
            )
        )
    for i in range(1, 11):
        questions.append(
            item(
                f"psd_{i:02d}",
                "正定半正定判别",
                "正定半正定",
                rf"设 \(A\in\mathbb{{S}}^n\)。若对任意非零向量 \(x\)，有 \(x^\top A x>0\)，则 \(A\) 属于哪类矩阵（变式 {i}）？",
                [("A", "正定矩阵"), ("B", "半正定矩阵"), ("C", "负定矩阵"), ("D", "不定矩阵")],
                "A",
            )
        )
    questions.append(
        {
            "id": "short_01",
            "type": "short_answer",
            "stem": "大家是否喜欢这样的小测形式？",
        }
    )
    questions.append(
        {
            "id": "short_02",
            "type": "short_answer",
            "stem": "大家对课程还有什么问题？建议？期许？",
        }
    )
    return questions


def dump_yaml(questions):
    tags = [
        "凸集定义判定",
        "仿射与凸的区别",
        "凸组合条件",
        "凸包概念",
        "仿射包概念",
        "锥定义判定",
        "凸锥判定",
        "正定半正定判别",
    ]
    lines = [
        f"quiz_id: {yq('week2_l1')}",
        f"title: {yq('Week2-L1 课后小测（凸集与相关集合）')}",
        "sampling:",
        "  groups:",
    ]
    for tag in tags:
        lines.append(f"    - tag: {yq(tag)}")
        lines.append("      pick: 1")
    lines.append("questions:")
    for q in questions:
        lines.extend(
            [
                f"  - id: {yq(q['id'])}",
                f"    type: {yq(q['type'])}",
                f"    stem: {yq(q['stem'])}",
            ]
        )
        if q.get("correct_answer"):
            lines.append(f"    correct_answer: {yq(q['correct_answer'])}")
        if q.get("knowledge_tag"):
            lines.append(f"    knowledge_tag: {yq(q['knowledge_tag'])}")
        if q.get("pool_tag"):
            lines.append(f"    pool_tag: {yq(q['pool_tag'])}")
        if "options" in q:
            lines.append("    options:")
            for key, text in q["options"]:
                lines.append(f"      - key: {yq(key)}")
                lines.append(f"        text: {yq(text)}")
    return "\n".join(lines) + "\n"


def main():
    questions = build_questions()
    OUTPUT.parent.mkdir(parents=True, exist_ok=True)
    OUTPUT.write_text(dump_yaml(questions), encoding="utf-8")


if __name__ == "__main__":
    main()
