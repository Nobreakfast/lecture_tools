// Copyright 2024-2026 course-assistant contributors.
// SPDX-License-Identifier: MIT

import { test, expect } from "@playwright/test";
import { TeacherPage } from "../pages/teacher.page";
import { StudentPage } from "../pages/student.page";
import { QuizPage } from "../pages/quiz.page";
import { ResultPage } from "../pages/result.page";
import { getSeedResult, TEACHER_ID, TEACHER_PASSWORD } from "../helpers/seed";

test.describe("Quiz retry and navigation", () => {
  test("student submits → retries → gets new attempt", async ({
    browser,
  }) => {
    const seed = getSeedResult();

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
    await studentPage.joinQuiz("重试学生", "2024R01", "测试班级");

    // First attempt
    const quiz1 = new QuizPage(studentPage.page);
    await quiz1.waitForLoad();
    await quiz1.answerAllViaAPI({
      q1: "A",
      q2: "N",
      q3: "D",
      q4: "A",
      q5: "第一次",
    });
    await quiz1.submit();

    const result1 = new ResultPage(studentPage.page);
    await result1.waitForLoad();
    const score1 = await result1.getScore();
    expect(score1.correct).toBe(0);

    // Retry
    await result1.retry();
    expect(studentPage.page.url()).toContain("/quiz");

    // Second attempt with correct answers
    const quiz2 = new QuizPage(studentPage.page);
    await quiz2.waitForLoad();
    await quiz2.answerAllViaAPI({
      q1: "B",
      q2: "Y",
      q3: "A,B,C",
      q4: "A",
      q5: "第二次全对",
    });
    await quiz2.submit();

    const result2 = new ResultPage(studentPage.page);
    await result2.waitForLoad();
    const score2 = await result2.getScore();
    expect(score2.correct).toBe(3);
    expect(score2.total).toBe(3);

    await teacherCtx.close();
    await studentCtx.close();
  });

  test("student returns home from result page", async ({ browser }) => {
    const seed = getSeedResult();

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
    await studentPage.joinQuiz("回首学生", "2024H01", "测试班级");

    const quiz = new QuizPage(studentPage.page);
    await quiz.waitForLoad();
    await quiz.answerAllViaAPI({
      q1: "B",
      q2: "Y",
      q3: "A,B,C",
      q4: "A",
      q5: "回首页测试",
    });
    await quiz.submit();

    const result = new ResultPage(studentPage.page);
    await result.waitForLoad();

    // Sign out (clears student_token, goes home)
    await result.goHome();
    expect(studentPage.page.url()).toMatch(/\/$/);

    await teacherCtx.close();
    await studentCtx.close();
  });

  test("AI summary button is present on result page", async ({ browser }) => {
    const seed = getSeedResult();

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
    await studentPage.joinQuiz("AI总结学生", "2024AI1", "测试班级");

    const quiz = new QuizPage(studentPage.page);
    await quiz.waitForLoad();
    await quiz.answerAllViaAPI({
      q1: "B",
      q2: "Y",
      q3: "A,B,C",
      q4: "A",
      q5: "AI总结测试",
    });
    await quiz.submit();

    const result = new ResultPage(studentPage.page);
    await result.waitForLoad();

    // AI button should be visible (even if AI service isn't configured)
    await expect(result.aiBtn).toBeVisible();

    // Click it — should show some response (error or summary)
    await result.aiBtn.click();
    await studentPage.page.waitForTimeout(2000);
    // The summary area should become visible (even with error content)
    await expect(result.summary).toBeVisible({ timeout: 10_000 });

    await teacherCtx.close();
    await studentCtx.close();
  });
});
