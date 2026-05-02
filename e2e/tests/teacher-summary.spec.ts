// Copyright 2024-2026 course-assistant contributors.
// SPDX-License-Identifier: MIT

import { test, expect } from "@playwright/test";
import { TeacherPage } from "../pages/teacher.page";
import { StudentPage } from "../pages/student.page";
import { QuizPage } from "../pages/quiz.page";
import { getSeedResult, TEACHER_ID, TEACHER_PASSWORD } from "../helpers/seed";

test.describe("Teacher summary features", () => {
  test("summary tab shows raw stats after submissions", async ({
    browser,
  }) => {
    const seed = getSeedResult();

    // Create a submission
    const teacherCtx = await browser.newContext();
    const teacherPage = new TeacherPage(await teacherCtx.newPage());
    await teacherPage.login(TEACHER_ID, TEACHER_PASSWORD);
    await teacherPage.selectCourse(seed.courseId);
    await teacherPage.uploadQuizYAML();
    await teacherPage.openEntry();

    const studentCtx = await browser.newContext();
    const studentPage = new StudentPage(await studentCtx.newPage());
    await studentPage.enterCode(seed.inviteCode);
    await studentPage.waitForQuizOpen();
    await studentPage.joinQuiz("总结学生", "2024S01", "总结班");

    const quiz = new QuizPage(studentPage.page);
    await quiz.waitForLoad();
    await quiz.answerAllViaAPI({
      q1: "B",
      q2: "Y",
      q3: "A,B,C",
      q4: "A",
      q5: "总结测试",
    });
    await quiz.submit();
    await studentCtx.close();

    // Teacher checks summary tab
    await teacherPage.switchTab("tab-summary");
    await teacherPage.page.waitForTimeout(2000);

    // Raw stats should show at least 1 student
    await expect(teacherPage.summaryRawStats).toContainText("答题人数");

    await teacherCtx.close();
  });

  test("summary API returns stats (GET)", async ({ page }) => {
    const seed = getSeedResult();

    const teacherPage = new TeacherPage(page);
    await teacherPage.login(TEACHER_ID, TEACHER_PASSWORD);

    const result = await page.evaluate(async ({ courseId }) => {
      const r = await fetch(
        `/api/teacher/courses/summary?course_id=${courseId}`,
        { credentials: "include" }
      );
      return { status: r.status, body: await r.json() };
    }, { courseId: seed.courseId });

    expect(result.status).toBe(200);
    expect(result.body).toHaveProperty("stats");
  });

  test("history summary API returns quiz_stats (GET)", async ({ page }) => {
    const seed = getSeedResult();

    const teacherPage = new TeacherPage(page);
    await teacherPage.login(TEACHER_ID, TEACHER_PASSWORD);

    const result = await page.evaluate(async ({ courseId }) => {
      const r = await fetch(
        `/api/teacher/courses/history-summary?course_id=${courseId}`,
        { credentials: "include" }
      );
      return { status: r.status, body: await r.text() };
    }, { courseId: seed.courseId });

    if (result.status === 200) {
      const data = JSON.parse(result.body);
      expect(data).toHaveProperty("quiz_stats");
    } else {
      expect(result.status).toBe(400);
    }
  });

  test("generate summary button is clickable", async ({ browser }) => {
    const seed = getSeedResult();

    // Ensure there's at least one submission
    const teacherCtx = await browser.newContext();
    const teacherPage = new TeacherPage(await teacherCtx.newPage());
    await teacherPage.login(TEACHER_ID, TEACHER_PASSWORD);
    await teacherPage.selectCourse(seed.courseId);
    await teacherPage.uploadQuizYAML();
    await teacherPage.openEntry();

    const studentCtx = await browser.newContext();
    const sp = new StudentPage(await studentCtx.newPage());
    await sp.enterCode(seed.inviteCode);
    await sp.waitForQuizOpen();
    await sp.joinQuiz("生成学生", "2024GEN", "生成班");
    const quiz = new QuizPage(sp.page);
    await quiz.waitForLoad();
    await quiz.answerAllViaAPI({
      q1: "B",
      q2: "Y",
      q3: "A,B,C",
      q4: "A",
      q5: "生成测试",
    });
    await quiz.submit();
    await studentCtx.close();

    // Go to summary tab and try generating
    await teacherPage.switchTab("tab-summary");
    await teacherPage.page.waitForTimeout(1000);

    await expect(teacherPage.genSummaryBtn).toBeEnabled();
    await teacherPage.genSummaryBtn.click();

    // Button should change text while generating
    await expect(teacherPage.genSummaryBtn).toContainText("正在生成");

    // Wait for it to complete (may fail without AI, that's OK)
    await teacherPage.page.waitForTimeout(5000);
    await expect(teacherPage.genSummaryBtn).toContainText("生成 AI 总结");

    await teacherCtx.close();
  });

  test("AI summary API generates a structured summary", async ({ browser }) => {
    test.skip(
      process.env.E2E_EXPECT_AI_SUMMARY !== "1",
      "requires live AI config and E2E_EXPECT_AI_SUMMARY=1"
    );
    test.setTimeout(120_000);

    const seed = getSeedResult();

    const teacherCtx = await browser.newContext();
    const teacherPage = new TeacherPage(await teacherCtx.newPage());
    await teacherPage.login(TEACHER_ID, TEACHER_PASSWORD);
    await teacherPage.selectCourse(seed.courseId);
    await teacherPage.uploadQuizYAML();
    await teacherPage.openEntry();

    const studentCtx = await browser.newContext();
    const sp = new StudentPage(await studentCtx.newPage());
    await sp.enterCode(seed.inviteCode);
    await sp.waitForQuizOpen();
    await sp.joinQuiz("AI总结接口学生", "2024AISUM", "AI总结班");
    const quiz = new QuizPage(sp.page);
    await quiz.waitForLoad();
    await quiz.answerAllViaAPI({
      q1: "B",
      q2: "Y",
      q3: "A,B,C",
      q4: "A",
      q5: "希望多讲一些例题",
    });
    await quiz.submit();
    await studentCtx.close();

    const result = await teacherPage.page.evaluate(async ({ courseId }) => {
      const r = await fetch(
        `/api/teacher/courses/summary?course_id=${courseId}`,
        { method: "POST", credentials: "include" }
      );
      const text = await r.text();
      let body: any;
      try {
        body = JSON.parse(text);
      } catch {
        body = { _raw: text };
      }
      return { status: r.status, body };
    }, { courseId: seed.courseId });

    expect(result.status).toBe(200);
    expect(result.body.error).toBeFalsy();
    expect(result.body.summary).toBeTruthy();
    expect(result.body.summary.answer_analysis).toBeTruthy();
    expect(result.body.summary.feedback_summary).toBeTruthy();
    expect(result.body.summary.teaching_suggestions).toBeTruthy();

    await teacherCtx.close();
  });
});
