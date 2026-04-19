// Copyright 2024-2026 course-assistant contributors.
// SPDX-License-Identifier: MIT

import { test, expect } from "@playwright/test";
import * as path from "path";
import { TeacherPage } from "../pages/teacher.page";
import { getSeedResult, TEACHER_ID, TEACHER_PASSWORD } from "../helpers/seed";

test.describe("Teacher quiz bank management", () => {
  let teacherPage: TeacherPage;

  test.beforeEach(async ({ page }) => {
    teacherPage = new TeacherPage(page);
    await teacherPage.login(TEACHER_ID, TEACHER_PASSWORD);
  });

  test("upload YAML to quiz bank and see in list", async () => {
    const seed = getSeedResult();
    await teacherPage.selectCourse(seed.courseId);
    await teacherPage.switchSubTab("sub-quiz");

    const fixturePath = path.resolve(
      __dirname,
      "../fixtures/quiz.bank.yaml"
    );
    await teacherPage.yamlFileInput.setInputFiles(fixturePath);
    await teacherPage.uploadYamlBtn.click();
    await teacherPage.page.waitForTimeout(500);

    // Server uses filename (minus extension) as quiz bank ID
    await expect(teacherPage.quizBankList).toContainText("quiz.bank");
  });

  test("load quiz bank sets it as current quiz", async () => {
    const seed = getSeedResult();
    await teacherPage.selectCourse(seed.courseId);

    // Upload first
    const fixturePath = path.resolve(
      __dirname,
      "../fixtures/quiz.bank.yaml"
    );
    await teacherPage.switchSubTab("sub-quiz");
    await teacherPage.yamlFileInput.setInputFiles(fixturePath);
    await teacherPage.uploadYamlBtn.click();
    await teacherPage.page.waitForTimeout(500);

    // Load via API — server uses filename stem as quiz bank ID
    const loadRes = await teacherPage.page.evaluate(
      async ({ courseId }) => {
        const r = await fetch(
          `/api/teacher/courses/quiz-bank/load?course_id=${courseId}`,
          {
            method: "POST",
            headers: { "Content-Type": "application/json" },
            credentials: "include",
            body: JSON.stringify({ quiz_id: "quiz.bank" }),
          }
        );
        return { status: r.status, body: await r.json() };
      },
      { courseId: seed.courseId }
    );
    expect(loadRes.status).toBe(200);
    expect(loadRes.body.ok).toBe(true);

    // Verify quiz title changed
    await teacherPage.switchTab("tab-attempts");
    await teacherPage.page.waitForTimeout(500);
    await expect(teacherPage.quizTitle).toContainText("题库测试小测");

    // Restore original quiz for other tests: upload + load
    await teacherPage.uploadQuizYAML();
    await teacherPage.page.evaluate(
      async ({ courseId }) => {
        await fetch(
          `/api/teacher/courses/quiz-bank/load?course_id=${courseId}`,
          {
            method: "POST",
            headers: { "Content-Type": "application/json" },
            credentials: "include",
            body: JSON.stringify({ quiz_id: "quiz.sample" }),
          }
        );
      },
      { courseId: seed.courseId }
    );
  });

  test("delete quiz bank removes from list", async () => {
    const seed = getSeedResult();
    await teacherPage.selectCourse(seed.courseId);

    // Upload a unique quiz
    const yamlContent = `quiz_id: "del_test_${Date.now()}"
title: "临时题库"
questions:
  - id: "dq1"
    type: "yes_no"
    stem: "测试？"
    options:
      - key: "Y"
        text: "是"
      - key: "N"
        text: "否"
    correct_answer: "Y"
`;
    const quizId = yamlContent.match(/quiz_id:\s*"([^"]+)"/)?.[1] ?? "";

    await teacherPage.page.evaluate(
      async ({ courseId, yaml, qid }) => {
        const fd = new FormData();
        fd.append(
          "files",
          new Blob([yaml], { type: "text/yaml" }),
          qid + ".yaml"
        );
        await fetch(
          `/api/teacher/courses/quiz-bank/upload?course_id=${courseId}`,
          { method: "POST", credentials: "include", body: fd }
        );
      },
      { courseId: seed.courseId, yaml: yamlContent, qid: quizId }
    );

    await teacherPage.switchSubTab("sub-quiz");
    await teacherPage.page.waitForTimeout(500);
    await expect(teacherPage.quizBankList).toContainText(quizId);

    // Delete via API
    const delRes = await teacherPage.page.evaluate(
      async ({ courseId, quizId }) => {
        const r = await fetch(
          `/api/teacher/courses/quiz-bank/delete?course_id=${courseId}`,
          {
            method: "POST",
            headers: { "Content-Type": "application/json" },
            credentials: "include",
            body: JSON.stringify({ quiz_id: quizId }),
          }
        );
        return r.status;
      },
      { courseId: seed.courseId, quizId }
    );
    expect(delRes).toBe(200);
  });
});
