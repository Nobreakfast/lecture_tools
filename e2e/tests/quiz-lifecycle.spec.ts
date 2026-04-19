// Copyright 2024-2026 course-assistant contributors.
// SPDX-License-Identifier: MIT

import { test, expect } from "@playwright/test";
import { TeacherPage } from "../pages/teacher.page";
import { StudentPage } from "../pages/student.page";
import { QuizPage } from "../pages/quiz.page";
import { ResultPage } from "../pages/result.page";
import { getSeedResult } from "../helpers/seed";
import { TEACHER_ID, TEACHER_PASSWORD } from "../helpers/seed";

test.describe("Quiz lifecycle (end-to-end)", () => {
  test("teacher loads quiz → student answers → result → teacher sees attempt", async ({
    browser,
  }) => {
    const seed = getSeedResult();

    // ── Teacher: login, select course, upload quiz, open entry ──
    const teacherCtx = await browser.newContext();
    const teacherPage = new TeacherPage(await teacherCtx.newPage());
    await teacherPage.login(TEACHER_ID, TEACHER_PASSWORD);

    await teacherPage.selectCourse(seed.courseId);
    await teacherPage.uploadQuizYAML();
    await teacherPage.openEntry();

    const entryText = await teacherPage.getEntryStatusText();
    expect(entryText).toContain("开放");

    // ── Student: enter invite code, join quiz ──
    const studentCtx = await browser.newContext();
    const studentPageObj = new StudentPage(await studentCtx.newPage());
    await studentPageObj.enterCode(seed.inviteCode);
    await studentPageObj.waitForQuizOpen();
    await studentPageObj.joinQuiz("测试学生", "2024001", "测试班级");

    // ── Quiz: answer all questions ──
    const quizPage = new QuizPage(studentPageObj.page);
    await quizPage.waitForLoad();

    const quizTitle = await quizPage.getTitle();
    expect(quizTitle).toBe("第一周课堂反馈");

    // Answer all questions via API (questions are shuffled, so index-based clicking is unreliable)
    await quizPage.answerAllViaAPI({
      q1: "B",        // single_choice: struct
      q2: "Y",        // yes_no: 是
      q3: "A,B,C",    // multi_choice: 定义法, 一阶条件, 二阶条件
      q4: "A",        // survey: 更多案例
      q5: "我觉得凸函数的几何意义还不太清楚。",
    });

    // ── Submit ──
    await quizPage.submit();

    // ── Result: verify score ──
    const resultPage = new ResultPage(studentPageObj.page);
    await resultPage.waitForLoad();

    const titleText = await resultPage.getTitle();
    expect(titleText).toContain("第一周课堂反馈");

    const score = await resultPage.getScore();
    expect(score.correct).toBe(3);
    expect(score.total).toBe(3);

    const results = await resultPage.getQuestionResults();
    expect(results.ok).toBe(3);
    expect(results.bad).toBe(0);

    // ── Teacher: verify attempt appears ──
    await teacherPage.switchTab("tab-attempts");
    // Wait for the attempts list to refresh — the page might need a moment
    await teacherPage.page.waitForTimeout(1000);
    // Reload the page to ensure fresh data
    await teacherPage.page.reload();
    await teacherPage.viewMain.waitFor({ state: "visible" });
    await teacherPage.selectCourse(seed.courseId);
    await teacherPage.switchTab("tab-attempts");
    await teacherPage.page.waitForTimeout(1000);

    const attemptRows = teacherPage.attemptsList.locator("tr");
    // Should have at least one data row containing the student name
    await expect(
      teacherPage.attemptsList.locator("text=测试学生")
    ).toBeVisible({ timeout: 5000 });

    // ── Teacher: close entry ──
    await teacherPage.closeEntry();
    const closedText = await teacherPage.getEntryStatusText();
    expect(closedText).toContain("已关闭");

    // Cleanup
    await teacherCtx.close();
    await studentCtx.close();
  });
});
